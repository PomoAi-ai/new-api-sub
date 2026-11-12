package store

import (
	"context"
	"encoding/json"
)

// Template is what non-super callers get for a channel type: everything about
// the channel except its key, name and (if allowed) base URL.
type Template struct {
	ID                 int64           `json:"id"`
	Name               string          `json:"name"`
	ChannelType        int             `json:"channel_type"`
	BaseURL            string          `json:"base_url"`
	AllowCustomBaseURL bool            `json:"allow_custom_base_url"`
	Models             string          `json:"models"`
	Group              string          `json:"group"`
	Priority           int64           `json:"priority"`
	Weight             int             `json:"weight"`
	TestModel          string          `json:"test_model"`
	Extra              json.RawMessage `json:"extra"`
	Enabled            bool            `json:"enabled"`
	PoolIDs            []int64         `json:"pool_ids"` // where its channels go, in order
	CreatedAt          int64           `json:"created_at"`
	UpdatedAt          int64           `json:"updated_at"`
}

const templateCols = `id, name, channel_type, base_url, allow_custom_base_url, models, group_name,
	priority, weight, test_model, extra::text, enabled, to_json(pool_ids)::text, EXTRACT(EPOCH FROM created_at)::BIGINT, EXTRACT(EPOCH FROM updated_at)::BIGINT`

type scanner interface{ Scan(dest ...any) error }

func scanTemplate(r scanner) (Template, error) {
	var t Template
	var pools []byte
	err := r.Scan(&t.ID, &t.Name, &t.ChannelType, &t.BaseURL, &t.AllowCustomBaseURL, &t.Models,
		&t.Group, &t.Priority, &t.Weight, &t.TestModel, (*[]byte)(&t.Extra), &t.Enabled, &pools, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return t, err
	}
	return t, json.Unmarshal(pools, &t.PoolIDs)
}

// templateWriteErr maps a duplicate name to ErrConflict.
func templateWriteErr(err error) error {
	if pgCode(err) == "23505" {
		return ErrConflict
	}
	return err
}

func (s *Store) templates(ctx context.Context, where string, args ...any) ([]Template, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+templateCols+` FROM channel_templates`+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ListTemplates(ctx context.Context, onlyEnabled bool) ([]Template, error) {
	if onlyEnabled {
		return s.templates(ctx, ` WHERE enabled`)
	}
	return s.templates(ctx, ``)
}

// EnabledTemplates returns the enabled templates of a channel type.
func (s *Store) EnabledTemplates(ctx context.Context, channelType int) ([]Template, error) {
	return s.templates(ctx, ` WHERE channel_type = $1 AND enabled`, channelType)
}

func (s *Store) Template(ctx context.Context, id int64) (Template, error) {
	t, err := scanTemplate(s.db.QueryRowContext(ctx, `SELECT `+templateCols+` FROM channel_templates WHERE id = $1`, id))
	return t, notFound(err)
}

// CreateTemplate returns ErrConflict on a duplicate name.
func (s *Store) CreateTemplate(ctx context.Context, t Template) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO channel_templates (name, channel_type, base_url, allow_custom_base_url, models,
		  group_name, priority, weight, test_model, extra, enabled, pool_ids)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, COALESCE($12::bigint[], '{}')) RETURNING id`,
		t.Name, t.ChannelType, t.BaseURL, t.AllowCustomBaseURL, t.Models, t.Group, t.Priority, t.Weight,
		t.TestModel, string(t.Extra), t.Enabled, t.PoolIDs).Scan(&id)
	return id, templateWriteErr(err)
}

// UpdateTemplate does not touch channels already created from it. It returns
// ErrConflict on a duplicate name.
func (s *Store) UpdateTemplate(ctx context.Context, t Template) error {
	return templateWriteErr(affectedOne(s.db.ExecContext(ctx, `
		UPDATE channel_templates SET name = $2, channel_type = $3, base_url = $4,
		  allow_custom_base_url = $5, models = $6, group_name = $7, priority = $8, weight = $9,
		  test_model = $10, extra = $11, enabled = $12, pool_ids = COALESCE($13::bigint[], '{}'), updated_at = now()
		WHERE id = $1`,
		t.ID, t.Name, t.ChannelType, t.BaseURL, t.AllowCustomBaseURL, t.Models, t.Group, t.Priority, t.Weight,
		t.TestModel, string(t.Extra), t.Enabled, t.PoolIDs)))
}

// DeleteTemplate leaves existing mappings with template_id NULL.
func (s *Store) DeleteTemplate(ctx context.Context, id int64) error {
	return affectedOne(s.db.ExecContext(ctx, `DELETE FROM channel_templates WHERE id = $1`, id))
}
