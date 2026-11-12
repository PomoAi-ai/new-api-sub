package logstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"newapisub/internal/newapi"
	"newapisub/internal/store"
)

// testMongo is a database of its own on TEST_MONGO_URI, dropped afterwards.
func testMongo(t *testing.T) *Store {
	t.Helper()
	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("TEST_MONGO_URI not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, uri, "t_"+strings.ToLower(rand.Text()[:12]))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.logs.Database().Drop(ctx)
		s.Close(ctx)
	})
	return s
}

// testPG is a schema of its own on TEST_PG_DSN, dropped afterwards, and its DSN.
func testPG(t *testing.T) (*sql.DB, string) {
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
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, u.String()
}

func (s *Store) count(t *testing.T, filter bson.D) int64 {
	t.Helper()
	n, err := s.logs.CountDocuments(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIndexesKeepLogsUniquePerPoolFor30Days(t *testing.T) {
	s := testMongo(t)
	cur, err := s.logs.Indexes().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var specs []bson.M
	if err := cur.All(context.Background(), &specs); err != nil {
		t.Fatal(err)
	}
	byName := map[string]bson.M{}
	for _, spec := range specs {
		byName[spec["name"].(string)] = spec
	}
	if ttl := byName["ts_1"]["expireAfterSeconds"]; ttl != int32(2592000) {
		t.Errorf("ts TTL = %v (%T)", ttl, ttl)
	}
	if byName["pool_id_1_log_id_1"]["unique"] != true {
		t.Errorf("pool_id+log_id: %v", byName["pool_id_1_log_id_1"])
	}
	if byName["request_id_1"]["partialFilterExpression"] == nil {
		t.Errorf("request_id: %v", byName["request_id_1"])
	}
	if _, ok := byName["pool_id_1_created_at_-1_log_id_-1"]; !ok {
		t.Errorf("no list index: %v", byName)
	}
}

func TestInsertIsIdempotentAndNeverOverwrites(t *testing.T) {
	s := testMongo(t)
	ctx := context.Background()
	now := time.Now().Unix()
	logs := []newapi.Log{
		{ID: 1, CreatedAt: now - 10, Type: 2, Quota: 5, RequestID: "r1", Content: "upstream said sk-abcdefghijklmnop",
			Other: `{"cache_tokens":7,"x":"key sk-abcdefghijklmnop"}`},
		{ID: 2, CreatedAt: now - 9, Type: 3}, // management log: not kept
		{ID: 3, CreatedAt: now - 8, Type: 5},
		{ID: 4, CreatedAt: now - 7, Type: 6, Quota: 2},
	}
	n, conflicts, err := s.Insert(ctx, 1, logs)
	if err != nil || n != 3 || len(conflicts) != 0 {
		t.Fatalf("first: n=%d conflicts=%v err=%v", n, conflicts, err)
	}
	n, conflicts, err = s.Insert(ctx, 1, logs)
	if err != nil || n != 0 || len(conflicts) != 0 || s.count(t, bson.D{}) != 3 {
		t.Fatalf("again: n=%d conflicts=%v err=%v", n, conflicts, err)
	}
	// Another pool has its own ids.
	if n, _, err := s.Insert(ctx, 2, logs[:1]); err != nil || n != 1 {
		t.Fatalf("pool 2: n=%d err=%v", n, err)
	}

	var stored doc
	if err := s.logs.FindOne(ctx, bson.D{{Key: "pool_id", Value: 1}, {Key: "log_id", Value: 1}}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.Content+stored.Other, "sk-abc") || stored.CacheRead != 7 || stored.TS.Unix() != now-10 {
		t.Errorf("stored %+v", stored)
	}

	reused := logs[0]
	reused.Quota, reused.RequestID = 99, "other"
	n, conflicts, err = s.Insert(ctx, 1, []newapi.Log{reused, logs[3]})
	if err != nil || n != 0 || len(conflicts) != 1 {
		t.Fatalf("reused id: n=%d conflicts=%v err=%v", n, conflicts, err)
	}
	if c := conflicts[0]; c.LogID != 1 || c.FP != Fingerprint(reused) || c.StoredFP != Fingerprint(logs[0]) || c.RequestID != "other" {
		t.Errorf("conflict %+v", c)
	}
	var after doc
	if err := s.logs.FindOne(ctx, bson.D{{Key: "pool_id", Value: 1}, {Key: "log_id", Value: 1}}).Decode(&after); err != nil || after.Quota != 5 {
		t.Errorf("overwritten: %+v %v", after, err)
	}
}

func TestOtherIntMatchesTheSQL(t *testing.T) {
	for other, want := range map[string]int64{
		`{"cache_tokens":12}`:             12,
		`{"cache_tokens": 1234567890123}`: 0, // 13 digits
		`{"a":{"cache_tokens":7}}`:        7, // the text match finds nested fields too
		`not json "cache_tokens":5 x`:     5,
		`{"cache_tokens":-3}`:             0,
		`{"cache_tokens":"9"}`:            0,
		`{"cache_tokens":5.5}`:            5,
		``:                                0,
	} {
		if got := otherInt(otherCacheRead, other); got != want {
			t.Errorf("%s: %d, want %d", other, got, want)
		}
	}
}

// fakeExport is new-api's export over logs: [start, end], newest first.
type fakeExport struct {
	logs   []newapi.Log
	err    error
	served time.Time
	calls  [][2]int64
}

func (f *fakeExport) ExportLogs(_ context.Context, start, end int64, fn func(newapi.Log) error) (int, time.Time, error) {
	f.calls = append(f.calls, [2]int64{start, end})
	if f.err != nil {
		return 0, f.served, f.err
	}
	n := 0
	for i := len(f.logs) - 1; i >= 0; i-- {
		if l := f.logs[i]; l.CreatedAt >= start && l.CreatedAt <= end {
			n++
			if err := fn(l); err != nil {
				return n, f.served, err
			}
		}
	}
	return n, f.served, nil
}

type syncEnv struct {
	t     *testing.T
	st    *store.Store
	logs  *Store
	pools map[int64]*fakeExport
	s     *Syncer
}

func newSyncEnv(t *testing.T) *syncEnv {
	logs := testMongo(t)
	_, dsn := testPG(t)
	st, err := store.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &syncEnv{t: t, st: st, logs: logs, pools: map[int64]*fakeExport{}}
	e.s = &Syncer{St: st, Logs: opened(logs), Pool: func(id int64) Exporter {
		if p, ok := e.pools[id]; ok {
			return p
		}
		return nil
	}}
	return e
}

// opened is a Lazy that opened as s.
func opened(s *Store) *Lazy {
	l := &Lazy{ready: make(chan struct{}), s: s}
	close(l.ready)
	return l
}

// pool adds a pool syncing from a day back in windows of an hour, all in one run.
func (e *syncEnv) pool(logs ...newapi.Log) int64 {
	e.t.Helper()
	ctx := context.Background()
	id, err := e.st.NextPoolID(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	name := "p" + rand.Text()[:6]
	if err := e.st.CreatePool(ctx, store.Pool{ID: id, Name: name, BaseURL: "http://" + name, PAT: []byte("x"), DBDSN: []byte("x"),
		LogMode: "direct", AcceptNew: true, UpdatedBy: "test"}); err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.SetSyncControl(ctx, id, store.SyncControl{Enabled: true, IntervalS: 30, WindowS: 3600, MaxWindows: 60}, 1); err != nil {
		e.t.Fatal(err)
	}
	e.pools[id] = &fakeExport{logs: logs, served: time.Now()}
	return id
}

func (e *syncEnv) state(id int64) store.PoolSync {
	e.t.Helper()
	ps, err := e.st.PoolSync(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return ps
}

func (e *syncEnv) run(id int64) store.PoolSync {
	e.t.Helper()
	e.s.syncPool(context.Background(), e.state(id))
	return e.state(id)
}

func (e *syncEnv) conflicts(id int64) []store.SyncConflict {
	e.t.Helper()
	cs, err := e.st.SyncConflicts(context.Background(), id, 50)
	if err != nil {
		e.t.Fatal(err)
	}
	return cs
}

// recent are logs of the last hours, ids rising with time, oldest first.
func recent(firstID int64, n int, prefix string) []newapi.Log {
	now := time.Now().Unix()
	out := make([]newapi.Log, n)
	for i := range out {
		out[i] = newapi.Log{ID: firstID + int64(i), CreatedAt: now - 7200 + int64(i)*60, Type: 2, ChannelID: 3, Quota: int64(i),
			RequestID: prefix + rand.Text()[:10]}
	}
	return out
}

func TestSyncReadsEveryWindowOnceAndRerunsStoreNothingTwice(t *testing.T) {
	e := newSyncEnv(t)
	logs := recent(100, 50, "a")
	id := e.pool(logs...)
	before := e.state(id).Cursor
	ps := e.run(id)
	now := time.Now().Unix()
	if ps.Cursor < now-settle-2 || ps.Cursor > now-settle || ps.Paused != "" || ps.RowsTotal != 50 || ps.MaxLogID != 149 || ps.LastOKAt == 0 {
		t.Fatalf("after a run: %+v", ps)
	}
	calls := e.pools[id].calls
	if calls[0][0] != before-overlap {
		t.Errorf("first window starts %d, want the cursor less the overlap %d", calls[0][0], before-overlap)
	}
	for i := 1; i < len(calls); i++ {
		if calls[i][0] != calls[i-1][1]+1-overlap {
			t.Errorf("window %d starts at %d after one ending at %d", i, calls[i][0], calls[i-1][1])
		}
	}
	if got := e.logs.count(t, bson.D{{Key: "pool_id", Value: id}}); got != 50 {
		t.Fatalf("%d logs stored", got)
	}

	// The same windows again: nothing new, no conflict.
	if err := e.st.ResetSync(context.Background(), id, 1, "test"); err != nil {
		t.Fatal(err)
	}
	ps = e.run(id)
	if got := e.logs.count(t, bson.D{{Key: "pool_id", Value: id}}); got != 50 || ps.Paused != "" || ps.RowsTotal != 50 || len(e.conflicts(id)) != 0 {
		t.Fatalf("rerun: %d logs, %+v, %v", got, ps, e.conflicts(id))
	}
}

func TestSyncFailureBacksOffAndKeepsTheCursor(t *testing.T) {
	e := newSyncEnv(t)
	id := e.pool()
	e.pools[id].err = errors.New("new-api GET /api/log/export: the stream ended before its trailer")
	before := e.state(id)
	ps := e.run(id)
	if ps.Cursor != before.Cursor || ps.Failures != 1 || ps.BackoffUntil <= time.Now().Unix() || ps.LastError != "导出中断或不完整" {
		t.Fatalf("%+v", ps)
	}
	e.pools[id].err = &newapi.Rejected{Method: "GET", Path: "/api/log/export", Message: "Another log export is running. Try again after it finishes."}
	if ps = e.run(id); ps.Failures != 2 || ps.LastError != "同步用户在 new-api 上有另一个导出正在进行" {
		t.Fatalf("%+v", ps)
	}
	due, err := e.st.DueSyncs(context.Background())
	if err != nil || len(due) != 0 {
		t.Fatalf("due while backing off: %v %v", due, err)
	}
	delete(e.pools, id)
	if ps = e.run(id); ps.Failures != 3 || ps.Cursor != before.Cursor {
		t.Fatalf("pool unavailable: %+v", ps)
	}
}

// While MongoDB cannot be reached, never opened or gone since, a due sync fails
// as such and no window moves its cursor; once it answers, the failure clears.
func TestSyncFailsVisiblyWhileMongoIsDown(t *testing.T) {
	e := newSyncEnv(t)
	ctx := context.Background()
	id := e.pool()
	before := e.state(id)
	up := e.s.Logs
	e.s.Logs = &Lazy{ready: make(chan struct{})}
	if !e.s.round(ctx, false) {
		t.Error("not noted down")
	}
	ps := e.state(id)
	if ps.LastError != storeDown || ps.Failures != 1 || ps.BackoffUntil == 0 || ps.Cursor != before.Cursor || len(e.pools[id].calls) != 0 {
		t.Fatalf("never opened: %+v, %d exports", ps, len(e.pools[id].calls))
	}
	if e.s.Logs = up; e.s.round(ctx, true) {
		t.Error("still down")
	}
	if ps = e.state(id); ps.LastError != "" || ps.Failures != 0 || ps.BackoffUntil != 0 {
		t.Fatalf("back: %+v", ps)
	}

	// Gone since it opened: windows with nothing to store keep the cursor too.
	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://127.0.0.1:1").SetServerSelectionTimeout(200 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Disconnect(ctx) })
	e.s.Logs = opened(&Store{client: client, logs: client.Database("x").Collection("logs")})
	if ps = e.run(id); ps.LastError != storeDown || ps.Cursor != before.Cursor || len(e.pools[id].calls) == 0 {
		t.Fatalf("gone: %+v", ps)
	}
}

// The server log of a failed run has the fixed text, never new-api's words.
func TestSyncFailureLogsTheFixedTextOnly(t *testing.T) {
	e := newSyncEnv(t)
	id := e.pool()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	for _, c := range []struct {
		err  error
		want string
	}{
		{&newapi.Rejected{Method: "GET", Path: "/api/log/export", Message: "The access token lacks the required permission: log:read", Status: 403},
			"new-api 拒绝了 PAT，请检查令牌和 log:read 权限"},
		{fmt.Errorf("new-api GET /api/log/export: %w", &url.Error{Op: "Get", URL: "http://secret-host/api/log/export", Err: errors.New("connection refused")}),
			"new-api 暂时不可用"},
	} {
		e.pools[id].err = c.err
		if err := e.st.SetSyncControl(context.Background(), id, store.SyncControl{Enabled: true, IntervalS: 30, WindowS: 3600, MaxWindows: 60}, 1); err != nil {
			t.Fatal(err)
		}
		if ps := e.run(id); ps.LastError != c.want {
			t.Errorf("%v: last error %q", c.err, ps.LastError)
		}
	}
	if got := buf.String(); strings.Contains(got, "lacks the required") || strings.Contains(got, "secret-host") || !strings.Contains(got, "new-api 暂时不可用") {
		t.Errorf("server log: %s", got)
	}
}

func TestSyncPausesOnConflicts(t *testing.T) {
	ctx := context.Background()
	t.Run("fingerprint, until accepted", func(t *testing.T) {
		e := newSyncEnv(t)
		logs := recent(1, 5, "f")
		id := e.pool(logs...)
		reused := logs[2]
		reused.Quota = 1000
		if _, _, err := e.logs.Insert(ctx, id, []newapi.Log{reused}); err != nil {
			t.Fatal(err)
		}
		ps := e.run(id)
		cs := e.conflicts(id)
		if ps.Paused != "fingerprint" || ps.Cursor > reused.CreatedAt || len(cs) != 1 || cs[0].Kind != "fingerprint" || cs[0].LogID != 3 {
			t.Fatalf("%+v %+v", ps, cs)
		}
		if strings.Contains(string(cs[0].Detail), "content") {
			t.Errorf("detail %s", cs[0].Detail)
		}
		resumed, err := e.st.ResolveConflict(ctx, id, cs[0].ID, "root")
		if err != nil || !resumed {
			t.Fatalf("resolve: %v %v", resumed, err)
		}
		if ps = e.run(id); ps.Paused != "" || ps.Cursor <= reused.CreatedAt {
			t.Fatalf("accepted conflict paused again: %+v", ps)
		}
		var kept doc
		if err := e.logs.logs.FindOne(ctx, bson.D{{Key: "pool_id", Value: id}, {Key: "log_id", Value: 3}}).Decode(&kept); err != nil || kept.Quota != 1000 {
			t.Errorf("stored log overwritten: %+v %v", kept, err)
		}
	})
	t.Run("duplicate source", func(t *testing.T) {
		e := newSyncEnv(t)
		logs := recent(1, 5, "d")
		first := e.pool(logs...)
		if ps := e.run(first); ps.Paused != "" || ps.RowsTotal != 5 {
			t.Fatalf("first pool: %+v", ps)
		}
		second := e.pool(logs...) // the same new-api again
		ps := e.run(second)
		cs := e.conflicts(second)
		if ps.Paused != "duplicate_source" || ps.RowsTotal != 0 || len(cs) != 1 || !strings.Contains(string(cs[0].Detail), `"other_pool_id"`) {
			t.Fatalf("%+v %+v", ps, cs)
		}
		if got := e.logs.count(t, bson.D{{Key: "pool_id", Value: second}}); got != 0 {
			t.Errorf("%d logs stored twice", got)
		}
	})
	t.Run("zero id", func(t *testing.T) {
		e := newSyncEnv(t)
		logs := recent(0, 3, "z")
		id := e.pool(logs...)
		if ps := e.run(id); ps.Paused != "zero_id" || len(e.conflicts(id)) != 1 {
			t.Fatalf("%+v %+v", ps, e.conflicts(id))
		}
	})
	t.Run("id regression, resumed on a new baseline", func(t *testing.T) {
		e := newSyncEnv(t)
		old := recent(1000, 10, "o")[:5] // the first hour
		id := e.pool(old...)
		if ps := e.run(id); ps.MaxLogID != 1004 {
			t.Fatalf("%+v", ps)
		}
		// new-api counted ids anew: logs from minutes later, ids far below.
		fresh := recent(1, 10, "n")[8:]
		for i := range fresh {
			fresh[i].CreatedAt = time.Now().Unix() - 60 + int64(i)
		}
		fresh[0].CreatedAt -= 30
		e.pools[id].logs = append(old, fresh...)
		if err := e.st.ResetSync(ctx, id, 1, "test"); err != nil { // read the hour again
			t.Fatal(err)
		}
		ps := e.state(id)
		if err := e.st.AdvanceSync(ctx, id, ps.Cursor, store.SyncWindow{Cursor: time.Now().Unix() - 600, MaxLogID: 1004, MaxLogAt: old[4].CreatedAt}); err != nil {
			t.Fatal(err)
		}
		before := e.state(id)
		ps = e.run(id)
		if ps.Paused != "id_regression" || ps.Cursor != before.Cursor {
			t.Fatalf("%+v", ps)
		}
		cs := e.conflicts(id)
		if resumed, err := e.st.ResolveConflict(ctx, id, cs[0].ID, "root"); err != nil || !resumed {
			t.Fatalf("resolve: %v %v", resumed, err)
		}
		if ps = e.run(id); ps.Paused != "" || ps.MaxLogID != fresh[1].ID {
			t.Fatalf("after resolving: %+v", ps)
		}
	})
	t.Run("clock skew only warns", func(t *testing.T) {
		e := newSyncEnv(t)
		id := e.pool(recent(1, 3, "c")...)
		e.pools[id].served = time.Now().Add(-5 * time.Minute)
		ps := e.run(id)
		cs := e.conflicts(id)
		if ps.Paused != "" || ps.RowsTotal != 3 || len(cs) != 1 || cs[0].Kind != "clock_skew" {
			t.Fatalf("%+v %+v", ps, cs)
		}
	})
}
