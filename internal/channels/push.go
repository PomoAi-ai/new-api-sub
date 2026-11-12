package channels

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"newapisub/internal/connection"
	"newapisub/internal/newapi"
	"newapisub/internal/secret"
	"newapisub/internal/store"
)

const (
	pushMaxAttempts = 10
	pushCooldown    = time.Minute // a pool that failed is left alone this long
	pushBackoffMax  = time.Hour
	// The channels of a pool without a database are read this often: from
	// new-api's list while it has at most statePages pages, else one by one.
	stateEvery = 5 * time.Minute
	stateAgain = time.Minute // while stateRate left some unread
	statePages = 30
	// stateReserve of the pages are left to the reads by id while the last
	// round could not do them all: a list of almost statePages pages would
	// take every read, and a channel deleted in new-api would never be read.
	stateReserve = 5
)

// new-api limits this host to 360 calls in 180s, which the pages and the log
// sync share: the worker's own calls, and its reads of channel lists.
var (
	pushRate  = rateWindow{limit: 60, per: 180 * time.Second}
	stateRate = rateWindow{limit: 30, per: 180 * time.Second}
)

// Pusher is the push worker: it creates the channels recorded by create in
// their pools and brings created ones in line with what was changed since, one
// new-api write at a time. One runs, in the admin backend, under an advisory
// lock. A channel's key is decrypted only here, only in memory. It also keeps
// the rows of the channels in pools without a database, which the pages read.
type Pusher struct {
	Store *store.Store
	Pools *connection.Pools
	Box   *secret.Box

	cooling map[int64]time.Time // pool -> until when it is left alone
	due     map[int64]time.Time // pool without a database -> when its channels are read next
	unread  map[int64]bool      // pool without a database -> stateRate left reads by id undone
}

// Run drains the queue every every until ctx ends.
func (p *Pusher) Run(ctx context.Context, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		p.Drain(ctx)
		p.Refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Drain carries out the jobs that are due, until none is left of the pools
// that can take them.
func (p *Pusher) Drain(ctx context.Context) {
	for ctx.Err() == nil {
		j, err := p.Store.ClaimJob(ctx, p.skip())
		if errors.Is(err, store.ErrNotFound) {
			return
		}
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("push worker: claim a job: %v", err)
			}
			return
		}
		p.run(ctx, j)
	}
}

// skip is the pools no job may go to now: cooling down, or down.
func (p *Pusher) skip() []int64 {
	var out []int64
	now := time.Now()
	for _, pool := range p.Pools.List() {
		if pool.Reason != "" || p.cooling[pool.ID].After(now) {
			out = append(out, pool.ID)
		}
	}
	return out
}

func (p *Pusher) cool(poolID int64) {
	if p.cooling == nil {
		p.cooling = map[int64]time.Time{}
	}
	p.cooling[poolID] = time.Now().Add(pushCooldown)
}

// jobErr is a job that did not go through: Rejected means new-api refused it,
// which counts towards giving up a create; limited is new-api's rate limit or
// ours, which counts for nothing.
type jobErr struct {
	msg      string // a fixed text or new-api's masked refusal
	rejected bool
	limited  bool
}

func (e *jobErr) Error() string { return e.msg }

// callErr sorts a failed new-api call of pool; the full error goes to the
// server log.
func callErr(pool *connection.Pool, err error) *jobErr {
	var rej *newapi.Rejected
	if errors.As(err, &rej) {
		return &jobErr{msg: rej.Message, rejected: true}
	}
	log.Printf("push worker: pool %d %q: %s", pool.ID, pool.Name, logged(err))
	if strings.Contains(err.Error(), ": HTTP 429:") {
		return &jobErr{msg: "new-api 限流", limited: true}
	}
	return &jobErr{msg: "new-api 暂时不可用"}
}

// logged is err for the server log, without the address of the pool's new-api:
// the log names the pool.
func logged(err error) string {
	s := err.Error()
	var ue *url.Error
	if errors.As(err, &ue) {
		s = strings.ReplaceAll(s, strconv.Quote(ue.URL), "(new-api)")
	}
	// A dial error names the host:port it could not reach.
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Addr != nil {
		s = strings.ReplaceAll(s, oe.Addr.String(), "(new-api)")
	}
	return s
}

var errRate = &jobErr{msg: "推送太频繁，稍后继续", limited: true}

