// Package channels implements the channel API that the portal and the admin
// backend share: creating, listing, renaming, testing, enabling, refreshing the
// balance of and soft-deleting channels, each limited to the caller's scope. A
// channel is recorded here first and pushed to the new-api pools of its
// template by the push worker; writes go through each pool's management API and
// reads through its read-only database. No response ever contains a channel key.
package channels

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/connection"
	"newapisub/internal/httpx"
	"newapisub/internal/newapi"
	"newapisub/internal/secret"
	"newapisub/internal/store"
)

// Service serves the channel routes of one process.
type Service struct {
	Store  *store.Store
	Pools  *connection.Pools
	Box    *secret.Box // seals submitted keys for the push worker
	Audit  audit.Recorder
	Portal bool // the caller is a portal user rather than an admin

	exports        sync.Map     // operator id -> running log export
	exportsRunning atomic.Int32 // log exports running in this process
}

// Register mounts the channel routes and their logs behind req. Restore, purge
// and the placement routes exist only in the admin backend.
func (s *Service) Register(mux *http.ServeMux, req func(http.Handler) http.Handler) {
	s.registerLogs(mux, req)
	a := func(action string, fn audit.Handler) http.Handler { return req(s.Audit.Wrap(action, fn)) }
	mux.Handle("GET /api/channel-types", req(s.Audit.View("channel_types", s.types)))
	mux.Handle("GET /api/pool-names", req(httpx.Handler(s.poolNames)))
	mux.Handle("GET /api/channels", req(s.Audit.View("channels", s.list)))
	mux.Handle("POST /api/channels", a("channel.create", s.create))
	mux.Handle("PUT /api/channels/{id}", a("channel.update", s.update))
	mux.Handle("POST /api/channels/{id}/test", a("channel.test", s.test))
	mux.Handle("POST /api/channels/{id}/status", a("channel.status", s.setStatus))
	mux.Handle("POST /api/channels/{id}/balance", a("channel.balance", s.balance))
	s.registerKeys(mux, a)
	softDelete := a("channel.delete", s.softDelete)
	if s.Portal {
		mux.Handle("DELETE /api/channels/{id}", softDelete)
		return
	}
	mux.Handle("POST /api/channels/{id}/restore", a("channel.restore", s.restore))
	mux.Handle("POST /api/channels/{id}/placements/{pool_id}/retry", a("channel.placement_retry", s.retryPlacement))
	mux.Handle("DELETE /api/channels/{id}/placements/{pool_id}", a("channel.placement_cancel", s.cancelPlacement))
	purge := a("channel.purge", s.purge)
	mux.HandleFunc("DELETE /api/channels/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("purge") == "1" {
			purge.ServeHTTP(w, r)
			return
		}
		softDelete.ServeHTTP(w, r)
	})
}

func (s *Service) isSuper(r *http.Request) bool {
	return !s.Portal && auth.Role(r.Context()) == store.RoleSuper
}

// scope is everything the caller may see, soft-deleted channels included: a
// portal user's channels, a regular admin's own keys and their users'
// channels, or all of them for a super.
func (s *Service) scope(r *http.Request) store.ChannelFilter {
	switch id := auth.UserID(r.Context()); {
	case s.Portal:
		return store.ChannelFilter{UserID: id}
	case s.isSuper(r):
		return store.ChannelFilter{}
	default:
		return store.ChannelFilter{AdminID: id}
	}
}

// narrowed is scope plus the admin-only filters: ?user_id=, where 0 selects
// keys added by admins themselves, and ?owner=self, the keys the caller added.
func (s *Service) narrowed(r *http.Request) (store.ChannelFilter, error) {
	f := s.scope(r)
	if s.Portal {
		return f, nil
	}
	switch r.URL.Query().Get("owner") {
	case "":
	case "self":
		f.AdminID, f.OwnKeys = auth.UserID(r.Context()), true
	default:
		return f, httpx.Errorf(http.StatusBadRequest, "owner 只能为 self")
	}
	raw := r.URL.Query().Get("user_id")
	if raw == "" {
		return f, nil
	}
	uid, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || uid < 0 {
		return f, httpx.Errorf(http.StatusBadRequest, "无效的 user_id")
	}
	if uid == 0 {
		f.OwnKeys = true
	} else {
		f.UserID = uid
	}
	return f, nil
}

