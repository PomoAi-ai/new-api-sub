package store

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrNoPool means none of a template's pools takes new channels.
var ErrNoPool = errors.New("no pool takes new channels")

// Mapping records a channel's owner: a portal user (UserID > 0) or the admin
// who added it (UserID 0). AdminID is the admin whose scope it belongs to
// either way.
type Mapping struct {
	// ChannelID is the local id, which every API shows. A channel from before
	// pools (Spec nil) has its pool 1 id here.
	ChannelID     int
	UserID        int64
	Username      string
	AdminID       int64
	AdminUsername string
	Name          string // name given at creation
	CreatedAt     int64
	DeletedAt     *int64 // soft-delete time; nil while active
	TemplateID    *int64 // the template it was created from; nil once that is deleted
	WantStatus    int    // 1 or 2, what every placement is brought to
	Spec          json.RawMessage
	Placements    []Placement // by seq
}

// Placement is a channel in one pool. NewAPIID is its id there, 0 until State
// is created.
type Placement struct {
	PoolID      int64  `json:"pool_id"`
	Seq         int    `json:"seq"`
	NewAPIID    int    `json:"newapi_channel_id"`
	State       string `json:"state"` // pending, pushing, created or failed
	Remark      string `json:"remark"`
	Attempts    int    `json:"attempts"`
	Error       string `json:"last_error"`
	StatusDirty bool   `json:"status_dirty"`
	ConfigDirty bool   `json:"config_dirty"`
	KeysDirty   bool   `json:"keys_dirty"`
	// Cached is the new-api row of a placement in a pool without a database,
	// as read at CachedAt (Unix seconds, 0 for never); null if it was gone.
	Cached   json.RawMessage `json:"state_json"`
	CachedAt int64           `json:"state_at"`
}

const placementsJSON = `COALESCE((SELECT json_agg(json_build_object('pool_id', p.pool_id, 'seq', p.seq,
	  'newapi_channel_id', COALESCE(p.newapi_channel_id, 0), 'state', p.state, 'remark', p.remark, 'attempts', p.attempts,
	  'last_error', p.last_error, 'status_dirty', p.status_dirty, 'config_dirty', p.config_dirty, 'keys_dirty', p.keys_dirty,
	  'state_json', p.state_json, 'state_at', COALESCE(EXTRACT(EPOCH FROM p.state_at)::BIGINT, 0))
	  ORDER BY p.seq, p.pool_id) FROM channel_placements p WHERE p.channel_id = c.newapi_channel_id), '[]')::text`

// ChannelFilter selects mappings; zero fields mean "any".
type ChannelFilter struct {
	AdminID     int64 // the scope of a regular admin
	UserID      int64 // one portal user
	OwnKeys     bool  // only channels no portal user owns
	ActiveOnly  bool
	DeletedOnly bool
	ChannelID   int
}

func (f ChannelFilter) conds() *conds {
	c := &conds{}
	if f.AdminID > 0 {
		c.add("c.admin_id = $%d", f.AdminID)
	}
	if f.UserID > 0 {
		c.add("c.user_id = $%d", f.UserID)
	}
	if f.ChannelID > 0 {
		c.add("c.newapi_channel_id = $%d", f.ChannelID)
	}
	if f.OwnKeys {
		c.raw("c.user_id IS NULL")
	}
	if f.ActiveOnly {
		c.raw("c.deleted_at IS NULL")
	}
	if f.DeletedOnly {
		c.raw("c.deleted_at IS NOT NULL")
	}
	return c
}