// write counts one new-api call of the worker to pool against pushRate.
func write(poolID int64) error {
	if pushRate.take(int(poolID), 1) == 0 {
		return errRate
	}
	return nil
}

func (p *Pusher) run(ctx context.Context, j store.Job) {
	pool := p.Pools.Get(j.PoolID)
	var err error
	switch {
	case pool == nil || pool.Reason != "":
		err = &jobErr{msg: "号池不可用", limited: true}
	case j.State == "pushing":
		err = p.create(ctx, pool, j)
	default:
		err = p.reconcile(ctx, pool, j)
	}
	var je *jobErr
	if err == nil || !errors.As(err, &je) {
		if err != nil {
			log.Printf("push worker: channel %d in pool %d: %s", j.ChannelID, j.PoolID, logged(err))
		}
		return
	}
	p.cool(j.PoolID)
	if j.State == "pushing" && !je.limited && (je.rejected || j.Attempts+1 >= pushMaxAttempts) {
		if err := p.Store.FailPush(ctx, j.ChannelID, j.PoolID, je.msg); err != nil {
			log.Printf("push worker: give up channel %d in pool %d: %v", j.ChannelID, j.PoolID, err)
			return
		}
		p.audit(ctx, j, false, map[string]any{"error": je.msg})
		return
	}
	after := min(30*time.Second<<min(j.Attempts, 7), pushBackoffMax)
	if je.limited {
		after = pushCooldown
	}
	if err := p.Store.Backoff(ctx, j.ChannelID, j.PoolID, after, je.msg, !je.limited); err != nil && !errors.Is(err, store.ErrNotFound) {
		log.Printf("push worker: put off channel %d in pool %d: %v", j.ChannelID, j.PoolID, err)
	}
}

// create pushes a channel to a pool. The remark is looked up first, every
// time: a create of an earlier attempt may have gone through without its
// answer, or the worker died before recording it.
func (p *Pusher) create(ctx context.Context, pool *connection.Pool, j store.Job) error {
	if found, err := p.byRemark(ctx, pool, j); err != nil || found {
		return err
	}
	key, err := p.Box.ChannelKey.Open(KeyField(j.ChannelID), j.KeySealed)
	if err != nil {
		return &jobErr{msg: "本地保存的密钥无法解密", rejected: true}
	}
	sp := parseSpec(j.Spec)
	ch := sp.ChannelInput
	ch.Name, ch.Key, ch.Remark, ch.Status = j.Name, string(key), j.Remark, j.WantStatus
	if err := write(pool.ID); err != nil {
		return err
	}
	ids, err := pool.API.CreateChannel(ctx, newapi.CreateRequest{Mode: sp.Mode, MultiKeyMode: sp.MultiKeyMode, Channel: ch})
	if err == nil && len(ids) == 1 {
		return p.record(ctx, pool, j, ids[0])
	}
	var rej *newapi.Rejected
	if errors.As(err, &rej) { // nothing was created
		return &jobErr{msg: rej.Message, rejected: true}
	}
	// The answer does not say what happened: either way the remark does.
	if found, lookupErr := p.byRemark(ctx, pool, j); lookupErr != nil || found {
		return lookupErr
	}
	if err != nil {
		return callErr(pool, err)
	}
	return &jobErr{msg: "new-api 响应缺少渠道 id，且按 remark 未找到渠道"}
}

// byRemark records the new-api channel of j's remark, if there is one.
func (p *Pusher) byRemark(ctx context.Context, pool *connection.Pool, j store.Job) (bool, error) {
	var ids []int
	var err error
	if pool.DB != nil {
		ids, err = pool.DB.ChannelIDsByRemark(ctx, j.Remark)
	} else {
		ids, err = remarkByAPI(ctx, pool, j)
	}
	var limited *jobErr // our rate limit
	if errors.As(err, &limited) {
		return false, err
	}
	if err != nil && pool.DB == nil { // new-api's list: worded as any call of it, but a refused read gives up nothing
		je := callErr(pool, err)
		je.rejected = false
		return false, je
	}
	if err != nil {
		log.Printf("push worker: look up remark of channel %d in pool %d: %v", j.ChannelID, pool.ID, err)
		return false, &jobErr{msg: "按 remark 反查失败"}
	}
	if len(ids) == 0 {
		return false, nil
	}
	if len(ids) > 1 {
		log.Printf("ORPHAN new-api channels %v in pool %d share remark=%s: channel %d takes %d", ids[1:], pool.ID, j.Remark, j.ChannelID, ids[0])
	}
	return true, p.record(ctx, pool, j, ids[0])
}

