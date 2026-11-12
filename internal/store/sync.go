package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// SyncControl is what an admin sets of a pool's log sync. The ranges are
// newapi_pool_sync's CHECKs.
type SyncControl struct {
	Enabled    bool `json:"enabled"`
	IntervalS  int  `json:"interval_s"`
	WindowS    int  `json:"window_s"`
	MaxWindows int  `json:"max_windows"`
}

// PoolSync is a row of newapi_pool_sync. Times are Unix seconds, 0 for never.
type PoolSync struct {
	PoolID int64 `json:"pool_id"`
	SyncControl
	Cursor        int64  `json:"cursor"`
	MaxLogID      int64  `json:"max_log_id"`
	MaxLogAt      int64  `json:"max_log_at"`
	Paused        string `json:"paused"` // the kind of conflict that paused it, "" running
	Failures      int    `json:"failures"`
	BackoffUntil  int64  `json:"backoff_until"`
	LastRunAt     int64  `json:"last_run_at"`
	LastOKAt      int64  `json:"last_ok_at"`
	LastError     string `json:"last_error"`
	LastRows      int    `json:"last_rows"`
	LastMS        int    `json:"last_ms"`
	RowsTotal     int64  `json:"rows_total"`
	NewestLogAt   int64  `json:"newest_log_at"`
	OpenConflicts int    `json:"open_conflicts"`
}

const poolSyncCols = `s.pool_id, s.enabled, s.interval_s, s.window_s, s.max_windows, s.cursor, s.max_log_id, s.max_log_at,
	s.paused, s.failures, COALESCE(EXTRACT(EPOCH FROM s.backoff_until)::BIGINT, 0), COALESCE(EXTRACT(EPOCH FROM s.last_run_at)::BIGINT, 0),
	COALESCE(EXTRACT(EPOCH FROM s.last_ok_at)::BIGINT, 0), s.last_error, s.last_rows, s.last_ms, s.rows_total, s.newest_log_at,
	(SELECT count(*) FROM log_sync_conflicts c WHERE c.pool_id = s.pool_id AND c.resolved_at IS NULL)`

func scanPoolSync(r scanner) (PoolSync, error) {
	var p PoolSync
	err := r.Scan(&p.PoolID, &p.Enabled, &p.IntervalS, &p.WindowS, &p.MaxWindows, &p.Cursor, &p.MaxLogID, &p.MaxLogAt,
		&p.Paused, &p.Failures, &p.BackoffUntil, &p.LastRunAt, &p.LastOKAt, &p.LastError, &p.LastRows, &p.LastMS,
		&p.RowsTotal, &p.NewestLogAt, &p.OpenConflicts)
	return p, err
}

func (s *Store) poolSyncs(ctx context.Context, where string) ([]PoolSync, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+poolSyncCols+` FROM newapi_pool_sync s`+where+` ORDER BY s.pool_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PoolSync{}
	for rows.Next() {
		p, err := scanPoolSync(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PoolSyncs lists the sync of every pool that ever had one set.
func (s *Store) PoolSyncs(ctx context.Context) ([]PoolSync, error) { return s.poolSyncs(ctx, "") }

// DueSyncs lists the syncs to run now: on, not paused, past their backoff and
// their interval since the last run.
func (s *Store) DueSyncs(ctx context.Context) ([]PoolSync, error) {
	return s.poolSyncs(ctx, ` WHERE s.enabled AND s.paused = '' AND (s.backoff_until IS NULL OR s.backoff_until <= now())
		AND (s.last_run_at IS NULL OR s.last_run_at + s.interval_s * interval '1 second' <= now())`)
}

// PoolSync is the sync of one pool; ErrNotFound if none was ever set.
func (s *Store) PoolSync(ctx context.Context, poolID int64) (PoolSync, error) {
	p, err := scanPoolSync(s.db.QueryRowContext(ctx, `SELECT `+poolSyncCols+` FROM newapi_pool_sync s WHERE s.pool_id = $1`, poolID))
	return p, notFound(err)
}

// MaxBackfillDays is the most a sync reaches back: older logs are past the
// retention of the sync store.
const MaxBackfillDays = 30

// SetSyncControl stores c. The cursor of a sync that never ran starts
// backfillDays back, once it is on; one that ran goes on from where it is.
// The backoff ends: what made the runs fail may be what was changed.
// ErrNotFound: no such pool.
func (s *Store) SetSyncControl(ctx context.Context, poolID int64, c SyncControl, backfillDays int) error {
	if backfillDays < 0 || backfillDays > MaxBackfillDays {
		return fmt.Errorf("backfill of %d days is outside 0-%d", backfillDays, MaxBackfillDays)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO newapi_pool_sync (pool_id, enabled, interval_s, window_s, max_windows, cursor)
		VALUES ($1, $2, $3, $4, $5, EXTRACT(EPOCH FROM now())::BIGINT - $6 * 86400)
		ON CONFLICT (pool_id) DO UPDATE SET enabled = EXCLUDED.enabled, interval_s = EXCLUDED.interval_s,
		  window_s = EXCLUDED.window_s, max_windows = EXCLUDED.max_windows, failures = 0, backoff_until = NULL, updated_at = now(),
		  cursor = CASE WHEN newapi_pool_sync.last_run_at IS NULL AND EXCLUDED.enabled THEN EXCLUDED.cursor ELSE newapi_pool_sync.cursor END`,
		poolID, c.Enabled, c.IntervalS, c.WindowS, c.MaxWindows, backfillDays)
	if pgCode(err) == "23503" {
		return ErrNotFound
	}
	return err
}

