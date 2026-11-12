package channels

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"newapisub/internal/connection"
	"newapisub/internal/httpx"
	"newapisub/internal/newapi"
	"newapisub/internal/store"
)

// A channel is one local record and a placement in every pool it goes to; each
// created placement is a new-api channel with that pool's own id. The local id
// is the only one any answer calls the channel by.

var (
	errPushing  = httpx.Errorf(http.StatusConflict, "渠道正在推送到号池，请稍后再试")
	errPoolDown = httpx.Errorf(http.StatusServiceUnavailable, "号池暂时不可用，请稍后再试")
	errGone     = httpx.Errorf(http.StatusNotFound, "渠道已在 new-api 删除")
	errFailed   = httpx.Errorf(http.StatusConflict, "渠道推送到号池失败，请联系管理员重试")
)

// each runs fn for every item at once; the results and errors are in the
// order of items.
func each[E, T any](items []E, fn func(E) (T, error)) ([]T, []error) {
	out, errs := make([]T, len(items)), make([]error, len(items))
	var wg sync.WaitGroup
	for i, it := range items {
		wg.Go(func() { out[i], errs[i] = fn(it) })
	}
	wg.Wait()
	return out, errs
}

// factor converts a pool's quota into the home pool's, which the pages show
// money in; false while either is not known.
func (s *Service) factor(p *connection.Pool) (float64, bool) {
	home := s.Pools.Home().QuotaPerUnit
	return home / p.QuotaPerUnit, home > 0 && p.QuotaPerUnit > 0
}

func scale(quota int64, factor float64) int64 {
	if factor == 1 {
		return quota
	}
	return int64(math.Round(float64(quota) * factor))
}

// rows reads the new-api rows of the channels of ms placed anywhere, one query
// per pool; a pool without a database has the rows the push worker last read.
// down holds the channels that could not be read.
func (s *Service) rows(ctx context.Context, ms []store.Mapping) (map[store.PoolChannel]newapi.Channel, map[store.PoolChannel]bool) {
	pools := s.Pools.List()
	found, errs := each(pools, func(p *connection.Pool) (map[int]newapi.Channel, error) {
		ids := store.InPool(ms, p.ID)
		switch {
		case len(ids) == 0:
			return nil, nil
		case p.Reason != "":
			return nil, errPoolDown
		case p.DB == nil:
			return cachedRows(ms, p.ID)
		}
		return p.DB.Channels(ctx, ids)
	})
	rows, down := map[store.PoolChannel]newapi.Channel{}, map[store.PoolChannel]bool{}
	for i, p := range pools {
		if errs[i] != nil {
			if errs[i] != errPoolDown && errs[i] != errNotRead {
				log.Printf("read the channels of pool %d: %v", p.ID, errs[i])
			}
			for _, id := range store.InPool(ms, p.ID) {
				down[store.PoolChannel{PoolID: p.ID, NewAPIID: id}] = true
			}
		}
		for id, c := range found[i] {
			rows[store.PoolChannel{PoolID: p.ID, NewAPIID: id}] = c
		}
	}
	return rows, down
}

// errNotRead: the push worker has not read the channels of a pool without a
// database since one of them was created.
var errNotRead = errors.New("channel not read yet")

// staleAfter is how old a cached row of a pool without a database is called
// stale: the push worker reads each one every few minutes when it can.
const staleAfter = 15 * time.Minute

// stale says a cached row was read longer than staleAfter ago.
func stale(at int64) bool { return at > 0 && at < time.Now().Add(-staleAfter).Unix() }

// cached is the row a placement in a pool without a database was last read
// with: nil if new-api no longer had it, errNotRead before the first read.
func cached(p store.Placement) (*newapi.Channel, error) {
	if p.CachedAt == 0 {
		return nil, errNotRead
	}
	var c *newapi.Channel
	if err := json.Unmarshal(p.Cached, &c); err != nil {
		return nil, fmt.Errorf("cached row of channel %d: %w", p.NewAPIID, err)
	}
	return c, nil
}

