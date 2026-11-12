package channels_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"newapisub/internal/store"
)

func (e *env) userPath(id int64, suffix string) string {
	return "/api/users/" + strconv.FormatInt(id, 10) + suffix
}

func (e *env) channelRow(id int) (name, models, key, baseURL string) {
	e.t.Helper()
	err := e.raw.QueryRow(`SELECT name, models, key, base_url FROM channels WHERE id = $1`, e.nid(id)).Scan(&name, &models, &key, &baseURL)
	if err != nil {
		e.t.Fatal(err)
	}
	return
}

func (e *env) localName(id int) string {
	e.t.Helper()
	var name string
	if err := e.raw.QueryRow(`SELECT name FROM portal_channels WHERE newapi_channel_id = $1`, id).Scan(&name); err != nil {
		e.t.Fatal(err)
	}
	return name
}

func TestUpdateChannelReachesNewAPIWithNameAndModelsOnly(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	id := alice.create(ch{"name": "old", "key": "sk-original"})[0]
	_, _, _, baseURL := e.channelRow(id)

	alice.want(204, "PUT", e.channelPath(id, ""), ch{"name": " new ", "models": "tpl-b", "id": 999, "key": "sk-injected",
		"base_url": "https://evil.example.com", "group": "vip", "priority": 99, "status": 2, "type": 1})

	var sent map[string]any
	puts := e.callsTo("PUT", "/api/channel/")
	if len(puts) != 1 || json.Unmarshal([]byte(puts[0].Body), &sent) != nil {
		t.Fatalf("new-api got %+v", puts)
	}
	// tpl-a, the test model, is gone from the channel, so that moves along.
	if !maps.Equal(sent, map[string]any{"id": float64(e.nid(id)), "name": "new", "models": "tpl-b", "test_model": "tpl-b"}) {
		t.Errorf("new-api got %v", sent)
	}
	name, models, key, url := e.channelRow(id)
	if name != "new" || models != "tpl-b" || key != "sk-original" || url != baseURL {
		t.Errorf("channel = %q %q %q %q", name, models, key, url)
	}
	if got := e.localName(id); got != "new" {
		t.Errorf("local name = %q", got)
	}

	var detail string
	if err := e.raw.QueryRow(`SELECT detail::text FROM audit_logs WHERE action = 'channel.update' AND success`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	var d struct {
		Changes struct {
			Name, Models struct{ From, To string }
		}
	}
	json.Unmarshal([]byte(detail), &d)
	if d.Changes.Name.From != "old" || d.Changes.Name.To != "new" || d.Changes.Models.From != "tpl-a,tpl-b" || d.Changes.Models.To != "tpl-b" {
		t.Errorf("audit detail = %s", detail)
	}
	if strings.Contains(detail, "sk-") {
		t.Errorf("audit detail holds a key: %s", detail)
	}
}

func TestUpdateChannelSendsATestModelOnlyWhenTheCurrentOneIsGone(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	id := alice.create(ch{})[0] // tests tpl-a, serves tpl-a and tpl-b
	path := e.channelPath(id, "")
	put := func(body ch, want map[string]any) {
		t.Helper()
		alice.want(204, "PUT", path, body)
		puts := e.callsTo("PUT", "/api/channel/")
		var sent map[string]any
		json.Unmarshal([]byte(puts[len(puts)-1].Body), &sent)
		want["id"] = float64(e.nid(id))
		if !maps.Equal(sent, want) {
			t.Errorf("%v: new-api got %v, want %v", body, sent, want)
		}
	}
	put(ch{"name": "n"}, map[string]any{"name": "n", "models": "tpl-a,tpl-b"})
	put(ch{"name": "n", "models": "tpl-a"}, map[string]any{"name": "n", "models": "tpl-a"})
	put(ch{"name": "n", "models": "tpl-b"}, map[string]any{"name": "n", "models": "tpl-b", "test_model": "tpl-b"})
	var testModel string
	if err := e.raw.QueryRow(`SELECT test_model FROM channels WHERE id = $1`, e.nid(id)).Scan(&testModel); err != nil || testModel != "tpl-b" {
		t.Errorf("test model = %q, %v", testModel, err)
	}
	for _, none := range []string{`NULL`, `''`} { // new-api then picks one itself
		if _, err := e.raw.Exec(`UPDATE channels SET test_model = `+none+` WHERE id = $1`, e.nid(id)); err != nil {
			t.Fatal(err)
		}
		put(ch{"name": "n", "models": "tpl-a"}, map[string]any{"name": "n", "models": "tpl-a"})
	}
	// A channel with no models left over to pick from must not break a rename.
	if _, err := e.raw.Exec(`UPDATE channels SET test_model = 'old', models = '' WHERE id = $1`, e.nid(id)); err != nil {
		t.Fatal(err)
	}
	put(ch{"name": "n"}, map[string]any{"name": "n", "models": ""})
}

func TestUpdateChannelModelsMustComeFromTheEnabledTemplate(t *testing.T) {
	e := newEnv(t)
	super, alice := e.asAdmin(e.super), e.asUser(e.alice)
	id := alice.create(ch{})[0]
	path := e.channelPath(id, "")
	for _, c := range []caller{alice, super} {
		c.want(400, "PUT", path, ch{"name": "n", "models": "evil"})
		c.want(400, "PUT", path, ch{"name": "n", "models": "tpl-a,evil"})
		c.want(400, "PUT", path, ch{"name": "n", "models": " , "})
		c.want(400, "PUT", path, ch{"name": "  ", "models": "tpl-a"})
	}
	if len(e.callsTo("PUT", "/api/channel/")) != 0 {
		t.Error("a refused edit reached new-api")
	}
	alice.want(204, "PUT", path, ch{"name": "n", "models": "tpl-b,tpl-a"})
	if _, models, _, _ := e.channelRow(id); models != "tpl-a,tpl-b" {
		t.Errorf("same set should stay as it was: %q", models)
	}

	// The template shrank, and then went away: renaming still works, changing models does not.
	if _, err := e.raw.Exec(`UPDATE channel_templates SET models = 'tpl-a'`); err != nil {
		t.Fatal(err)
	}
	alice.want(204, "PUT", path, ch{"name": "kept", "models": "tpl-a,tpl-b"})
	alice.want(400, "PUT", path, ch{"name": "kept", "models": "tpl-b"})
	if _, err := e.raw.Exec(`UPDATE channel_templates SET enabled = false`); err != nil {
		t.Fatal(err)
	}
	alice.want(400, "PUT", path, ch{"name": "kept", "models": "tpl-a"})
	alice.want(204, "PUT", path, ch{"name": "renamed"})
	if name, models, _, _ := e.channelRow(id); name != "renamed" || models != "tpl-a,tpl-b" {
		t.Errorf("channel = %q %q", name, models)
	}
}

func TestUpdateChannelFollowsTheScope(t *testing.T) {
	e := newEnv(t)
	alice, bobby := e.asUser(e.alice), e.asUser(e.bobby)
	adminA, adminB, super := e.asAdmin(e.a), e.asAdmin(e.b), e.asAdmin(e.super)
	id := alice.create(ch{})[0]
	aKey := adminA.create(ch{})[0]
	body := ch{"name": "n", "models": "tpl-a"}
	bobby.want(404, "PUT", e.channelPath(id, ""), body)
	adminB.want(404, "PUT", e.channelPath(id, ""), body)
	alice.want(404, "PUT", e.channelPath(aKey, ""), body)
	alice.want(404, "PUT", e.channelPath(999999, ""), body)
	if len(e.callsTo("PUT", "/api/channel/")) != 0 {
		t.Error("an out-of-scope edit reached new-api")
	}
	alice.want(204, "PUT", e.channelPath(id, ""), body)
	adminA.want(204, "PUT", e.channelPath(id, ""), body)
	super.want(204, "PUT", e.channelPath(id, ""), body)

	// new-api's refusal leaves the local name alone.
	e.rejectUpdate = true
	if got := alice.want(502, "PUT", e.channelPath(id, ""), ch{"name": "refused"}); !strings.Contains(got, "refused by new-api") {
		t.Errorf("refusal = %s", got)
	}
	if got := e.localName(id); got != "n" {
		t.Errorf("local name = %q", got)
	}
	e.rejectUpdate = false

	alice.want(204, "DELETE", e.channelPath(id, ""), nil)
	before := len(e.callsTo("PUT", "/api/channel/"))
	alice.want(404, "PUT", e.channelPath(id, ""), body)
	super.want(404, "PUT", e.channelPath(id, ""), body)
	if after := len(e.callsTo("PUT", "/api/channel/")); after != before {
		t.Error("an edit of a deleted channel reached new-api")
	}
}

// logScopeWorld has alice and bobby each with a channel with one log, in groups of their own.
func logScopeWorld(t *testing.T, e *env) (aliceCh, bobbyCh int, query string) {
	t.Helper()
	aliceCh, bobbyCh = e.asUser(e.alice).create(ch{})[0], e.asUser(e.bobby).create(ch{})[0]
	if _, err := e.raw.Exec(`UPDATE channels SET "group" = 'bobby-group' WHERE id = $1`, e.nid(bobbyCh)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	e.addLog(logRow{channel: aliceCh, typ: 2, at: now, model: "m-alice", token: "t", quota: 3, prompt: 1, output: 1, spent: 1, other: "{}"})
	e.addLog(logRow{channel: bobbyCh, typ: 2, at: now, model: "m-bobby", token: "t", quota: 5, prompt: 1, output: 1, spent: 1, other: "{}"})
	return aliceCh, bobbyCh, fmt.Sprintf("start=%d&end=%d", now-60, now+60)
}

// checkLogReach asserts what c reads of the two logs through every log route.
func checkLogReach(t *testing.T, name string, c caller, query string, wantBobby bool) {
	t.Helper()
	logs := c.want(200, "GET", "/api/logs?"+query, nil)
	_, csv := c.download(query + "&tz_offset=0")
	quota := decode[struct{ Quota int64 }](t, c.want(200, "GET", "/api/logs/stat?"+query, nil)).Quota
	groups := decode[struct{ Groups []string }](t, c.want(200, "GET", "/api/logs/groups", nil)).Groups
	wantQuota, wantGroups := int64(3), []string{"tpl-group"}
	if wantBobby {
		wantQuota, wantGroups = 8, []string{"bobby-group", "tpl-group"}
	}
	if !strings.Contains(logs, "m-alice") || strings.Contains(logs, "m-bobby") != wantBobby {
		t.Errorf("%s: logs = %s", name, logs)
	}
	if !strings.Contains(csv, "m-alice") || strings.Contains(csv, "m-bobby") != wantBobby {
		t.Errorf("%s: export = %s", name, csv)
	}
	if quota != wantQuota {
		t.Errorf("%s: stat quota = %d, want %d", name, quota, wantQuota)
	}
	if !slices.Equal(groups, wantGroups) {
		t.Errorf("%s: groups = %v, want %v", name, groups, wantGroups)
	}
}

func TestTemplateWithoutARealModelIsRefusedAndCannotBreakACreate(t *testing.T) {
	e := newEnv(t)
	super := e.asAdmin(e.super)
	tpl := ch{"name": "commas", "channel_type": 1, "group": "g", "enabled": true, "test_model": "m"}
	for _, models := range []string{"", " ", ",", " , ,"} {
		tpl["models"] = models
		super.want(400, "POST", "/api/templates", tpl)
		super.want(400, "PUT", "/api/templates/1", tpl)
	}
	// One saved before the check answers a create with an error, not a panic.
	if _, err := e.raw.Exec(`UPDATE channel_templates SET models = ',', test_model = 'm'`); err != nil {
		t.Fatal(err)
	}
	e.asUser(e.alice).want(400, "POST", "/api/channels", ch{"type": 20, "name": "n", "key": "k", "mode": "single"})
}

func TestLogScopeHoldsPortalUsersToTheirChannels(t *testing.T) {
	e := newEnv(t)
	alice, adminA, super := e.asUser(e.alice), e.asAdmin(e.a), e.asAdmin(e.super)
	_, bobbyCh, query := logScopeWorld(t, e)
	scope := func() string {
		return decode[struct {
			LogScope string `json:"log_scope"`
		}](t, alice.want(200, "GET", "/api/me", nil)).LogScope
	}
	setGlobal := func(v string) {
		t.Helper()
		x := decode[store.Settings](t, super.want(200, "GET", "/api/settings", nil))
		x.LogScope = v
		super.want(200, "PUT", "/api/settings", x)
	}
	setUser := func(v string) {
		t.Helper()
		adminA.want(204, "POST", e.userPath(e.alice, "/log-scope"), ch{"log_scope": v})
	}

	if got := scope(); got != "all" {
		t.Errorf("seed: %s", got)
	}
	checkLogReach(t, "seed", alice, query, true)

	setGlobal("self")
	if got := scope(); got != "self" {
		t.Errorf("global self: %s", got)
	}
	checkLogReach(t, "global self", alice, query, false)
	// The query cannot widen it, and asking for a foreign channel is the usual 404.
	checkLogReach(t, "global self, asking for more", alice, query+"&owner=&user_id="+strconv.FormatInt(e.bobby, 10), false)
	for _, path := range []string{"/api/logs?", "/api/logs/stat?", "/api/logs/export?tz_offset=0&"} {
		alice.want(404, "GET", path+query+"&channel_id="+strconv.Itoa(bobbyCh), nil)
	}
	// Admins are not held to it.
	checkLogReach(t, "super under global self", super, query, true)
	checkLogReach(t, "admin under global self", adminA, query, true)

	setUser("all")
	checkLogReach(t, "user all over global self", alice, query, true)

	setGlobal("all")
	setUser("self")
	if got := scope(); got != "self" {
		t.Errorf("user self: %s", got)
	}
	checkLogReach(t, "user self over global all", alice, query, false)

	setUser("")
	checkLogReach(t, "following again", alice, query, true)

	// A user without any channel gets nothing, not everything.
	lone := e.asUser(e.mustUser("lonely", e.a))
	setGlobal("self")
	if got := decode[struct{ Groups []string }](t, lone.want(200, "GET", "/api/logs/groups", nil)).Groups; len(got) != 0 {
		t.Errorf("groups of a user without channels: %v", got)
	}
	if got := lone.want(200, "GET", "/api/logs?"+query, nil); strings.Contains(got, "m-alice") {
		t.Errorf("logs of a user without channels: %s", got)
	}
}

func TestLogScopeThatCannotBeReadFailsClosed(t *testing.T) {
	for name, damage := range map[string]string{
		"missing": `DELETE FROM app_settings WHERE key = 'log_scope'`,
		"unknown": `UPDATE app_settings SET value = 'everyone' WHERE key = 'log_scope'`,
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			alice := e.asUser(e.alice)
			_, _, query := logScopeWorld(t, e)
			if _, err := e.raw.Exec(damage); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/api/logs?" + query, "/api/logs/stat?" + query, "/api/logs/groups", "/api/me",
				"/api/logs/export?tz_offset=0&" + query} {
				if got := alice.want(500, "GET", path, nil); strings.Contains(got, "m-bobby") {
					t.Errorf("%s: %s", path, got)
				}
			}
			// An explicit choice for the user does not depend on the global value.
			e.asAdmin(e.a).want(204, "POST", e.userPath(e.alice, "/log-scope"), ch{"log_scope": "self"})
			alice.want(200, "GET", "/api/logs?"+query, nil)
		})
	}
}