// remarkByAPI looks for j's remark in a pool without a database, among its
// newest channels: three pages of new-api's list at most, or back to 5 minutes
// before the create was first tried.
func remarkByAPI(ctx context.Context, pool *connection.Pool, j store.Job) ([]int, error) {
	var ids []int
	for page := 1; page <= 3; page++ {
		if err := write(pool.ID); err != nil {
			return nil, err
		}
		cs, total, err := pool.API.RecentChannels(ctx, page)
		if err != nil {
			return nil, err
		}
		older := false
		for _, c := range cs {
			if c.Remark == j.Remark {
				ids = append(ids, c.ID)
			}
			older = older || c.CreatedTime < j.StartedAt-300
		}
		if older || len(cs) == 0 || page*newapi.ChannelPageSize >= total {
			break
		}
	}
	slices.Sort(ids)
	return ids, nil
}

// record registers the new-api channel a create made. Whether it took the
// status it was sent with is not known, so a want_status other than 1 leaves
// status_dirty, to be confirmed. A placement no longer being pushed (the
// channel was purged meanwhile) leaves an orphan, deleted at once: the one
// place this system calls new-api's DELETE.
func (p *Pusher) record(ctx context.Context, pool *connection.Pool, j store.Job, newapiID int) error {
	err := p.Store.MarkCreated(ctx, j.ChannelID, pool.ID, newapiID, 1, parseSpec(j.Spec).Mode == "multi_to_single")
	detail := map[string]any{"pool_id": pool.ID, "newapi_channel_id": newapiID, "remark": j.Remark}
	if !errors.Is(err, store.ErrNotFound) {
		if err == nil {
			p.audit(ctx, j, true, detail)
			delete(p.due, pool.ID) // the pages read the new channel from there
		}
		return err
	}
	ok := true
	if err := pool.API.DeleteChannel(ctx, newapiID); err != nil {
		ok, detail["error"] = false, callErr(pool, err).msg
		log.Printf("ORPHAN new-api channel %d in pool %d remark=%s: its channel is gone and the compensating delete failed", newapiID, pool.ID, j.Remark)
	}
	p.auditSystem(ctx, "channel.create_compensate", j.ChannelID, ok, detail)
	return nil
}

// reconcile brings a created placement in line with what is flagged dirty.
func (p *Pusher) reconcile(ctx context.Context, pool *connection.Pool, j store.Job) error {
	c, exists, err := p.current(ctx, pool, j)
	if err != nil {
		return err
	}
	if !exists { // deleted in new-api's panel: nothing left to bring in line
		return p.clear(ctx, j)
	}
	have := 0 // unknown: a pool without a database not read yet
	if c != nil {
		have = c.Status
	}
	// A cached status can be behind, so a pool without a database is always sent it.
	if j.StatusDirty && (pool.DB == nil || have != j.WantStatus) {
		if err := write(pool.ID); err != nil {
			return err
		}
		changed, err := pool.API.SetStatus(ctx, j.NewAPIID, j.WantStatus)
		if err != nil {
			return callErr(pool, err)
		}
		if !changed && have != j.WantStatus { // new-api's cache has not learned the channel yet
			return &jobErr{msg: "new-api 尚未同步该渠道", limited: true}
		}
	}
	sp := parseSpec(j.Spec)
	if j.ConfigDirty {
		if err := write(pool.ID); err != nil {
			return err
		}
		if err := pool.API.UpdateChannel(ctx, j.NewAPIID, j.Name, sp.Models, sp.TestModel); err != nil {
			return callErr(pool, err)
		}
	}
	if j.KeysDirty {
		if err := p.alignKeys(ctx, pool, j); err != nil {
			return err
		}
	}
	if c != nil {
		recache(ctx, p.Store, pool, j.NewAPIID, *c, func(c *newapi.Channel) {
			if j.StatusDirty {
				c.Status = j.WantStatus
			}
			if j.ConfigDirty {
				c.Name, c.Models = j.Name, sp.Models
				if sp.TestModel != "" {
					c.TestModel = &sp.TestModel
				}
			}
		})
	}
	return p.clear(ctx, j)
}

