package store

import (
	"context"
	"database/sql"
	"errors"
)

// ErrLastSuper refuses to disable or delete the only enabled super admin.
var ErrLastSuper = errors.New("last enabled super admin")

// AdminInfo is one row of the admin list.
type AdminInfo struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	Role         string `json:"role"`
	Status       int    `json:"status"`
	UserCount    int    `json:"user_count"`
	ChannelCount int    `json:"channel_count"`
	CreatedAt    int64  `json:"created_at"`
	MFAEnabled   bool   `json:"mfa_enabled"`
}

func (s *Store) ListAdmins(ctx context.Context) ([]AdminInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.username, a.role, a.status,
		       (SELECT count(*) FROM portal_users u WHERE u.admin_id = a.id),
		       (SELECT count(*) FROM portal_channels c WHERE c.admin_id = a.id),
		       EXTRACT(EPOCH FROM a.created_at)::BIGINT,
		       EXISTS (SELECT 1 FROM auth_factors f WHERE f.admin_user_id = a.id AND f.kind <> 'backup')
		FROM admin_users a ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminInfo{}
	for rows.Next() {
		var x AdminInfo
		if err := rows.Scan(&x.ID, &x.Username, &x.Role, &x.Status, &x.UserCount, &x.ChannelCount, &x.CreatedAt, &x.MFAEnabled); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// CreateAdmin inserts an admin; a duplicate username returns ErrConflict.
func (s *Store) CreateAdmin(ctx context.Context, name, hash, role string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO admin_users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		name, hash, role).Scan(&id)
	if pgCode(err) == "23505" {
		return 0, ErrConflict
	}
	return id, err
}

// lockAdmin serializes admin status changes and deletes, then returns the
// target's status and whether it is the only enabled super. Without the lock
// two supers could disable each other at the same time.
func lockAdmin(ctx context.Context, tx *sql.Tx, id int64) (status int, lastSuper bool, err error) {
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(7243002)`); err != nil {
		return 0, false, err
	}
	err = tx.QueryRowContext(ctx, `
		SELECT a.status, a.role = 'super' AND a.status = 1 AND NOT EXISTS (
		  SELECT 1 FROM admin_users o WHERE o.role = 'super' AND o.status = 1 AND o.id <> a.id)
		FROM admin_users a WHERE a.id = $1`, id).Scan(&status, &lastSuper)
	return status, lastSuper, notFound(err)
}

// SetAdminStatus sets status; disabling also invalidates sessions. It returns
// the previous status.
func (s *Store) SetAdminStatus(ctx context.Context, id int64, status int) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	prev, lastSuper, err := lockAdmin(ctx, tx, id)
	if err != nil {
		return 0, err
	}
	if status == 2 && lastSuper {
		return prev, ErrLastSuper
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_users SET status = $2::smallint,
		  session_version = session_version + CASE WHEN $2::smallint = 2 THEN 1 ELSE 0 END
		WHERE id = $1`, id, status); err != nil {
		return prev, err
	}
	return prev, tx.Commit()
}

// DeleteAdmin fails with ErrConflict while the admin still owns users or
// channels. It returns the deleted username.
func (s *Store) DeleteAdmin(ctx context.Context, id int64) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	_, lastSuper, err := lockAdmin(ctx, tx, id)
	if err != nil {
		return "", err
	}
	if lastSuper {
		return "", ErrLastSuper
	}
	var name string
	err = tx.QueryRowContext(ctx, `DELETE FROM admin_users WHERE id = $1 RETURNING username`, id).Scan(&name)
	if pgCode(err) == "23503" {
		return "", ErrConflict
	}
	if err != nil {
		return "", notFound(err)
	}
	return name, tx.Commit()
}
