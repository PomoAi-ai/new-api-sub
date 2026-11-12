package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// testStore is the app database in a schema of its own.
func testStore(t *testing.T) (*Store, string) {
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
	st, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, u.String()
}

func (s *Store) mustExec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func (s *Store) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// legacyChannel is what a release before pools records: the channel under its pool 1 id, without a spec.
func (s *Store) legacyChannel(t *testing.T, id int) {
	t.Helper()
	s.mustExec(t, `INSERT INTO portal_channels (admin_id, newapi_channel_id, name) VALUES (1, $1, 'old')`, id)
}

func TestUpgradeFromBeforePools(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	st.mustExec(t, `INSERT INTO admin_users (id, username, password_hash) VALUES (1, 'root', 'x')`)
	st.mustExec(t, `INSERT INTO newapi_connection (base_url, pat, db_dsn, updated_by) VALUES ('http://newapi', 'p', 'd', 'x')`)
	st.mustExec(t, `INSERT INTO channel_templates (name, channel_type, models, group_name) VALUES ('t', 1, 'm', 'default')`)
	st.legacyChannel(t, 37)
	st.legacyChannel(t, 12000) // pool 1 ids can be past 10000 already
	// The database as that release left it: no pool, no local ids yet.
	st.mustExec(t, `DROP SEQUENCE portal_channel_ids`)
	for range 2 { // every start runs it again
		if err := migrate(ctx, st.db); err != nil {
			t.Fatal(err)
		}
	}

	pools, err := st.Pools(ctx)
	if err != nil || len(pools) != 1 || pools[0].ID != 1 || !pools[0].AADLegacy || pools[0].BaseURL != "http://newapi" {
		t.Fatalf("pools: %+v, %v", pools, err)
	}
	if tpl, err := st.Template(ctx, 1); err != nil || !slices.Equal(tpl.PoolIDs, []int64{1}) {
		t.Errorf("template: %+v, %v", tpl.PoolIDs, err)
	}
	ms, err := st.Channels(ctx, ChannelFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if len(m.Placements) != 1 || !reflect.DeepEqual(m.Placements[0], Placement{PoolID: 1, NewAPIID: m.ChannelID, State: "created",
			Cached: json.RawMessage("null")}) || m.Spec != nil {
			t.Errorf("channel %d: %+v", m.ChannelID, m.Placements)
		}
	}
	ids, err := st.NextChannelIDs(ctx, 1)
	if err != nil || !slices.Equal(ids, []int{12001}) {
		t.Errorf("first local id: %v, %v", ids, err)
	}

	// A rollback adds a pool 1 channel past the local ids; the next start places it and moves past it.
	st.legacyChannel(t, 12005)
	if err := migrate(ctx, st.db); err != nil {
		t.Fatal(err)
	}
	if n := st.count(t, `SELECT count(*) FROM channel_placements WHERE pool_id = 1 AND newapi_channel_id = 12005`); n != 1 {
		t.Errorf("rollback channel: %d placements", n)
	}
	if ids, err := st.NextChannelIDs(ctx, 1); err != nil || ids[0] != 12006 {
		t.Errorf("after rollback: %v, %v", ids, err)
	}
	if n := st.count(t, `SELECT count(*) FROM channel_placements`); n != 3 {
		t.Errorf("%d placements", n)
	}

	// Once every pool is gone, the connection of before must not come back as pool 1.
	st.mustExec(t, `DELETE FROM channel_placements`)
	st.mustExec(t, `UPDATE channel_templates SET pool_ids = '{}'`)
	st.mustExec(t, `UPDATE portal_channels SET spec = '{}'`) // so that the backfill leaves them
	if err := st.DeletePool(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, st.db); err != nil {
		t.Fatal(err)
	}
	if v, err := st.PoolsVersion(ctx); err != nil || v != "" {
		t.Errorf("pools after delete: %q, %v", v, err)
	}
	if ok, err := st.ImportPool(ctx, Pool{Name: "号池 1", BaseURL: "http://newapi", PAT: []byte("p"), DBDSN: []byte("d"), UpdatedBy: "x"}); ok || err != nil {
		t.Errorf("imported over a deleted pool: %v, %v", ok, err)
	}
}

