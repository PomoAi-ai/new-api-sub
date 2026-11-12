// Package store is the project's own PostgreSQL database: portal/admin
// users, channel templates, channel ownership and audit logs.
package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed schema.sql
var schema string

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

type Store struct{ db *sql.DB }

// Open connects, pings and applies schema.sql under an advisory lock so
// portal and admin can start concurrently.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open app db: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping app db: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate app db: %w", err)
	}
	return &Store{db: db}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(7243001)"); err != nil {
		return err
	}
	// No arguments, so pgx sends the multi-statement file via simple protocol.
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

// WorkerLock keeps the push worker and the log sync to one admin process. The
// other advisory keys are 7243001 (schema, pools) and 7243002 (admin status).
const WorkerLock = 7243003

// Lock is a session-level advisory lock, held on a connection of its own for as
// long as that lives.
type Lock struct{ conn *sql.Conn }

// TryLock takes the advisory lock key, or returns nil when another session
// holds it.
func (s *Store) TryLock(ctx context.Context, key int64) (*Lock, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var ok bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&ok); err != nil || !ok {
		conn.Close()
		return nil, err
	}
	return &Lock{conn: conn}, nil
}

// Check fails once the connection, and with it the lock, is gone.
func (l *Lock) Check(ctx context.Context) error {
	_, err := l.conn.ExecContext(ctx, "SELECT 1")
	return err
}

// Release ends the session rather than handing it back to the pool, which
// would keep the lock.
func (l *Lock) Release() {
	l.conn.Raw(func(any) error { return driver.ErrBadConn })
	l.conn.Close()
}

func pgCode(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// notFound maps sql.ErrNoRows to ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// conds builds a parameterized WHERE clause. Each condition's format string
// takes its placeholder number.
type conds struct {
	list []string
	args []any
}

func (c *conds) add(cond string, v any) {
	c.args = append(c.args, v)
	c.list = append(c.list, fmt.Sprintf(cond, len(c.args)))
}

func (c *conds) raw(cond string) { c.list = append(c.list, cond) }

func (c *conds) where() string {
	if len(c.list) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(c.list, " AND ")
}

func affectedOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