// types lists what each enabled template fixes, so a channel type with several
// appears as many times. Only a super gets the whole extra: it can hold secrets,
// and the server uses it from the template whatever the form shows.
// poolNames lists every pool by id and name, for the pool filter of any role;
// the names already show on each log row.
func (s *Service) poolNames(w http.ResponseWriter, r *http.Request) error {
	type view struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	pools := s.Pools.List()
	out := make([]view, len(pools))
	for i, p := range pools {
		out[i] = view{p.ID, p.Name}
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Service) types(w http.ResponseWriter, r *http.Request) error {
	ts, err := s.Store.ListTemplates(r.Context(), true)
	if err != nil {
		return err
	}
	type view struct {
		Type               int    `json:"type"`
		TemplateID         int64  `json:"template_id"`
		TemplateName       string `json:"template_name"`
		BaseURL            string `json:"base_url"`
		AllowCustomBaseURL bool   `json:"allow_custom_base_url"`
		Models             string `json:"models"`
		Group              string `json:"group"`
		Priority           int64  `json:"priority"`
		Weight             int    `json:"weight"`
		TestModel          string `json:"test_model"`
		Extra              any    `json:"extra"`
	}
	out := make([]view, len(ts))
	for i, t := range ts {
		var extra any = t.Extra
		if !s.isSuper(r) {
			x, err := newapi.ParseExtra(t.Extra)
			if err != nil { // it was checked when the template was saved
				return err
			}
			extra = x.Public()
		}
		out[i] = view{t.ChannelType, t.ID, t.Name, t.BaseURL, t.AllowCustomBaseURL, t.Models, t.Group, t.Priority, t.Weight, t.TestModel, extra}
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type owner struct {
	UserID        int64  `json:"user_id"`
	Username      string `json:"username"`
	AdminID       int64  `json:"admin_id"`
	AdminUsername string `json:"admin_username"`
}

// listItem is a channel as the pages show it: the new-api row merged over its
// pools (merged) plus what only this system knows about it. BalanceFromKeys
// says Balance is the total of the stored key balances of a multi-key channel,
// which new-api cannot query.
type listItem struct {
	newapi.Channel
	BalanceFromKeys bool            `json:"balance_from_keys"`
	Exists          bool            `json:"exists"`
	DeletedAt       *int64          `json:"deleted_at"`
	TemplateID      *int64          `json:"template_id"`
	Owner           *owner          `json:"owner,omitempty"`
	PushState       string          `json:"push_state"`
	Placements      []placementView `json:"placements"`
	// StatusPending: disabled here, and some pool still has it enabled until
	// the push worker gets there.
	StatusPending bool `json:"status_pending,omitempty"`
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	f, err := s.narrowed(r)
	if err != nil {
		return err
	}
	if s.Portal {
		f.ActiveOnly = true
	} else {
		switch r.URL.Query().Get("view") {
		case "", "active":
			f.ActiveOnly = true
		case "deleted":
			f.DeletedOnly = true
		default:
			return httpx.Errorf(http.StatusBadRequest, "view 只能为 active 或 deleted")
		}
	}
	ms, err := s.Store.Channels(r.Context(), f)
	if err != nil {
		return err
	}
	rows, down := s.rows(r.Context(), ms)
	totals, err := s.Store.KeyBalanceTotals(r.Context(), store.IDs(ms))
	if err != nil {
		return err
	}
	out := make([]listItem, len(ms))
	for i, m := range ms {
		c, exists := s.merged(resolve(s, m, rows, down))
		out[i] = listItem{Channel: c, Exists: exists, DeletedAt: m.DeletedAt, TemplateID: m.TemplateID,
			PushState: pushState(m.Placements), Placements: s.placementsView(r, m.Placements)}
		if m.WantStatus == 2 && c.Status == 1 && slices.ContainsFunc(m.Placements, func(p store.Placement) bool { return p.StatusDirty }) {
			out[i].Status, out[i].StatusPending = 2, true
		}
		if t, found := totals[c.ID]; found && c.MultiKeySize() > 0 {
			out[i].Balance, out[i].BalanceUpdatedTime, out[i].BalanceFromKeys = t.Balance, t.CheckedAt, true
		}
		if !s.Portal {
			out[i].Owner = &owner{m.UserID, m.Username, m.AdminID, m.AdminUsername}
		}
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// lookup resolves {id} to a channel in the caller's scope (404 otherwise) and
// its new-api rows.
func (s *Service) lookup(r *http.Request, ev *audit.Event) (target, error) {
	ev.TargetType, ev.TargetID = "channel", r.PathValue("id")
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return target{}, err
	}
	if id > math.MaxInt32 { // portal_channels.newapi_channel_id is INT
		return target{}, httpx.Errorf(http.StatusNotFound, "渠道不存在")
	}
	m, err := s.Store.Channel(r.Context(), int(id), s.scope(r))
	if errors.Is(err, store.ErrNotFound) {
		return target{}, httpx.Errorf(http.StatusNotFound, "渠道不存在")
	}
	if err != nil {
		return target{}, err
	}
	ev.Detail["name"] = m.Name
	if !s.Portal {
		ev.Detail["owner"] = owner{m.UserID, m.Username, m.AdminID, m.AdminUsername}
	}
	rows, down := s.rows(r.Context(), []store.Mapping{m})
	return resolve(s, m, rows, down), nil
}

// operable is lookup for operations on active channels: a soft-deleted channel
// is 404 even to admins, who must restore it first.
func (s *Service) operable(r *http.Request, ev *audit.Event) (target, error) {
	t, err := s.lookup(r, ev)
	if err == nil && t.m.DeletedAt != nil {
		err = httpx.Errorf(http.StatusNotFound, "渠道不存在")
	}
	return t, err
}

// UpstreamErr turns a failed new-api call into a 502. new-api's own refusal,
// from which the client has hidden any quoted key, is passed on so the user
// sees why (for example that the channel type has no balance query). Any other
// failure names new-api's internal address, and this text reaches users and the
// audit log they read, so the full error goes to the server log only.
func UpstreamErr(err error) error {
	var rej *newapi.Rejected
	if errors.As(err, &rej) {
		return httpx.Errorf(http.StatusBadGateway, "%s", rej.Message)
	}
	log.Printf("new-api call failed: %v", err)
	if strings.Contains(err.Error(), ": HTTP 429:") {
		return httpx.Errorf(http.StatusBadGateway, "new-api 限流，请约 3 分钟后再试")
	}
	return httpx.Errorf(http.StatusBadGateway, "new-api 暂时不可用")
}

// ownTemplate is the template a channel's models are chosen from: its own while
// enabled, else the only enabled template of the type, if there is just one.
func ownTemplate(enabled []store.Template, id *int64) (store.Template, bool) {
	if id != nil {
		if i := slices.IndexFunc(enabled, func(t store.Template) bool { return t.ID == *id }); i >= 0 {
			return enabled[i], true
		}
	}
	if len(enabled) == 1 {
		return enabled[0], true
	}
	return store.Template{}, false
}

// update changes a channel's name and models, the only fields anyone may edit
// after creation. Models left as they are need no template, so a channel keeps
// its name editable after its template shrank or went away. Pools it does not
// reach now, or not yet, get it from the push worker.
func (s *Service) update(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	t, err := s.operable(r, ev)
	if err != nil {
		return err
	}
	if t.gone() {
		return errGone
	}
	var req struct {
		Name   string  `json:"name"`
		Models *string `json:"models"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	name, err := validName(req.Name)
	if err != nil {
		return err
	}
	sp := parseSpec(t.m.Spec)
	c := newapi.Channel{Type: sp.Type, Name: t.m.Name, Models: sp.Models, TestModel: &sp.TestModel}
	if len(t.lives) > 0 {
		c = t.lives[0].c
	}
	models := c.Models
	if req.Models != nil && !sameModels(*req.Models, c.Models) {
		enabled, err := s.Store.EnabledTemplates(r.Context(), c.Type)
		if err != nil {
			return err
		}
		tpl, ok := ownTemplate(enabled, t.m.TemplateID)
		if !ok {
			return httpx.Errorf(http.StatusBadRequest, "该渠道的模板已停用，只能修改名称")
		}
		if models, err = pickModels(tpl.Models, *req.Models); err != nil {
			return err
		}
	}
	changes := map[string]any{"name": map[string]any{"from": c.Name, "to": name},
		"models": map[string]any{"from": c.Models, "to": models}}
	// new-api's scheduled test would go on testing a model the channel gave up, and
	// could disable the channel for it.
	current, testModel := "", ""
	if c.TestModel != nil {
		current = *c.TestModel
	}
	if selected := ModelList(models); current != "" && len(selected) > 0 && !slices.Contains(selected, current) {
		testModel = selected[0]
		changes["test_model"] = map[string]any{"from": current, "to": testModel}
	}
	ev.Detail["changes"] = changes
	sp.Models, sp.TestModel = models, cmp.Or(testModel, current)
	raw, err := json.Marshal(sp)
	if err != nil {
		return err
	}
	// The cross-system write must not be abandoned halfway if the client disconnects.
	ctx := context.WithoutCancel(r.Context())
	pending, err := t.fanOut(func(l live) error {
		if err := l.pool.API.UpdateChannel(ctx, l.p.NewAPIID, name, models, testModel); err != nil {
			return UpstreamErr(err)
		}
		recache(ctx, s.Store, l.pool, l.p.NewAPIID, l.c, func(c *newapi.Channel) {
			c.Name, c.Models = name, models
			if testModel != "" {
				c.TestModel = &testModel
			}
		})
		return nil
	})
	if t.failed(pending, err) {
		return err
	}
	err = s.Store.SetSpec(ctx, t.m.ChannelID, name, raw)
	if errors.Is(err, store.ErrNotFound) { // lost a race with a delete
		return httpx.Errorf(http.StatusNotFound, "渠道不存在")
	}
	if err != nil {
		return err
	}
	return fannedOut(w, pending)
}

// sameModels compares two model lists as sets. Empty requested lists are never
// the same, so they fail as selecting nothing.
func sameModels(requested, current string) bool {
	a, b := ModelList(requested), ModelList(current)
	slices.Sort(a)
	slices.Sort(b)
	return len(a) > 0 && slices.Equal(a, b)
}

func (s *Service) test(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	t, err := s.operable(r, ev)
	if err != nil {
		return err
	}
	l, err := s.lead(r, t)
	if err != nil {
		return err
	}
	var opts newapi.TestOptions
	if r.ContentLength != 0 { // the parameters are optional, and so is a body holding them
		if err := httpx.Decode(w, r, &opts); err != nil {
			return err
		}
	}
	ok, msg, secs, err := l.pool.API.TestChannel(r.Context(), l.p.NewAPIID, opts)
	if err != nil {
		return UpstreamErr(err)
	}
	if !s.Portal {
		ev.Detail["pool_id"] = l.pool.ID
	}
	ev.Detail["model"] = opts.Model
	ev.Detail["result"] = ok
	ev.Failed = !ok
	ev.Detail["message"] = msg
	ev.Detail["time"] = secs
	httpx.JSON(w, http.StatusOK, map[string]any{"success": ok, "message": msg, "time": secs})
	return nil
}

// setStatus sets the status in the pools that have the channel, then records
// it as what every placement is to have: pushes still to come create it so,
// and the push worker sets it where it did not go through.
func (s *Service) setStatus(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	t, err := s.operable(r, ev)
	if err != nil {
		return err
	}
	if t.gone() {
		return errGone
	}
	status, err := httpx.DecodeStatus(w, r)
	if err != nil {
		return err
	}
	c, _ := s.merged(t)
	ev.Detail["from"], ev.Detail["to"] = c.Status, status
	ctx := context.WithoutCancel(r.Context())
	pending, err := t.fanOut(func(l live) error { return s.applyStatus(ctx, l, status) })
	if t.failed(pending, err) {
		return err
	}
	// shortcut: enabling while a soft delete of the same channel runs can leave it deleted yet enabled, recheck the mark after this call if that ever happens.
	if err := s.Store.SetWantStatus(ctx, t.m.ChannelID, status); err != nil {
		return err
	}
	return fannedOut(w, pending)
}

// applyStatus sets the status in one pool and confirms it took effect: new-api
// reports success without doing anything for a channel its memory cache has
// not learned yet (SYNC_FREQUENCY). l.c is the row read before the call.
func (s *Service) applyStatus(ctx context.Context, l live, status int) error {
	changed, err := l.pool.API.SetStatus(ctx, l.p.NewAPIID, status)
	if err != nil {
		return UpstreamErr(err)
	}
	if !changed && l.c.Status != status {
		return httpx.Errorf(http.StatusConflict, "渠道刚创建，new-api 尚未同步，请稍后再试")
	}
	recache(ctx, s.Store, l.pool, l.p.NewAPIID, l.c, func(c *newapi.Channel) { c.Status = status })
	return nil
}

func (s *Service) balance(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	t, err := s.operable(r, ev)
	if err != nil {
		return err
	}
	l, err := s.lead(r, t)
	if err != nil {
		return err
	}
	if l.c.MultiKeySize() > 0 { // new-api refuses these
		return httpx.Errorf(http.StatusBadRequest, "多 Key 渠道请在「管理 Key」中按 Key 查询余额")
	}
	res, err := l.pool.API.UpdateBalance(r.Context(), l.p.NewAPIID)
	if err != nil {
		// new-api answers from a memory cache that learns new channels on its next
		// sync (SYNC_FREQUENCY); lead already confirmed the row exists.
		var rej *newapi.Rejected
		if errors.As(err, &rej) && strings.Contains(rej.Message, "no longer exists") {
			return httpx.Errorf(http.StatusConflict, "渠道刚创建，new-api 尚未同步，请稍后再刷新余额")
		}
		return UpstreamErr(err)
	}
	if res.Balance != nil {
		recache(r.Context(), s.Store, l.pool, l.p.NewAPIID, l.c, func(c *newapi.Channel) {
			c.Balance, c.BalanceUpdatedTime = *res.Balance, time.Now().Unix()
		})
	}
	ev.Detail["balance"] = res.Balance
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// softDelete only marks the channel, so its logs and stats stay available for
// reconciliation and an admin can restore it. It is disabled everywhere first
// because new-api knows nothing of the mark and would keep routing to it; what
// is still being pushed is created disabled, and a pool not reached now is
// disabled by the push worker. It never calls new-api's DELETE.
func (s *Service) softDelete(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	t, err := s.operable(r, ev)
	if err != nil {
		return err
	}
	ev.Detail["orphan"] = len(t.lives) == 0
	ctx := context.WithoutCancel(r.Context())
	if len(t.lives) > 0 {
		c, _ := s.merged(t)
		ev.Detail["from"] = c.Status
	}
	pending, err := t.fanOut(func(l live) error {
		if l.c.Status == 2 { // also replaces auto-disabled (3), which new-api may enable again by itself
			return nil
		}
		return s.applyStatus(ctx, l, 2)
	})
	if t.failed(pending, err) { // hidden while still routed to
		return err
	}
	if err := s.Store.SetWantStatus(ctx, t.m.ChannelID, 2); err != nil {
		return err
	}
	err = s.Store.SoftDeleteChannel(ctx, t.m.ChannelID, s.Audit.ActorType+":"+ev.ActorName)
	if errors.Is(err, store.ErrNotFound) { // lost a race with another delete
		return httpx.Errorf(http.StatusNotFound, "渠道不存在")
	}
	if err != nil {
		return err
	}
	return fannedOut(w, pending)
}

// restore undoes a soft delete. The channel stays disabled until someone
// enables it.
func (s *Service) restore(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	t, err := s.lookup(r, ev)
	if err != nil {
		return err
	}
	ev.Detail["orphan"] = len(t.lives) == 0
	err = s.Store.RestoreChannel(r.Context(), t.m.ChannelID)
	if errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusConflict, "渠道未被删除")
	}
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// purge removes the record of a channel no pool has any more, with its
// placements: one still being pushed is deleted again by the push worker. It
// never calls new-api's DELETE, and channels still there keep their record for
// reconciliation.
func (s *Service) purge(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	t, err := s.lookup(r, ev)
	if err != nil {
		return err
	}
	switch {
	case len(t.lives) > 0:
		return httpx.Errorf(http.StatusConflict, "渠道仍在 new-api 中，不能清理")
	case len(t.down) > 0:
		return errPoolDown
	}
	if err := s.Store.DeleteMapping(r.Context(), t.m.ChannelID); errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusNotFound, "渠道不存在")
	} else if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
