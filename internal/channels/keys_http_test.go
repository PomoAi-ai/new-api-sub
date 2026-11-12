package channels_test

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"newapisub/internal/channels"
	"newapisub/internal/keyprobe"
	"newapisub/internal/store"
)

// fakeMultiKey is new-api's POST /api/channel/multi_key/manage: statuses live in
// e.keyStatus, the keys in the channels table, and every key goes out with its
// preview and, when disabled, a reason quoting it, as new-api's would.
func (e *fake) fakeMultiKey(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		ChannelID int    `json:"channel_id"`
		Action    string `json:"action"`
		KeyIndex  *int   `json:"key_index"`
		PageSize  int    `json:"page_size"`
	}
	json.Unmarshal(body, &req)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, call{Method: r.Method, Path: r.URL.Path, Body: string(body)})
	if e.denied == r.URL.Path {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"success":false,"message":"The access token lacks the required permission: channel:write"}`)
		return
	}
	var key string
	if err := e.raw.QueryRow(`SELECT key FROM channels WHERE id = $1`, req.ChannelID).Scan(&key); err != nil {
		fmt.Fprint(w, `{"success":false,"message":"Channel does not exist"}`)
		return
	}
	st := e.keyStatus[req.ChannelID]
	if st == nil {
		st = map[int]int{}
		e.keyStatus[req.ChannelID] = st
	}
	if req.KeyIndex != nil && e.refuseKeys[*req.KeyIndex] {
		fmt.Fprintf(w, `{"success":false,"message":"refused by new-api: %s"}`, strings.Split(key, "\n")[*req.KeyIndex])
		return
	}
	switch req.Action {
	case "get_key_status":
		var keys []map[string]any
		for i, k := range strings.Split(strings.Trim(key, "\n"), "\n") {
			x := map[string]any{"index": i, "status": cmp.Or(st[i], 1), "key_preview": k[:min(10, len(k))] + "..."}
			if st[i] > 1 {
				x["disabled_time"], x["reason"] = 100, "status code 401: Incorrect API key provided: "+k
			}
			keys = append(keys, x)
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
			"keys": keys, "total": len(keys), "page": 1, "page_size": req.PageSize, "total_pages": 1}})
	case "enable_key":
		delete(st, *req.KeyIndex)
		fmt.Fprint(w, `{"success":true,"message":"Key enabled"}`)
	case "disable_key":
		st[*req.KeyIndex] = 2
		fmt.Fprint(w, `{"success":true,"message":"Key disabled"}`)
	default:
		fmt.Fprint(w, `{"success":false,"message":"Unsupported operation"}`)
	}
}

// fakeOpenRouter accepts keys containing "good" and echoes every key it gets,
// in the model's answer or in the error. A key that also contains "broke" is
// refused with HTTP 200, as OpenRouter does for some failures. It returns the
// number of calls; the bodies of the model calls go to e.chats.
func (e *env) fakeOpenRouter() *atomic.Int32 {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		authz := r.Header.Get("Authorization")
		if !strings.Contains(authz, "good") {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"error":{"message":"User not found for %s","code":401}}`, authz)
			return
		}
		switch r.URL.Path {
		case "/api/v1/chat/completions":
			body, _ := io.ReadAll(r.Body)
			e.mu.Lock()
			e.chats = append(e.chats, string(body))
			e.mu.Unlock()
			if strings.Contains(authz, "broke") {
				fmt.Fprintf(w, `{"error":{"code":402,"message":"No credits left on %s"}}`, authz)
				return
			}
			fmt.Fprintf(w, `{"id":%q,"choices":[{"message":{"role":"assistant","content":%q}}]}`, authz, authz)
		case "/api/v1/credits":
			fmt.Fprint(w, `{"data":{"total_credits":10,"total_usage":2.5}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	e.t.Cleanup(srv.Close)
	old := keyprobe.OpenRouterURL
	keyprobe.OpenRouterURL = srv.URL
	e.t.Cleanup(func() { keyprobe.OpenRouterURL = old })
	return &n
}

func (e *env) setKeyProbe(on bool) {
	e.t.Helper()
	super := e.asAdmin(e.super)
	x := decode[store.Settings](e.t, super.want(200, "GET", "/api/settings", nil))
	x.KeyProbe = on
	super.want(200, "PUT", "/api/settings", x)
}

type keysResp struct {
	ProbeEnabled   bool     `json:"probe_enabled"`
	ProbeSupported bool     `json:"probe_supported"`
	TotalBalance   *float64 `json:"total_balance"`
	Keys           []struct {
		Index        int      `json:"index"`
		Status       int      `json:"status"`
		DisabledTime int64    `json:"disabled_time"`
		Reason       string   `json:"reason"`
		Balance      *float64 `json:"balance"`
		BalanceAt    *int64   `json:"balance_at"`
		BalanceError string   `json:"balance_error"`
	} `json:"keys"`
}

type testResp struct {
	Results []struct {
		Index     int    `json:"index"`
		OK        bool   `json:"ok"`
		Message   string `json:"message"`
		LatencyMS *int64 `json:"latency_ms"`
	} `json:"results"`
}

type switchResp struct {
	Done  []int  `json:"done"`
	Error string `json:"error"`
}

func at(indexes ...int) ch { return ch{"indexes": append([]int{}, indexes...)} }

func test(model string, indexes ...int) ch {
	return ch{"indexes": append([]int{}, indexes...), "model": model}
}

// probeable adds a multi-key channel the key routes can reach OpenRouter for.
func (e *env) probeable(c caller, keys ...string) int {
	e.t.Helper()
	id := c.create(ch{"mode": "multi_to_single", "multi_key_mode": "polling", "key": strings.Join(keys, "\n")})[0]
	if _, err := e.raw.Exec(`UPDATE channels SET base_url = '' WHERE id = $1`, e.nid(id)); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) configure(id int, modelMapping, paramOverride any) {
	e.t.Helper()
	if _, err := e.raw.Exec(`UPDATE channels SET model_mapping = $1, param_override = $2 WHERE id = $3`, modelMapping, paramOverride, e.nid(id)); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) lastAudit(action string) string {
	e.t.Helper()
	var detail string
	var success bool
	if err := e.raw.QueryRow(`SELECT detail::text, success FROM audit_logs WHERE action = $1 ORDER BY id DESC LIMIT 1`, action).Scan(&detail, &success); err != nil {
		e.t.Fatal(err)
	}
	return strconv.FormatBool(success) + " " + detail
}

func TestKeyManagementNeverRevealsAKey(t *testing.T) {
	e := newEnv(t)
	e.fakeOpenRouter()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	keys := []string{"sk-or-v1-good0aaaaaaaaaaaaaaaaaaaaaaaaaa", "sk-or-v1-bad1bbbbbbbbbbbbbbbbbbbbbbbbbb", "sk-or-v1-good2cccccccccccccccccccccccccc", "sk-or-v1-good3brokedddddddddddddddddddd"}
	alice := e.asUser(e.alice)
	id := e.probeable(alice, keys...)
	e.mu.Lock()
	e.keyStatus[e.nid(id)] = map[int]int{2: 3}
	e.mu.Unlock()
	path := func(suffix string) string { return e.channelPath(id, "/keys"+suffix) }
	var bodies []string
	keep := func(b string) string { bodies = append(bodies, b); return b }

	v := decode[keysResp](t, keep(alice.want(200, "GET", path(""), nil)))
	if v.ProbeEnabled || !v.ProbeSupported || v.TotalBalance != nil || len(v.Keys) != 4 {
		t.Fatalf("keys = %+v", v)
	}
	if k := v.Keys[2]; k.Status != 3 || k.DisabledTime != 100 || !strings.Contains(k.Reason, "Incorrect API key provided: ••••") {
		t.Errorf("auto-disabled key = %+v", k)
	}
	keep(alice.want(403, "POST", path("/test"), test("tpl-a", 0)))
	keep(alice.want(403, "POST", path("/balance"), nil))

	e.setKeyProbe(true)
	if r := decode[switchResp](t, keep(alice.want(200, "POST", path("/disable"), at(1)))); !slices.Equal(r.Done, []int{1}) || r.Error != "" {
		t.Errorf("disable = %+v", r)
	}
	keep(alice.want(200, "POST", path("/enable"), at(1)))
	keep(alice.want(404, "POST", path("/enable"), at(4)))
	keep(alice.want(400, "POST", path("/test"), test("tpl-a", -1)))

	// An order of its own: the results come back by index whatever finishes first.
	r := decode[testResp](t, keep(alice.want(200, "POST", path("/test"), test("tpl-a", 3, 0, 1))))
	if len(r.Results) != 3 || r.Results[0].Index != 0 || r.Results[1].Index != 1 || r.Results[2].Index != 3 {
		t.Fatalf("results = %+v", r)
	}
	if good := r.Results[0]; !good.OK || good.LatencyMS == nil {
		t.Errorf("test of a good key = %+v", good)
	}
	if refused := r.Results[1]; refused.OK || !strings.Contains(refused.Message, "HTTP 401") {
		t.Errorf("test of a refused key = %+v", refused)
	}
	if broke := r.Results[2]; broke.OK || !strings.Contains(broke.Message, "No credits left") {
		t.Errorf("test of a key answered with HTTP 200 and an error = %+v", broke)
	}
	audited := e.lastAudit("channel.key_test")
	if !strings.HasPrefix(audited, "false ") || !strings.Contains(audited, `"checked": 3`) || !strings.Contains(audited, `"failed": 2`) ||
		!strings.Contains(audited, `"model": "tpl-a"`) || strings.Contains(audited, "User not found") {
		t.Errorf("audit = %s", audited)
	}
	v = decode[keysResp](t, keep(alice.want(200, "POST", path("/balance"), nil)))
	if v.TotalBalance == nil || *v.TotalBalance != 22.5 || *v.Keys[0].Balance != 7.5 || v.Keys[1].Balance != nil ||
		!strings.Contains(v.Keys[1].BalanceError, "HTTP 401") || v.Keys[1].BalanceAt == nil {
		t.Errorf("balances = %s", bodies[len(bodies)-1])
	}
	keep(alice.want(200, "POST", path("/balance"), at(0)))
	keep(alice.want(404, "POST", path("/balance"), at(4)))

	list := keep(alice.want(200, "GET", "/api/channels", nil))
	if !strings.Contains(list, `"balance":22.5,`) || !strings.Contains(list, `"balance_from_keys":true`) {
		t.Errorf("list = %s", list)
	}
	// new-api cannot query a multi-key channel's balance; the page asks per key instead.
	if b := keep(alice.want(400, "POST", e.channelPath(id, "/balance"), nil)); !strings.Contains(b, "管理 Key") {
		t.Errorf("channel balance = %s", b)
	}

	var actions []string
	for _, c := range e.callsTo("POST", "/api/channel/multi_key/manage") {
		var req struct {
			Action   string `json:"action"`
			KeyIndex *int   `json:"key_index"`
		}
		json.Unmarshal([]byte(c.Body), &req)
		if req.Action != "get_key_status" {
			actions = append(actions, req.Action+":"+strconv.Itoa(*req.KeyIndex))
		}
	}
	if !slices.Equal(actions, []string{"disable_key:1", "enable_key:1"}) {
		t.Errorf("new-api got %v", actions)
	}

	var audits, stored string
	e.raw.QueryRow(`SELECT string_agg(action || ' ' || detail::text, E'\n') FROM audit_logs WHERE action LIKE '%key%'`).Scan(&audits)
	e.raw.QueryRow(`SELECT string_agg(error, E'\n') FROM channel_key_balances`).Scan(&stored)
	for _, action := range []string{"view.channel_keys", "channel.key_enable", "channel.key_disable", "channel.key_test", "channel.key_balance"} {
		if !strings.Contains(audits, action+" ") {
			t.Errorf("no audit row for %s:\n%s", action, audits)
		}
	}
	everything := strings.Join(append(bodies, audits, stored, logs.String()), "\n")
	for _, k := range keys {
		// The whole key, new-api's preview of it, and the part after the common prefix.
		for _, leak := range []string{k, k[:10], k[len(k)-12:]} {
			if strings.Contains(everything, leak) {
				t.Errorf("%q got out:\n%s", leak, everything)
			}
		}
	}
}

func TestKeyRoutesRefuseWhatTheyMustNotDo(t *testing.T) {
	e := newEnv(t)
	calls := e.fakeOpenRouter()
	e.setKeyProbe(true)
	alice, bobby, adminA, adminB := e.asUser(e.alice), e.asUser(e.bobby), e.asAdmin(e.a), e.asAdmin(e.b)
	multi := alice.create(ch{"mode": "multi_to_single", "multi_key_mode": "random", "key": "sk-or-v1-good0\nsk-or-v1-good1"})[0]
	single := alice.create(ch{})[0]
	path := func(id int, suffix string) string { return e.channelPath(id, "/keys"+suffix) }

	for _, c := range []caller{bobby, adminB} {
		c.want(404, "GET", path(multi, ""), nil)
		c.want(404, "POST", path(multi, "/disable"), at(0))
		c.want(404, "POST", path(multi, "/test"), test("tpl-a", 0))
		c.want(404, "POST", path(multi, "/balance"), nil)
	}
	adminA.want(200, "GET", path(multi, ""), nil)
	alice.want(400, "GET", path(single, ""), nil)
	alice.want(400, "POST", path(single, "/test"), test("tpl-a", 0))

	// The template's base_url is not OpenRouter's own, and neither is a look-alike.
	for _, base := range []string{"https://openrouter.example.com", "https://openrouter.ai.example.com/api", "https://openrouter.ai:8443"} {
		if _, err := e.raw.Exec(`UPDATE channels SET base_url = $1 WHERE id = $2`, base, e.nid(multi)); err != nil {
			t.Fatal(err)
		}
		if v := decode[keysResp](t, alice.want(200, "GET", path(multi, ""), nil)); v.ProbeSupported {
			t.Errorf("%s: probe supported", base)
		}
		alice.want(400, "POST", path(multi, "/test"), test("tpl-a", 0))
		alice.want(400, "POST", path(multi, "/balance"), nil)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("OpenRouter got %d calls for channels that are not its own", n)
	}
	if _, err := e.raw.Exec(`UPDATE channels SET base_url = 'https://openrouter.ai/api/v1' WHERE id = $1`, e.nid(multi)); err != nil {
		t.Fatal(err)
	}
	alice.want(200, "POST", path(multi, "/test"), test("tpl-a", 0))
	// One key read per channel at a time, whichever kind: a running one turns both away.
	channels.ProbeBusy.Store(multi, struct{}{})
	alice.want(409, "POST", path(multi, "/balance"), nil)
	alice.want(409, "POST", path(multi, "/test"), test("tpl-a", 0))
	channels.ProbeBusy.Delete(multi)

	// Without the key connection there is no fallback to another one.
	if _, err := e.raw.Exec(`UPDATE newapi_pools SET key_dsn = NULL WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	e.serve()
	noProbe := e.asAdmin(e.super)
	for p, body := range map[string]ch{"/test": test("tpl-a", 0), "/balance": nil} {
		if b := noProbe.want(503, "POST", path(multi, p), body); !strings.Contains(b, "未配置 key 读取连接") {
			t.Errorf("%s without a key connection: %s", p, b)
		}
	}
	noProbe.want(200, "GET", path(multi, ""), nil)

	// The switch stops the key reads only.
	e.setKeyProbe(false)
	alice.want(403, "POST", path(multi, "/test"), test("tpl-a", 0))
	alice.want(403, "POST", path(multi, "/balance"), nil)
	alice.want(200, "POST", path(multi, "/disable"), at(0))
	if n := calls.Load(); n != 1 {
		t.Errorf("OpenRouter calls = %d, want the one allowed test", n)
	}

	alice.want(204, "DELETE", e.channelPath(multi, ""), nil)
	e.setKeyProbe(true)
	alice.want(404, "GET", path(multi, ""), nil)
	alice.want(404, "POST", path(multi, "/enable"), at(0))
	adminA.want(404, "POST", path(multi, "/test"), test("tpl-a", 0))
}

func TestKeyTestSendsTheChannelsOwnConfiguration(t *testing.T) {
	e := newEnv(t)
	e.fakeOpenRouter()
	e.setKeyProbe(true)
	alice := e.asUser(e.alice)
	id := e.probeable(alice, "sk-or-v1-good0", "sk-or-v1-good1")
	path := e.channelPath(id, "/keys/test")
	sent := func() map[string]any {
		t.Helper()
		e.chats = nil
		if r := decode[testResp](t, alice.want(200, "POST", path, test("tpl-a", 0))); !r.Results[0].OK {
			t.Fatalf("test = %+v", r)
		}
		return decode[map[string]any](t, e.chats[0])
	}

	if b := sent(); b["model"] != "tpl-a" || b["max_tokens"] != 16.0 || b["stream"] != false || b["provider"] != nil {
		t.Errorf("no configuration: %v", b)
	}
	only := map[string]any{"only": []any{"openai"}}
	e.configure(id, `{"tpl-a":"openai/gpt-x"}`, `{"operations":[{"mode":"set","path":"provider.only","value":["openai"]}]}`)
	if b := sent(); b["model"] != "openai/gpt-x" || !reflect.DeepEqual(b["provider"], only) {
		t.Errorf("mapping and operations: %v", b)
	}
	e.configure(id, nil, `{"provider":{"only":["openai"]}}`)
	if b := sent(); b["model"] != "tpl-a" || !reflect.DeepEqual(b["provider"], only) {
		t.Errorf("plain object: %v", b)
	}
}

// A test that cannot carry the channel's routing must not go out at all: it
// would reach upstreams the channel is configured to stay away from.
func TestKeyTestSendsNothingItCannotSendFaithfully(t *testing.T) {
	e := newEnv(t)
	calls := e.fakeOpenRouter()
	e.setKeyProbe(true)
	alice := e.asUser(e.alice)
	id := e.probeable(alice, "sk-or-v1-good0", "sk-or-v1-good1")
	path := e.channelPath(id, "/keys/test")

	for _, override := range []string{
		`{"operations":[{"mode":"delete","path":"provider"}]}`,
		`{"operations":[{"mode":"set","path":"provider.only","value":["openai"],"conditions":[{"path":"model","mode":"full","value":"x"}]}]}`,
	} {
		e.configure(id, nil, override)
		if b := alice.want(400, "POST", path, test("tpl-a", 0, 1)); !strings.Contains(b, "param_override") {
			t.Errorf("%s: %s", override, b)
		}
	}
	e.configure(id, nil, nil)
	// Models of other channels would spend the key's money on whatever is dearest.
	for _, model := range []string{"gpt-4-expensive", "", "tpl-a,tpl-b", " tpl-a"} {
		alice.want(400, "POST", path, test(model, 0))
	}
	if _, err := e.raw.Exec(`UPDATE channels SET models = 'tpl-b' WHERE id = $1`, e.nid(id)); err != nil {
		t.Fatal(err)
	}
	alice.want(400, "POST", path, test("tpl-a", 0)) // the template has it, the channel gave it up
	if n := calls.Load(); n != 0 {
		t.Errorf("OpenRouter got %d calls", n)
	}
}

func TestSwitchingKeysStopsAtTheFirstRefusal(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	key := "sk-or-v1-good2cccccccccccccccccccccccccc"
	id := e.probeable(alice, "sk-or-v1-good0", "sk-or-v1-good1", key, "sk-or-v1-good3")
	e.refuseKeys = map[int]bool{2: true}

	body := alice.want(200, "POST", e.channelPath(id, "/keys/disable"), at(1, 0, 2, 3))
	r := decode[switchResp](t, body)
	if !slices.Equal(r.Done, []int{1, 0}) || !strings.Contains(r.Error, "refused by new-api") || strings.Contains(body, key) {
		t.Errorf("disable = %s", body)
	}
	var sent []int
	for _, c := range e.callsTo("POST", "/api/channel/multi_key/manage") {
		var req struct {
			KeyIndex *int `json:"key_index"`
		}
		json.Unmarshal([]byte(c.Body), &req)
		if req.KeyIndex != nil {
			sent = append(sent, *req.KeyIndex)
		}
	}
	if !slices.Equal(sent, []int{1, 0, 2}) {
		t.Errorf("new-api got %v, in that order and nothing after the refusal", sent)
	}
	audited := e.lastAudit("channel.key_disable")
	if !strings.HasPrefix(audited, "false ") || !strings.Contains(audited, `"checked": 2`) || !strings.Contains(audited, `"failed": 2`) ||
		strings.Contains(audited, "refused") {
		t.Errorf("audit = %s", audited)
	}
}

func TestKeyIndexesAreCheckedTheSameEverywhere(t *testing.T) {
	e := newEnv(t)
	calls := e.fakeOpenRouter()
	e.setKeyProbe(true)
	alice := e.asUser(e.alice)
	id := e.probeable(alice, "sk-or-v1-good0", "sk-or-v1-good1")
	sends := len(e.callsTo("POST", "/api/channel/multi_key/manage"))
	many := make([]int, 101)
	for route, body := range map[string]func(...int) ch{
		"/enable":  at,
		"/disable": at,
		"/test":    func(i ...int) ch { return test("tpl-a", i...) },
		"/balance": at,
	} {
		path := e.channelPath(id, "/keys"+route)
		alice.want(400, "POST", path, body())
		alice.want(400, "POST", path, body(many...))
		alice.want(400, "POST", path, body(-1))
		alice.want(400, "POST", path, body(9, -1)) // malformed goes before absent
		alice.want(404, "POST", path, body(0, 2))
	}
	alice.want(400, "POST", e.channelPath(id, "/keys/enable"), ch{}) // only the balance has a default
	if n := calls.Load(); n != 0 {
		t.Errorf("OpenRouter got %d calls", n)
	}
	if n := len(e.callsTo("POST", "/api/channel/multi_key/manage")); n != sends {
		t.Errorf("new-api got %d switches", n-sends)
	}

	if r := decode[switchResp](t, alice.want(200, "POST", e.channelPath(id, "/keys/enable"), at(many[:100]...))); !slices.Equal(r.Done, []int{0}) {
		t.Errorf("100 repeats of one key = %+v", r)
	}
	if r := decode[testResp](t, alice.want(200, "POST", e.channelPath(id, "/keys/test"), test("tpl-a", 1, 1))); len(r.Results) != 1 {
		t.Errorf("a repeated key was tested twice: %+v", r)
	}
}

func TestRateWindowGrantsUpToItsLimit(t *testing.T) {
	if first, second := channels.TakeRate(5, 1, 3); first != 3 || second != 2 {
		t.Errorf("granted %d then %d of 5", first, second)
	}
}
