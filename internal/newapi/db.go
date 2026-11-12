package newapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// dialect covers the two databases new-api supports that we can read.
type dialect struct{ pg bool }

// detectDialect mirrors new-api's DSN handling: postgres(ql):// is
// PostgreSQL, SQLite/ClickHouse forms are unsupported, anything else must
// parse as a MySQL DSN.
func detectDialect(dsn string) (driver string, d dialect, err error) {
	switch {
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		return "pgx", dialect{pg: true}, nil
	case dsn == "", dsn == "local",
		strings.HasPrefix(dsn, "clickhouse://"), strings.HasPrefix(dsn, "tcp://"),
		strings.HasPrefix(dsn, "http://"), strings.HasPrefix(dsn, "https://"):
		return "", dialect{}, fmt.Errorf("unsupported new-api database DSN (only PostgreSQL and MySQL are supported)")
	}
	// The parser's error quotes the DSN, password included.
	if _, err := mysql.ParseDSN(dsn); err != nil {
		return "", dialect{}, errors.New("new-api database DSN is neither PostgreSQL nor a valid MySQL DSN")
	}
	return "mysql", dialect{}, nil
}

// DB reads new-api's channels (main DB) and logs (log DB, may be separate,
// so the two are never joined).
type DB struct {
	main, logs   *sql.DB
	mainD, logsD dialect
}

// Connections per process, 10 open and 5 idle in all: the database behind both
// is usually new-api's primary, and README caps the role at 20 for the two
// processes. A request beyond these waits until its context ends. Logs get more
// because an export holds a connection for as long as it runs.
const (
	mainMaxOpen, mainMaxIdle = 3, 2
	logsMaxOpen, logsMaxIdle = 7, 3
)

func openOne(ctx context.Context, name, dsn string, maxOpen, maxIdle int) (*sql.DB, dialect, error) {
	driver, d, err := detectDialect(dsn)
	if err != nil {
		return nil, d, fmt.Errorf("%s: %w", name, err)
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, d, fmt.Errorf("open %s: %w", name, err)
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, d, fmt.Errorf("ping %s: %w", name, err)
	}
	return db, d, nil
}

// OpenConn opens a pool on a new-api database with OpenDB's DSN handling; pg
// says whether it is PostgreSQL, else it is MySQL.
func OpenConn(ctx context.Context, name, dsn string, maxOpen, maxIdle int) (db *sql.DB, pg bool, err error) {
	db, d, err := openOne(ctx, name, dsn, maxOpen, maxIdle)
	return db, d.pg, err
}

func OpenDB(ctx context.Context, mainDSN, logDSN string) (*DB, error) {
	main, mainD, err := openOne(ctx, "NEWAPI_DB_DSN", mainDSN, mainMaxOpen, mainMaxIdle)
	if err != nil {
		return nil, err
	}
	logs, logsD, err := openOne(ctx, "NEWAPI_LOG_DB_DSN", logDSN, logsMaxOpen, logsMaxIdle)
	if err != nil {
		main.Close()
		return nil, err
	}
	return &DB{main: main, logs: logs, mainD: mainD, logsD: logsD}, nil
}

// refused is a query the role may not run, or on a table its database lacks: a
// separate log database has no channels.
func refused(err error) bool {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code == "42501" || pe.Code == "42P01"
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return me.Number == 1142 || me.Number == 1143 || me.Number == 1146
	}
	return false
}

// ReadsKeys says whether the main or the log connection can read channels.key,
// which only the key connection may. Comparing DSNs cannot tell: two may differ
// in a parameter and still log in as one role.
func (d *DB) ReadsKeys(ctx context.Context) (bool, error) {
	for _, c := range []struct {
		db *sql.DB
		d  dialect
	}{{d.main, d.mainD}, {d.logs, d.logsD}} {
		query := "SELECT `key` FROM channels WHERE 1=0"
		if c.d.pg {
			query = `SELECT "key" FROM channels WHERE 1=0`
		}
		var key string
		err := c.db.QueryRowContext(ctx, query).Scan(&key)
		if errors.Is(err, sql.ErrNoRows) {
			return true, nil
		}
		if !refused(err) {
			return false, err
		}
	}
	return false, nil
}

// SetMaxOpenConns caps each of the two pools, for a short-lived DB.
func (d *DB) SetMaxOpenConns(n int) {
	d.main.SetMaxOpenConns(n)
	d.logs.SetMaxOpenConns(n)
}

func (d *DB) Close() {
	d.main.Close()
	d.logs.Close()
}