// cachedRows is DB.Channels for a pool without a database, from the cache.
func cachedRows(ms []store.Mapping, poolID int64) (map[int]newapi.Channel, error) {
	out := map[int]newapi.Channel{}
	for _, m := range ms {
		for _, p := range m.Placements {
			if p.PoolID != poolID || p.State != "created" {
				continue
			}
			c, err := cached(p)
			if err != nil {
				return nil, err
			}
			if c != nil {
				out[p.NewAPIID] = *c
			}
		}
	}
	return out, nil
}

// recache keeps the cached row of a placement in a pool without a database in
// step with a change that went through there; the push worker reads it again
// later anyway.
func recache(ctx context.Context, st *store.Store, pool *connection.Pool, newapiID int, c newapi.Channel, edit func(*newapi.Channel)) {
	if pool.DB != nil {
		return
	}
	edit(&c)
	raw, err := json.Marshal(c)
	if err == nil {
		err = st.CacheStates(ctx, pool.ID, map[int]string{newapiID: string(raw)})
	}
	if err != nil {
		log.Printf("cache channel %d of pool %d: %v", newapiID, pool.ID, err)
	}
}

// live is a placement whose new-api channel is there.
type live struct {
	pool *connection.Pool
	p    store.Placement
	c    newapi.Channel
}

// target is a channel of the caller's scope as the pools have it now.
type target struct {
	m     store.Mapping
	lives []live  // by seq; the first leads
	down  []int64 // pools of created placements that could not be read
}

func resolve(s *Service, m store.Mapping, rows map[store.PoolChannel]newapi.Channel, down map[store.PoolChannel]bool) target {
	t := target{m: m}
	for _, p := range m.Placements {
		if p.State != "created" {
			continue
		}
		k := store.PoolChannel{PoolID: p.PoolID, NewAPIID: p.NewAPIID}
		if down[k] {
			t.down = append(t.down, p.PoolID)
		} else if c, ok := rows[k]; ok {
			t.lives = append(t.lives, live{s.Pools.Get(p.PoolID), p, c})
		}
	}
	return t
}

// pushing says some placement is not created yet.
func (t target) pushing() bool {
	return slices.ContainsFunc(t.m.Placements, func(p store.Placement) bool { return p.State == "pending" || p.State == "pushing" })
}

// gone: no pool has the channel or will get it.
func (t target) gone() bool { return len(t.lives) == 0 && len(t.down) == 0 && !t.pushing() }

// need is for what only a live new-api channel can do.
func (t target) need() error {
	switch {
	case len(t.lives) > 0:
		return nil
	case len(t.down) > 0:
		return errPoolDown
	case t.pushing():
		return errPushing
	case slices.ContainsFunc(t.m.Placements, func(p store.Placement) bool { return p.State == "failed" }):
		return errFailed
	}
	return errGone
}

// lead is the live placement an operation on one pool uses: the first, or for
// an admin asking for one with ?pool_id=, that pool's.
func (s *Service) lead(r *http.Request, t target) (live, error) {
	if err := t.need(); err != nil {
		return live{}, err
	}
	raw := r.URL.Query().Get("pool_id")
	if s.Portal || raw == "" {
		return t.lives[0], nil
	}
	for _, l := range t.lives {
		if raw == strconv.FormatInt(l.pool.ID, 10) {
			return l, nil
		}
	}
	return live{}, httpx.Errorf(http.StatusNotFound, "该渠道在此号池没有可用的投放")
}

// fanOut runs fn on every live placement. pending are the pools it did not
// reach, those down included, which the push worker brings in line; err is the
// first failure, and the answer when it is the only outcome (failed).
func (t target) fanOut(fn func(live) error) (pending []int64, err error) {
	pending = slices.Clone(t.down)
	for _, l := range t.lives {
		if e := fn(l); e != nil {
			log.Printf("channel %d in pool %d: %s", t.m.ChannelID, l.pool.ID, httpx.Public(e))
			pending, err = append(pending, l.pool.ID), cmp.Or(err, e)
		}
	}
	return pending, err
}

