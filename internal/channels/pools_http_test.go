package channels_test

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"newapisub/internal/store"
)

type placementResp struct {
	State    string `json:"state"`
	PoolID   int64  `json:"pool_id"`
	PoolName string `json:"pool_name"`
	Error    string `json:"error"`
	NewAPIID int    `json:"newapi_channel_id"`
}

type pooledItem struct {
	ID         int             `json:"id"`
	Name       string          `json:"name"`
	Status     int             `json:"status"`
	Exists     bool            `json:"exists"`
	UsedQuota  int64           `json:"used_quota"`
	PushState  string          `json:"push_state"`
	Placements []placementResp `json:"placements"`
	Pending    bool            `json:"status_pending"`
}

func (c caller) pooledList() []pooledItem {
	c.e.t.Helper()
	return decode[[]pooledItem](c.e.t, c.want(200, "GET", "/api/channels", nil))
}

func (e *env) placementState(local int, pool int64) (state, lastError string) {
	e.t.Helper()
	if err := e.raw.QueryRow(`SELECT state, last_error FROM channel_placements WHERE channel_id = $1 AND pool_id = $2`, local, pool).
		Scan(&state, &lastError); err != nil {
		e.t.Fatal(err)
	}
	return state, lastError
}

// due makes every put-off job due now, as time passing would.
func (e *env) due() {
	e.t.Helper()
	if _, err := e.raw.Exec(`UPDATE channel_placements SET next_attempt_at = now()`); err != nil {
		e.t.Fatal(err)
	}
}

func (f *fake) status(id int) int {
	f.t.Helper()
	var s int
	if err := f.raw.QueryRow(`SELECT status FROM channels WHERE id = $1`, id).Scan(&s); err != nil {
		f.t.Fatal(err)
	}
	return s
}

type logPage struct {
	Items []struct {
		ChannelID       int    `json:"channel_id"`
		NewAPIChannelID int    `json:"newapi_channel_id"`
		PoolID          int64  `json:"pool_id"`
		PoolName        string `json:"pool_name"`
		ModelName       string `json:"model_name"`
		Quota           int64  `json:"quota"`
	} `json:"items"`
	Total    int `json:"total"`
	Degraded []struct {
		PoolID int64  `json:"pool_id"`
		Name   string `json:"name"`
		Reason string `json:"reason"`
	} `json:"degraded"`
}

