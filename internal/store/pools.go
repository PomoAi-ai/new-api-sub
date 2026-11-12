package store

import (
	"context"
	"errors"
)

// Pool is a row of newapi_pools. The byte fields are ciphertext; DBDSN is empty
// only for a pool that reads its logs from the sync store, LogDSN and KeyDSN may
// be empty.
type Pool struct {
	ID           int64
	Name         string
	BaseURL      string
	PAT          []byte
	DBDSN        []byte
	LogDSN       []byte
	KeyDSN       []byte
	AADLegacy    bool   // sealed under newapi_connection's field names
	LogMode      string // direct or sync
	AcceptNew    bool
	QuotaPerUnit float64 // 0 until the pool was first opened
	Version      int64
	UpdatedBy    string
	UpdatedAt    int64
}

const poolCols = `id, name, base_url, pat, db_dsn, log_dsn, key_dsn, aad_legacy, log_mode, accept_new,
	COALESCE(quota_per_unit, 0), version, updated_by, EXTRACT(EPOCH FROM updated_at)::BIGINT`

func scanPool(r scanner) (Pool, error) {
	var p Pool
	err := r.Scan(&p.ID, &p.Name, &p.BaseURL, &p.PAT, &p.DBDSN, &p.LogDSN, &p.KeyDSN, &p.AADLegacy, &p.LogMode, &p.AcceptNew,
		&p.QuotaPerUnit, &p.Version, &p.UpdatedBy, &p.UpdatedAt)
	return p, err
}

// Pools lists every pool by id.
func (s *Store) Pools(ctx context.Context) ([]Pool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+poolCols+` FROM newapi_pools ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pool{}
	for rows.Next() {
		p, err := scanPool(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Pool(ctx context.Context, id int64) (Pool, error) {
	p, err := scanPool(s.db.QueryRowContext(ctx, `SELECT `+poolCols+` FROM newapi_pools WHERE id = $1`, id))
	return p, notFound(err)
}

// PoolsVersion is id:version of every pool, by id and comma-separated; empty
// for none. Both processes restart when it changes.
func (s *Store) PoolsVersion(ctx context.Context) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(string_agg(id || ':' || version, ',' ORDER BY id), '') FROM newapi_pools`).Scan(&v)
	return v, err
}

