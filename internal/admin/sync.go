package admin

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/httpx"
	"newapisub/internal/logstore"
	"newapisub/internal/newapi"
	"newapisub/internal/security"
	"newapisub/internal/store"
)

var (
	errNoMongo   = httpx.Errorf(http.StatusBadRequest, "未配置 MONGO_URI，号池不能同步日志")
	errMongoDown = httpx.Errorf(http.StatusServiceUnavailable, "日志库暂时不可用，请稍后再试")
	errNoSync    = httpx.Errorf(http.StatusNotFound, "该号池尚未设置同步")
)

// syncView is a pool's log sync as the panel shows it: times are Unix seconds,
// null for never, and lag_s is how far the cursor is behind now.
type syncView struct {
	store.PoolSync
	LagS         int64  `json:"lag_s"`
	BackoffUntil *int64 `json:"backoff_until"`
	LastRunAt    *int64 `json:"last_run_at"`
	LastOKAt     *int64 `json:"last_ok_at"`
	NewestLogAt  *int64 `json:"newest_log_at"`
}

func unix(t int64) *int64 {
	if t == 0 {
		return nil
	}
	return &t
}

func newSyncView(ps store.PoolSync) *syncView {
	return &syncView{ps, max(time.Now().Unix()-ps.Cursor, 0), unix(ps.BackoffUntil), unix(ps.LastRunAt), unix(ps.LastOKAt), unix(ps.NewestLogAt)}
}

// syncTarget is the pool of {id} for the audit log; 404 for none.
func (s *Server) syncTarget(r *http.Request, ev *audit.Event) (int64, error) {
	id, err := target(r, ev, "pool")
	if err != nil {
		return 0, err
	}
	row, err := s.Store.Pool(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return 0, httpx.Errorf(http.StatusNotFound, "号池不存在")
	}
	if err != nil {
		return 0, err
	}
	ev.Detail["name"] = row.Name
	return id, nil
}

// setSync sets how a pool's logs are synced; nothing restarts on it. Turning a
// sync on the first time starts it backfill_days back.
func (s *Server) setSync(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := s.syncTarget(r, ev)
	if err != nil {
		return err
	}
	var req struct {
		store.SyncControl
		BackfillDays int `json:"backfill_days"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	c := req.SyncControl
	switch {
	case c.IntervalS < 30 || c.IntervalS > 3600:
		return httpx.Errorf(http.StatusBadRequest, "同步间隔须为 30–3600 秒")
	case c.WindowS < 60 || c.WindowS > 3600:
		return httpx.Errorf(http.StatusBadRequest, "窗口长度须为 60–3600 秒")
	case c.MaxWindows < 1 || c.MaxWindows > 60:
		return httpx.Errorf(http.StatusBadRequest, "每轮窗口数须为 1–60")
	case req.BackfillDays < 0 || req.BackfillDays > store.MaxBackfillDays:
		return httpx.Errorf(http.StatusBadRequest, "回补天数须为 0–%d", store.MaxBackfillDays)
	case c.Enabled && s.Mongo == nil:
		return errNoMongo
	}
	ev.Detail["sync"] = req
	if err := s.Store.SetSyncControl(r.Context(), id, c, req.BackfillDays); errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusNotFound, "号池不存在")
	} else if err != nil {
		return err
	}
	return s.answerPool(w, r, http.StatusOK, id)
}

// resetSync deletes the synced logs of a pool and starts its sync over from
// backfill_days back, resolving its conflicts: for after new-api reused its ids.
func (s *Server) resetSync(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := s.syncTarget(r, ev)
	if err != nil {
		return err
	}
	if err := s.Security.Require(r, security.PurposeNewAPIConfig); err != nil {
		return err
	}
	var req struct {
		BackfillDays int `json:"backfill_days"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	if req.BackfillDays < 0 || req.BackfillDays > store.MaxBackfillDays {
		return httpx.Errorf(http.StatusBadRequest, "回补天数须为 0–%d", store.MaxBackfillDays)
	}
	if s.Mongo == nil {
		return errNoMongo
	}
	logs := s.Mongo.Get()
	if logs == nil {
		return errMongoDown
	}
	if _, err := s.Store.PoolSync(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		return errNoSync
	} else if err != nil {
		return err
	}
	ctx := context.WithoutCancel(r.Context()) // the logs are gone either way: the cursor must follow
	ev.Detail["backfill_days"] = req.BackfillDays
	n, err := logs.DropPool(ctx, id)
	if err != nil {
		log.Printf("reset the sync of pool %d: %v", id, err)
		return errMongoDown
	}
	ev.Detail["deleted"] = n
	if err := s.Store.ResetSync(ctx, id, req.BackfillDays, auth.Username(ctx)); errors.Is(err, store.ErrNotFound) {
		return errNoSync
	} else if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"deleted": n})
	return nil
}