// Both pools have a new-api channel 1. Neither must stand in for the other,
// and a channel that is not ours never passes for one of ours.
func TestPoolsWithTheSameIDsStayApart(t *testing.T) {
	e := newEnv(t)
	e.template(30, "https://p2.example.com", false, 2)
	alice, bobby, super := e.asUser(e.alice), e.asUser(e.bobby), e.asAdmin(e.super)
	a := alice.create(ch{})[0]
	b := bobby.create(ch{"type": 30})[0]
	if e.placed(a, 1) != 1 || e.placed(b, 2) != 1 {
		t.Fatalf("new-api ids %d and %d, want 1 in both pools", e.placed(a, 1), e.placed(b, 2))
	}
	now := time.Now().Unix()
	logRange := fmt.Sprintf("start=%d&end=%d", now-60, now+60)
	e.addLog(logRow{channel: a, typ: 2, at: now - 30, model: "m-alice", quota: 10, other: "{}"})
	e.p2.insertLog(logRow{channel: 1, typ: 2, at: now - 20, model: "m-bobby", quota: 20, other: "{}"}) // pool 2's unit is half as much
	e.p2.insertLog(logRow{channel: 7, typ: 2, at: now - 10, model: "m-other", quota: 4, other: "{}"})  // not ours
	for _, id := range []int64{e.alice, e.bobby} {
		if _, err := e.st.SetPortalLogScope(context.Background(), id, store.LogScopeSelf); err != nil {
			t.Fatal(err)
		}
	}
	for c, want := range map[caller]struct {
		id    int
		model string
		pool  int64
	}{alice: {a, "m-alice", 1}, bobby: {b, "m-bobby", 2}} {
		logs := decode[logPage](t, c.want(200, "GET", "/api/logs?"+logRange, nil))
		if logs.Total != 1 || logs.Items[0].ChannelID != want.id || logs.Items[0].ModelName != want.model || logs.Items[0].Quota != 10 ||
			logs.Items[0].PoolID != want.pool || logs.Items[0].PoolName == "" || logs.Items[0].NewAPIChannelID != 0 {
			t.Errorf("%d: logs %+v", want.id, logs)
		}
		for _, path := range []string{"/api/stats?by=channel&", "/api/stats/flow?", "/api/stats?by=pool&"} {
			rows := decode[struct {
				Rows []struct {
					ChannelID int    `json:"channel_id"`
					PoolID    int64  `json:"pool_id"`
					Quota     int64  `json:"quota"`
					ModelName string `json:"model_name"`
				} `json:"rows"`
			}](t, c.want(200, "GET", path+logRange, nil)).Rows
			if len(rows) != 1 || rows[0].Quota != 10 || (path == "/api/stats?by=pool&" && rows[0].PoolID != want.pool) ||
				(path != "/api/stats?by=pool&" && rows[0].ChannelID != want.id) {
				t.Errorf("%d %s: %+v", want.id, path, rows)
			}
		}
		other := a
		if want.id == a {
			other = b
		}
		c.want(404, "GET", "/api/stats?"+logRange+"&channel_id="+strconv.Itoa(other), nil)
	}

	all := decode[logPage](t, super.want(200, "GET", "/api/logs?"+logRange, nil))
	if all.Total != 3 || all.Items[0].ChannelID != 0 || all.Items[0].NewAPIChannelID != 7 || all.Items[0].PoolID != 2 ||
		all.Items[1].ChannelID != b || all.Items[2].ChannelID != a {
		t.Errorf("super: %+v", all)
	}
	if regular := decode[logPage](t, e.asAdmin(e.a).want(200, "GET", "/api/logs?"+logRange, nil)); regular.Items[0].NewAPIChannelID != 0 {
		t.Errorf("a regular admin sees a foreign channel's id: %+v", regular.Items[0])
	}
	// Ids in a filter are ours: pool 2's channel 7, or 1, is no channel of ours.
	for _, id := range []string{"1", "7"} {
		if got := decode[logPage](t, super.want(200, "GET", "/api/logs?"+logRange+"&channel_id="+id, nil)); got.Total != 0 {
			t.Errorf("channel_id=%s: %+v", id, got)
		}
	}
	if got := decode[logPage](t, super.want(200, "GET", "/api/logs?"+logRange+"&pool_id=1", nil)); got.Total != 1 || got.Items[0].ChannelID != a {
		t.Errorf("pool_id=1: %+v", got)
	}
	// No such pool, or for the portal one none of its channels is in: the same answer.
	for _, c := range []struct {
		who  caller
		pool string
	}{{super, "99"}, {alice, "2"}, {alice, "99"}} {
		if got := c.who.want(400, "GET", "/api/logs?"+logRange+"&pool_id="+c.pool, nil); !strings.Contains(got, "号池不存在") {
			t.Errorf("pool_id=%s: %s", c.pool, got)
		}
	}
	alice.want(200, "GET", "/api/stats?by=pool&"+logRange+"&pool_id=1", nil)
}

