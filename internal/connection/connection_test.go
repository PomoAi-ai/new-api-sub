package connection

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"

	"newapisub/internal/config"
	"newapisub/internal/newapi"
	"newapisub/internal/secret"
	"newapisub/internal/store"
)

func TestReasonIsAlwaysAFixedText(t *testing.T) {
	const secret = "leak@pa:ss/word"
	quoting := func(err error) error { return fmt.Errorf("open postgres://u:%s@host/db: %w", secret, err) }
	for want, err := range map[string]error{
		"new-api 拒绝了 PAT，请检查令牌和权限": quoting(&newapi.Rejected{Status: 401, Message: secret}),
		"new-api 拒绝了请求":            quoting(&newapi.Rejected{Status: 400, Message: secret}),
		"数据库用户名或密码错误":              quoting(&pgconn.PgError{Code: "28P01", Message: secret}),
		"数据库不存在":                   quoting(&pgconn.PgError{Code: "3D000", Message: secret}),
		"数据库角色权限不足":                quoting(&pgconn.PgError{Code: "42501", Message: secret}),
		"数据库连接数已满":                 quoting(&pgconn.PgError{Code: "53300", Message: secret}),
		"数据库返回错误":                  quoting(&pgconn.PgError{Code: "XX000", Message: secret}),
		"连接超时":                     quoting(context.DeadlineExceeded),
		"无法连接到服务器":                 quoting(&net.OpError{Op: "dial", Err: errors.New(secret)}),
		"连接失败":                     quoting(errors.New(secret)),
	} {
		if got := reason(err); got != want || strings.Contains(got, secret) {
			t.Errorf("%v: got %q want %q", err, got, want)
		}
	}
	for number, want := range map[uint16]string{1045: "数据库用户名或密码错误", 1049: "数据库不存在", 1142: "数据库角色权限不足", 1143: "数据库角色权限不足",
		1040: "数据库连接数已满", 1203: "数据库连接数已满"} {
		if got := reason(quoting(&mysql.MySQLError{Number: number, Message: secret})); got != want {
			t.Errorf("%d: %q", number, got)
		}
	}
}

func TestReasonOnRealDriverErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, dsn := range []string{
		"postgres://leakuser:leak@pa:ss/word@host:notaport/db",
		"postgres://leakuser:leakpass@127.0.0.1:1/db?sslmode=disable",
		"leakuser:leak@pa:ss/word@tcp(unclosed/db",
		"leakuser:leakpass@tcp(127.0.0.1:1)/db",
	} {
		_, err := newapi.OpenDB(ctx, dsn, dsn)
		if err == nil {
			t.Fatalf("%s opened", dsn)
		}
		if got := reason(err); strings.Contains(got, "leak") {
			t.Errorf("%q", got)
		}
	}
}

func TestHostHasNoCredentials(t *testing.T) {
	for dsn, want := range map[string]string{
		"postgres://u:p@db:5432/new-api?sslmode=disable": "db:5432/new-api",
		"u:p@tcp(db:3306)/new-api?parseTime=true":        "db:3306/new-api",
		"":                                "",
		"postgres://u:p@ss:w@bad:port/db": "",
	} {
		if got := Host(dsn); got != want {
			t.Errorf("%q: %q", dsn, got)
		}
	}
}

func TestWatch(t *testing.T) {
	var version atomic.Value
	version.Store("1:5")
	failing := atomic.Bool{}
	failing.Store(true)
	stopped := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads := atomic.Int32{}
	go Watch(ctx, time.Millisecond, func(context.Context) (string, error) {
		reads.Add(1)
		if failing.Load() {
			return "", errors.New("app database is away")
		}
		return version.Load().(string), nil
	}, "1:5", func() { close(stopped) })

	// Failed reads and unchanged pools must not stop anything.
	for reads.Load() < 5 {
		time.Sleep(time.Millisecond)
	}
	failing.Store(false)
	for reads.Load() < 10 {
		time.Sleep(time.Millisecond)
	}
	select {
	case <-stopped:
		t.Fatal("stopped with nothing changed")
	default:
	}
	version.Store("1:5,2:1") // a pool added
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("not stopped by the new pool")
	}
}

