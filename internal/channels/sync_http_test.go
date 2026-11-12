package channels_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"newapisub/internal/config"
	"newapisub/internal/connection"
	"newapisub/internal/logstore"
	"newapisub/internal/newapi"
)

// useMongo gives e a database of its own on TEST_MONGO_URI, dropped afterwards.
func (e *env) useMongo() {
	e.t.Helper()
	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		e.t.Skip("TEST_MONGO_URI not set")
	}
	nonce := make([]byte, 6)
	rand.Read(nonce)
	db := "t_" + hex.EncodeToString(nonce)
	ctx, cancel := context.WithCancel(context.Background())
	e.t.Cleanup(cancel)
	e.mongo = logstore.Dial(ctx, uri, db, time.Second)
	wait, done := context.WithTimeout(ctx, 30*time.Second)
	defer done()
	if e.mongo.Wait(wait) == nil {
		e.t.Fatal("MongoDB did not open")
	}
	e.t.Cleanup(func() {
		client, err := mongo.Connect(options.Client().ApplyURI(uri))
		if err != nil {
			return
		}
		client.Database(db).Drop(context.Background())
		client.Disconnect(context.Background())
		e.mongo.Close(context.Background())
	})
}

// syncEnv has pool 1 read directly and pool 2 sync its logs into MongoDB,
// without a database of its own; templates send channels to both.
func syncEnv(t *testing.T) *env {
	e := bareEnv(t)
	e.useMongo()
	e.addPool("号池 1", e.fake, e.dsn)
	m := connection.Merged{Name: "号池 2", LogMode: "sync", AcceptNew: true, Config: config.NewAPI{BaseURL: e.p2.newAPI.URL, PAT: "nap_test"}}
	if _, err := connection.Save(context.Background(), e.st, e.box, m, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.raw.Exec(`UPDATE channel_templates SET pool_ids = '{1,2}'`); err != nil {
		t.Fatal(err)
	}
	e.serve()
	return e
}

// synced counts the logs of a pool in the sync store.
func (e *env) synced(poolID int64) int64 {
	e.t.Helper()
	f := newapi.Filter{AllChannels: true, End: time.Now().Unix() + 1, Page: 1, PageSize: 1}
	_, n, err := e.mongo.Reader(poolID).Logs(context.Background(), f, false)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestAPoolWithoutADatabase(t *testing.T) {
	e := syncEnv(t)
	ctx := context.Background()
	alice := e.asUser(e.alice)
	id := alice.create(nil)[0]
	// new-api did not answer the id: the worker found it by its remark in new-api's list.
	n2 := e.placed(id, 2)
	if n2 == 0 {
		t.Fatal("not pushed to the pool without a database")
	}

	// The pages read the rows the push worker last read; money counts them as of then.
	if _, err := e.p2.raw.Exec(`UPDATE channels SET used_quota = 2000000 WHERE id = $1`, n2); err != nil {
		t.Fatal(err)
	}
	e.push()
	if l := alice.list(""); len(l) != 1 || !l[0].Exists {
		t.Fatalf("%+v", l)
	}
	acct := decode[struct {
		UsedUSD float64 `json:"used_usd"`
		AsOf    *int64  `json:"as_of"`
	}](t, alice.want(200, "GET", "/api/settlements", nil))
	if acct.UsedUSD != 2 || acct.AsOf == nil {
		t.Errorf("%+v", acct)
	}
	// A change that went through there is kept in the cached row.
	alice.want(204, "POST", e.channelPath(id, "/status"), ch{"status": 2})
	var cachedStatus string
	if err := e.raw.QueryRow(`SELECT state_json->>'status' FROM channel_placements WHERE channel_id = $1 AND pool_id = 2`, id).Scan(&cachedStatus); err != nil {
		t.Fatal(err)
	}
	if e.p2.status(n2) != 2 || cachedStatus != "2" {
		t.Errorf("new-api %d, cached %s", e.p2.status(n2), cachedStatus)
	}

	// Its logs come from MongoDB, by local id, in the home pool's unit.
	now := time.Now().Unix()
	if _, _, err := e.mongo.Get().Insert(ctx, 2, []newapi.Log{
		{ID: 1, CreatedAt: now - 60, Type: 2, ChannelID: n2, ChannelName: "their name", ModelName: "m", Quota: 1000, RequestID: "r1"},
		{ID: 2, CreatedAt: now - 30, Type: 2, ChannelID: 999, ChannelName: "someone else's", ModelName: "m", Quota: 500, RequestID: "r2"},
	}); err != nil {
		t.Fatal(err)
	}
	e.addLog(logRow{channel: id, typ: 2, at: now - 90, model: "m", quota: 300, prompt: 1, output: 1})
	rng := fmt.Sprintf("start=%d&end=%d", now-3600, now+60)
	type logs struct {
		Items []struct {
			ID              int64  `json:"id"`
			PoolID          int64  `json:"pool_id"`
			PoolName        string `json:"pool_name"`
			ChannelID       int    `json:"channel_id"`
			NewAPIChannelID int    `json:"newapi_channel_id"`
			ChannelName     string `json:"channel_name"`
			Quota           int64  `json:"quota"`
		} `json:"items"`
		Total     int64 `json:"total"`
		Retention *struct {
			PoolIDs []int64 `json:"pool_ids"`
		} `json:"retention"`
	}
	for _, c := range []caller{e.asAdmin(e.super), alice} {
		got := decode[logs](t, c.want(200, "GET", "/api/logs?"+rng, nil))
		if got.Total != 3 || len(got.Items) != 3 || got.Retention != nil {
			t.Fatalf("%+v", got)
		}
		theirs, ours := got.Items[0], got.Items[1]
		if ours.PoolID != 2 || ours.PoolName != "号池 2" || ours.ChannelID != id || ours.Quota != 500 || ours.NewAPIChannelID != 0 {
			t.Errorf("our channel's log: %+v", ours)
		}
		// Not ours: never under one of our ids; the name new-api gave it only for an admin.
		if super := c.mux == e.adminMux; theirs.ChannelID != 0 || (theirs.ChannelName == "someone else's") != super || theirs.Quota != 250 {
			t.Errorf("another's channel's log: %+v", theirs)
		}
		if super := c.mux == e.adminMux; (theirs.NewAPIChannelID == 999) != super {
			t.Errorf("new-api id %d shown to super=%v", theirs.NewAPIChannelID, super)
		}
	}
	old := decode[logs](t, alice.want(200, "GET", fmt.Sprintf("/api/logs?start=%d&end=%d", now-40*86400, now+60), nil))
	if old.Retention == nil || !slices.Equal(old.Retention.PoolIDs, []int64{2}) {
		t.Errorf("retention %+v", old.Retention)
	}
	// A file would leave out what the sync no longer has, and say nothing.
	if got := alice.want(400, "GET", fmt.Sprintf("/api/logs/export?tz_offset=0&start=%d&end=%d", now-40*86400, now+60), nil); !strings.Contains(got, "30 天") {
		t.Errorf("export: %s", got)
	}

	// Per pool: only the caller's channels, each pool's quota in the home pool's unit.
	type poolRow struct {
		PoolID   int64  `json:"pool_id"`
		PoolName string `json:"pool_name"`
		Count    int64  `json:"count"`
		Quota    int64  `json:"quota"`
	}
	for _, c := range []caller{e.asAdmin(e.super), alice} {
		rows := decode[struct {
			Rows []poolRow `json:"rows"`
		}](t, c.want(200, "GET", "/api/stats?by=pool&"+rng, nil)).Rows
		want := []poolRow{{2, "号池 2", 1, 500}, {1, "号池 1", 1, 300}}
		if !slices.Equal(rows, want) {
			t.Errorf("%+v", rows)
		}
	}
	stat := decode[struct {
		Quota  int64   `json:"quota"`
		Synced []int64 `json:"synced_pool_ids"`
	}](t, alice.want(200, "GET", "/api/logs/stat?"+rng, nil))
	if stat.Quota != 1050 || !slices.Equal(stat.Synced, []int64{2}) {
		t.Errorf("%+v", stat)
	}
}

// Beyond the part of new-api's list the worker reads, a channel is read by its
// id; only new-api saying it has none makes it gone. Money never waits for the
// worker's first read.
func TestTheChannelsOfAPoolWithoutADatabaseAreReadByID(t *testing.T) {
	e := syncEnv(t)
	alice := e.asUser(e.alice)
	id := alice.create(nil)[0]
	n2 := e.placed(id, 2)
	// Someone else's channels push ours off the pages the worker reads.
	if _, err := e.p2.raw.Exec(`INSERT INTO channels (key, name, status, created_time) SELECT 'k', 'theirs', 1, 100 FROM generate_series(1, 3001)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.p2.raw.Exec(`UPDATE channels SET used_quota = 2000000 WHERE id = $1`, n2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.raw.Exec(`UPDATE channel_placements SET state_json = NULL, state_at = NULL WHERE pool_id = 2`); err != nil {
		t.Fatal(err)
	}
	type account struct {
		UsedUSD float64 `json:"used_usd"`
		AsOf    *int64  `json:"as_of"`
		Stale   bool    `json:"stale"`
	}
	if acct := decode[account](t, alice.want(200, "GET", "/api/settlements", nil)); acct.UsedUSD != 2 || acct.AsOf == nil || acct.Stale {
		t.Errorf("never read: %+v", acct)
	}

	if _, err := e.raw.Exec(`UPDATE channel_placements SET state_at = now() - interval '20 minutes' WHERE pool_id = 2`); err != nil {
		t.Fatal(err)
	}
	if acct := decode[account](t, alice.want(200, "GET", "/api/settlements", nil)); !acct.Stale {
		t.Errorf("read long ago: %+v", acct)
	}
	if _, err := e.p2.raw.Exec(`UPDATE channels SET used_quota = 3000000 WHERE id = $1`, n2); err != nil {
		t.Fatal(err)
	}
	e.push()
	if pages := e.p2.callsTo("GET", "/api/channel/"); len(pages) == 0 || pages[len(pages)-1].Path != fmt.Sprintf("/api/channel/%d", n2) {
		t.Errorf("not read by id: %+v", pages)
	}
	if acct := decode[account](t, alice.want(200, "GET", "/api/settlements", nil)); acct.UsedUSD != 3 || acct.Stale {
		t.Errorf("read again: %+v", acct)
	}

	if _, err := e.p2.raw.Exec(`DELETE FROM channels WHERE id = $1`, n2); err != nil {
		t.Fatal(err)
	}
	e.push()
	var gone bool
	if err := e.raw.QueryRow(`SELECT state_json IS NULL AND state_at IS NOT NULL FROM channel_placements WHERE channel_id = $1 AND pool_id = 2`, id).Scan(&gone); err != nil || !gone {
		t.Errorf("deleted in new-api: gone=%v %v", gone, err)
	}
}

// A create tried before, and put off since, is looked for back to its first
// attempt, not this one.
func TestARetriedPushLooksBackToItsFirstAttempt(t *testing.T) {
	e := syncEnv(t)
	id := e.asUser(e.alice).submit(nil)[0]
	var remark string
	if err := e.raw.QueryRow(`UPDATE channel_placements SET attempt_started_at = now() - interval '1 hour'
		WHERE channel_id = $1 AND pool_id = 2 RETURNING remark`, id).Scan(&remark); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	var made int
	if err := e.p2.raw.QueryRow(`INSERT INTO channels (key, name, status, remark, models, created_time) VALUES ('k', 'n', 1, $1, 'tpl-a', $2) RETURNING id`,
		remark, now-3600).Scan(&made); err != nil {
		t.Fatal(err)
	}
	if _, err := e.p2.raw.Exec(`INSERT INTO channels (key, name, status, created_time) SELECT 'k', 'theirs', 1, $1 FROM generate_series(1, 100)`, now-1800); err != nil {
		t.Fatal(err)
	}
	e.push()
	if n := len(e.p2.callsTo("POST", "/api/channel/")); n != 0 || e.placed(id, 2) != made {
		t.Errorf("created again: %d creates, placed at %d, made %d", n, e.placed(id, 2), made)
	}
}

func TestOnlyASuperControlsTheSync(t *testing.T) {
	e := syncEnv(t)
	ctx := context.Background()
	root, regular := e.asAdmin(e.super), e.asAdmin(e.a)
	control := ch{"enabled": true, "interval_s": 60, "window_s": 600, "max_windows": 6, "backfill_days": 1}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/api/pools/2/sync", control}, {"GET", "/api/pools/2/conflicts", nil},
		{"POST", "/api/pools/2/conflicts/resolve", ch{"id": 1}}, {"POST", "/api/pools/2/sync/reset", ch{"backfill_days": 0}},
		{"GET", "/api/pools/2/sync/verify", nil},
	} {
		regular.want(403, c.method, c.path, c.body)
	}

	root.want(400, "PUT", "/api/pools/2/sync", ch{"enabled": true, "interval_s": 10, "window_s": 600, "max_windows": 6})
	pool := decode[struct {
		Sync *struct {
			Enabled   bool   `json:"enabled"`
			IntervalS int    `json:"interval_s"`
			LagS      int64  `json:"lag_s"`
			LastRunAt *int64 `json:"last_run_at"`
		} `json:"sync"`
	}](t, root.want(200, "PUT", "/api/pools/2/sync", control))
	if s := pool.Sync; s == nil || !s.Enabled || s.IntervalS != 60 || s.LastRunAt != nil || s.LagS < 86400-60 || s.LagS > 86400+60 {
		t.Fatalf("%+v", pool.Sync)
	}

	// A conflict that paused the sync: once accepted, it goes on.
	if _, err := e.st.AddConflict(ctx, 2, "fingerprint", 7, map[string]any{"fp": "a"}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.PauseSync(ctx, 2, "fingerprint"); err != nil {
		t.Fatal(err)
	}
	cs := decode[[]struct {
		ID         int64  `json:"id"`
		Kind       string `json:"kind"`
		ResolvedAt *int64 `json:"resolved_at"`
	}](t, root.want(200, "GET", "/api/pools/2/conflicts", nil))
	if len(cs) != 1 || cs[0].Kind != "fingerprint" || cs[0].ResolvedAt != nil {
		t.Fatalf("%+v", cs)
	}
	if got := root.want(200, "POST", "/api/pools/2/conflicts/resolve", ch{"id": cs[0].ID}); got != `{"resumed":true}`+"\n" {
		t.Errorf("%s", got)
	}
	root.want(404, "POST", "/api/pools/2/conflicts/resolve", ch{"id": cs[0].ID})

	// Starting over deletes the pool's synced logs, and needs the proof.
	if _, _, err := e.mongo.Get().Insert(ctx, 2, []newapi.Log{{ID: 1, CreatedAt: time.Now().Unix() - 60, Type: 2, ChannelID: 5, RequestID: "x"}}); err != nil {
		t.Fatal(err)
	}
	root.want(403, "POST", "/api/pools/2/sync/reset", ch{"backfill_days": 0})
	if n := e.synced(2); n != 1 {
		t.Fatalf("%d logs after a refused reset", n)
	}
	root.withProof("newapi_config").want(200, "POST", "/api/pools/2/sync/reset", ch{"backfill_days": 0})
	if n := e.synced(2); n != 0 {
		t.Errorf("%d logs after the reset", n)
	}
	root.want(400, "GET", "/api/pools/2/sync/verify", nil) // nothing to compare with: no database

	// Without MONGO_URI a pool that syncs cannot start.
	if _, _, err := connection.Load(ctx, e.st, e.box, "admin", nil); err == nil || errors.Is(err, connection.ErrUnavailable) {
		t.Errorf("loaded without MongoDB: %v", err)
	}
}

func TestTheSameNewAPICannotBeAddedTwice(t *testing.T) {
	e := newEnv(t)
	e.fake.insertLog(logRow{channel: 37, typ: 2, at: time.Now().Unix() - 60, model: "m", quota: 1})
	// Another address of pool 1's new-api, and its database.
	body := ch{"name": "again", "base_url": strings.Replace(e.newAPI.URL, "127.0.0.1", "localhost", 1), "pat": "nap_test", "db_dsn": e.dsn, "id": 0}
	res := decode[struct {
		OK    bool `json:"ok"`
		Steps []struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		} `json:"steps"`
	}](t, e.asAdmin(e.super).want(200, "POST", "/api/pools/test", body))
	if last := res.Steps[len(res.Steps)-1]; res.OK || last.Name != "重复检测" || !strings.Contains(last.Message, "号池 1") {
		t.Errorf("%+v", res)
	}
}
