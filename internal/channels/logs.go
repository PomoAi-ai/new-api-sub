package channels

import (
	"cmp"
	"context"
	"log"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"newapisub/internal/auth"
	"newapisub/internal/connection"
	"newapisub/internal/httpx"
	"newapisub/internal/newapi"
	"newapisub/internal/store"
)

// syncedLogSpan is how far back a pool that syncs its logs has them.
const syncedLogSpan = 30 * 86400

// registerLogs mounts the read side of the pools' logs. The log pages are open
// to every role unless a portal user's log scope says otherwise (logView), the
// stats limited by logFilter or restrict; only a super gets end-user data from
// either.
func (s *Service) registerLogs(mux *http.ServeMux, req func(http.Handler) http.Handler) {
	v := func(name string, fn httpx.Handler) http.Handler { return req(s.Audit.View(name, fn)) }
	mux.Handle("GET /api/logs", v("logs", s.logs))
	mux.Handle("GET /api/logs/stat", v("log_stat", s.logStat))
	mux.Handle("GET /api/logs/groups", v("log_groups", s.logGroups))
	mux.Handle("GET /api/logs/export", req(s.Audit.Wrap("log.export", s.exportLogs)))
	mux.Handle("GET /api/stats", v("stats", s.stats))
	mux.Handle("GET /api/stats/perf", v("stats_perf", s.perf))
	mux.Handle("GET /api/stats/flow", v("stats_flow", s.flow))
	mux.Handle("GET /api/stats/users", v("stats_users", s.userStats))
}

type degradedPool struct {
	PoolID int64  `json:"pool_id"`
	Name   string `json:"name,omitempty"`
	Reason string `json:"reason"`
}

type retention struct {
	Since   int64   `json:"since"`
	PoolIDs []int64 `json:"pool_ids"`
}

// notices go next to the rows of an answer: the pools left out of it, and
// those with nothing before since.
type notices struct {
	Degraded  []degradedPool `json:"degraded,omitempty"`
	Retention *retention     `json:"retention,omitempty"`
}

// poolQuery is the part of a log query one pool answers. f holds that pool's
// own channel ids; factor turns its quota into the home pool's.
type poolQuery struct {
	pool   *connection.Pool
	f      newapi.Filter
	factor float64
}

// logPlan is a log query cut into pools.
type logPlan struct {
	qs    []poolQuery
	index map[store.PoolChannel]int // a pool's new-api channel -> its local id
	names map[int]string            // local id -> name
	mu    sync.Mutex
	notices
	portal bool // the pool's own reason is for the admins only
}

func (q *logPlan) degrade(p *connection.Pool, reason string) {
	if q.portal {
		reason = "号池暂时不可用"
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.Degraded = append(q.Degraded, degradedPool{p.ID, p.Name, reason})
}

// local is the local id of a pool's new-api channel, 0 for one that is not
// ours: its id there is someone else's and must not pass for one of ours.
func (q *logPlan) local(poolID int64, id int) int {
	return q.index[store.PoolChannel{PoolID: poolID, NewAPIID: id}]
}

// split cuts f, whose channel ids are local, into a query per pool, or for
// ?pool_id= into that pool's alone, for every role. Only the
// open log pages asking for no channel in particular take a pool whole; any
// other query takes only the new-api ids its channels have there, never an id
// as it came. ms are the channels f may name, and those its rows can be. A pool
// that cannot answer, or whose quota cannot be converted, is a notice.
func (s *Service) split(r *http.Request, f newapi.Filter, ms []store.Mapping) (*logPlan, error) {
	var only int64
	if raw := r.URL.Query().Get("pool_id"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			return nil, httpx.Errorf(http.StatusBadRequest, "无效的 pool_id")
		}
		// The portal knows only the pools its channels are in.
		if s.Pools.Get(n) == nil || s.Portal && len(store.InPool(ms, n)) == 0 {
			return nil, httpx.Errorf(http.StatusBadRequest, "号池不存在")
		}
		only = n
	}
	q := &logPlan{index: store.PlacementIndex(ms), names: make(map[int]string, len(ms)), portal: s.Portal}
	asked := make(map[int]bool, len(f.ChannelIDs))
	for _, id := range f.ChannelIDs {
		asked[id] = true
	}
	for _, m := range ms {
		q.names[m.ChannelID] = m.Name
	}
	var synced []int64
	for _, p := range s.Pools.List() {
		if only != 0 && p.ID != only {
			continue
		}
		var ids []int
		for _, m := range ms {
			if asked[m.ChannelID] {
				ids = append(ids, store.InPool([]store.Mapping{m}, p.ID)...)
			}
		}
		pf, ok := f.ForPool(ids)
		if !ok {
			continue
		}
		factor, known := s.factor(p)
		switch {
		case p.Reason != "":
			q.degrade(p, p.Reason)
		case p.Logs == nil:
			q.degrade(p, "号池没有可读取的日志")
		case !known:
			q.degrade(p, "号池的额度单位未知")
		default:
			q.qs = append(q.qs, poolQuery{p, pf, factor})
			if p.LogMode == "sync" {
				synced = append(synced, p.ID)
			}
		}
	}
	if now := time.Now().Unix(); len(synced) > 0 && f.Start < now-syncedLogSpan {
		q.Retention = &retention{now - syncedLogSpan, synced}
	}
	return q, nil
}