// testStore is the app database in a schema of its own, and its DSN, which
// also stands in for new-api's database.
func testStore(t *testing.T) (*store.Store, *sql.DB, string) {
	t.Helper()
	base := os.Getenv("TEST_PG_DSN")
	if base == "" {
		t.Skip("TEST_PG_DSN not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	schema := "t_" + strings.ToLower(rand.Text()[:12])
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE") })
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	raw, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	st, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, raw, u.String()
}

// fakeNewAPI answers what opening a pool asks, or 503 while down.
func fakeNewAPI(t *testing.T, down *atomic.Bool) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case down.Load():
			w.WriteHeader(http.StatusServiceUnavailable)
		case r.URL.Path == "/api/channel/":
			fmt.Fprint(w, `{"success":true,"data":{"items":[]}}`)
		case r.URL.Path == "/api/status":
			fmt.Fprint(w, `{"success":true,"data":{"quota_per_unit":500000}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func testBox(t *testing.T) *secret.Box {
	t.Helper()
	b, err := secret.New([]byte(rand.Text() + rand.Text()))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func addPool(t *testing.T, st *store.Store, box *secret.Box, name, baseURL, dsn string) int64 {
	t.Helper()
	ctx := context.Background()
	m := Merged{Name: name, LogMode: "direct", AcceptNew: true, Config: config.NewAPI{BaseURL: baseURL, PAT: "nap_test", DBDSN: dsn}}
	id, err := Save(ctx, st, box, m, "test")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLoadDegradesOnlyTheFailingPool(t *testing.T) {
	st, _, dsn := testStore(t)
	box := testBox(t)
	ctx := context.Background()
	var up, down atomic.Bool
	down.Store(true)
	good := addPool(t, st, box, "good", fakeNewAPI(t, &up), dsn)
	flaky := addPool(t, st, box, "flaky", fakeNewAPI(t, &down), dsn)
	lost := addPool(t, st, testBox(t), "lost", fakeNewAPI(t, &up), dsn) // sealed under another master key

	pools, fp, err := Load(ctx, st, box, "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pools.Close()
	if stored, err := st.PoolsVersion(ctx); err != nil || fp != stored {
		t.Errorf("fingerprint %q, stored %q, %v", fp, stored, err)
	}
	if p := pools.Get(good); p.Reason != "" || p.DB == nil || p.API == nil || p.Logs == nil || p.QuotaPerUnit != 500000 {
		t.Errorf("good: %+v", p)
	}
	if p := pools.Get(flaky); p.Reason != "连接失败" || p.DB != nil {
		t.Errorf("flaky: %+v", p)
	}
	if p := pools.Get(lost); p.Reason != reasonDecrypt || p.DB != nil {
		t.Errorf("lost: %+v", p)
	}
	if pools.Home().ID != good || len(pools.List()) != 3 || pools.Get(99) != nil {
		t.Errorf("home %d, %d pools", pools.Home().ID, len(pools.List()))
	}
	if row, err := st.Pool(ctx, good); err != nil || row.QuotaPerUnit != 500000 || row.Version != 1 {
		t.Errorf("stored quota unit: %+v, %v", row, err)
	}

	// The flaky pool comes back in place, with no restart.
	down.Store(false)
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go pools.Retry(rctx, 5*time.Millisecond, st, box)
	deadline := time.Now().Add(5 * time.Second)
	for pools.Get(flaky).Reason != "" {
		if time.Now().After(deadline) {
			t.Fatal("flaky pool did not come back")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if p := pools.Get(lost); p.Reason != reasonDecrypt {
		t.Errorf("lost: %+v", p)
	}
}

func TestLoadWithoutPoolsIsUnavailable(t *testing.T) {
	st, _, _ := testStore(t)
	for _, v := range []string{"NEWAPI_BASE_URL", "NEWAPI_PAT", "NEWAPI_DB_DSN", "NEWAPI_LOG_DB_DSN", "NEWAPI_KEY_DB_DSN"} {
		t.Setenv(v, "")
	}
	if _, fp, err := Load(context.Background(), st, testBox(t), "admin", nil); !errors.Is(err, ErrUnavailable) || fp != "" {
		t.Errorf("%q, %v", fp, err)
	}
}

// The connection of before becomes pool 1 at startup, sealed under its old
// field names, and the first Load seals it to the pool.
func TestLegacyConnectionIsSealedAgain(t *testing.T) {
	st, raw, dsn := testStore(t)
	box := testBox(t)
	ctx := context.Background()
	var up atomic.Bool
	base := fakeNewAPI(t, &up)
	if _, err := raw.ExecContext(ctx, `INSERT INTO newapi_connection (base_url, pat, db_dsn, updated_by) VALUES ($1, $2, $3, 'x')`,
		base, box.NewAPI.Seal("newapi.pat", []byte("nap_old")), box.NewAPI.Seal("newapi.db_dsn", []byte(dsn))); err != nil {
		t.Fatal(err)
	}
	again, err := store.Open(ctx, dsn) // the next start migrates it
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if row, err := st.Pool(ctx, 1); err != nil || !row.AADLegacy || row.Name != "号池 1" {
		t.Fatalf("migrated: %+v, %v", row, err)
	}

	pools, fp, err := Load(ctx, st, box, "portal", nil)
	if err != nil {
		t.Fatal(err)
	}
	pools.Close()
	if p := pools.Home(); p.ID != 1 || p.Reason != "" {
		t.Errorf("pool 1: %+v", p)
	}
	row, err := st.Pool(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if pat, err := box.NewAPI.Open("pool:1:pat", row.PAT); row.AADLegacy || err != nil || string(pat) != "nap_old" {
		t.Errorf("resealed: legacy=%v %v", row.AADLegacy, err)
	}
	if _, err := box.NewAPI.Open("newapi.pat", row.PAT); err == nil {
		t.Error("still opens under the old name")
	}
	if stored, _ := st.PoolsVersion(ctx); stored != fp || fp != "1:1" {
		t.Errorf("resealing moved the version: %q -> %q", fp, stored)
	}
}