func TestPushFindsWhatAnEarlierAttemptCreated(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)

	// A worker created the channel and died before recording it.
	crashed := alice.submit(ch{})[0]
	var remark string
	if err := e.raw.QueryRow(`UPDATE channel_placements SET state = 'pushing', attempt_started_at = now(), next_attempt_at = now()
		WHERE channel_id = $1 RETURNING remark`, crashed).Scan(&remark); err != nil {
		t.Fatal(err)
	}
	var made int
	if err := e.raw.QueryRow(`INSERT INTO channels (key, name, status, remark, models, created_time) VALUES ('k', 'n', 1, $1, 'tpl-a', 100) RETURNING id`, remark).Scan(&made); err != nil {
		t.Fatal(err)
	}
	e.push()
	if len(e.callsTo("POST", "/api/channel/")) != 0 || e.placed(crashed, 1) != made {
		t.Errorf("created again: %d creates, placed at %d", len(e.callsTo("POST", "/api/channel/")), e.placed(crashed, 1))
	}

	// The create goes through and its answer is lost.
	e.breakCreate = true
	lost := alice.create(ch{})[0]
	e.breakCreate = false
	var n int
	if err := e.raw.QueryRow(`SELECT count(*) FROM channels c JOIN channel_placements p ON p.remark = c.remark WHERE p.channel_id = $1`, lost).Scan(&n); err != nil || n != 1 {
		t.Errorf("%d channels with its remark (%v)", n, err)
	}
	if state, _ := e.placementState(lost, 1); state != "created" || e.nid(lost) == 0 {
		t.Errorf("state %s", state)
	}

	// A refusal is final until someone retries it.
	e.rejectCreate = true
	refused := alice.create(ch{})[0]
	if state, msg := e.placementState(refused, 1); state != "failed" || msg != "refused by new-api" {
		t.Errorf("refused: %s %q", state, msg)
	}
	if n := e.auditCount("channel.push", false); n != 1 {
		t.Errorf("%d failed push records", n)
	}
	item := alice.pooledList()[0]
	if item.ID != refused || item.PushState != "failed" || item.Exists || item.Placements[0].Error != "" || item.Placements[0].PoolName != "号池 1" {
		t.Errorf("portal: %+v", item)
	}
	alice.want(409, "POST", e.channelPath(refused, "/test"), nil)
	alice.want(404, "POST", e.channelPath(refused, "/placements/1/retry"), nil) // not the portal's
	adminA := e.asAdmin(e.a)
	if p := adminA.pooledList()[0].Placements[0]; p.Error != "refused by new-api" || p.NewAPIID != 0 {
		t.Errorf("admin: %+v", p)
	}
	e.rejectCreate = false
	e.asAdmin(e.b).want(404, "POST", e.channelPath(refused, "/placements/1/retry"), nil)
	adminA.want(404, "POST", e.channelPath(refused, "/placements/2/retry"), nil)
	adminA.want(202, "POST", e.channelPath(refused, "/placements/1/retry"), nil)
	adminA.want(409, "POST", e.channelPath(refused, "/placements/1/retry"), nil)
	e.push()
	if state, _ := e.placementState(refused, 1); state != "created" {
		t.Errorf("after the retry: %s", state)
	}
	if n := e.auditCount("channel.push", true); n != 3 {
		t.Errorf("%d push records", n)
	}
}

// A channel deleted before it reached a pool reaches it disabled.
func TestSoftDeleteWhilePushingCreatesItDisabled(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	id := alice.submit(ch{})[0]
	alice.want(204, "DELETE", e.channelPath(id, ""), nil)
	e.push()
	if got := e.lastCreate().Create.Channel.Status; got != 2 || e.status(e.nid(id)) != 2 {
		t.Errorf("sent status %d, now %d", got, e.status(e.nid(id)))
	}
}