func TestNewChannelsAndTheirPlacements(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	st.mustExec(t, `INSERT INTO admin_users (id, username, password_hash) VALUES (1, 'root', 'x')`)
	for _, p := range []Pool{{ID: 1, Name: "a", BaseURL: "http://a"}, {ID: 2, Name: "b", BaseURL: "http://b"}, {ID: 3, Name: "c", BaseURL: "http://c"}} {
		p.PAT, p.DBDSN, p.LogMode, p.AcceptNew, p.UpdatedBy = []byte("p"), []byte("d"), "direct", p.ID != 3, "x"
		if err := st.CreatePool(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := st.NextChannelIDs(ctx, 2)
	if err != nil || ids[0] < 10000 {
		t.Fatalf("%v, %v", ids, err)
	}
	ch := NewChannel{ID: ids[0], AdminID: 1, Name: "n", Spec: []byte(`{"models":"m"}`), KeySealed: []byte("sealed"), PoolIDs: []int64{2, 3, 1}}
	nowhere := NewChannel{ID: ids[1], AdminID: 1, Name: "n", Spec: []byte(`{}`), PoolIDs: []int64{3}}
	if err := st.CreateChannels(ctx, []NewChannel{ch, nowhere}); !errors.Is(err, ErrNoPool) {
		t.Fatalf("pool 3 takes no new channels: %v", err)
	}
	if n := st.count(t, `SELECT count(*) FROM portal_channels`); n != 0 {
		t.Fatalf("%d channels left by a failed create", n)
	}
	if err := st.CreateChannels(ctx, []NewChannel{ch}); err != nil {
		t.Fatal(err)
	}
	m, err := st.Channel(ctx, ch.ID, ChannelFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Placements) != 2 || m.Placements[0].PoolID != 2 || m.Placements[1].PoolID != 1 || m.Placements[1].Seq != 2 ||
		m.Placements[0].State != "pending" || !strings.HasPrefix(m.Placements[0].Remark, "admin:a1:") || m.WantStatus != 1 {
		t.Fatalf("%+v", m)
	}

	// The create in pool 2 comes first; the status changes while it is pushed.
	j, err := st.ClaimJob(ctx, nil)
	if err != nil || j.PoolID != 2 || j.State != "pushing" || string(j.KeySealed) != "sealed" || j.Remark != m.Placements[0].Remark {
		t.Fatalf("%+v, %v", j, err)
	}
	if err := st.SetWantStatus(ctx, ch.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkCreated(ctx, ch.ID, 2, 37, 1, false); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkCreated(ctx, ch.ID, 2, 37, 1, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("recorded twice: %v", err)
	}
	// Pool 1 cools down, so the dirty status of pool 2 is next.
	j, err = st.ClaimJob(ctx, []int64{1})
	if err != nil || j.PoolID != 2 || j.State != "created" || !j.StatusDirty || j.KeySealed != nil || j.WantStatus != 2 {
		t.Fatalf("%+v, %v", j, err)
	}
	// Marked again while the job runs: its clear must not drop that.
	if err := st.MarkDirty(ctx, ch.ID, []int64{2}, true, false, false); err != nil {
		t.Fatal(err)
	}
	if err := st.ClearDirty(ctx, j); !errors.Is(err, ErrNotFound) {
		t.Errorf("cleared over a newer mark: %v", err)
	}
	j, err = st.ClaimJob(ctx, []int64{1})
	if err != nil || j.PoolID != 2 || !j.StatusDirty {
		t.Fatalf("%+v, %v", j, err)
	}
	if err := st.ClearDirty(ctx, j); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimJob(ctx, []int64{1}); !errors.Is(err, ErrNotFound) {
		t.Errorf("pool 2 has nothing left: %v", err)
	}

	// Pool 1: rate limited, then refused, then retried.
	j, err = st.ClaimJob(ctx, nil)
	if err != nil || j.PoolID != 1 {
		t.Fatalf("%+v, %v", j, err)
	}
	if err := st.Backoff(ctx, ch.ID, 1, 3600e9, "限流", false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimJob(ctx, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("claimed during its backoff: %v", err)
	}
	st.mustExec(t, `UPDATE channel_placements SET next_attempt_at = now()`)
	if j, err = st.ClaimJob(ctx, nil); err != nil || j.Attempts != 0 {
		t.Fatalf("%+v, %v", j, err)
	}
	if err := st.FailPush(ctx, ch.ID, 1, "拒绝"); err != nil {
		t.Fatal(err)
	}
	if err := st.RetryPlacement(ctx, ch.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.CancelPlacement(ctx, ch.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.CancelPlacement(ctx, ch.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("cancelled a created placement: %v", err)
	}

	ms, err := st.Channels(ctx, ChannelFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if got := InPool(ms, 2); !slices.Equal(got, []int{37}) || InPool(ms, 1) != nil {
		t.Errorf("in pool 2: %v", got)
	}
	if idx := PlacementIndex(ms); len(idx) != 1 || idx[PoolChannel{2, 37}] != ch.ID {
		t.Errorf("index: %v", idx)
	}
	if err := st.DeletePool(ctx, 2); !errors.Is(err, ErrConflict) {
		t.Errorf("deleted a pool with channels: %v", err)
	}
}

func TestTryLockIsHeldByOneSession(t *testing.T) {
	st, dsn := testStore(t)
	ctx := context.Background()
	other, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	l, err := st.TryLock(ctx, WorkerLock)
	if err != nil || l == nil {
		t.Fatalf("%v, %v", l, err)
	}
	if l2, err := other.TryLock(ctx, WorkerLock); l2 != nil || err != nil {
		t.Fatalf("taken twice: %v", err)
	}
	if err := l.Check(ctx); err != nil {
		t.Fatal(err)
	}
	l.Release()
	// The server frees the lock once it has seen the session close, a moment later.
	var l2 *Lock
	for deadline := time.Now().Add(2 * time.Second); l2 == nil && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if l2, err = other.TryLock(ctx, WorkerLock); err != nil {
			t.Fatal(err)
		}
	}
	if l2 == nil {
		t.Fatal("not free after release")
	}
	l2.Release()
}

// The admin worker holds WorkerLock for good; admin status changes and deletes
// must not wait on it.
func TestAdminWritesDoNotWaitOnTheWorkerLock(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	l, err := st.TryLock(ctx, WorkerLock)
	if err != nil || l == nil {
		t.Fatalf("%v, %v", l, err)
	}
	defer l.Release()
	id, err := st.CreateAdmin(ctx, "lock-probe", "x", "admin")
	if err != nil {
		t.Fatal(err)
	}
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := st.SetAdminStatus(tctx, id, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteAdmin(tctx, id); err != nil {
		t.Fatal(err)
	}
}

// Saving a pool or its sync control ends the sync's backoff: what failed may be
// what was set right.
func TestSavingAPoolEndsItsSyncBackoff(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	id, err := st.NextPoolID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreatePool(ctx, Pool{ID: id, Name: "p", BaseURL: "http://p", PAT: []byte("x"), LogMode: "sync", AcceptNew: true, UpdatedBy: "t"}); err != nil {
		t.Fatal(err)
	}
	control := SyncControl{Enabled: true, IntervalS: 60, WindowS: 600, MaxWindows: 6}
	backedOff := func() bool {
		t.Helper()
		ps, err := st.PoolSync(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return ps.Failures > 0 || ps.BackoffUntil > 0
	}
	for _, save := range []func() error{
		func() error {
			p, err := st.Pool(ctx, id)
			if err == nil {
				err = st.SavePool(ctx, p)
			}
			return err
		},
		func() error { return st.SetSyncControl(ctx, id, control, 0) },
	} {
		if err := st.SetSyncControl(ctx, id, control, 0); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if err := st.FailSync(ctx, id, "new-api 拒绝了 PAT"); err != nil {
				t.Fatal(err)
			}
		}
		if !backedOff() {
			t.Fatal("no backoff after failures")
		}
		if err := save(); err != nil {
			t.Fatal(err)
		}
		if backedOff() {
			t.Error("still backing off after a save")
		}
	}
}
