package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// AuditLog is one recorded operation. Detail must never hold passwords,
// channel keys or the PAT.
type AuditLog struct {
	ID           int64           `json:"id"`
	ActorType    string          `json:"actor_type"`
	ActorID      *int64          `json:"actor_id"`
	ActorName    string          `json:"actor_name"`
	Action       string          `json:"action"`
	TargetType   string          `json:"target_type"`
	TargetID     string          `json:"target_id"`
	Success      bool            `json:"success"`
	Detail       json.RawMessage `json:"detail"`
	IP           string          `json:"ip"`
	ForwardedFor string          `json:"forwarded_for"`
	UserAgent    string          `json:"user_agent"`
	CreatedAt    int64           `json:"created_at"`
}

func (s *Store) InsertAudit(ctx context.Context, a AuditLog) error {
	if len(a.Detail) == 0 {
		a.Detail = json.RawMessage("{}")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_logs (actor_type, actor_id, actor_name, action, target_type, target_id,
		  success, detail, ip, forwarded_for, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		a.ActorType, a.ActorID, a.ActorName, a.Action, a.TargetType, a.TargetID,
		a.Success, string(a.Detail), a.IP, a.ForwardedFor, a.UserAgent)
	return err
}

// AuditFilter fields are optional except Page and PageSize; zero values
// mean "any".
type AuditFilter struct {
	ActorType string
	ActorID   int64
	ActorName string
	Action    string
	Category  string // an action's prefix: "channel" is channel.*
	Success   *bool
	NoViews   bool   // leave out the view.* reads
	IP        string // a complete address, matched exactly
	IPPrefix  string // the start of an address; only characters LIKE takes literally
	Start     int64
	End       int64
	AdminID   int64 // scope of a regular admin: their own and their portal users' operations
	Page      int   // from 1
	PageSize  int
}

// ListAudit returns one page, newest first, and the total count.
func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]AuditLog, int64, error) {
	var c conds
	if f.ActorType != "" {
		c.add("actor_type = $%d", f.ActorType)
	}
	if f.ActorID > 0 {
		c.add("actor_id = $%d", f.ActorID)
	}
	if f.ActorName != "" {
		c.add("actor_name = $%d", f.ActorName)
	}
	if f.Action != "" {
		c.add("action = $%d", f.Action)
	}
	if f.Category != "" {
		c.add("action LIKE $%d", f.Category+".%")
	}
	if f.Success != nil {
		c.add("success = $%d", *f.Success)
	}
	if f.NoViews {
		c.raw("action NOT LIKE 'view.%'")
	}
	if f.IP != "" {
		c.add("ip = $%d", f.IP)
	}
	if f.IPPrefix != "" {
		c.add("ip LIKE $%d", f.IPPrefix+"%")
	}
	if f.Start > 0 {
		c.add("created_at >= to_timestamp($%d::BIGINT)", f.Start)
	}
	if f.End > 0 {
		c.add("created_at < to_timestamp($%d::BIGINT)", f.End)
	}
	if f.AdminID > 0 {
		c.add(`((actor_type = 'admin' AND actor_id = $%[1]d) OR
			(actor_type = 'portal' AND actor_id IN (SELECT id FROM portal_users WHERE admin_id = $%[1]d)))`, f.AdminID)
	}
	where := c.where()
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM audit_logs`+where, c.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args := append(c.args, f.PageSize, (f.Page-1)*f.PageSize)
	q := `SELECT id, actor_type, actor_id, actor_name, action, target_type, target_id, success,
		detail::text, ip, forwarded_for, user_agent, EXTRACT(EPOCH FROM created_at)::BIGINT FROM audit_logs` +
		where + fmt.Sprintf(" ORDER BY id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AuditLog{}
	for rows.Next() {
		var a AuditLog
		var detail string
		if err := rows.Scan(&a.ID, &a.ActorType, &a.ActorID, &a.ActorName, &a.Action, &a.TargetType, &a.TargetID,
			&a.Success, &detail, &a.IP, &a.ForwardedFor, &a.UserAgent, &a.CreatedAt); err != nil {
			return nil, 0, err
		}
		a.Detail = json.RawMessage(detail)
		out = append(out, a)
	}
	return out, total, rows.Err()
}