// SyncWindow is what one window of a sync read.
type SyncWindow struct {
	Cursor   int64 // the new cursor: the window's end
	MaxLogID int64 // the newest id and created_at it read, 0 for none
	MaxLogAt int64
	Rows     int // logs it stored that the sync store did not have
	MS       int
}

// AdvanceSync moves the cursor from from to w.Cursor. ErrConflict: the sync
// changed meanwhile (paused, reset or moved by another worker), and the
// window does not count.
func (s *Store) AdvanceSync(ctx context.Context, poolID, from int64, w SyncWindow) error {
	err := affectedOne(s.db.ExecContext(ctx, `
		UPDATE newapi_pool_sync SET cursor = $3, max_log_id = GREATEST(max_log_id, $4), max_log_at = GREATEST(max_log_at, $5),
		  newest_log_at = GREATEST(newest_log_at, $5), last_rows = $6::int, last_ms = $7, rows_total = rows_total + $6::int,
		  failures = 0, backoff_until = NULL, last_error = '', last_run_at = now(), last_ok_at = now(), updated_at = now()
		WHERE pool_id = $1 AND cursor = $2 AND paused = ''`,
		poolID, from, w.Cursor, w.MaxLogID, w.MaxLogAt, w.Rows, w.MS))
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	return err
}

// MarkSyncRun notes a run that found nothing to read yet.
func (s *Store) MarkSyncRun(ctx context.Context, poolID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE newapi_pool_sync SET last_run_at = now(), updated_at = now() WHERE pool_id = $1`, poolID)
	return err
}

// FailSync notes a failed run under reason, a fixed text, and backs off for
// the interval doubled per failure in a row, 30 minutes at most.
func (s *Store) FailSync(ctx context.Context, poolID int64, reason string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE newapi_pool_sync SET failures = failures + 1, last_error = $2, last_run_at = now(), updated_at = now(),
		  backoff_until = now() + LEAST(interval_s * power(2, LEAST(failures, 16)), 1800) * interval '1 second'
		WHERE pool_id = $1`, poolID, reason)
	return err
}

// ClearSyncFailures ends the failures and backoff of the syncs whose last run
// failed on reason, once what failed is back.
func (s *Store) ClearSyncFailures(ctx context.Context, reason string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE newapi_pool_sync SET failures = 0, backoff_until = NULL, last_error = '', updated_at = now() WHERE last_error = $1`, reason)
	return err
}

// PauseSync stops the sync of a pool on a conflict of kind, until a super
// resolves it or resets the sync.
func (s *Store) PauseSync(ctx context.Context, poolID int64, kind string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE newapi_pool_sync SET paused = $2, last_run_at = now(), updated_at = now() WHERE pool_id = $1`, poolID, kind)
	return err
}