// current is j's new-api row, exists false if new-api no longer has it. In a
// pool without a database it is the row last read, nil before the first read.
func (p *Pusher) current(ctx context.Context, pool *connection.Pool, j store.Job) (c *newapi.Channel, exists bool, err error) {
	if pool.DB != nil {
		rows, err := pool.DB.Channels(ctx, []int{j.NewAPIID})
		if err != nil {
			log.Printf("push worker: read channel %d of pool %d: %v", j.NewAPIID, pool.ID, err)
			return nil, false, &jobErr{msg: "读取渠道失败"}
		}
		row, ok := rows[j.NewAPIID]
		return &row, ok, nil
	}
	m, err := p.Store.Channel(ctx, j.ChannelID, store.ChannelFilter{})
	if errors.Is(err, store.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	for _, pl := range m.Placements {
		if pl.PoolID != pool.ID {
			continue
		}
		c, err := cached(pl)
		if errors.Is(err, errNotRead) {
			return nil, true, nil
		}
		return c, err == nil && c != nil, err
	}
	return nil, false, nil
}

// Refresh reads the channels of every pool without a database that is due,
// for the pages and the group filter, and notes those new-api no longer has.
// A pool whose reads stateRate cut short is due again sooner.
func (p *Pusher) Refresh(ctx context.Context) {
	if p.due == nil {
		p.due, p.unread = map[int64]time.Time{}, map[int64]bool{}
	}
	for _, pool := range p.Pools.List() {
		if ctx.Err() != nil || pool.Reason != "" || pool.DB != nil || time.Now().Before(p.due[pool.ID]) {
			continue
		}
		left, err := p.readChannels(ctx, pool, p.unread[pool.ID])
		p.due[pool.ID] = time.Now().Add(stateEvery)
		if left > 0 {
			p.due[pool.ID] = time.Now().Add(stateAgain)
		}
		p.unread[pool.ID] = left > 0
		if err != nil && ctx.Err() == nil {
			log.Printf("push worker: read the channels of pool %d, those not read stay as last read: %s", pool.ID, logged(err))
		}
	}
}

// readChannels caches the rows of the channels placed in a pool without a
// database: those on new-api's list while it is short enough to read whole
// (100 a call), every other one by its id, the longest unread first. Only
// new-api saying it has no such id marks one gone. left is how many stateRate
// left unread; reserve keeps stateReserve pages for them.
func (p *Pusher) readChannels(ctx context.Context, pool *connection.Pool, reserve bool) (left int, err error) {
	ms, err := p.Store.Channels(ctx, store.ChannelFilter{})
	if err != nil {
		return 0, err
	}
	var ours []store.Placement
	for _, m := range ms {
		for _, pl := range m.Placements {
			if pl.PoolID == pool.ID && pl.State == "created" {
				ours = append(ours, pl)
			}
		}
	}
	slices.SortFunc(ours, func(a, b store.Placement) int { return cmp.Compare(a.CachedAt, b.CachedAt) })
	want := map[int]bool{}
	for _, pl := range ours {
		want[pl.NewAPIID] = true
	}
	rows, groups, whole := map[int]string{}, []string{}, false
	pages := statePages
	if reserve {
		pages -= stateReserve
	}
	for page := 1; page <= pages && stateRate.take(int(pool.ID), 1) > 0; page++ {
		cs, total, err := pool.API.RecentChannels(ctx, page)
		if err != nil {
			return 0, err
		}
		for _, c := range cs {
			groups = append(groups, c.Group)
			if want[c.ID] {
				if rows[c.ID], err = rowJSON(c.Channel); err != nil {
					return 0, err
				}
			}
		}
		// A list too long to read whole is read no further than its first page.
		if whole = len(cs) == 0 || page*newapi.ChannelPageSize >= total; whole || total > statePages*newapi.ChannelPageSize {
			break
		}
	}
	if whole {
		if err := p.Store.SetPoolGroups(ctx, pool.ID, newapi.Groups(groups...)); err != nil {
			return 0, err
		}
	}
	// Beyond the pages read, or moved by a delete while they were read.
	var rest []int
	for _, pl := range ours {
		if _, ok := rows[pl.NewAPIID]; !ok {
			rest = append(rest, pl.NewAPIID)
		}
	}
	left, err = readStates(ctx, pool, rest, rows)
	return left, errors.Join(err, p.Store.CacheStates(ctx, pool.ID, rows))
}

// readStates reads ids of a pool without a database one at a time into rows,
// as far as stateRate allows: the row as JSON, or "" for an id new-api does not
// have. It stops at the first failure: the rest stay as last read. left is how
// many stateRate left unread.
func readStates(ctx context.Context, pool *connection.Pool, ids []int, rows map[int]string) (left int, err error) {
	for i, id := range ids {
		if stateRate.take(int(pool.ID), 1) == 0 {
			return len(ids) - i, nil
		}
		c, err := pool.API.Channel(ctx, id)
		if errors.Is(err, newapi.ErrNoChannel) {
			rows[id] = ""
			continue
		}
		if err == nil {
			rows[id], err = rowJSON(c)
		}
		if err != nil {
			return 0, fmt.Errorf("channel %d: %w", id, err)
		}
	}
	return 0, nil
}

func rowJSON(c newapi.Channel) (string, error) {
	raw, err := json.Marshal(c)
	return string(raw), err
}

func (p *Pusher) clear(ctx context.Context, j store.Job) error {
	if err := p.Store.ClearDirty(ctx, j); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err // ErrNotFound: flagged again meanwhile, the flags stay for the next round
	}
	return nil
}