func TestAChannelInTwoPools(t *testing.T) {
	e := newEnv(t)
	e.template(40, "https://both.example.com", false, 1, 2)
	alice, super := e.asUser(e.alice), e.asAdmin(e.super)
	id := alice.create(ch{"type": 40})[0]
	one, two := e.placed(id, 1), e.placed(id, 2)
	if one == 0 || two == 0 {
		t.Fatalf("placed at %d and %d", one, two)
	}
	item := alice.pooledList()[0]
	if item.PushState != "done" || len(item.Placements) != 2 || item.Placements[1].PoolName != "号池 2" || item.Placements[1].NewAPIID != 0 {
		t.Errorf("portal: %+v", item)
	}
	if p := super.pooledList()[0].Placements; p[0].NewAPIID != one || p[1].NewAPIID != two {
		t.Errorf("super: %+v", p)
	}

	// Pool 2 does not take the change now: the worker brings it in line later.
	e.p2.notInCache = true
	if body := alice.want(202, "POST", e.channelPath(id, "/status"), ch{"status": 2}); !strings.Contains(body, `"pending_pools":[2]`) {
		t.Error(body)
	}
	if e.status(one) != 2 || e.p2.status(two) != 1 {
		t.Errorf("statuses %d %d", e.status(one), e.p2.status(two))
	}
	// Disabled as asked, and said to be on its way while one pool still serves it.
	if got := alice.pooledList()[0]; got.Status != 2 || !got.Pending {
		t.Errorf("listed while one pool still serves it: %+v", got)
	}
	e.p2.notInCache = false
	e.due()
	e.push()
	if e.p2.status(two) != 2 {
		t.Errorf("pool 2 status %d", e.p2.status(two))
	}
	if got := alice.pooledList()[0]; got.Status != 2 || got.Pending {
		t.Errorf("listed once every pool took it: %+v", got)
	}
	e.p2.rejectUpdate = true
	alice.want(202, "PUT", e.channelPath(id, ""), ch{"name": "renamed"})
	e.p2.rejectUpdate = false
	e.due()
	e.push()
	var name string
	if err := e.p2.raw.QueryRow(`SELECT name FROM channels WHERE id = $1`, two).Scan(&name); err != nil || name != "renamed" {
		t.Errorf("pool 2 name %q (%v)", name, err)
	}
	// Nothing took it: an error, and nothing changes.
	e.rejectUpdate, e.p2.rejectUpdate = true, true
	alice.want(502, "PUT", e.channelPath(id, ""), ch{"name": "again"})
	e.rejectUpdate, e.p2.rejectUpdate = false, false
	if got := e.localName(id); got != "renamed" {
		t.Errorf("local name %q", got)
	}

	// Used quota in each pool's own unit, shown in the home pool's.
	if _, err := e.raw.Exec(`UPDATE channels SET used_quota = $1 WHERE id = $2`, testQuotaPerUnit, one); err != nil {
		t.Fatal(err)
	}
	if _, err := e.p2.raw.Exec(`UPDATE channels SET used_quota = $1 WHERE id = $2`, 2*testQuotaPerUnit, two); err != nil {
		t.Fatal(err)
	}
	if got := alice.pooledList()[0].UsedQuota; got != 2*testQuotaPerUnit {
		t.Errorf("used quota %d", got)
	}
	used := func() float64 {
		return decode[struct {
			UsedUSD float64 `json:"used_usd"`
		}](t, alice.want(200, "GET", "/api/settlements", nil)).UsedUSD
	}
	if got := used(); got != 2 {
		t.Errorf("used %v USD", got)
	}

	// Pool 2 goes away: the logs say so, money is not added up from part of it.
	now := time.Now().Unix()
	logRange := fmt.Sprintf("start=%d&end=%d", now-60, now+60)
	e.addLog(logRow{channel: id, typ: 2, at: now - 10, model: "m", quota: 3, other: "{}"})
	e.p2.newAPI.Close()
	e.serve()
	alice = e.asUser(e.alice)
	logs := decode[logPage](t, alice.want(200, "GET", "/api/logs?"+logRange, nil))
	if logs.Total != 1 || len(logs.Degraded) != 1 || logs.Degraded[0].PoolID != 2 || logs.Degraded[0].Reason != "号池暂时不可用" {
		t.Errorf("logs: %+v", logs)
	}
	// Only the admins learn why.
	if d := decode[logPage](t, e.asAdmin(e.super).want(200, "GET", "/api/logs?"+logRange, nil)).Degraded; len(d) != 1 || d[0].Reason == "" || d[0].Reason == "号池暂时不可用" {
		t.Errorf("admin degraded: %+v", d)
	}
	// A script is told too, and gets the pool of each row: a log id is unique only with it.
	script := e.asToken(e.alice)
	type notices struct {
		Items []struct {
			PoolID int64 `json:"pool_id"`
		} `json:"items"`
		Degraded []struct {
			PoolID int64 `json:"pool_id"`
		} `json:"degraded"`
	}
	for _, path := range []string{"/api/log/self", "/api/log/self/stat"} {
		got := decode[notices](t, string(script.ok("GET", path, nil).Data))
		if len(got.Degraded) != 1 || got.Degraded[0].PoolID != 2 || path == "/api/log/self" && (len(got.Items) != 1 || got.Items[0].PoolID != 1) {
			t.Errorf("%s: %+v", path, got)
		}
	}
	if got := decode[notices](t, script.want(200, "GET", "/api/data/self", nil)); len(got.Degraded) != 1 {
		t.Errorf("data/self: %+v", got)
	}
	alice.want(502, "GET", "/api/settlements", nil)
	if item := alice.pooledList()[0]; !item.Exists || item.Status != 2 {
		t.Errorf("listed: %+v", item)
	}
	e.asAdmin(e.a).want(409, "DELETE", e.channelPath(id, "?purge=1"), nil) // pool 1 still has it
}