// failed says a change reached no pool and none is left to bring it to later:
// it did not happen, and is answered as the error it met.
func (t target) failed(pending []int64, err error) bool {
	return err != nil && len(pending) == len(t.lives) && len(t.down) == 0 && !t.pushing()
}

// fannedOut answers a change every pool should take: 204 when they all did,
// else 202 with those the push worker still brings in line.
func fannedOut(w http.ResponseWriter, pending []int64) error {
	if len(pending) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{"pending_pools": pending})
	return nil
}

// spec is what every pool gets of a channel: its key, name, remark and status
// are added when it is pushed.
type spec struct {
	newapi.ChannelInput
	Mode         string `json:"mode"` // single or multi_to_single
	MultiKeyMode string `json:"multi_key_mode,omitempty"`
}

// parseSpec reads a channel's spec; one from before pools has only what an
// update set, if anything.
func parseSpec(raw json.RawMessage) spec {
	var sp spec
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &sp); err != nil {
			log.Printf("unreadable channel spec: %v", err) // written by this package only
		}
	}
	return sp
}

type placementView struct {
	State    string `json:"state"`
	PoolID   int64  `json:"pool_id,omitempty"`
	PoolName string `json:"pool_name,omitempty"`
	Error    string `json:"error,omitempty"`
	NewAPIID int    `json:"newapi_channel_id,omitempty"`
	// In a pool without a database: when its row was last read, stale if long ago.
	StateAt int64 `json:"state_at,omitempty"`
	Stale   bool  `json:"stale,omitempty"`
}

// placementsView shows everyone the state and pool of each placement, an
// admin also why it failed, a super also its new-api id.
func (s *Service) placementsView(r *http.Request, ps []store.Placement) []placementView {
	out := make([]placementView, len(ps))
	for i, p := range ps {
		out[i].State, out[i].PoolID, out[i].StateAt, out[i].Stale = p.State, p.PoolID, p.CachedAt, stale(p.CachedAt)
		if pool := s.Pools.Get(p.PoolID); pool != nil {
			out[i].PoolName = pool.Name
		}
		if !s.Portal {
			out[i].Error = p.Error
		}
		if s.isSuper(r) {
			out[i].NewAPIID = p.NewAPIID
		}
	}
	return out
}

// pushState sums up the placements: pending before any is created, done when
// all are, failed when one failed and none is created, partial otherwise.
func pushState(ps []store.Placement) string {
	created, failed := 0, 0
	for _, p := range ps {
		switch p.State {
		case "created":
			created++
		case "failed":
			failed++
		}
	}
	switch {
	case len(ps) > 0 && created == len(ps):
		return "done"
	case created > 0:
		return "partial"
	case failed > 0:
		return "failed"
	}
	return "pending"
}

// merged is the one row the pages show of a channel: the lead pool's, enabled
// if any pool has it enabled, the used quota of all of them in the home pool's
// unit and the latest balance. Without any it is made of the local record.
func (s *Service) merged(t target) (c newapi.Channel, exists bool) {
	if len(t.lives) == 0 {
		sp := parseSpec(t.m.Spec)
		c = newapi.Channel{ID: t.m.ChannelID, Name: t.m.Name, Type: sp.Type, Models: sp.Models, Group: sp.Group,
			CreatedTime: t.m.CreatedAt, ChannelInfo: json.RawMessage("{}")}
		if t.pushing() { // the status it will be created with
			c.Status = t.m.WantStatus
		}
		return c, len(t.down) > 0
	}
	c = t.lives[0].c
	c.ID, c.UsedQuota = t.m.ChannelID, 0
	statuses := map[int]bool{}
	for _, l := range t.lives {
		statuses[l.c.Status] = true
		if f, ok := s.factor(l.pool); ok {
			c.UsedQuota += scale(l.c.UsedQuota, f)
		}
		if l.c.BalanceUpdatedTime > c.BalanceUpdatedTime {
			c.Balance, c.BalanceUpdatedTime = l.c.Balance, l.c.BalanceUpdatedTime
		}
	}
	switch {
	case statuses[1]:
		c.Status = 1
	case statuses[3]:
		c.Status = 3
	default:
		c.Status = 2
	}
	return c, true
}