// ask runs fn on every pool of q at once and returns the answers of those
// that gave one; the others become notices.
func ask[T any](ctx context.Context, q *logPlan, fn func(context.Context, poolQuery) (T, error)) []T {
	res, errs := each(q.qs, func(pq poolQuery) (T, error) { return fn(ctx, pq) })
	var out []T
	for i, err := range errs {
		if err != nil {
			log.Printf("logs of pool %d: %v", q.qs[i].pool.ID, err)
			q.degrade(q.qs[i].pool, "号池查询失败")
			continue
		}
		out = append(out, res[i])
	}
	return out
}

// restrict limits f to the caller's channels (soft-deleted ones keep their
// history for reconciliation) and returns them.
func (s *Service) restrict(r *http.Request, f *newapi.Filter) ([]store.Mapping, error) {
	scope, err := s.narrowed(r)
	if err != nil {
		return nil, err
	}
	ms, err := s.Store.Channels(r.Context(), scope)
	if err != nil {
		return nil, err
	}
	if err := f.Restrict(store.IDs(ms)); err != nil {
		return nil, httpx.Errorf(http.StatusNotFound, "渠道不存在")
	}
	return ms, nil
}

// logFilter parses the log/stats query, limited to the caller's channels. Only
// a super may search by end user: for anyone else a result would confirm that
// the name exists, so the search is dropped.
func (s *Service) logFilter(r *http.Request) (newapi.Filter, *logPlan, error) {
	f, err := newapi.ParseFilter(r.URL.Query())
	if err != nil {
		return f, nil, httpx.Errorf(http.StatusBadRequest, "%s", err.Error())
	}
	if !s.isSuper(r) {
		f.DropEndUserSearch()
	}
	ms, err := s.restrict(r, &f)
	if err != nil {
		return f, nil, err
	}
	q, err := s.split(r, f, ms)
	return f, q, err
}

// selfLogs says whether the caller is held to its own channels' logs. Only portal
// users have a log scope. A scope that cannot be read is an error, not all logs.
func (s *Service) selfLogs(r *http.Request) (bool, error) {
	if !s.Portal {
		return false, nil
	}
	scope, err := s.Store.EffectiveLogScope(r.Context(), auth.UserID(r.Context()))
	return scope == store.LogScopeSelf, err
}

// logView parses the query of the log pages. Every role reads all logs, which
// hide the end users from anyone but a super, unless its log scope forbids it.
// owner and user_id ask for less: the channels of the caller, or of a user
// within its scope.
func (s *Service) logView(r *http.Request) (newapi.Filter, *logPlan, error) {
	v := r.URL.Query()
	f, err := newapi.ParseFilter(v)
	if err != nil {
		return f, nil, httpx.Errorf(http.StatusBadRequest, "%s", err.Error())
	}
	if !s.isSuper(r) {
		f.DropEndUserSearch()
	}
	self, err := s.selfLogs(r)
	if err != nil {
		return f, nil, err
	}
	var ms []store.Mapping
	if self || v.Has("owner") || v.Has("user_id") {
		ms, err = s.restrict(r, &f)
	} else {
		f.AllChannels = true
		ms, err = s.Store.Channels(r.Context(), store.ChannelFilter{})
	}
	if err != nil {
		return f, nil, err
	}
	q, err := s.split(r, f, ms)
	return f, q, err
}