// A pool that fails once the export runs is a 503, as one down from the start
// is: never a short file, never a 500.
func TestAnExportFailsWholeWhenAPoolFailsMidway(t *testing.T) {
	e := newEnv(t)
	now := time.Now().Unix()
	e.addLog(logRow{channel: 37, typ: 2, at: now - 10, model: "m", quota: 3, other: "{}"})
	query := fmt.Sprintf("tz_offset=0&start=%d&end=%d", now-60, now+60)
	super := e.asAdmin(e.super)
	if status, body := super.download(query); status != 200 {
		t.Fatalf("%d %s", status, body)
	}
	if _, err := e.p2.raw.Exec(`ALTER TABLE logs RENAME TO logs_gone`); err != nil {
		t.Fatal(err)
	}
	if status, body := super.download(query); status != 503 || !strings.Contains(body, "号池暂时不可用") {
		t.Errorf("%d %s", status, body)
	}
}

// Keys follow the lead pool where a pool fell short, except those new-api
// disabled by itself.
func TestKeysOfAChannelInTwoPools(t *testing.T) {
	e := newEnv(t)
	e.template(40, "https://both.example.com", false, 1, 2)
	alice := e.asUser(e.alice)
	id := alice.create(ch{"type": 40, "mode": "multi_to_single", "multi_key_mode": "polling", "key": "sk-a\nsk-b\nsk-c"})[0]
	two := e.placed(id, 2)
	e.p2.refuseKeys = map[int]bool{1: true}
	r := decode[switchResp](t, alice.want(200, "POST", e.channelPath(id, "/keys/disable"), at(0, 1)))
	if !slices.Equal(r.Done, []int{0}) || !strings.Contains(r.Error, "号池 2") {
		t.Errorf("disable = %+v", r)
	}
	e.p2.refuseKeys = nil
	e.p2.keyStatus[two][2] = 3
	e.due()
	e.push()
	if got := e.p2.keyStatus[two]; got[0] != 2 || got[1] != 2 || got[2] != 3 {
		t.Errorf("pool 2 keys %v", got)
	}
	var dirty bool
	if err := e.raw.QueryRow(`SELECT keys_dirty FROM channel_placements WHERE channel_id = $1 AND pool_id = 2`, id).Scan(&dirty); err != nil || dirty {
		t.Errorf("still dirty: %v %v", dirty, err)
	}
}

// A lead whose channel was deleted in new-api's panel hands the lead on: the
// placement behind is not put off for ever.
func TestKeysFollowTheNextPoolWhenTheLeadIsGone(t *testing.T) {
	e := newEnv(t)
	e.template(40, "https://both.example.com", false, 1, 2)
	alice := e.asUser(e.alice)
	id := alice.create(ch{"type": 40, "mode": "multi_to_single", "multi_key_mode": "polling", "key": "sk-a\nsk-b"})[0]
	e.p2.refuseKeys = map[int]bool{0: true}
	if r := decode[switchResp](t, alice.want(200, "POST", e.channelPath(id, "/keys/disable"), at(0))); len(r.Done) != 0 || !strings.Contains(r.Error, "号池 2") {
		t.Fatalf("disable = %+v", r)
	}
	e.p2.refuseKeys = nil
	if _, err := e.raw.Exec(`DELETE FROM channels WHERE id = $1`, e.nid(id)); err != nil {
		t.Fatal(err)
	}
	e.due()
	e.push()
	var dirty bool
	if err := e.raw.QueryRow(`SELECT keys_dirty FROM channel_placements WHERE channel_id = $1 AND pool_id = 2`, id).Scan(&dirty); err != nil || dirty {
		t.Errorf("still dirty: %v %v", dirty, err)
	}
}

