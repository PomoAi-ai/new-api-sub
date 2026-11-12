package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Roles of admin_users.
const (
	RoleSuper = "super" // everything, plus admins and templates
	RoleAdmin = "admin" // own portal users and channels
)

type User struct {
	ID             int64  `json:"id"`
	Username       string `json:"username"`
	PasswordHash   string `json:"-"`
	Role           string `json:"role,omitempty"` // admin_users only
	Status         int    `json:"status"`
	SessionVersion int    `json:"-"`
	AdminID        int64  `json:"admin_id,omitempty"`       // portal_users only: the owning admin
	AdminUsername  string `json:"admin_username,omitempty"` // filled by ListPortalUsers
	ChannelCount   int    `json:"channel_count"`
	CreatedAt      int64  `json:"created_at"`
	// Filled for the user list only.
	MFAEnabled bool     `json:"mfa_enabled"` // a TOTP or passkey is bound; backup codes do not count
	LogScope   string   `json:"log_scope"`   // portal_users only: empty follows the global setting
	UsedUSD    *float64 `json:"used_usd"`    // by the caller, from new-api; nil if that could not be read
	SettledUSD float64  `json:"settled_usd"` // the latest settlement
}

// Users is one user table (portal_users or admin_users). extra selects the
// role and admin_id columns, each of which only one table has; fk is the table's
// column in auth_factors.
type Users struct {
	db    *sql.DB
	table string
	extra string
	fk    string
}

func (s *Store) PortalUsers() Users {
	return Users{s.db, "portal_users", "'', admin_id", "portal_user_id"}
}
func (s *Store) AdminUsers() Users { return Users{s.db, "admin_users", "role, 0", "admin_user_id"} }

// Realm names the table, which is what tells the two systems' accounts apart:
// their ids overlap.
func (u Users) Realm() string { return u.table }

func (u Users) one(ctx context.Context, where string, arg any) (User, error) {
	var x User
	err := u.db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT id, username, password_hash, status, session_version, EXTRACT(EPOCH FROM created_at)::BIGINT, %s
		 FROM %s WHERE %s = $1`, u.extra, u.table, where), arg).
		Scan(&x.ID, &x.Username, &x.PasswordHash, &x.Status, &x.SessionVersion, &x.CreatedAt, &x.Role, &x.AdminID)
	return x, notFound(err)
}

func (u Users) ByName(ctx context.Context, name string) (User, error) {
	return u.one(ctx, "username", name)
}

func (u Users) Get(ctx context.Context, id int64) (User, error) {
	return u.one(ctx, "id", id)
}

// Session implements auth.Lookup.
func (u Users) Session(ctx context.Context, id int64) (string, string, int, bool, error) {
	x, err := u.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return "", "", 0, false, nil
	}
	if err != nil {
		return "", "", 0, false, err
	}
	return x.Username, x.Role, x.SessionVersion, x.Status == 1, nil
}

// BumpVersion invalidates all existing sessions of the user.
func (u Users) BumpVersion(ctx context.Context, id int64) error {
	return affectedOne(u.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET session_version = session_version + 1 WHERE id = $1`, u.table), id))
}

// SetPassword stores a new hash, invalidates sessions, revokes a portal user's
// API tokens in the same statement and returns the new session version.
func (u Users) SetPassword(ctx context.Context, id int64, hash string) (int, error) {
	revoke := ""
	if u.table == "portal_users" {
		revoke = `WITH revoked AS (DELETE FROM api_tokens WHERE user_id = $1) `
	}
	var ver int
	err := u.db.QueryRowContext(ctx, fmt.Sprintf(
		`%sUPDATE %s SET password_hash = $2, session_version = session_version + 1 WHERE id = $1
		 RETURNING session_version`, revoke, u.table), id, hash).Scan(&ver)
	return ver, notFound(err)
}

// SetUsername renames the user: a taken name returns ErrConflict. Sessions stay
// valid, they hold the id.
func (u Users) SetUsername(ctx context.Context, id int64, name string) error {
	err := affectedOne(u.db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET username = $2 WHERE id = $1`, u.table), id, name))
	if pgCode(err) == "23505" {
		return ErrConflict
	}
	return err
}

func (u Users) Count(ctx context.Context) (int, error) {
	var n int
	err := u.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM %s`, u.table)).Scan(&n)
	return n, err
}

// CreatePortalUser inserts a user owned by adminID: a duplicate username
// returns ErrConflict, a missing admin ErrNotFound.
func (s *Store) CreatePortalUser(ctx context.Context, name, hash string, adminID int64) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO portal_users (username, password_hash, admin_id) VALUES ($1, $2, $3) RETURNING id`,
		name, hash, adminID).Scan(&id)
	switch pgCode(err) {
	case "23505":
		return 0, ErrConflict
	case "23503":
		return 0, ErrNotFound
	}
	return id, err
}

// ListPortalUsers returns the users of adminID, or everyone when it is 0.
func (s *Store) ListPortalUsers(ctx context.Context, adminID int64) ([]User, error) {
	var c conds
	if adminID > 0 {
		c.add("u.admin_id = $%d", adminID)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.username, u.status, EXTRACT(EPOCH FROM u.created_at)::BIGINT, u.admin_id, a.username,
		       (SELECT count(*) FROM portal_channels c WHERE c.user_id = u.id), u.log_scope,
		       COALESCE((SELECT s.amount_usd FROM settlements s WHERE s.user_id = u.id ORDER BY s.id DESC LIMIT 1), 0),
		       EXISTS (SELECT 1 FROM auth_factors f WHERE f.portal_user_id = u.id AND f.kind <> 'backup')
		FROM portal_users u JOIN admin_users a ON a.id = u.admin_id`+c.where()+` ORDER BY u.id`, c.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var x User
		if err := rows.Scan(&x.ID, &x.Username, &x.Status, &x.CreatedAt, &x.AdminID, &x.AdminUsername, &x.ChannelCount,
			&x.LogScope, &x.SettledUSD, &x.MFAEnabled); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// SetPortalStatus sets status; disabling also invalidates sessions. It
// returns the previous status.
func (s *Store) SetPortalStatus(ctx context.Context, id int64, status int) (int, error) {
	var prev int
	err := s.db.QueryRowContext(ctx, `
		UPDATE portal_users n SET status = $2::smallint,
		  session_version = n.session_version + CASE WHEN $2::smallint = 2 THEN 1 ELSE 0 END
		FROM portal_users o WHERE n.id = o.id AND n.id = $1
		RETURNING o.status`, id, status).Scan(&prev)
	return prev, notFound(err)
}

// DeletePortalUser fails with ErrConflict while the user still owns channels,
// soft-deleted ones included, or has settlements. It returns the deleted username.
func (s *Store) DeletePortalUser(ctx context.Context, id int64) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx, `DELETE FROM portal_users WHERE id = $1 RETURNING username`, id).Scan(&name)
	if pgCode(err) == "23503" {
		return "", ErrConflict
	}
	return name, notFound(err)
}