// logItem is a log row as the pages show it, with its pool: by local channel
// id, which is 0 for a channel that is not ours. Only a super sees such a
// channel's id in its pool.
type logItem struct {
	newapi.Log
	NewAPIChannelID int `json:"newapi_channel_id,omitempty"`
}

func (s *Service) logItem(r *http.Request, q *logPlan, l newapi.Log) logItem {
	it := logItem{Log: l}
	if it.ChannelID = q.local(l.PoolID, l.ChannelID); it.ChannelID == 0 && s.isSuper(r) {
		it.NewAPIChannelID = l.ChannelID
	}
	if it.ChannelID == 0 && s.Portal { // what another party named its channel is for the admins only
		it.ChannelName = ""
	}
	if p := s.Pools.Get(l.PoolID); p != nil {
		it.PoolName = p.Name
	}
	return it
}

func (s *Service) logs(w http.ResponseWriter, r *http.Request) error {
	f, q, err := s.logView(r)
	if err != nil {
		return err
	}
	// One pool pages by itself; several are each read from their first row and merged.
	offset, size, merge := f.Offset(), f.PageSize, len(q.qs) > 1
	if merge {
		if offset+size > newapi.MaxMergeRows {
			return httpx.Errorf(http.StatusBadRequest, "翻页太深，请缩小时间范围或加筛选条件")
		}
		for i := range q.qs {
			q.qs[i].f.Page, q.qs[i].f.PageSize = 1, offset+size
		}
	}
	type part struct {
		items []newapi.Log
		total int64
	}
	full := s.isSuper(r)
	parts := ask(r.Context(), q, func(ctx context.Context, pq poolQuery) (part, error) {
		items, total, err := pq.pool.Logs.Logs(ctx, pq.f, full)
		if err != nil {
			return part{}, err
		}
		names, err := pq.pool.Logs.ChannelNames(ctx)
		if err != nil {
			return part{}, err
		}
		for i := range items {
			l := &items[i]
			l.PoolID, l.ChannelName, l.Quota = pq.pool.ID, cmp.Or(names[l.ChannelID], l.ChannelName), scale(l.Quota, pq.factor) // a sync reader names them itself
		}
		return part{items, total}, nil
	})
	var lists [][]newapi.Log
	var total int64
	for _, p := range parts {
		lists, total = append(lists, p.items), total+p.total
	}
	items := []newapi.Log{}
	if merge {
		items = newapi.MergeLogs(lists, offset, size)
	} else if len(lists) == 1 {
		items = lists[0]
	}
	out := make([]logItem, len(items))
	for i, l := range items {
		out[i] = s.logItem(r, q, l)
	}
	httpx.JSON(w, http.StatusOK, struct {
		Items []logItem `json:"items"`
		Total int64     `json:"total"`
		notices
	}{out, total, q.notices})
	return nil
}

// logGroups feeds the group filter of callers who may not read new-api's list.
func (s *Service) logGroups(w http.ResponseWriter, r *http.Request) error {
	self, err := s.selfLogs(r)
	if err != nil {
		return err
	}
	var ms []store.Mapping
	if self {
		if ms, err = s.Store.Channels(r.Context(), s.scope(r)); err != nil {
			return err
		}
	}
	pools := s.Pools.List()
	found, errs := each(pools, func(p *connection.Pool) ([]string, error) {
		switch {
		case p.Reason != "":
			return nil, nil
		case p.DB == nil && !self: // as the push worker last read them
			return s.Store.PoolGroups(r.Context(), p.ID)
		case p.DB == nil:
			rows, err := cachedRows(ms, p.ID)
			var lists []string
			for _, c := range rows {
				lists = append(lists, c.Group)
			}
			return newapi.Groups(lists...), err
		case !self:
			return p.DB.AllChannelGroups(r.Context())
		}
		ids := store.InPool(ms, p.ID)
		if len(ids) == 0 {
			return nil, nil
		}
		return p.DB.ChannelGroups(r.Context(), ids)
	})
	groups := []string{}
	for i, g := range found {
		if errs[i] != nil {
			log.Printf("groups of pool %d: %v", pools[i].ID, errs[i])
		}
		groups = append(groups, g...)
	}
	slices.Sort(groups)
	httpx.JSON(w, http.StatusOK, map[string]any{"groups": slices.Compact(groups)})
	return nil
}

