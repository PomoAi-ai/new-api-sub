package channels_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"newapisub/internal/security"
	"newapisub/internal/store"
)

type reply struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type channelPage struct {
	Items []struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Status int    `json:"status"`
	} `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

func (c caller) api(status int, method, path string, body any) reply {
	c.e.t.Helper()
	return decode[reply](c.e.t, c.want(status, method, path, body))
}

// ok asserts a successful call; failed asserts a business failure, which is 200.
func (c caller) ok(method, path string, body any) reply {
	c.e.t.Helper()
	r := c.api(200, method, path, body)
	if !r.Success {
		c.e.t.Fatalf("%s %s: %s", method, path, r.Message)
	}
	return r
}

func (c caller) failed(method, path string, body any) string {
	c.e.t.Helper()
	r := c.api(200, method, path, body)
	if r.Success {
		c.e.t.Fatalf("%s %s succeeded", method, path)
	}
	return r.Message
}

// addChannel creates through the token API and returns the ids.
// addChannel creates through the token, and has the push worker push it.
func (c caller) addChannel(body ch) []int {
	c.e.t.Helper()
	ids := decode[struct {
		IDs []int `json:"ids"`
	}](c.e.t, string(c.ok("POST", "/api/channel/", body).Data)).IDs
	c.e.push()
	return ids
}

func oneChannel(key string) ch {
	return ch{"mode": "single", "channel": ch{"type": 20, "name": "n", "key": key}}
}

func (c caller) channelPage(query string) channelPage {
	c.e.t.Helper()
	return decode[channelPage](c.e.t, string(c.ok("GET", "/api/channel/"+query, nil).Data))
}

func TestTokenIsShownOnceAndStoredAsHash(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	created := alice.withProof(security.PurposeTokenCreate).want(201, "POST", "/api/tokens", ch{"name": "batch"})
	tok := decode[struct {
		ID    int    `json:"id"`
		Token string `json:"token"`
	}](t, created)
	var stored, hint string
	if err := e.raw.QueryRow(`SELECT token_hash, hint FROM api_tokens WHERE id = $1`, tok.ID).Scan(&stored, &hint); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(tok.Token))
	if stored != hex.EncodeToString(sum[:]) {
		t.Errorf("stored %q is not the token's SHA-256", stored)
	}
	listed := alice.want(200, "GET", "/api/tokens", nil)
	if strings.Contains(listed, tok.Token) || strings.Contains(listed, stored) || !strings.Contains(listed, hint) {
		t.Errorf("token list: %s", listed)
	}
	caller{e: e, mux: e.portalMux, token: tok.Token}.ok("GET", "/api/channel/", nil)
	e.asUser(e.bobby).want(404, "DELETE", "/api/tokens/"+strconv.Itoa(tok.ID), nil)
	alice.want(204, "DELETE", "/api/tokens/"+strconv.Itoa(tok.ID), nil)
	caller{e: e, mux: e.portalMux, token: tok.Token}.api(401, "GET", "/api/channel/", nil)

	var n int
	if err := e.raw.QueryRow(`SELECT count(*) FROM audit_logs WHERE action IN ('auth.token_create', 'auth.token_delete') AND success`).Scan(&n); err != nil || n != 2 {
		t.Errorf("audited %d token operations (%v)", n, err)
	}
}

func TestTokenLimitsAndExpiry(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	alice.want(400, "POST", "/api/tokens", ch{"name": strings.Repeat("x", 65)})
	alice.want(400, "POST", "/api/tokens", ch{"name": "past", "expires_at": 1})
	alice.want(400, "POST", "/api/tokens", ch{"name": "far", "expires_at": int64(1) << 60})

	in := time.Now().Add(time.Hour).Unix()
	tok := decode[struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expires_at"`
	}](t, alice.withProof(security.PurposeTokenCreate).want(201, "POST", "/api/tokens", ch{"name": "soon", "expires_at": in}))
	if tok.ExpiresAt != in {
		t.Errorf("expires_at = %d, want %d", tok.ExpiresAt, in)
	}
	script := caller{e: e, mux: e.portalMux, token: tok.Token}
	script.ok("GET", "/api/channel/", nil)
	if _, err := e.raw.Exec(`UPDATE api_tokens SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	script.api(401, "GET", "/api/channel/", nil)

	for range 9 {
		alice.withProof(security.PurposeTokenCreate).want(201, "POST", "/api/tokens", ch{"name": "more"})
	}
	alice.withProof(security.PurposeTokenCreate).want(409, "POST", "/api/tokens", ch{"name": "one too many"})
	e.asUser(e.bobby).withProof(security.PurposeTokenCreate).want(201, "POST", "/api/tokens", ch{"name": "own limit"})
}

// A token reaches the mapped routes and nothing else, and a session reaches none of them.
func TestTokenAndSessionDoNotCrossOver(t *testing.T) {
	e := newEnv(t)
	script := e.asToken(e.alice)
	for _, r := range [][2]string{{"GET", "/api/tokens"}, {"POST", "/api/tokens"}, {"DELETE", "/api/tokens/1"}, {"POST", "/api/me/password"},
		{"POST", "/api/auth/logout"}, {"GET", "/api/me"}, {"GET", "/api/channels"}, {"GET", "/api/settlements"}, {"GET", "/api/logs/export"}} {
		script.want(401, r[0], r[1], ch{"name": "x"})
	}
	if r := e.asUser(e.alice).api(401, "GET", "/api/channel/", nil); r.Success || r.Message == "" {
		t.Errorf("session on the program API: %+v", r)
	}
	for _, r := range [][2]string{{"GET", "/api/channel/search"}, {"GET", "/api/channel/models"}, {"GET", "/api/channel/ops"}, {"GET", "/api/channel/test"},
		{"GET", "/api/channel/update_balance"}, {"POST", "/api/channel/fetch_models"}, {"POST", "/api/channel/batch"}, {"PUT", "/api/channel/tag"},
		{"DELETE", "/api/channel/disabled"}, {"DELETE", "/api/channel/"}, {"POST", "/api/channel/1/key"}, {"GET", "/api/channel/multi_key/manage"},
		{"GET", "/api/log/"}, {"GET", "/api/log/self/export"}, {"GET", "/api/log/self/search"}, {"GET", "/api/data/"}, {"GET", "/api/user/self"}, {"GET", "/api/token/"}} {
		script.want(404, r[0], r[1], nil)
	}
	(caller{e: e, mux: e.adminMux, token: script.token}).want(404, "GET", "/api/channel/", nil)
}

func TestTokensFollowTheAccount(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	script := e.asToken(e.alice)

	e.asUser(e.alice).want(204, "POST", "/api/auth/logout", nil)
	script.ok("GET", "/api/channel/", nil)

	// Admin ids and portal user ids overlap, and the token belongs to the portal user.
	if _, err := e.st.AdminUsers().SetPassword(ctx, e.alice, "not-a-hash-either"); err != nil {
		t.Fatal(err)
	}
	script.ok("GET", "/api/channel/", nil)

	if _, err := e.st.SetPortalStatus(ctx, e.alice, 2); err != nil {
		t.Fatal(err)
	}
	script.api(401, "GET", "/api/channel/", nil)
	if _, err := e.st.SetPortalStatus(ctx, e.alice, 1); err != nil {
		t.Fatal(err)
	}
	script.ok("GET", "/api/channel/", nil)

	if _, err := e.st.PortalUsers().SetPassword(ctx, e.alice, "not-a-hash-either"); err != nil {
		t.Fatal(err)
	}
	script.api(401, "GET", "/api/channel/", nil)

	script = e.asToken(e.bobby)
	if _, err := e.st.DeletePortalUser(ctx, e.bobby); err != nil {
		t.Fatalf("a user with a token must stay deletable: %v", err)
	}
	script.api(401, "GET", "/api/channel/", nil)
}

func TestChannelLifecycleThroughTheToken(t *testing.T) {
	e := newEnv(t)
	script := e.asToken(e.alice)

	// No trailing slash: the mux must not redirect this into a list.
	id := decode[struct {
		IDs []int `json:"ids"`
	}](t, string(script.ok("POST", "/api/channel", oneChannel("sk-secret-key")).Data)).IDs[0]
	// Until the push worker has created it, there is nothing to test: 409, which a script tries again on.
	script.api(409, "GET", fmt.Sprintf("/api/channel/test/%d", id), nil)
	e.push()
	if len(e.callsTo("POST", "/api/channel/")) != 1 {
		t.Fatal("new-api received no create")
	}
	detail := fmt.Sprintf("/api/channel/%d", id)
	if got := decode[struct {
		ID int `json:"id"`
	}](t, string(script.ok("GET", detail, nil).Data)); got.ID != id {
		t.Errorf("detail: %+v", got)
	}

	script.ok("PUT", "/api/channel", ch{"id": id, "name": "renamed", "models": "tpl-a", "priority": 99})
	if got := e.localName(id); got != "renamed" {
		t.Errorf("name = %q", got)
	}
	script.ok("PUT", "/api/channel/", ch{"id": id, "models": "tpl-a"})
	if got := e.localName(id); got != "renamed" {
		t.Errorf("PUT without name changed it to %q", got)
	}
	e.asToken(e.bobby).want(404, "PUT", "/api/channel/", ch{"id": id})

	if r := script.ok("POST", detail+"/status", ch{"status": 2}); string(r.Data) != "true" {
		t.Errorf("status data = %s", r.Data)
	}
	if got := script.channelPage("").Items[0].Status; got != 2 {
		t.Errorf("status = %d", got)
	}
	script.failed("POST", detail+"/status", ch{"status": 3})

	tested := decode[struct {
		Success bool    `json:"success"`
		Message string  `json:"message"`
		Time    float64 `json:"time"`
	}](t, script.want(200, "GET", fmt.Sprintf("/api/channel/test/%d?model=pass", id), nil))
	if !tested.Success || tested.Time != 0.5 {
		t.Errorf("test: %+v", tested)
	}
	if r := script.api(200, "GET", fmt.Sprintf("/api/channel/test/%d?model=other", id), nil); r.Success || r.Message != "upstream 401" {
		t.Errorf("failed test: %+v", r)
	}
	if b := script.want(200, "GET", fmt.Sprintf("/api/channel/update_balance/%d", id), nil); !strings.Contains(b, `"balance":12.5`) || !strings.Contains(b, `"success":true`) {
		t.Errorf("balance: %s", b)
	}

	script.ok("DELETE", detail, nil)
	if n := script.channelPage("").Total; n != 0 {
		t.Errorf("%d channels after the delete", n)
	}
	script.api(404, "GET", detail, nil)
	if calls := e.callsTo("DELETE", "/api/channel/"); len(calls) != 0 {
		t.Errorf("new-api's hard delete called: %v", calls)
	}
}

func TestCreateTakesOnlyTheCallersFields(t *testing.T) {
	e := newEnv(t)
	script := e.asToken(e.alice)
	script.addChannel(ch{"mode": "single", "batch_add_set_key_prefix_2_name": true, "channel": ch{"type": 20, "name": "n", "key": "sk-a-key",
		"models": "tpl-b", "group": "evil", "priority": 99, "weight": 99, "param_override": `{"x":1}`, "setting": `{"y":1}`, "base_url": "https://evil.example.com"}})
	c := e.lastCreate().Create
	if c.KeyPrefix || c.Channel.Group != "tpl-group" || c.Channel.Priority != 7 || c.Channel.Weight != 3 || c.Channel.ParamOverride != "" ||
		c.Channel.BaseURL != "https://openrouter.example.com" || c.Channel.Models != "tpl-b" {
		t.Errorf("create: %+v", c)
	}
	if ids := script.addChannel(ch{"mode": "batch", "channel": ch{"type": 20, "name": "b", "key": "sk-one\nsk-two"}}); len(ids) != 2 {
		t.Errorf("batch ids = %v", ids)
	}
	second := e.addTemplate("tpl-20-b", 20, "tpl-a", true)
	script.failed("POST", "/api/channel/", oneChannel("sk-a-key"))
	for _, body := range []ch{
		{"mode": "single", "template_id": second, "channel": ch{"type": 20, "name": "top", "key": "sk-a-key"}},
		{"mode": "single", "channel": ch{"type": 20, "name": "nested", "key": "sk-a-key", "template_id": second}},
	} {
		if ids := script.addChannel(body); len(ids) != 1 {
			t.Errorf("template %v: ids = %v", body, ids)
		}
	}
	script.failed("POST", "/api/channel/", ch{"mode": "single"})
	if ids := script.addChannel(ch{"mode": "multi_to_single", "template_id": second, "channel": ch{"type": 20, "name": "m", "key": "sk-one\nsk-two"}}); len(ids) != 1 {
		t.Errorf("no multi_key_mode, like new-api: %v", ids)
	}
}

func TestChannelListPagesAndFiltersLikeNewAPI(t *testing.T) {
	e := newEnv(t)
	e.template(1, "https://api.example.com", false)
	script := e.asToken(e.alice)
	var ids []int
	for i := range 12 {
		ids = append(ids, script.addChannel(oneChannel("sk-key-"+strconv.Itoa(i)))...)
	}
	other := script.addChannel(ch{"mode": "single", "channel": ch{"type": 1, "name": "t1", "key": "sk-key-x"}})[0]
	script.ok("POST", fmt.Sprintf("/api/channel/%d/status", ids[0]), ch{"status": 2})
	if _, err := e.raw.Exec(`UPDATE channels SET status = 3 WHERE id = $1`, e.nid(ids[1])); err != nil {
		t.Fatal(err)
	}

	first := script.channelPage("")
	if len(first.Items) != 10 || first.Total != 13 || first.Page != 1 || first.PageSize != 10 || first.Items[0].ID != other {
		t.Errorf("first page: %+v", first)
	}
	if p := script.channelPage("?p=2&ps=5"); len(p.Items) != 5 || p.Page != 2 || p.PageSize != 5 {
		t.Errorf("ps alias: %+v", p)
	}
	if p := script.channelPage("?p=99999999999&page_size=1000"); len(p.Items) != 0 || p.PageSize != 100 || p.Total != 13 {
		t.Errorf("past the end: %+v", p)
	}
	if p := script.channelPage("?p=x&page_size=-1"); p.Page != 1 || p.PageSize != 10 {
		t.Errorf("invalid paging: %+v", p)
	}
	for query, want := range map[string]int{"status=disabled": 2, "status=0": 2, "status=enabled": 11, "status=1": 11, "status=whatever": 13,
		"type=1": 1, "type=20": 12, "group=tpl-group": 13, "group=none": 0, "group=all": 13} {
		if p := script.channelPage("?page_size=100&" + query); p.Total != want || len(p.Items) != want {
			t.Errorf("%s: total %d, %d items, want %d", query, p.Total, len(p.Items), want)
		}
	}
	script.failed("GET", "/api/channel/?tag_mode=true", nil)
}

func TestScriptsCannotDoWhatThePageCannot(t *testing.T) {
	e := newEnv(t)
	script := e.asToken(e.alice)
	id := script.addChannel(oneChannel("sk-secret-key"))[0]
	for _, field := range []string{"key", "base_url", "key_mode", "multi_key_mode", "status"} {
		if msg := script.failed("PUT", "/api/channel/", ch{"id": id, "name": "x", field: "1"}); !strings.Contains(msg, field) {
			t.Errorf("%s: %s", field, msg)
		}
		if field == "status" {
			script.failed("PUT", "/api/channel/", ch{"id": id, "name": "x", field: 0})
		}
	}
	if calls := e.callsTo("PUT", "/api/channel/"); len(calls) != 0 {
		t.Fatalf("new-api was called: %v", calls)
	}
	script.ok("PUT", "/api/channel/", ch{"id": id, "name": "x", "key": "", "base_url": nil, "status": nil})

	multi := script.addChannel(ch{"mode": "multi_to_single", "multi_key_mode": "polling", "channel": ch{"type": 20, "name": "m", "key": "sk-one\nsk-two"}})[0]
	before := len(e.callsTo("POST", "/api/channel/multi_key/manage"))
	for _, action := range []string{"delete_key", "delete_disabled_keys", "enable_all_keys", "disable_all_keys", "whatever", ""} {
		script.failed("POST", "/api/channel/multi_key/manage", ch{"channel_id": multi, "action": action, "key_index": 0})
	}
	if after := len(e.callsTo("POST", "/api/channel/multi_key/manage")); after != before {
		t.Errorf("%d key actions reached new-api", after-before)
	}
}

func TestTokenStaysInItsOwnersScope(t *testing.T) {
	e := newEnv(t)
	id := e.asToken(e.alice).addChannel(ch{"mode": "multi_to_single", "multi_key_mode": "polling", "channel": ch{"type": 20, "name": "m", "key": "sk-one\nsk-two"}})[0]
	bobby := e.asToken(e.bobby)
	path := fmt.Sprintf("/%d", id)
	for _, r := range [][2]string{{"GET", "/api/channel" + path}, {"DELETE", "/api/channel" + path}, {"POST", "/api/channel" + path + "/status"},
		{"GET", "/api/channel/test" + path}, {"GET", "/api/channel/update_balance" + path}} {
		bobby.api(404, r[0], r[1], ch{"status": 2})
	}
	e.setKeyProbe(true)
	bobby.api(404, "POST", "/api/channel/multi_key/manage", ch{"channel_id": id, "action": "test_keys", "key_index": 0, "model": "tpl-a"})
	bobby.api(404, "POST", "/api/channel/multi_key/manage", ch{"channel_id": id, "action": "get_key_balance"})
	bobby.api(404, "PUT", "/api/channel/", ch{"id": id, "name": "stolen"})
	bobby.api(404, "POST", "/api/channel/multi_key/manage", ch{"channel_id": id, "action": "get_key_status"})
	bobby.api(404, "POST", "/api/channel/multi_key/manage", ch{"channel_id": id, "action": "disable_key", "key_index": 0})
	if n := bobby.channelPage("").Total; n != 0 {
		t.Errorf("bobby sees %d channels", n)
	}
}

func TestMultiKeyManageThroughTheToken(t *testing.T) {
	e := newEnv(t)
	e.fakeOpenRouter()
	e.setKeyProbe(true)
	script := e.asToken(e.alice)
	id := e.probeable(e.asUser(e.alice), "sk-or-v1-good0aaaaaaaaaaaaaaaaaaaaaaaaaa", "sk-or-v1-good1bbbbbbbbbbbbbbbbbbbbbbbbbb", "sk-or-v1-good2cccccccccccccccccccccccccc") // gitleaks:allow
	manage := func(body ch) ch {
		body["channel_id"] = id
		return body
	}
	const path = "/api/channel/multi_key/manage"
	script.ok("POST", path, manage(ch{"action": "disable_key", "key_index": 1}))
	e.mu.Lock()
	e.keyStatus[e.nid(id)][2] = 3
	e.mu.Unlock()

	type status struct {
		Keys []struct {
			Index int `json:"index"`
		} `json:"keys"`
		Total               int `json:"total"`
		Page                int `json:"page"`
		PageSize            int `json:"page_size"`
		TotalPages          int `json:"total_pages"`
		EnabledCount        int `json:"enabled_count"`
		ManualDisabledCount int `json:"manual_disabled_count"`
		AutoDisabledCount   int `json:"auto_disabled_count"`
	}
	read := func(body ch) (status, string) {
		raw := string(script.ok("POST", path, manage(body)).Data)
		return decode[status](t, raw), raw
	}
	all, raw := read(ch{"action": "get_key_status"})
	if len(all.Keys) != 3 || all.Total != 3 || all.PageSize != 50 || all.EnabledCount != 1 || all.ManualDisabledCount != 1 || all.AutoDisabledCount != 1 {
		t.Errorf("all keys: %+v", all)
	}
	if strings.Contains(raw, "key_preview") || strings.Contains(raw, "sk-or") {
		t.Errorf("key material in %s", raw)
	}
	if s, _ := read(ch{"action": "get_key_status", "status": 2}); s.Total != 1 || s.Keys[0].Index != 1 || s.EnabledCount != 1 {
		t.Errorf("status filter: %+v", s)
	}
	if s, _ := read(ch{"action": "get_key_status", "page": 9, "page_size": 2}); s.Page != 2 || s.TotalPages != 2 || len(s.Keys) != 1 {
		t.Errorf("page past the end: %+v", s)
	}

	script.ok("POST", path, manage(ch{"action": "enable_key", "key_index": 1}))
	script.failed("POST", path, manage(ch{"action": "enable_key"}))
	script.api(404, "POST", path, manage(ch{"action": "disable_key", "key_index": 9}))
	e.mu.Lock()
	e.refuseKeys = map[int]bool{0: true}
	e.mu.Unlock()
	if msg := script.failed("POST", path, manage(ch{"action": "disable_key", "key_index": 0})); strings.Contains(msg, "sk-or") || !strings.Contains(msg, "refused") {
		t.Errorf("refusal: %s", msg)
	}

	tested := decode[testResp](t, string(script.ok("POST", path, manage(ch{"action": "test_keys", "key_indexes": []int{0, 1}, "model": "tpl-a"})).Data))
	if len(tested.Results) != 2 || !tested.Results[0].OK {
		t.Errorf("test_keys: %+v", tested)
	}
	if got := decode[testResp](t, string(script.ok("POST", path, manage(ch{"action": "test_keys", "key_index": 2, "model": "tpl-a"})).Data)); len(got.Results) != 1 {
		t.Errorf("test_keys by key_index: %+v", got)
	}
	balances := decode[keysResp](t, string(script.ok("POST", path, manage(ch{"action": "get_key_balance", "key_indexes": []int{0}})).Data))
	if balances.Keys[0].Balance == nil || *balances.Keys[0].Balance != 7.5 || balances.Keys[1].Balance != nil {
		t.Errorf("get_key_balance: %+v", balances)
	}
	if got := decode[keysResp](t, string(script.ok("POST", path, manage(ch{"action": "get_key_balance"})).Data)); got.TotalBalance == nil || *got.TotalBalance != 22.5 {
		t.Errorf("get_key_balance of all: %+v", got)
	}

	e.setKeyProbe(false)
	if r := script.api(403, "POST", path, manage(ch{"action": "test_keys", "key_index": 0, "model": "tpl-a"})); r.Success {
		t.Errorf("probe switched off: %+v", r)
	}
}

func TestLogsThroughTheToken(t *testing.T) {
	e := newEnv(t)
	script := e.asToken(e.alice)
	id := script.addChannel(oneChannel("sk-secret-key"))[0]
	other := e.asToken(e.bobby).addChannel(oneChannel("sk-other-key"))[0]
	now := time.Now().Unix()
	e.addLog(logRow{channel: id, typ: 2, at: now - 100, model: "m", token: "end-token", quota: 5})
	e.addLog(logRow{channel: id, typ: 2, at: now - 99, model: "m", token: "end-token", quota: 7})

	type page struct {
		Items []struct {
			ID        int    `json:"id"`
			Channel   int    `json:"channel"`
			ChannelID int    `json:"channel_id"`
			Username  string `json:"username"`
		} `json:"items"`
		Total    int `json:"total"`
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
	}
	// Nothing given: the default range must fit the inner 92 days.
	all := decode[page](t, string(script.ok("GET", "/api/log/self", nil).Data))
	if all.Total != 2 || all.Page != 1 || all.PageSize != 10 || all.Items[0].Channel != id || all.Items[0].ChannelID != id {
		t.Errorf("logs: %+v", all)
	}
	if strings.Contains(script.want(200, "GET", "/api/log/self", nil), "end-user") {
		t.Error("end user not hidden")
	}
	// new-api's end is inclusive.
	q := fmt.Sprintf("?start_timestamp=%d&end_timestamp=%d", now-100, now-100)
	if got := decode[page](t, string(script.ok("GET", "/api/log/self"+q, nil).Data)); got.Total != 1 {
		t.Errorf("one-second range: %+v", got)
	}
	if got := decode[page](t, string(script.ok("GET", fmt.Sprintf("/api/log/self?channel=%d&p=2&ps=1", id), nil).Data)); len(got.Items) != 1 || got.Page != 2 || got.PageSize != 1 {
		t.Errorf("channel filter: %+v", got)
	}
	script.ok("GET", "/api/log/self?channel=0", nil)

	stat := decode[struct {
		Quota int `json:"quota"`
	}](t, string(script.ok("GET", "/api/log/self/stat", nil).Data))
	if stat.Quota != 12 {
		t.Errorf("stat: %+v", stat)
	}
	rows := decode[[]struct {
		Quota int `json:"quota"`
	}](t, string(script.ok("GET", "/api/data/self?by=channel", nil).Data))
	if len(rows) == 0 || rows[0].Quota == 0 {
		t.Errorf("data: %+v", rows)
	}
	script.api(404, "GET", fmt.Sprintf("/api/data/self?channel=%d", other), nil)
	if msg := script.failed("GET", fmt.Sprintf("/api/log/self?start_timestamp=1&end_timestamp=%d", now), nil); !strings.Contains(msg, "92") {
		t.Errorf("long range: %s", msg)
	}
	// The end is inclusive, so 92 days of it is one second over the inner limit.
	const span = 92 * 86400
	script.failed("GET", fmt.Sprintf("/api/log/self?start_timestamp=%d&end_timestamp=%d", now-span, now), nil)
	script.ok("GET", fmt.Sprintf("/api/log/self?start_timestamp=%d&end_timestamp=%d", now-span+1, now), nil)
}

func TestLogScopeHoldsForTheToken(t *testing.T) {
	e := newEnv(t)
	script := e.asToken(e.alice)
	own := script.addChannel(oneChannel("sk-secret-key"))[0]
	other := e.asToken(e.bobby).addChannel(oneChannel("sk-other-key"))[0]
	now := time.Now().Unix()
	e.addLog(logRow{channel: own, typ: 2, at: now - 10, model: "m", quota: 1})
	e.addLog(logRow{channel: other, typ: 2, at: now - 10, model: "m", quota: 1})
	type page struct {
		Items []struct {
			Channel int `json:"channel"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if got := decode[page](t, string(script.ok("GET", "/api/log/self", nil).Data)); got.Total != 2 {
		t.Errorf("scope all: %+v", got)
	}
	if _, err := e.st.SetPortalLogScope(context.Background(), e.alice, store.LogScopeSelf); err != nil {
		t.Fatal(err)
	}
	if got := decode[page](t, string(script.ok("GET", "/api/log/self", nil).Data)); got.Total != 1 || got.Items[0].Channel != own {
		t.Errorf("scope self: %+v", got)
	}
	script.api(404, "GET", fmt.Sprintf("/api/log/self?channel=%d", other), nil)
	script.api(404, "GET", fmt.Sprintf("/api/log/self/stat?channel=%d", other), nil)
}