// alignKeys gives j's placement the manually disabled keys (status 2) of the
// lead placement: the first created one, in a pool that is up, that is not
// itself behind; when every one is behind (each pool fell short at another
// key), the first created one, which all the others then follow. One whose keys
// new-api refuses to tell (deleted in its panel) hands the lead on to the next.
// Keys new-api disabled by itself (3) are left alone on both sides.
func (p *Pusher) alignKeys(ctx context.Context, pool *connection.Pool, j store.Job) error {
	m, err := p.Store.Channel(ctx, j.ChannelID, store.ChannelFilter{})
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var leads, behind []*store.Placement
	for i := range m.Placements {
		pl := &m.Placements[i]
		if q := p.Pools.Get(pl.PoolID); pl.State != "created" || q == nil || q.Reason != "" {
			continue
		}
		if pl.PoolID != pool.ID && !pl.KeysDirty {
			leads = append(leads, pl)
		} else {
			behind = append(behind, pl)
		}
	}
	for _, lead := range append(leads, behind...) {
		if lead.PoolID == pool.ID { // j's placement leads: the keys stay as the request left them
			return nil
		}
		ref := p.Pools.Get(lead.PoolID)
		want, err := ref.API.KeyStatuses(ctx, lead.NewAPIID)
		// Only a refusal of this channel (success=false, no HTTP error) moves the lead on;
		// a 401/403 or a failing new-api backs off, so a broken PAT cannot clear j's flag.
		if rej := (*newapi.Rejected)(nil); errors.As(err, &rej) && rej.Status == 0 {
			log.Printf("push worker: channel %d: keys of pool %d refused, the next placement leads: %v", j.ChannelID, ref.ID, err)
			continue
		}
		if err != nil {
			return callErr(ref, err)
		}
		return p.copyKeys(ctx, pool, j, want)
	}
	return nil // not reached: j's own placement is among them
}

// copyKeys enables and disables the keys of j's placement as want has them.
func (p *Pusher) copyKeys(ctx context.Context, pool *connection.Pool, j store.Job, want []newapi.KeyStatus) error {
	have, err := pool.API.KeyStatuses(ctx, j.NewAPIID)
	if err != nil {
		return callErr(pool, err)
	}
	for i := range min(len(want), len(have)) {
		w, h := want[i].Status, have[i].Status
		if w == h || w == 3 || h == 3 {
			continue
		}
		if err := write(pool.ID); err != nil {
			return err
		}
		if err := pool.API.SetKeyEnabled(ctx, j.NewAPIID, i, w == 1); err != nil {
			return callErr(pool, err)
		}
	}
	return nil
}

// audit records what became of a push, as the system.
func (p *Pusher) audit(ctx context.Context, j store.Job, ok bool, detail map[string]any) {
	detail["pool_id"], detail["remark"] = j.PoolID, j.Remark
	p.auditSystem(ctx, "channel.push", j.ChannelID, ok, detail)
}

func (p *Pusher) auditSystem(ctx context.Context, action string, channelID int, ok bool, detail map[string]any) {
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = []byte("{}")
	}
	if err := p.Store.InsertAudit(ctx, store.AuditLog{ActorType: "admin", ActorName: "system", Action: action, TargetType: "channel",
		TargetID: strconv.Itoa(channelID), Success: ok, Detail: raw}); err != nil {
		log.Printf("audit %s channel %d: %v", action, channelID, err)
	}
}