// ResetSync starts a pool's sync over from backfillDays back, for after its
// synced logs were deleted, and resolves its open conflicts as by.
// ErrNotFound: the pool has no sync.
func (s *Store) ResetSync(ctx context.Context, poolID int64, backfillDays int, by string) error {
	if backfillDays < 0 || backfillDays > MaxBackfillDays {
		return fmt.Errorf("backfill of %d days is outside 0-%d", backfillDays, MaxBackfillDays)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := affectedOne(tx.ExecContext(ctx, `
		UPDATE newapi_pool_sync SET cursor = EXTRACT(EPOCH FROM now())::BIGINT - $2 * 86400, max_log_id = 0, max_log_at = 0,
		  paused = '', failures = 0, backoff_until = NULL, last_error = '', updated_at = now()
		WHERE pool_id = $1`, poolID, backfillDays)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE log_sync_conflicts SET resolved_by = $2, resolved_at = now() WHERE pool_id = $1 AND resolved_at IS NULL`, poolID, by); err != nil {
		return err
	}
	return tx.Commit()
}

// SyncConflict is a row of log_sync_conflicts. Times are Unix seconds.
type SyncConflict struct {
	ID         int64           `json:"id"`
	PoolID     int64           `json:"pool_id"`
	Kind       string          `json:"kind"`
	LogID      int64           `json:"log_id"`
	Detail     json.RawMessage `json:"detail"`
	Count      int             `json:"count"`
	FirstSeen  int64           `json:"first_seen"`
	LastSeen   int64           `json:"last_seen"`
	ResolvedBy string          `json:"resolved_by"`
	ResolvedAt int64           `json:"resolved_at"` // 0 while open
}

// AddConflict records a conflict, or counts it again while it is open. A
// fingerprint or duplicate_source conflict a super accepted before is not
// recorded again: added is false, and it must not pause the sync.
func (s *Store) AddConflict(ctx context.Context, poolID int64, kind string, logID int64, detail any) (added bool, err error) {
	raw, err := json.Marshal(detail)
	if err != nil {
		return false, err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO log_sync_conflicts (pool_id, kind, log_id, detail)
		SELECT $1::bigint, $2::text, $3::bigint, $4::jsonb WHERE NOT ($2 IN ('fingerprint', 'duplicate_source') AND EXISTS (
		  SELECT 1 FROM log_sync_conflicts WHERE pool_id = $1 AND kind = $2 AND log_id = $3 AND resolved_at IS NOT NULL))
		ON CONFLICT (pool_id, kind, log_id) WHERE resolved_at IS NULL
		DO UPDATE SET count = log_sync_conflicts.count + 1, last_seen = now(), detail = EXCLUDED.detail
		RETURNING id`, poolID, kind, logID, string(raw)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// SyncConflicts lists up to limit conflicts of a pool, open ones first, then
// the latest.
func (s *Store) SyncConflicts(ctx context.Context, poolID int64, limit int) ([]SyncConflict, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, pool_id, kind, log_id, detail::text, count, EXTRACT(EPOCH FROM first_seen)::BIGINT,
		  EXTRACT(EPOCH FROM last_seen)::BIGINT, resolved_by, COALESCE(EXTRACT(EPOCH FROM resolved_at)::BIGINT, 0)
		FROM log_sync_conflicts WHERE pool_id = $1 ORDER BY resolved_at IS NOT NULL, last_seen DESC, id DESC LIMIT $2`, poolID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SyncConflict{}
	for rows.Next() {
		var c SyncConflict
		if err := rows.Scan(&c.ID, &c.PoolID, &c.Kind, &c.LogID, (*[]byte)(&c.Detail), &c.Count, &c.FirstSeen, &c.LastSeen,
			&c.ResolvedBy, &c.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ResolveConflict marks an open conflict accepted by by. Once no conflict
// that pauses is open, the sync resumes, and after an id regression takes the
// ids it reads next as the new baseline. ErrNotFound: no such open conflict.
func (s *Store) ResolveConflict(ctx context.Context, poolID, id int64, by string) (resumed bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := affectedOne(tx.ExecContext(ctx, `
		UPDATE log_sync_conflicts SET resolved_by = $3, resolved_at = now() WHERE id = $2 AND pool_id = $1 AND resolved_at IS NULL`,
		poolID, id, by)); err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE newapi_pool_sync SET paused = '', failures = 0, backoff_until = NULL, updated_at = now(),
		  max_log_id = CASE WHEN paused = 'id_regression' THEN 0 ELSE max_log_id END,
		  max_log_at = CASE WHEN paused = 'id_regression' THEN 0 ELSE max_log_at END
		WHERE pool_id = $1 AND paused <> '' AND NOT EXISTS (
		  SELECT 1 FROM log_sync_conflicts WHERE pool_id = $1 AND resolved_at IS NULL AND kind <> 'clock_skew')`, poolID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, tx.Commit()
}