func (s *Service) logStat(w http.ResponseWriter, r *http.Request) error {
	_, q, err := s.logView(r)
	if err != nil {
		return err
	}
	synced := []int64{} // minutes behind: the rates count the pools read directly
	for _, pq := range q.qs {
		if pq.pool.LogMode == "sync" {
			synced = append(synced, pq.pool.ID)
		}
	}
	parts := ask(r.Context(), q, func(ctx context.Context, pq poolQuery) (newapi.Usage, error) {
		u, err := pq.pool.Logs.Usage(ctx, pq.f)
		u.Quota = scale(u.Quota, pq.factor)
		if pq.pool.LogMode == "sync" {
			u.Tokens, u.Spans = 0, nil
		}
		return u, err
	})
	httpx.JSON(w, http.StatusOK, struct {
		newapi.LogStat
		SyncedPoolIDs []int64 `json:"synced_pool_ids"` // left out of rpm, tpm and concurrency
		notices
	}{newapi.SumUsage(parts, time.Now().Unix()), synced, q.notices})
	return nil
}

// statRows answers rows of the stats routes.
func statRows[T any](w http.ResponseWriter, rows []T, q *logPlan) error {
	httpx.JSON(w, http.StatusOK, struct {
		Rows []T `json:"rows"`
		notices
	}{rows, q.notices})
	return nil
}

// pooled is the stats rows of one pool.
type pooled[T any] struct {
	Row      T
	PoolID   int64
	PoolName string
}

// statPoolRow and flowPoolRow are rows of one pool, for split=pool and by=pool.
type statPoolRow struct {
	newapi.StatRow
	PoolID   int64  `json:"pool_id"`
	PoolName string `json:"pool_name"`
}

type flowPoolRow struct {
	newapi.FlowRow
	PoolID   int64  `json:"pool_id"`
	PoolName string `json:"pool_name"`
}

// splitPool reads ?split=pool: rows of each pool apart rather than added up.
func splitPool(r *http.Request) (bool, error) {
	switch r.URL.Query().Get("split") {
	case "":
		return false, nil
	case "pool":
		return true, nil
	}
	return false, httpx.Errorf(http.StatusBadRequest, "split 只能为 pool")
}

// perPool answers the rows of each pool apart, biggest first by quota.
func perPool[T, O any](w http.ResponseWriter, q *logPlan, parts []pooled[[]T], wrap func(T, int64, string) O, quota func(O) int64) error {
	rows := []O{}
	for _, p := range parts {
		for _, r := range p.Row {
			rows = append(rows, wrap(r, p.PoolID, p.PoolName))
		}
	}
	slices.SortStableFunc(rows, func(a, b O) int { return cmp.Compare(quota(b), quota(a)) })
	return statRows(w, rows, q)
}

func statPool(r newapi.StatRow, id int64, name string) statPoolRow { return statPoolRow{r, id, name} }
func statQuota(r statPoolRow) int64                                { return r.Quota }