func TestUserLogScopeFollowsTheAdminsScope(t *testing.T) {
	e := newEnv(t)
	adminA, adminB, super := e.asAdmin(e.a), e.asAdmin(e.b), e.asAdmin(e.super)
	path := e.userPath(e.alice, "/log-scope")
	adminB.want(404, "POST", path, ch{"log_scope": "all"})
	adminA.want(404, "POST", e.userPath(999999, "/log-scope"), ch{"log_scope": "all"})
	e.asUser(e.alice).want(404, "POST", path, ch{"log_scope": "all"})
	adminA.want(400, "POST", path, ch{"log_scope": "everyone"})
	adminA.want(204, "POST", path, ch{"log_scope": "self"})
	var users []store.User
	json.Unmarshal([]byte(super.want(200, "GET", "/api/users", nil)), &users)
	i := slices.IndexFunc(users, func(u store.User) bool { return u.ID == e.alice })
	if i < 0 || users[i].LogScope != "self" {
		t.Errorf("users = %+v", users)
	}
	super.want(204, "POST", path, ch{"log_scope": ""})
}

func TestStatsByChannelTotalsTheScopeBiggestFirst(t *testing.T) {
	e := newEnv(t)
	alice, adminA, adminB := e.asUser(e.alice), e.asAdmin(e.a), e.asAdmin(e.b)
	small, big := alice.create(ch{"name": "small"})[0], alice.create(ch{"name": "big"})[0]
	foreign := e.asUser(e.bobby).create(ch{"name": "foreign"})[0]
	hour := int64(1_000_000_000)
	for at, quota := range map[int64]int{hour: 1, hour + 3600: 2} {
		e.addLog(logRow{channel: small, typ: 2, at: at, model: "m", token: "t", quota: quota, prompt: 1, output: 1, spent: 1, other: "{}"})
	}
	e.addLog(logRow{channel: big, typ: 2, at: hour, model: "m", token: "t", quota: 10, prompt: 1, output: 1, spent: 1, other: "{}"})
	e.addLog(logRow{channel: big, typ: 2, at: hour, model: "other", token: "t", quota: 10, prompt: 1, output: 1, spent: 1, other: "{}"})
	e.addLog(logRow{channel: foreign, typ: 2, at: hour, model: "m", token: "t", quota: 99, prompt: 1, output: 1, spent: 1, other: "{}"})
	query := fmt.Sprintf("/api/stats?by=channel&start=%d&end=%d", hour-3600, hour+7200)

	type row struct {
		CreatedAt   int64  `json:"created_at"`
		ChannelID   int    `json:"channel_id"`
		ChannelName string `json:"channel_name"`
		Count       int64  `json:"count"`
		Quota       int64  `json:"quota"`
		TokenUsed   int64  `json:"token_used"`
	}
	rows := func(c caller) []row {
		t.Helper()
		return decode[struct{ Rows []row }](t, c.want(200, "GET", query, nil)).Rows
	}
	want := []row{{hour - 3600, big, "big", 2, 20, 4}, {hour - 3600, small, "small", 2, 3, 4}}
	for name, c := range map[string]caller{"alice": alice, "her admin": adminA} {
		if got := rows(c); !slices.Equal(got, want) {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
	if got := rows(adminB); len(got) != 1 || got[0].ChannelID != foreign {
		t.Errorf("admin b: %+v", got)
	}
	alice.want(404, "GET", query+"&channel_id="+strconv.Itoa(foreign), nil)
	alice.want(400, "GET", strings.Replace(query, "by=channel", "by=model", 1), nil)
}

func TestSettlementsFollowTheScopeAndOnlyGrow(t *testing.T) {
	e := newEnv(t)
	alice, bobby := e.asUser(e.alice), e.asUser(e.bobby)
	adminA, adminB, super := e.asAdmin(e.a), e.asAdmin(e.b), e.asAdmin(e.super)
	kept, deleted := alice.create(ch{})[0], alice.create(ch{})[0]
	aKey := adminA.create(ch{})[0] // the admin's own key is not alice's to settle
	for id, units := range map[int]int{kept: 3, deleted: 2, aKey: 100} {
		if _, err := e.raw.Exec(`UPDATE channels SET used_quota = $1 WHERE id = $2`, units*testQuotaPerUnit, e.nid(id)); err != nil {
			t.Fatal(err)
		}
	}
	alice.want(204, "DELETE", e.channelPath(deleted, ""), nil) // still counts

	path := e.userPath(e.alice, "/settlements")
	type view struct {
		UsedUSD    float64 `json:"used_usd"`
		SettledUSD float64 `json:"settled_usd"`
		PendingUSD float64 `json:"pending_usd"`
		Items      []struct {
			ID        int64   `json:"id"`
			AmountUSD float64 `json:"amount_usd"`
			Note      string  `json:"note"`
			CreatedBy string  `json:"created_by"`
			CreatedAt int64   `json:"created_at"`
		} `json:"items"`
	}
	if v := decode[view](t, alice.want(200, "GET", "/api/settlements", nil)); v.UsedUSD != 5 || v.SettledUSD != 0 || v.PendingUSD != 5 || v.Items == nil || len(v.Items) != 0 {
		t.Errorf("no records: %+v", v)
	}

	// Out of scope is the same 404 as missing, and the portal cannot write.
	adminB.want(404, "GET", path, nil)
	adminB.want(404, "POST", path, ch{"amount_usd": 1})
	adminA.want(404, "GET", e.userPath(e.bobby, "/settlements"), nil)
	adminA.want(404, "POST", e.userPath(999999, "/settlements"), ch{"amount_usd": 1})
	alice.want(404, "POST", path, ch{"amount_usd": 1})
	alice.want(404, "GET", path, nil)
	alice.want(404, "POST", "/api/settlements", ch{"amount_usd": 1})
	for _, bad := range []ch{{}, {"amount_usd": -1}, {"amount_usd": 1e12}, {"amount_usd": "1"}, {"amount_usd": 1, "note": strings.Repeat("x", 201)}} {
		if got := adminA.want(400, "POST", path, bad); strings.Contains(got, "amount_usd") {
			t.Errorf("%v: the message names the field: %s", bad, got)
		}
	}

	adminA.want(201, "POST", path, ch{"amount_usd": 4, "note": " first "})
	super.want(201, "POST", path, ch{"amount_usd": 6.5})
	portalBody := alice.want(200, "GET", "/api/settlements", nil)
	for name, got := range map[string]string{"portal": portalBody, "admin": adminA.want(200, "GET", path, nil)} {
		v := decode[view](t, got)
		if v.UsedUSD != 5 || v.SettledUSD != 6.5 || v.PendingUSD != -1.5 || len(v.Items) != 2 {
			t.Fatalf("%s: %+v", name, v)
		}
		if v.Items[0].AmountUSD != 6.5 || v.Items[1].Note != "first" || v.Items[0].CreatedAt == 0 {
			t.Errorf("%s: items = %+v", name, v.Items)
		}
	}
	// An admin's login name stays in the backend.
	if v := decode[view](t, adminA.want(200, "GET", path, nil)); v.Items[0].CreatedBy != "root" || v.Items[1].CreatedBy != "admin-a" {
		t.Errorf("admin: items = %+v", v.Items)
	}
	if strings.Contains(portalBody, "created_by") || strings.Contains(portalBody, "admin-a") || strings.Contains(portalBody, "root") {
		t.Errorf("portal: %s", portalBody)
	}
	// The latest counts, also when it is lower.
	adminA.want(201, "POST", path, ch{"amount_usd": 2})
	if v := decode[view](t, adminA.want(200, "GET", path, nil)); v.SettledUSD != 2 || len(v.Items) != 3 {
		t.Errorf("after a lower record: %+v", v)
	}

	if v := decode[view](t, bobby.want(200, "GET", "/api/settlements", nil)); v.UsedUSD != 0 || len(v.Items) != 0 {
		t.Errorf("bobby sees %+v", v)
	}
	var users []store.User
	json.Unmarshal([]byte(adminA.want(200, "GET", "/api/users", nil)), &users)
	if len(users) != 1 || users[0].UsedUSD == nil || *users[0].UsedUSD != 5 || users[0].SettledUSD != 2 {
		t.Errorf("admin a's list: %+v", users)
	}
	json.Unmarshal([]byte(adminB.want(200, "GET", "/api/users", nil)), &users)
	if len(users) != 1 || users[0].ID != e.bobby || users[0].UsedUSD == nil || *users[0].UsedUSD != 0 {
		t.Errorf("admin b's list: %+v", users)
	}

	// Without new-api's database the page still works, and does not claim nothing was run.
	if _, err := e.raw.Exec(`ALTER TABLE channels RENAME TO channels_away`); err != nil {
		t.Fatal(err)
	}
	body := adminA.want(200, "GET", "/api/users", nil)
	if !strings.Contains(body, `"used_usd":null`) || !strings.Contains(body, `"settled_usd":2`) {
		t.Errorf("list without new-api's database: %s", body)
	}
	if _, err := e.raw.Exec(`ALTER TABLE channels_away RENAME TO channels`); err != nil {
		t.Fatal(err)
	}
	// There is no way to remove a record, so a user with some stays.
	withRecord := e.mustUser("recorded", e.a)
	adminA.want(201, "POST", e.userPath(withRecord, "/settlements"), ch{"amount_usd": 1})
	adminA.want(409, "DELETE", e.userPath(withRecord, ""), nil)
}

func TestSettingsAreForSupers(t *testing.T) {
	e := newEnv(t)
	super, adminA, alice := e.asAdmin(e.super), e.asAdmin(e.a), e.asUser(e.alice)
	logo := []byte("\x89PNG\r\n\x1a\n")
	good := ch{"log_scope": "self", "portal_name": " 门户 ", "admin_name": "后台", "portal_public_url": "https://portal.example.com"}
	adminA.want(403, "GET", "/api/settings", nil)
	adminA.want(403, "PUT", "/api/settings", good)
	if rec := rawPut(adminA, logo, "image/png"); rec.Code != 403 {
		t.Errorf("admin logo: %d", rec.Code)
	}
	adminA.want(403, "DELETE", "/api/settings/logo", nil)
	alice.want(404, "GET", "/api/settings", nil)
	alice.want(404, "PUT", "/api/settings", good)
	if rec := rawPut(alice, logo, "image/png"); rec.Code != 404 {
		t.Errorf("portal logo: %d", rec.Code)
	}

	got := decode[store.Settings](t, super.want(200, "GET", "/api/settings", nil))
	if got != (store.Settings{LogScope: "all", PortalName: "小蓝号池", AdminName: "小蓝号池管理后台"}) {
		t.Errorf("seed = %+v", got)
	}
	for _, field := range []ch{{"log_scope": "everyone"}, {"log_scope": ""}, {"portal_name": ""}, {"admin_name": "a\nb"},
		{"portal_public_url": "ftp://x"}, {"portal_public_url": "https://u:p@x.example.com"}} {
		bad := maps.Clone(good)
		maps.Copy(bad, field)
		super.want(400, "PUT", "/api/settings", bad)
	}
	super.want(200, "PUT", "/api/settings", good)

	type status struct {
		SystemName string `json:"system_name"`
		Logo       string `json:"logo"`
	}
	anon := func(c caller) caller { c.cookie = nil; return c }
	if s := decode[status](t, anon(alice).want(200, "GET", "/api/status", nil)); s.SystemName != "门户" || s.Logo != "/logo.png" {
		t.Errorf("portal status = %+v", s)
	}
	body := anon(adminA).want(200, "GET", "/api/status", nil)
	if s := decode[status](t, body); s.SystemName != "后台" || strings.Contains(body, "portal.example.com") {
		t.Errorf("admin status = %s", body)
	}
	if got := adminA.want(200, "GET", "/api/me", nil); !strings.Contains(got, `"portal_url":"https://portal.example.com"`) {
		t.Errorf("admin me = %s", got)
	}

	// Saving repairs a lost row, reading does not pretend it is there.
	if _, err := e.raw.Exec(`DELETE FROM app_settings WHERE key = 'portal_name'`); err != nil {
		t.Fatal(err)
	}
	super.want(500, "GET", "/api/settings", nil)
	anon(alice).want(500, "GET", "/api/status", nil)
	super.want(200, "PUT", "/api/settings", good)
	anon(alice).want(200, "GET", "/api/status", nil)
}

// rawPut uploads a logo the way the page does: the file as the request body.
func rawPut(c caller, data []byte, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("PUT", "/api/settings/logo", strings.NewReader(string(data)))
	req.Header.Set("Content-Type", contentType)
	req.AddCookie(c.cookie)
	rec := httptest.NewRecorder()
	c.mux.ServeHTTP(rec, req)
	return rec
}

func TestLogoAcceptsOnlyRasterImagesAndServesThemInert(t *testing.T) {
	e := newEnv(t)
	super, alice := e.asAdmin(e.super), e.asUser(e.alice)
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	// However it is labelled, what counts is what it is.
	for name, data := range map[string]string{
		"svg":  `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"xml":  `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`,
		"html": `<!doctype html><script>alert(1)</script>`,
		"gif":  "GIF89a......",
		"none": "",
	} {
		if rec := rawPut(super, []byte(data), "image/png"); rec.Code != 400 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if rec := rawPut(super, append(png, make([]byte, store.MaxLogoBytes)...), "image/png"); rec.Code != 413 {
		t.Errorf("too big: %d", rec.Code)
	}
	if rec := rawPut(super, append(png, make([]byte, store.MaxLogoBytes-len(png))...), "image/svg+xml"); rec.Code != 204 {
		t.Fatalf("exactly at the limit: %d %s", rec.Code, rec.Body)
	}

	anon := alice
	anon.cookie = nil
	var st struct {
		Logo string `json:"logo"`
	}
	json.Unmarshal([]byte(anon.want(200, "GET", "/api/status", nil)), &st)
	if !strings.HasPrefix(st.Logo, "/api/brand/logo?v=") {
		t.Fatalf("logo = %q", st.Logo)
	}
	rec := anon.do("GET", st.Logo, nil)
	h := rec.Header()
	if rec.Code != 200 || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Security-Policy") != "default-src 'none'" || !strings.Contains(h.Get("Cache-Control"), "immutable") {
		t.Errorf("versioned: %d %v", rec.Code, h)
	}
	if rec := anon.do("GET", "/api/brand/logo", nil); rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("unversioned: %d %v", rec.Code, rec.Header())
	}

	// Every upload gets a new URL, or the immutable one would keep the old image.
	first := st.Logo
	if rec := rawPut(super, png, "image/png"); rec.Code != 204 {
		t.Fatalf("second upload: %d", rec.Code)
	}
	json.Unmarshal([]byte(anon.want(200, "GET", "/api/status", nil)), &st)
	if st.Logo == first {
		t.Errorf("logo URL did not change: %s", st.Logo)
	}

	super.want(204, "DELETE", "/api/settings/logo", nil)
	super.want(204, "DELETE", "/api/settings/logo", nil)
	json.Unmarshal([]byte(anon.want(200, "GET", "/api/status", nil)), &st)
	if st.Logo != "/logo.png" {
		t.Errorf("after delete: %q", st.Logo)
	}
	anon.want(404, "GET", "/api/brand/logo", nil)
}

// The schema is applied on every start, onto data already there.
func TestMigrationKeepsDataAndUpgradesAnOlderDatabase(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	super := e.asAdmin(e.super)
	x := decode[store.Settings](t, super.want(200, "GET", "/api/settings", nil))
	x.LogScope, x.PortalName = "self", "renamed"
	super.want(200, "PUT", "/api/settings", x)
	reopen := func() {
		t.Helper()
		st, err := store.Open(ctx, e.dsn)
		if err != nil {
			t.Fatal(err)
		}
		st.Close()
	}
	reopen()
	if got := decode[store.Settings](t, super.want(200, "GET", "/api/settings", nil)); got != x {
		t.Errorf("settings after a restart: %+v, want %+v", got, x)
	}

	if _, err := e.raw.Exec(`ALTER TABLE portal_users DROP COLUMN log_scope;
		DROP TABLE app_settings, app_logo, settlements`); err != nil {
		t.Fatal(err)
	}
	reopen()
	var users []store.User
	json.Unmarshal([]byte(super.want(200, "GET", "/api/users", nil)), &users)
	if len(users) != 2 || users[0].LogScope != "" {
		t.Errorf("users after the upgrade: %+v", users)
	}
	if got := decode[store.Settings](t, super.want(200, "GET", "/api/settings", nil)); got.LogScope != "all" {
		t.Errorf("seeded log scope = %q: an upgrade must not change what users see", got.LogScope)
	}
	if _, err := e.raw.Exec(`UPDATE portal_users SET log_scope = 'everyone'`); err == nil {
		t.Error("the check on log_scope is missing")
	}
}
