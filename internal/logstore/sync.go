package logstore

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"newapisub/internal/newapi"
	"newapisub/internal/store"
)

const (
	// settle keeps a window off the last seconds: new-api commits a log a little
	// after its created_at, and not in id order.
	settle = 30
	// overlap reads the end of the window before again, for logs committed late;
	// the unique key drops what was read already.
	overlap = 120
	// skewMax is how far new-api's clock may be off ours. Behind by more than
	// settle and overlap, it writes logs into windows already read.
	skewMax   = 60
	batchRows = 1000
	tick      = 5 * time.Second
)

// Exporter is what the sync needs of a pool's new-api: *newapi.Client.
type Exporter interface {
	ExportLogs(ctx context.Context, start, end int64, fn func(newapi.Log) error) (count int, served time.Time, err error)
}

// storeDown is the fixed text of a run while MongoDB cannot be reached.
const storeDown = "日志库暂时不可用"

// Syncer copies the logs of every pool whose sync is on into the Store, one
// pool and one window at a time. Run it under the worker lock: two would read
// every window twice.
type Syncer struct {
	St   *store.Store
	Logs *Lazy
	// Pool is the new-api of a pool in this process, nil while it is
	// unavailable.
	Pool func(poolID int64) Exporter
}

// Run syncs whatever is due every few seconds until ctx ends.
func (s *Syncer) Run(ctx context.Context) {
	t := time.NewTicker(tick)
	defer t.Stop()
	down := true // what a run before this process noted may be left to clear
	for {
		down = s.round(ctx, down)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// round syncs whatever is due. While MongoDB cannot be reached, each due pool
// fails with storeDown instead; once it answers again, those failures are
// cleared. down is whether it could not be reached.
func (s *Syncer) round(ctx context.Context, wasDown bool) (down bool) {
	_, err := s.Logs.Ping(ctx)
	switch {
	case err != nil && !wasDown && ctx.Err() == nil:
		log.Printf("log sync: MongoDB cannot be reached: %v", err)
	case err == nil && wasDown:
		if err := s.St.ClearSyncFailures(ctx, storeDown); err != nil {
			log.Printf("log sync: clear the failures of MongoDB being down: %v", err)
			return true
		}
	}
	due, derr := s.St.DueSyncs(ctx)
	if derr != nil && ctx.Err() == nil {
		log.Printf("log sync: read due pools: %v", derr)
	}
	for _, ps := range due {
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			s.fail(ctx, ps.PoolID, storeDown, nil)
			continue
		}
		s.syncPool(ctx, ps)
	}
	return err != nil
}

// errPause ends a window that found a conflict which pauses the sync.
var errPause = errors.New("log sync paused")

// window is what reading one window found.
type window struct {
	maxID, maxAt int64
	beyond       bool // a log newer than any read before, by more than the overlap
	served       time.Time
	rows         int
	pause        string // the kind of conflict that pauses the sync
	batch        []newapi.Log
}

func (s *Syncer) syncPool(ctx context.Context, ps store.PoolSync) {
	ex := s.Pool(ps.PoolID)
	if ex == nil {
		s.fail(ctx, ps.PoolID, "号池在本进程不可用", nil)
		return
	}
	now := time.Now().Unix()
	safe := now - settle
	cursor := max(ps.Cursor, now-RetentionSeconds) // nothing older stays in the store
	ran := false
	for range ps.MaxWindows {
		to := min(cursor+int64(ps.WindowS), safe)
		if to <= cursor {
			break
		}
		ran = true
		started := time.Now()
		w := &window{}
		_, served, err := ex.ExportLogs(ctx, cursor-overlap, to-1, func(l newapi.Log) error { return s.take(ctx, ps, w, l) })
		w.served = served
		if err == nil {
			err = s.flush(ctx, ps, w)
		}
		if err == nil {
			err = s.check(ctx, ps, w)
		}
		if err == nil { // a window with nothing to store moves the cursor only while MongoDB answers
			if _, perr := s.Logs.Ping(ctx); perr != nil {
				err = downError{perr}
			}
		}
		if errors.Is(err, errPause) {
			if err := s.St.PauseSync(ctx, ps.PoolID, w.pause); err != nil {
				log.Printf("log sync: pause pool %d: %v", ps.PoolID, err)
			}
			log.Printf("log sync: pool %d paused on %s", ps.PoolID, w.pause)
			return
		}
		if err != nil {
			s.fail(ctx, ps.PoolID, reason(err), err)
			return
		}
		err = s.St.AdvanceSync(ctx, ps.PoolID, ps.Cursor, store.SyncWindow{
			Cursor: to, MaxLogID: w.maxID, MaxLogAt: w.maxAt, Rows: w.rows, MS: int(time.Since(started).Milliseconds())})
		if err != nil {
			if !errors.Is(err, store.ErrConflict) {
				log.Printf("log sync: save the cursor of pool %d: %v", ps.PoolID, err)
			}
			return // changed meanwhile: the next round starts from what it is now
		}
		ps.Cursor, cursor = to, to
		ps.MaxLogID, ps.MaxLogAt = max(ps.MaxLogID, w.maxID), max(ps.MaxLogAt, w.maxAt)
	}
	if !ran {
		if err := s.St.MarkSyncRun(ctx, ps.PoolID); err != nil {
			log.Printf("log sync: note the run of pool %d: %v", ps.PoolID, err)
		}
	}
}

// take checks a log as it comes and stores the logs a batch at a time.
func (s *Syncer) take(ctx context.Context, ps store.PoolSync, w *window, l newapi.Log) error {
	if l.ID == 0 { // ClickHouse has no log ids, so no key to sync by
		if err := s.conflict(ctx, ps.PoolID, "zero_id", 0, map[string]any{"created_at": l.CreatedAt, "request_id": l.RequestID}); err != nil {
			return err
		}
		w.pause = "zero_id"
		return errPause
	}
	w.maxID, w.maxAt = max(w.maxID, l.ID), max(w.maxAt, l.CreatedAt)
	w.beyond = w.beyond || l.CreatedAt > ps.MaxLogAt+overlap
	w.batch = append(w.batch, l)
	if len(w.batch) < batchRows {
		return nil
	}
	return s.flush(ctx, ps, w)
}

// flush stores the batch unless another pool has one of its logs, and records
// the fingerprints it found taken.
func (s *Syncer) flush(ctx context.Context, ps store.PoolSync, w *window) error {
	batch := w.batch
	w.batch = nil
	ids := []string{}
	byRequest := map[string]int64{}
	for _, l := range batch {
		if l.RequestID != "" && stored(l.Type) {
			ids = append(ids, l.RequestID)
			byRequest[l.RequestID] = l.ID
		}
	}
	logs := s.Logs.Get() // open: the round found it answering
	dup, found, err := logs.OtherPool(ctx, ps.PoolID, ids)
	if err != nil {
		return storeError{err}
	}
	if found {
		added, err := s.addConflict(ctx, ps.PoolID, "duplicate_source", byRequest[dup.RequestID], dup)
		if err != nil {
			return err
		}
		if added {
			w.pause = "duplicate_source"
			return errPause
		}
	}
	inserted, conflicts, err := logs.Insert(ctx, ps.PoolID, batch)
	w.rows += inserted
	if err != nil {
		return storeError{err}
	}
	for _, c := range conflicts {
		added, err := s.addConflict(ctx, ps.PoolID, "fingerprint", c.LogID, c)
		if err != nil {
			return err
		}
		if added {
			w.pause = "fingerprint"
		}
	}
	if w.pause != "" {
		return errPause
	}
	return nil
}

// check looks at the window as a whole once it is stored.
func (s *Syncer) check(ctx context.Context, ps store.PoolSync, w *window) error {
	if now := time.Now(); !w.served.IsZero() && (w.served.Sub(now) > skewMax*time.Second || now.Sub(w.served) > skewMax*time.Second) {
		detail := map[string]any{"new_api_time": w.served.Unix(), "now": now.Unix()}
		if err := s.conflict(ctx, ps.PoolID, "clock_skew", 0, detail); err != nil {
			return err
		}
	}
	// Logs newer than any before but no id past the newest: new-api counts ids anew.
	if ps.MaxLogID > 0 && w.beyond && w.maxID < ps.MaxLogID {
		detail := map[string]any{"max_log_id": ps.MaxLogID, "window_max_id": w.maxID, "created_at": w.maxAt}
		if err := s.conflict(ctx, ps.PoolID, "id_regression", 0, detail); err != nil {
			return err
		}
		w.pause = "id_regression"
		return errPause
	}
	return nil
}

func (s *Syncer) conflict(ctx context.Context, poolID int64, kind string, logID int64, detail any) error {
	_, err := s.addConflict(ctx, poolID, kind, logID, detail)
	return err
}

func (s *Syncer) addConflict(ctx context.Context, poolID int64, kind string, logID int64, detail any) (bool, error) {
	added, err := s.St.AddConflict(ctx, poolID, kind, logID, detail)
	if err != nil {
		return false, appDBError{err}
	}
	return added, nil
}

// appDBError, storeError and downError tell a failure of the app database or
// of the sync store, or the sync store not answering, from one of the pool.
type (
	appDBError struct{ error }
	storeError struct{ error }
	downError  struct{ error }
)

func (e appDBError) Unwrap() error { return e.error }
func (e storeError) Unwrap() error { return e.error }
func (e downError) Unwrap() error  { return e.error }

// fail notes a failed run. The server log has why, the fixed text, and the
// error only when it is this system's own databases': never new-api's words.
func (s *Syncer) fail(ctx context.Context, poolID int64, why string, err error) {
	var app appDBError
	var st storeError
	var down downError
	switch {
	case errors.As(err, &app) || errors.As(err, &st) || errors.As(err, &down):
		log.Printf("log sync: pool %d: %s: %v", poolID, why, err)
	case err != nil:
		log.Printf("log sync: pool %d: %s", poolID, why)
	}
	if err := s.St.FailSync(ctx, poolID, why); err != nil && ctx.Err() == nil {
		log.Printf("log sync: note the failure of pool %d: %v", poolID, err)
	}
}

// reason is the fixed text of a failed run, for the sync panel.
func reason(err error) string {
	var rejected *newapi.Rejected
	var app appDBError
	var st storeError
	var down downError
	switch {
	case errors.As(err, &down):
		return storeDown
	case errors.As(err, &app):
		return "应用数据库读写失败"
	case errors.As(err, &st):
		return "日志库读写失败"
	case errors.Is(err, newapi.ErrRateLimited):
		return "new-api 限流，稍后重试"
	case errors.As(err, &rejected):
		switch {
		case strings.Contains(rejected.Message, "Another log export is running"):
			return "同步用户在 new-api 上有另一个导出正在进行"
		case rejected.Status == http.StatusUnauthorized || rejected.Status == http.StatusForbidden:
			return "new-api 拒绝了 PAT，请检查令牌和 log:read 权限"
		}
		return "new-api 拒绝了导出请求"
	case errors.Is(err, context.DeadlineExceeded):
		return "导出超时"
	case errors.As(err, new(*url.Error)) || strings.Contains(err.Error(), ": HTTP 5"): // no answer, or new-api's server error
		return "new-api 暂时不可用"
	}
	return "导出中断或不完整"
}