// conflictView is a conflict with resolved_at null while it is open.
type conflictView struct {
	store.SyncConflict
	ResolvedAt *int64 `json:"resolved_at"`
}

func (s *Server) listConflicts(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	cs, err := s.Store.SyncConflicts(r.Context(), id, 200)
	if err != nil {
		return err
	}
	out := make([]conflictView, len(cs))
	for i, c := range cs {
		out[i] = conflictView{c, unix(c.ResolvedAt)}
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// resolveConflict accepts a conflict; the sync goes on once none that pauses
// it is open.
func (s *Server) resolveConflict(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := s.syncTarget(r, ev)
	if err != nil {
		return err
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	ev.Detail["conflict_id"] = req.ID
	resumed, err := s.Store.ResolveConflict(r.Context(), id, req.ID, auth.Username(r.Context()))
	if errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusNotFound, "冲突不存在或已处理")
	}
	if err != nil {
		return err
	}
	ev.Detail["resumed"] = resumed
	httpx.JSON(w, http.StatusOK, map[string]any{"resumed": resumed})
	return nil
}

type tally struct {
	Count int64 `json:"count"`
	Quota int64 `json:"quota"`
}

func count(ctx context.Context, src newapi.LogSource, f newapi.Filter) (tally, error) {
	_, n, err := src.Logs(ctx, f, false)
	if err != nil {
		return tally{}, err
	}
	u, err := src.Usage(ctx, f)
	return tally{n, u.Quota}, err
}

// verifySync compares, for a pool with a database, its logs of the hours
// before the sync's cursor with what the sync stored.
func (s *Server) verifySync(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	hours := 24
	if raw := r.URL.Query().Get("hours"); raw != "" {
		if hours, err = strconv.Atoi(raw); err != nil || hours < 1 || hours > 720 {
			return httpx.Errorf(http.StatusBadRequest, "hours 须为 1–720")
		}
	}
	ps, err := s.Store.PoolSync(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return errNoSync
	}
	if err != nil {
		return err
	}
	switch p := s.pool(id); {
	case s.Mongo == nil:
		return errNoMongo
	case p == nil || p.Reason != "":
		return httpx.Errorf(http.StatusConflict, "号池在本进程不可用")
	case p.DB == nil:
		return httpx.Errorf(http.StatusBadRequest, "该号池没有数据库，无法对账")
	default:
		f := newapi.Filter{AllChannels: true, End: ps.Cursor, Page: 1, PageSize: 1}
		f.Start = max(ps.Cursor-int64(hours)*3600, time.Now().Unix()-logstore.RetentionSeconds)
		db, err := count(r.Context(), p.DB, f)
		if err != nil {
			log.Printf("verify the sync of pool %d: %v", id, err)
			return httpx.Errorf(http.StatusBadGateway, "号池数据库查询失败")
		}
		synced, err := count(r.Context(), s.Mongo.Reader(id), f)
		if err != nil {
			log.Printf("verify the sync of pool %d: %v", id, err)
			return errMongoDown
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"start": f.Start, "end": f.End, "db": db, "mongo": synced, "match": db == synced})
	}
	return nil
}