// stats is per hour and model; with by=channel the range's total per channel,
// with split=pool too per pool; with by=pool the range's total per pool.
func (s *Service) stats(w http.ResponseWriter, r *http.Request) error {
	by := r.URL.Query().Get("by")
	if by != "" && by != "channel" && by != "pool" {
		return httpx.Errorf(http.StatusBadRequest, "by 只能为 channel 或 pool")
	}
	split, err := splitPool(r)
	if err != nil {
		return err
	}
	f, q, err := s.logFilter(r)
	if err != nil {
		return err
	}
	parts := ask(r.Context(), q, func(ctx context.Context, pq poolQuery) (pooled[[]newapi.StatRow], error) {
		var rows []newapi.StatRow
		var err error
		if by == "" {
			rows, err = pq.pool.Logs.Stats(ctx, pq.f)
		} else {
			rows, err = pq.pool.Logs.ChannelStats(ctx, pq.f)
		}
		for i := range rows {
			rows[i].Quota = scale(rows[i].Quota, pq.factor)
			if by != "" {
				rows[i].ChannelID = q.local(pq.pool.ID, rows[i].ChannelID)
				rows[i].ChannelName = q.names[rows[i].ChannelID]
			}
		}
		return pooled[[]newapi.StatRow]{rows, pq.pool.ID, pq.pool.Name}, err
	})
	rowsOf := make([][]newapi.StatRow, len(parts))
	for i, p := range parts {
		rowsOf[i] = p.Row
	}
	switch {
	case by == "":
		return statRows(w, newapi.MergeStats(rowsOf), q)
	case by == "channel" && !split:
		return statRows(w, newapi.MergeChannelStats(rowsOf), q)
	case by == "channel":
		return perPool(w, q, parts, statPool, statQuota)
	}
	totals := make([]pooled[[]newapi.StatRow], len(parts))
	for i, p := range parts {
		var sum newapi.StatRow
		for _, r := range p.Row {
			sum.Count, sum.Quota, sum.TokenUsed = sum.Count+r.Count, sum.Quota+r.Quota, sum.TokenUsed+r.TokenUsed
			sum.CacheReadTokens, sum.CacheCreationTokens = sum.CacheReadTokens+r.CacheReadTokens, sum.CacheCreationTokens+r.CacheCreationTokens
		}
		sum.CreatedAt = f.Start
		totals[i] = pooled[[]newapi.StatRow]{[]newapi.StatRow{sum}, p.PoolID, p.PoolName}
	}
	return perPool(w, q, totals, statPool, statQuota)
}

// perf covers a window of its own rather than the page's range, as new-api's
// performance summary does.
func (s *Service) perf(w http.ResponseWriter, r *http.Request) error {
	f, err := newapi.ParsePerfWindow(r.URL.Query())
	if err != nil {
		return httpx.Errorf(http.StatusBadRequest, "%s", err.Error())
	}
	ms, err := s.restrict(r, &f)
	if err != nil {
		return err
	}
	q, err := s.split(r, f, ms)
	if err != nil {
		return err
	}
	counts := ask(r.Context(), q, func(ctx context.Context, pq poolQuery) (map[string]newapi.PerfCount, error) {
		return pq.pool.Logs.PerfCounts(ctx, pq.f)
	})
	httpx.JSON(w, http.StatusOK, struct {
		newapi.Perf
		notices
	}{newapi.BuildPerf(f, newapi.MergePerfCounts(counts)), q.notices})
	return nil
}

// flow adds up the flow chart's rows over the pools, or with split=pool keeps
// each pool's apart.
func (s *Service) flow(w http.ResponseWriter, r *http.Request) error {
	split, err := splitPool(r)
	if err != nil {
		return err
	}
	_, q, err := s.logFilter(r)
	if err != nil {
		return err
	}
	full := s.isSuper(r)
	parts := ask(r.Context(), q, func(ctx context.Context, pq poolQuery) (pooled[[]newapi.FlowRow], error) {
		rows, err := pq.pool.Logs.Flow(ctx, pq.f, full)
		for i := range rows {
			rows[i].Quota, rows[i].ChannelID = scale(rows[i].Quota, pq.factor), q.local(pq.pool.ID, rows[i].ChannelID)
			rows[i].ChannelName = q.names[rows[i].ChannelID]
		}
		return pooled[[]newapi.FlowRow]{rows, pq.pool.ID, pq.pool.Name}, err
	})
	if split {
		return perPool(w, q, parts, func(r newapi.FlowRow, id int64, name string) flowPoolRow { return flowPoolRow{r, id, name} },
			func(r flowPoolRow) int64 { return r.Quota })
	}
	rowsOf := make([][]newapi.FlowRow, len(parts))
	for i, p := range parts {
		rowsOf[i] = p.Row
	}
	return statRows(w, newapi.MergeFlow(rowsOf), q)
}

func (s *Service) userStats(w http.ResponseWriter, r *http.Request) error {
	if !s.isSuper(r) {
		return httpx.Errorf(http.StatusForbidden, "仅超级管理员可用")
	}
	_, q, err := s.logFilter(r)
	if err != nil {
		return err
	}
	parts := ask(r.Context(), q, func(ctx context.Context, pq poolQuery) ([]newapi.StatRow, error) {
		rows, err := pq.pool.Logs.UserStats(ctx, pq.f)
		for i := range rows {
			rows[i].Quota = scale(rows[i].Quota, pq.factor)
		}
		return rows, err
	})
	return statRows(w, newapi.MergeStats(parts), q)
}