func TestProgramAPINeverLeaksSecrets(t *testing.T) {
	e := newEnv(t)
	e.template(1, "https://api.example.com", false)
	if _, err := e.raw.Exec(`UPDATE channel_templates SET extra = '{"param_override":"{\"p\":1}","setting":"{\"s\":1}","settings":"{\"t\":1}","header_override":"{\"Authorization\":\"sk-tpl\"}"}'`); err != nil {
		t.Fatal(err)
	}
	script := e.asToken(e.alice)
	id := script.addChannel(oneChannel("sk-secret-key"))[0]
	multi := script.addChannel(ch{"mode": "multi_to_single", "multi_key_mode": "polling", "channel": ch{"type": 20, "name": "m", "key": "sk-secret-one\nsk-secret-two"}})[0]
	e.mu.Lock()
	e.keyStatus[e.nid(multi)] = map[int]int{0: 3}
	e.mu.Unlock()
	e.addLog(logRow{channel: id, typ: 2, at: time.Now().Unix() - 10, model: "m", other: `{"note":"sk-secret-key-in-a-log"}`})
	e.fakeOpenRouter()
	e.setKeyProbe(true)
	const manage = "/api/channel/multi_key/manage"
	probed := e.probeable(e.asUser(e.alice), "sk-secret-good0aaaaaaaaaaaaaaaaaaaaaaaaaa", "sk-secret-bad1bbbbbbbbbbbbbbbbbbbbbbbbbb") // gitleaks:allow
	e.mu.Lock()
	e.refuseKeys = map[int]bool{0: true}
	e.rawBalance = "upstream said sk-secret-key"
	e.mu.Unlock()

	bodies := []string{
		script.want(200, "GET", "/api/channel/", nil),
		script.want(200, "GET", fmt.Sprintf("/api/channel/%d", id), nil),
		script.want(200, "GET", "/api/channel/types", nil),
		script.want(200, "GET", "/api/log/self", nil),
		script.want(200, "POST", manage, ch{"channel_id": multi, "action": "get_key_status"}),
		script.want(200, "GET", fmt.Sprintf("/api/channel/test/%d?model=pass", id), nil),
		script.want(200, "GET", fmt.Sprintf("/api/channel/test/%d?model=quote", id), nil),
		script.want(200, "GET", fmt.Sprintf("/api/channel/update_balance/%d", id), nil),
		script.want(200, "POST", manage, ch{"channel_id": probed, "action": "disable_key", "key_index": 0}),
		script.want(200, "POST", manage, ch{"channel_id": probed, "action": "test_keys", "key_indexes": []int{0, 1}, "model": "tpl-a"}),
		script.want(200, "POST", manage, ch{"channel_id": probed, "action": "get_key_balance"}),
	}
	if !strings.Contains(bodies[6], "upstream refused") || !strings.Contains(bodies[7], `"raw_response":"upstream said `) ||
		!strings.Contains(bodies[8], "refused by new-api") || !strings.Contains(bodies[9], "HTTP 401") || !strings.Contains(bodies[10], "HTTP 401") {
		t.Errorf("failures must still say why: %v", bodies[6:])
	}
	for _, body := range bodies {
		for _, secret := range []string{"sk-secret", "sk-tpl", script.token, "key_preview", `"key"`, "other_info", "remark", "deleted_at", "header_override", "param_override",
			`"setting"`, `"settings"`, `"extra"`, "multi_key_disabled_reason"} {
			if strings.Contains(body, secret) {
				t.Errorf("%q in %s", secret, body)
			}
		}
	}
	if !strings.Contains(bodies[0], `"base_url":"https://openrouter.example.com"`) {
		t.Errorf("base_url is read-only, not hidden: %s", bodies[0])
	}
	var n int
	if err := e.raw.QueryRow(`SELECT count(*) FROM audit_logs WHERE detail::text LIKE '%ptl_%' OR detail::text LIKE '%sk-secret%' OR user_agent LIKE '%ptl_%'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("audit rows with a secret: %d (%v)", n, err)
	}
}

// A channel removed in new-api stays listed from our own record, as exists:false.
func TestChannelGoneFromNewAPI(t *testing.T) {
	e := newEnv(t)
	script := e.asToken(e.alice)
	id := script.addChannel(oneChannel("sk-secret-key"))[0]
	if _, err := e.raw.Exec(`DELETE FROM channels WHERE id = $1`, e.nid(id)); err != nil {
		t.Fatal(err)
	}
	disabled := string(script.ok("GET", "/api/channel/?status=disabled", nil).Data)
	if !strings.Contains(disabled, `"exists":false`) || !strings.Contains(disabled, `"status":0`) {
		t.Errorf("disabled list: %s", disabled)
	}
	path := fmt.Sprintf("/api/channel/%d", id)
	script.api(404, "POST", path+"/status", ch{"status": 1})
	script.api(404, "GET", fmt.Sprintf("/api/channel/test/%d", id), nil)
	before := len(e.callsTo("POST", "/api/channel/"))

	// Nothing is left in new-api, so this clears the record and calls nothing.
	script.ok("DELETE", path, nil)
	if after := len(e.callsTo("POST", "/api/channel/")); after != before || len(e.callsTo("DELETE", "/api/channel/")) != 0 {
		t.Errorf("new-api was called for a channel it does not have")
	}
	if got := e.lastAudit("channel.delete"); !strings.Contains(got, `"orphan": true`) {
		t.Errorf("audit: %s", got)
	}
	if n := script.channelPage("?status=disabled").Total; n != 0 {
		t.Errorf("%d channels left", n)
	}
	script.api(404, "DELETE", path, nil)
}

func TestTokenRateLimitIsPerUser(t *testing.T) {
	e := newEnv(t)
	script := e.asToken(e.alice)
	for range 120 {
		script.ok("GET", "/api/channel/types", nil)
	}
	rec := script.do("GET", "/api/channel/types", nil)
	if rec.Code != 429 {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
	if n, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || n < 1 || n > 180 {
		t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	if r := decode[reply](t, rec.Body.String()); r.Success || r.Message == "" {
		t.Errorf("429 body: %s", rec.Body)
	}
	e.asToken(e.bobby).ok("GET", "/api/channel/types", nil)
	e.asUser(e.alice).want(200, "GET", "/api/channels", nil)
}

func TestAuditNamesTheToken(t *testing.T) {
	e := newEnv(t)
	script := e.asToken(e.alice)
	var tokenID int64
	if err := e.raw.QueryRow(`SELECT id FROM api_tokens`).Scan(&tokenID); err != nil {
		t.Fatal(err)
	}
	script.addChannel(oneChannel("sk-secret-key"))
	if got := e.lastAudit("channel.create"); !strings.Contains(got, fmt.Sprintf(`"token_id": %d`, tokenID)) {
		t.Errorf("audit: %s", got)
	}
	script.channelPage("")
	if got := e.lastAudit("view.channels"); !strings.Contains(got, `"token_id"`) {
		t.Errorf("view audit: %s", got)
	}
	e.asUser(e.alice).create(nil)
	if got := e.lastAudit("channel.create"); strings.Contains(got, "token_id") {
		t.Errorf("session audited with a token: %s", got)
	}
}

func TestFailuresKeepOnlyTheStatusesAScriptActsOn(t *testing.T) {
	e := newEnv(t)
	script := e.asToken(e.alice)
	id := script.addChannel(oneChannel("sk-secret-key"))[0]
	e.mu.Lock()
	e.rejectUpdate, e.notInCache = true, true
	e.mu.Unlock()
	// new-api refusing is a 502 inside: it must not look like a retryable gateway error.
	if msg := script.failed("PUT", "/api/channel/", ch{"id": id, "name": "x"}); !strings.Contains(msg, "refused by new-api") {
		t.Errorf("refused update: %s", msg)
	}
	if r := script.api(409, "POST", fmt.Sprintf("/api/channel/%d/status", id), ch{"status": 2}); r.Success {
		t.Errorf("not synced yet: %+v", r)
	}
	script.failed("POST", "/api/channel/", ch{"mode": "bogus", "channel": ch{"type": 20, "name": "n", "key": "sk-a-key"}})
	if rec := script.do("POST", "/api/channel/", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"success":false`) {
		t.Errorf("empty body: %d %s", rec.Code, rec.Body)
	}
}