// Channels is the only source of a caller's channel scope.
func (s *Store) Channels(ctx context.Context, f ChannelFilter) ([]Mapping, error) {
	c := f.conds()
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.newapi_channel_id, COALESCE(c.user_id, 0), COALESCE(u.username, ''), c.admin_id, a.username, c.name,
		       EXTRACT(EPOCH FROM c.created_at)::BIGINT, EXTRACT(EPOCH FROM c.deleted_at)::BIGINT, c.template_id,
		       c.want_status, c.spec::text, `+placementsJSON+`
		FROM portal_channels c
		LEFT JOIN portal_users u ON u.id = c.user_id
		JOIN admin_users a ON a.id = c.admin_id`+c.where()+`
		ORDER BY c.newapi_channel_id DESC`, c.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mapping{}
	for rows.Next() {
		var m Mapping
		var placements []byte
		if err := rows.Scan(&m.ChannelID, &m.UserID, &m.Username, &m.AdminID, &m.AdminUsername, &m.Name,
			&m.CreatedAt, &m.DeletedAt, &m.TemplateID, &m.WantStatus, (*[]byte)(&m.Spec), &placements); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(placements, &m.Placements); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Channel returns one mapping within f, or ErrNotFound.
func (s *Store) Channel(ctx context.Context, channelID int, f ChannelFilter) (Mapping, error) {
	f.ChannelID = channelID
	ms, err := s.Channels(ctx, f)
	if err != nil {
		return Mapping{}, err
	}
	if len(ms) == 0 {
		return Mapping{}, ErrNotFound
	}
	return ms[0], nil
}

// NewChannel is a channel as submitted. ID is from NextChannelIDs, KeySealed
// its key sealed to it, and Spec what every pool gets of it, without key and
// remark. It goes to PoolIDs in that order, as far as they take new channels.
type NewChannel struct {
	ID              int
	UserID, AdminID int64 // UserID 0: the admin's own key
	TemplateID      int64
	Name            string
	Spec            json.RawMessage
	KeySealed       []byte
	PoolIDs         []int64
}

// NextChannelIDs takes n local channel ids.
func (s *Store) NextChannelIDs(ctx context.Context, n int) ([]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT nextval('portal_channel_ids') FROM generate_series(1, $1)`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CreateChannels records channels and queues a placement of each in every pool
// of its PoolIDs that takes new channels, all or none. ErrNoPool: a channel
// would go nowhere.
func (s *Store) CreateChannels(ctx context.Context, chs []NewChannel) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range chs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO portal_channels (user_id, admin_id, newapi_channel_id, template_id, name, spec, key_sealed)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			nullable(c.UserID), c.AdminID, c.ID, nullable(c.TemplateID), c.Name, string(c.Spec), c.KeySealed); err != nil {
			return err
		}
		remarks := make([]string, len(c.PoolIDs))
		for i := range remarks {
			remarks[i] = newRemark(c.UserID, c.AdminID)
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO channel_placements (channel_id, pool_id, seq, remark)
			SELECT $1, t.pool_id, t.seq - 1, t.remark FROM unnest($2::bigint[], $3::text[]) WITH ORDINALITY AS t(pool_id, remark, seq)
			JOIN newapi_pools p ON p.id = t.pool_id AND p.accept_new`, c.ID, c.PoolIDs, remarks)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return cmp.Or(err, ErrNoPool)
		}
	}
	return tx.Commit()
}

// newRemark names the owner for whoever reads new-api's panel. The nonce makes
// it unique, so the channel can be found again when new-api's response does
// not say which id it got.
func newRemark(userID, adminID int64) string {
	nonce := make([]byte, 16)
	rand.Read(nonce) // never fails: crypto/rand crashes instead
	if userID > 0 {
		return fmt.Sprintf("portal:u%d:%s", userID, hex.EncodeToString(nonce))
	}
	return fmt.Sprintf("admin:a%d:%s", adminID, hex.EncodeToString(nonce))
}

func nullable(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// SoftDeleteChannel marks an active channel deleted; ErrNotFound if it was
// not active.
func (s *Store) SoftDeleteChannel(ctx context.Context, channelID int, by string) error {
	return affectedOne(s.db.ExecContext(ctx, `
		UPDATE portal_channels SET deleted_at = now(), deleted_by = $2
		WHERE newapi_channel_id = $1 AND deleted_at IS NULL`, channelID, by))
}

// RestoreChannel clears the soft delete; ErrNotFound if it was not deleted.
func (s *Store) RestoreChannel(ctx context.Context, channelID int) error {
	return affectedOne(s.db.ExecContext(ctx, `
		UPDATE portal_channels SET deleted_at = NULL, deleted_by = ''
		WHERE newapi_channel_id = $1 AND deleted_at IS NOT NULL`, channelID))
}

// RenameChannel keeps the name of an active channel in step with new-api;
// ErrNotFound if it is not active.
func (s *Store) RenameChannel(ctx context.Context, channelID int, name string) error {
	return affectedOne(s.db.ExecContext(ctx, `
		UPDATE portal_channels SET name = $2 WHERE newapi_channel_id = $1 AND deleted_at IS NULL`, channelID, name))
}

func (s *Store) DeleteMapping(ctx context.Context, channelID int) error {
	return affectedOne(s.db.ExecContext(ctx, `DELETE FROM portal_channels WHERE newapi_channel_id = $1`, channelID))
}

// IDs returns the local channel ids of ms: for scope checks, never for a
// query of new-api (InPool).
func IDs(ms []Mapping) []int {
	ids := make([]int, len(ms))
	for i, m := range ms {
		ids[i] = m.ChannelID
	}
	return ids
}

// InPool returns the new-api ids in poolID of the channels of ms placed there.
func InPool(ms []Mapping, poolID int64) []int {
	var ids []int
	for _, m := range ms {
		for _, p := range m.Placements {
			if p.PoolID == poolID && p.State == "created" {
				ids = append(ids, p.NewAPIID)
			}
		}
	}
	return ids
}

// PoolChannel is a new-api channel: its pool and its id there.
type PoolChannel struct {
	PoolID   int64
	NewAPIID int
}

// PlacementIndex maps the new-api channels of ms to their local ids.
func PlacementIndex(ms []Mapping) map[PoolChannel]int {
	out := map[PoolChannel]int{}
	for _, m := range ms {
		for _, p := range m.Placements {
			if p.State == "created" {
				out[PoolChannel{p.PoolID, p.NewAPIID}] = m.ChannelID
			}
		}
	}
	return out
}