// NextPoolID takes the id of a pool to be created, so that its secrets can be
// sealed to it first.
func (s *Store) NextPoolID(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT nextval('newapi_pools_id_seq')`).Scan(&id)
	return id, err
}

// poolWriteErr maps a name or address another pool has to ErrConflict.
func poolWriteErr(err error) error {
	if pgCode(err) == "23505" {
		return ErrConflict
	}
	return err
}

// CreatePool stores p under p.ID, from NextPoolID. ErrConflict: another pool has
// its name or address.
func (s *Store) CreatePool(ctx context.Context, p Pool) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO newapi_pools (id, name, base_url, pat, db_dsn, log_dsn, key_dsn, log_mode, accept_new, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		p.ID, p.Name, p.BaseURL, p.PAT, p.DBDSN, p.LogDSN, p.KeyDSN, p.LogMode, p.AcceptNew, p.UpdatedBy)
	return poolWriteErr(err)
}

// SavePool replaces the pool p.Version was read from, sealed under its own
// field names, and moves the version on, which restarts both processes. Its
// log sync's backoff ends: a PAT set right is tried at the next interval.
// ErrConflict: it changed meanwhile, or another pool has the name or address.
func (s *Store) SavePool(ctx context.Context, p Pool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = affectedOne(tx.ExecContext(ctx, `
		UPDATE newapi_pools SET name = $2, base_url = $3, pat = $4, db_dsn = $5, log_dsn = $6, key_dsn = $7, aad_legacy = false,
		  log_mode = $8, accept_new = $9, updated_by = $10, version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $11`,
		p.ID, p.Name, p.BaseURL, p.PAT, p.DBDSN, p.LogDSN, p.KeyDSN, p.LogMode, p.AcceptNew, p.UpdatedBy, p.Version))
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	if err != nil {
		return poolWriteErr(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE newapi_pool_sync SET failures = 0, backoff_until = NULL, updated_at = now() WHERE pool_id = $1`, p.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletePool removes a pool no channel or template uses: ErrConflict if one
// does, ErrNotFound if there is no such pool.
func (s *Store) DeletePool(ctx context.Context, id int64) error {
	err := affectedOne(s.db.ExecContext(ctx, `
		DELETE FROM newapi_pools WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM channel_templates WHERE $1 = ANY(pool_ids))`, id))
	switch {
	case pgCode(err) == "23503": // channel_placements
		return ErrConflict
	case errors.Is(err, ErrNotFound):
		if _, err := s.Pool(ctx, id); err != nil {
			return err
		}
		return ErrConflict
	}
	return err
}

// backfillPool1 gives pool 1 the templates and channels from before pools, as
// schema.sql does at every start.
const backfillPool1 = `
UPDATE channel_templates SET pool_ids = '{1}' WHERE pool_ids = '{}';
INSERT INTO channel_placements (channel_id, pool_id, newapi_channel_id, state, created_at)
  SELECT c.newapi_channel_id, 1, c.newapi_channel_id, 'created', c.created_at FROM portal_channels c
  WHERE c.spec IS NULL AND NOT EXISTS (SELECT 1 FROM channel_placements p WHERE p.channel_id = c.newapi_channel_id)
  ON CONFLICT DO NOTHING;`

// ImportPool stores p as pool 1 unless any pool was ever stored, and says
// whether it did: of two processes importing at once, one does, and the other
// returns once it is stored. The templates and channels from before then belong
// to it.
func (s *Store) ImportPool(ctx context.Context, p Pool) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// The sequence moves before the row commits: the other process must wait to see both.
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(7243001)"); err != nil {
		return false, err
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO newapi_pools (id, name, base_url, pat, db_dsn, log_dsn, key_dsn, log_mode, updated_by)
		SELECT 1, $1, $2, $3, $4, $5, $6, 'direct', $7 WHERE NOT (SELECT is_called FROM newapi_pools_id_seq)
		ON CONFLICT (id) DO NOTHING`, p.Name, p.BaseURL, p.PAT, p.DBDSN, p.LogDSN, p.KeyDSN, p.UpdatedBy)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT setval('newapi_pools_id_seq', 1)`); err != nil {
		return false, err
	}
	// No arguments, so pgx sends both statements via simple protocol.
	if _, err := tx.ExecContext(ctx, backfillPool1); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// ResealPool stores a legacy pool's secrets sealed again under its own field
// names. It changes nothing once the pool was saved: that already resealed it.
func (s *Store) ResealPool(ctx context.Context, p Pool) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE newapi_pools SET pat = $2, db_dsn = $3, log_dsn = $4, key_dsn = $5, aad_legacy = false
		WHERE id = $1 AND aad_legacy`, p.ID, p.PAT, p.DBDSN, p.LogDSN, p.KeyDSN)
	return err
}

// SetPoolQPU keeps what the pool's /api/status said, for when it is down. The
// version stays: nothing restarts on it.
func (s *Store) SetPoolQPU(ctx context.Context, id int64, qpu float64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE newapi_pools SET quota_per_unit = $2 WHERE id = $1`, id, qpu)
	return err
}

// PoolUse is how many placements and templates use each pool, by id.
func (s *Store) PoolUse(ctx context.Context) (map[int64][2]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, (SELECT count(*) FROM channel_placements c WHERE c.pool_id = p.id),
		  (SELECT count(*) FROM channel_templates t WHERE p.id = ANY(t.pool_ids))
		FROM newapi_pools p`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][2]int{}
	for rows.Next() {
		var id int64
		var n [2]int
		if err := rows.Scan(&id, &n[0], &n[1]); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