// A lead whose PAT new-api turns away is not a missing channel: the pool that fell
// short keeps its flag and waits, rather than settling for the keys it has.
func TestKeysWaitWhenTheLeadsPATIsRefused(t *testing.T) {
	e := newEnv(t)
	e.template(40, "https://both.example.com", false, 1, 2)
	alice := e.asUser(e.alice)
	id := alice.create(ch{"type": 40, "mode": "multi_to_single", "multi_key_mode": "polling", "key": "sk-a\nsk-b"})[0]
	e.p2.refuseKeys = map[int]bool{0: true}
	alice.want(200, "POST", e.channelPath(id, "/keys/disable"), at(0))
	e.p2.refuseKeys = nil
	e.denied = "/api/channel/multi_key/manage"
	e.due()
	e.push()
	var dirty bool
	if err := e.raw.QueryRow(`SELECT keys_dirty FROM channel_placements WHERE channel_id = $1 AND pool_id = 2`, id).Scan(&dirty); err != nil || !dirty {
		t.Errorf("flag cleared on a refused PAT: %v %v", dirty, err)
	}
	if k := e.p2.keyStatus[e.placed(id, 2)]; cmp.Or(k[0], 1) != 1 {
		t.Errorf("pool 2 keys changed: %v", k)
	}
}

// When every pool fell short, each at another key, they all follow the first.
func TestKeysConvergeWhenEveryPoolFellShort(t *testing.T) {
	e := newEnv(t)
	e.template(40, "https://both.example.com", false, 1, 2)
	alice := e.asUser(e.alice)
	id := alice.create(ch{"type": 40, "mode": "multi_to_single", "multi_key_mode": "polling", "key": "sk-a\nsk-b\nsk-c"})[0]
	one, two := e.nid(id), e.placed(id, 2)
	e.refuseKeys, e.p2.refuseKeys = map[int]bool{1: true}, map[int]bool{0: true}
	if r := decode[switchResp](t, alice.want(200, "POST", e.channelPath(id, "/keys/disable"), at(0, 1))); len(r.Done) != 0 {
		t.Errorf("disable = %+v", r)
	}
	e.refuseKeys, e.p2.refuseKeys = nil, nil
	e.due()
	e.push()
	for pool, keys := range map[int64]map[int]int{1: e.keyStatus[one], 2: e.p2.keyStatus[two]} {
		if keys[0] != 2 || cmp.Or(keys[1], 1) != 1 {
			t.Errorf("pool %d keys %v", pool, keys)
		}
	}
	var dirty int
	if err := e.raw.QueryRow(`SELECT count(*) FROM channel_placements WHERE channel_id = $1 AND keys_dirty`, id).Scan(&dirty); err != nil || dirty != 0 {
		t.Errorf("%d still dirty: %v", dirty, err)
	}
}

// A channel recorded before pools is pool 1's under the id it always had.
func TestAChannelFromBeforePoolsKeepsItsID(t *testing.T) {
	e := newEnv(t)
	var old int
	if err := e.raw.QueryRow(`INSERT INTO channels (key, name, status, models, created_time) VALUES ('k', 'old', 1, 'tpl-a', 100) RETURNING id`).Scan(&old); err != nil {
		t.Fatal(err)
	}
	if _, err := e.raw.Exec(`INSERT INTO portal_channels (user_id, admin_id, newapi_channel_id, name) VALUES ($1, $2, $3, 'old')`, e.alice, e.a, old); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), e.dsn) // the next start's schema
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	alice := e.asUser(e.alice)
	item := alice.pooledList()[0]
	if item.ID != old || !item.Exists || item.PushState != "done" || e.nid(old) != old {
		t.Errorf("%+v", item)
	}
	alice.want(204, "POST", e.channelPath(old, "/status"), ch{"status": 2})
	if e.status(old) != 2 {
		t.Error("not disabled")
	}
	if id := alice.create(ch{})[0]; id < 10000 || id == e.nid(id) {
		t.Errorf("new channel %d, in pool 1 %d", id, e.nid(id))
	}
}
