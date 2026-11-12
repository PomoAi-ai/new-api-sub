package channels_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"newapisub/internal/audit"
)

func decode[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return v
}

func (c caller) from(ip string) caller {
	c.remote = ip
	return c
}

// download goes through a real server: a recorder has no write deadline and
// cannot show a broken connection.
func (c caller) download(query string) (int, string) {
	c.e.t.Helper()
	srv := httptest.NewServer(c.mux)
	defer srv.Close()
	req, err := http.NewRequest("GET", srv.URL+"/api/logs/export?"+query, nil)
	if err != nil {
		c.e.t.Fatal(err)
	}
	req.AddCookie(c.cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.e.t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// Each channel has its own model, so every route shows whose logs it returned.
func TestLogRoutesFollowTheScope(t *testing.T) {
	e := newEnv(t)
	alice, bobby := e.asUser(e.alice), e.asUser(e.bobby)
	adminA, adminB, super := e.asAdmin(e.a), e.asAdmin(e.b), e.asAdmin(e.super)
	now := time.Now().Unix()
	logRange := fmt.Sprintf("start=%d&end=%d", now-60, now+60)
	const (
		mAlice, mBobby, mKeyA, mKeyB, mSuper = "m-alice", "m-bobby", "m-key-a", "m-key-b", "m-super"
	)
	channels := map[string]int{
		mAlice: alice.create(ch{})[0], mBobby: bobby.create(ch{})[0],
		mKeyA: adminA.create(ch{})[0], mKeyB: adminB.create(ch{})[0],
		mSuper: super.create(ch{})[0],
	}
	for model, id := range channels {
		e.addLog(logRow{channel: id, typ: 2, at: now, model: model, token: "t", quota: 1, prompt: 1, output: 1, spent: 1, other: "{}"})
	}

	for _, c := range []struct {
		name  string
		as    caller
		query string
		want  []string
	}{
		{"alice", alice, "", []string{mAlice}},
		{"alice ignores owner", alice, "&owner=self", []string{mAlice}},
		{"alice ignores user_id", alice, "&user_id=" + strconv.FormatInt(e.bobby, 10), []string{mAlice}},
		{"admin a", adminA, "", []string{mAlice, mKeyA}},
		{"admin a only mine", adminA, "&owner=self", []string{mKeyA}},
		{"admin a one user", adminA, "&user_id=" + strconv.FormatInt(e.alice, 10), []string{mAlice}},
		{"admin a other user", adminA, "&user_id=" + strconv.FormatInt(e.bobby, 10), nil},
		{"admin b", adminB, "", []string{mBobby, mKeyB}},
		{"super", super, "", []string{mAlice, mBobby, mKeyA, mKeyB, mSuper}},
		{"super only mine", super, "&owner=self", []string{mSuper}},
	} {
		t.Run(c.name, func(t *testing.T) {
			sorted := func(models []string) []string {
				slices.Sort(models)
				return models
			}
			q := "/api/stats?" + logRange + c.query
			var got []string
			for _, r := range decode[struct {
				Rows []struct {
					Model string `json:"model_name"`
				}
			}](t, c.as.want(200, "GET", q, nil)).Rows {
				got = append(got, r.Model)
			}
			if !slices.Equal(sorted(got), c.want) {
				t.Errorf("stats: %v, want %v", got, c.want)
			}

			got = nil
			for _, r := range decode[struct {
				Rows []struct {
					Model string `json:"model_name"`
				}
			}](t, c.as.want(200, "GET", "/api/stats/flow?"+logRange+c.query, nil)).Rows {
				got = append(got, r.Model)
			}
			if !slices.Equal(sorted(got), c.want) {
				t.Errorf("flow: %v, want %v", got, c.want)
			}

			got = nil
			for _, r := range decode[struct {
				Models []struct {
					Model string `json:"model_name"`
				}
			}](t, c.as.want(200, "GET", "/api/stats/perf?x=1"+c.query, nil)).Models {
				got = append(got, r.Model)
			}
			if !slices.Equal(sorted(got), c.want) {
				t.Errorf("perf: %v, want %v", got, c.want)
			}

		})
	}

	foreign := "&channel_id=" + strconv.Itoa(channels[mBobby])
	adminA.want(404, "GET", "/api/stats/flow?"+logRange+foreign, nil)
	alice.want(404, "GET", "/api/stats/flow?"+logRange+foreign, nil)
	adminA.want(400, "GET", "/api/logs?"+logRange+"&owner=someone", nil)
}

// Logs are open: every role reads all of them, and asks for less with owner=self.
func TestLogPagesAreOpen(t *testing.T) {
	e := newEnv(t)
	alice, adminA, super := e.asUser(e.alice), e.asAdmin(e.a), e.asAdmin(e.super)
	mine, others := alice.create(ch{})[0], e.asUser(e.bobby).create(ch{})[0]
	now := time.Now().Unix()
	logRange := fmt.Sprintf("start=%d&end=%d", now-60, now+60)
	e.addLog(logRow{channel: mine, typ: 2, at: now, model: "m-mine", quota: 1, other: "{}"})
	e.addLog(logRow{channel: others, typ: 2, at: now, model: "m-other", quota: 2, other: "{}"})
	// In no one's scope: added on new-api directly.
	e.addLog(logRow{channel: 987654, typ: 2, at: now, model: "m-direct", quota: 4, other: "{}"})
	everything := []string{"m-direct", "m-mine", "m-other"}

	models := func(c caller, query string) []string {
		t.Helper()
		var got []string
		for _, r := range decode[struct {
			Items []struct {
				Model string `json:"model_name"`
			}
		}](t, c.want(200, "GET", "/api/logs?"+logRange+query, nil)).Items {
			got = append(got, r.Model)
		}
		slices.Sort(got)
		return got
	}
	exported := func(c caller, query string) []string {
		t.Helper()
		_, csv := c.download(logRange + "&tz_offset=0" + query)
		var got []string
		for _, m := range everything {
			if strings.Contains(csv, m) {
				got = append(got, m)
			}
		}
		return got
	}
	for name, c := range map[string]caller{"portal": alice, "admin": adminA, "super": super} {
		if got := models(c, ""); !slices.Equal(got, everything) {
			t.Errorf("%s list: %v", name, got)
		}
		if got := exported(c, ""); !slices.Equal(got, everything) {
			t.Errorf("%s export: %v", name, got)
		}
		stat := decode[struct{ Quota, RPM, Concurrency int }](t, c.want(200, "GET", "/api/logs/stat?"+logRange, nil))
		if stat.Quota != 7 || stat.RPM != 3 || stat.Concurrency != 3 {
			t.Errorf("%s stat: %+v", name, stat)
		}
		// A channel id out of scope is just a filter now.
		if got := models(c, "&channel_id="+strconv.Itoa(others)); !slices.Equal(got, []string{"m-other"}) {
			t.Errorf("%s filtered list: %v", name, got)
		}
	}
	if got := models(alice, "&owner=self"); !slices.Equal(got, []string{"m-mine"}) {
		t.Errorf("portal own list: %v", got)
	}
	if got := exported(alice, "&owner=self"); !slices.Equal(got, []string{"m-mine"}) {
		t.Errorf("portal own export: %v", got)
	}
	stat := decode[struct{ Quota int }](t, alice.want(200, "GET", "/api/logs/stat?"+logRange+"&owner=self", nil))
	if stat.Quota != 1 {
		t.Errorf("portal own stat: %+v", stat)
	}
	alice.want(404, "GET", "/api/logs?"+logRange+"&owner=self&channel_id="+strconv.Itoa(others), nil)
	if got := models(adminA, "&owner=self"); got != nil {
		t.Errorf("admin's own keys: %v", got)
	}

	// The dashboard is still limited to the caller's channels.
	alice.want(404, "GET", "/api/stats?"+logRange+"&tz_offset=0&channel_id="+strconv.Itoa(others), nil)
}

func TestOnlySuperSeesEndUsers(t *testing.T) {
	e := newEnv(t)
	id := e.asUser(e.alice).create(ch{})[0]
	other := `{"cache_tokens":3,"cache_creation_tokens":4,"request_path":"/v1/x","admin_info":{"use_channel":[1]},"channel_name":"hidden-name"}`
	e.addLog(logRow{channel: id, typ: 2, at: 100, model: "m", token: "secret-token", quota: 5, other: other})
	e.addLog(logRow{channel: id, typ: 2, at: 101, model: "m", token: "Model test", quota: 7, other: "not json"})
	endUser := []string{"end-user", "secret-token", "203.0.113.9", "admin_info", "hidden-name"}
	type row struct {
		UserID     int    `json:"user_id"`
		Username   string `json:"username"`
		TokenID    int    `json:"token_id"`
		TokenName  string `json:"token_name"`
		IP         string `json:"ip"`
		Group      string `json:"group"`
		RequestID  string `json:"request_id"`
		UpstreamID string `json:"upstream_request_id"`
		Other      string `json:"other"`
	}
	statQuota := func(c caller, query string) int {
		t.Helper()
		return decode[struct{ Quota int }](t, c.want(200, "GET", "/api/logs/stat?"+logRange+query, nil)).Quota
	}

	portal, admin, super := e.asUser(e.alice), e.asAdmin(e.a), e.asAdmin(e.super)
	for name, c := range map[string]caller{"portal": portal, "admin": admin} {
		list := c.want(200, "GET", "/api/logs?"+logRange, nil)
		_, csv := c.download(logRange + "&tz_offset=0&lang=en")
		flow := c.want(200, "GET", "/api/stats/flow?"+logRange, nil)
		for _, leak := range endUser {
			if strings.Contains(list, leak) {
				t.Errorf("%s list has %q: %s", name, leak, list)
			}
			if strings.Contains(csv, leak) {
				t.Errorf("%s export has %q:\n%s", name, leak, csv)
			}
		}
		for _, leak := range append(endUser, "vip-group", "req-id", "user_id", "token_id", "use_group") {
			if strings.Contains(flow, leak) {
				t.Errorf("%s flow has %q: %s", name, leak, flow)
			}
		}

		rows := decode[struct{ Items []row }](t, list).Items
		if len(rows) != 2 {
			t.Fatalf("%s rows: %+v", name, rows)
		}
		if got := rows[0]; got.TokenName != "Model test" || got.Other != "{}" {
			t.Errorf("%s channel test: %+v", name, got)
		}
		want := row{Group: "vip-group", RequestID: "req-id", UpstreamID: "up-req",
			Other: `{"cache_creation_tokens":4,"cache_tokens":3,"request_path":"/v1/x"}`}
		if rows[1] != want {
			t.Errorf("%s log: %+v", name, rows[1])
		}

		if !strings.Contains(csv, `"Time","Type","User","Token","Group","Model","Channel","Stream","Input Tokens","Output Tokens","Cache Read","Cache Write","Cost","Duration","IP","Request ID","Details"`) ||
			!strings.Contains(csv, `"••••","••••","vip-group"`) || !strings.Contains(csv, `"••••","Model test","vip-group"`) ||
			!strings.Contains(csv, `"3","4"`) || !strings.Contains(csv, `"••••","req-id"`) {
			t.Errorf("%s export:\n%s", name, csv)
		}

		// Searching by an end user must not confirm that it exists; the rest is theirs to search.
		for _, q := range []string{"", "&username=nobody", "&token_name=nobody", "&group=vip-group", "&request_id=req-id", "&upstream_request_id=up-req"} {
			if got := statQuota(c, q); got != 12 {
				t.Errorf("%s %q: quota %d", name, q, got)
			}
		}
		for _, q := range []string{"&group=nobody", "&request_id=nobody", "&upstream_request_id=nobody"} {
			if got := statQuota(c, q); got != 0 {
				t.Errorf("%s %q: quota %d", name, q, got)
			}
		}
		if body := c.want(200, "GET", "/api/logs?"+logRange+"&type=3", nil); !strings.Contains(body, `"total":0`) {
			t.Errorf("%s a type these logs never have: %s", name, body)
		}
		c.want(403, "GET", "/api/stats/users?"+logRange, nil)
	}

	list := super.want(200, "GET", "/api/logs?"+logRange, nil)
	_, csv := super.download(logRange + "&tz_offset=0&lang=en")
	flow := super.want(200, "GET", "/api/stats/flow?"+logRange, nil)
	for _, want := range []string{"end-user", "secret-token", "203.0.113.9"} {
		if !strings.Contains(list, want) || !strings.Contains(csv, want) {
			t.Errorf("super lacks %q:\n%s\n%s", want, list, csv)
		}
	}
	if !strings.Contains(list, "admin_info") || strings.Contains(csv, "••••") {
		t.Errorf("super list or export is filtered:\n%s\n%s", list, csv)
	}
	if !strings.Contains(flow, "end-user") || !strings.Contains(flow, "vip-group") {
		t.Errorf("super flow: %s", flow)
	}
	users := super.want(200, "GET", "/api/stats/users?"+logRange, nil)
	if !strings.Contains(users, `"username":"end-user"`) || !strings.Contains(users, `"quota":5`) {
		t.Errorf("user stats: %s", users)
	}
	for _, q := range []string{"&username=nobody", "&token_name=nobody"} {
		if got := statQuota(super, q); got != 0 {
			t.Errorf("super %q: quota %d", q, got)
		}
	}
}

func TestLogGroupsComeFromChannels(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	alice.create(ch{})
	want := []string{"tpl-group"}
	for name, c := range map[string]caller{"portal": alice, "super": e.asAdmin(e.super)} {
		got := decode[struct{ Groups []string }](t, c.want(200, "GET", "/api/logs/groups", nil)).Groups
		if !slices.Equal(got, want) {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestStatAndPerfNumbers(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	id := alice.create(ch{})[0]
	now := time.Now().Unix()
	logRange := fmt.Sprintf("start=%d&end=%d", now-7200, now+60)
	old := now - 3600 // in the range and the perf window, not in the last 60 seconds
	e.addLog(logRow{channel: id, typ: 2, at: now, model: "m", token: "t", quota: 100, prompt: 10, output: 20, spent: 2, other: "{}"})
	e.addLog(logRow{channel: id, typ: 2, at: old, model: "m", token: "t", quota: 50, prompt: 1, output: 40, spent: 4, other: ""})
	e.addLog(logRow{channel: id, typ: 5, at: now, model: "m", token: "t", quota: 0, prompt: 7, output: 7, spent: 6, other: "not json"})
	e.addLog(logRow{channel: id, typ: 6, at: now, model: "m", token: "t", quota: 30, other: "{}"})
	e.addLog(logRow{channel: id, typ: 2, at: now, model: "m", token: "Model test", quota: 999, prompt: 999, output: 999, spent: 99, other: "{}"})

	stat := decode[struct{ Quota, RPM, TPM int }](t, alice.want(200, "GET", "/api/logs/stat?"+logRange+"&type=5", nil))
	// Consumption only, whatever type is asked for. The channel test counts, as in new-api.
	if stat.Quota != 1149 || stat.RPM != 2 || stat.TPM != 2028 {
		t.Errorf("stat: %+v", stat)
	}

	var perf struct {
		Summary struct {
			AvgLatencyMs int     `json:"avg_latency_ms"`
			SuccessRate  float64 `json:"success_rate"`
			AvgTPS       float64 `json:"avg_tps"`
		} `json:"summary"`
		WindowStart int64 `json:"window_start"`
		WindowEnd   int64 `json:"window_end"`
		Models      []struct {
			Model       string  `json:"model_name"`
			SuccessRate float64 `json:"success_rate"`
		} `json:"models"`
	}
	json.Unmarshal([]byte(alice.want(200, "GET", "/api/stats/perf", nil)), &perf)
	s := perf.Summary
	// 2 of 3 requests succeeded, 12s in total, 60 tokens out in 6s.
	if s.AvgLatencyMs != 4000 || s.SuccessRate != 66.67 || s.AvgTPS != 10 {
		t.Errorf("perf: %+v", s)
	}
	if len(perf.Models) != 1 || perf.Models[0].Model != "m" || perf.Models[0].SuccessRate != 66.67 {
		t.Errorf("perf models: %+v", perf.Models)
	}
	if perf.WindowStart != now-now%3600-23*3600 || perf.WindowEnd < now {
		t.Errorf("perf window: %d %d, now %d", perf.WindowStart, perf.WindowEnd, now)
	}
	if body := e.asUser(e.bobby).want(200, "GET", "/api/stats/perf", nil); !strings.Contains(body, `"summary":null`) {
		t.Errorf("perf without requests: %s", body)
	}

	rows := decode[struct {
		Rows []struct {
			CreatedAt int64 `json:"created_at"`
			Count     int   `json:"count"`
			Quota     int   `json:"quota"`
			TokenUsed int   `json:"token_used"`
		}
	}](t, alice.want(200, "GET", "/api/stats?"+logRange+"&model_name=m", nil)).Rows
	var count, quota, tokens int
	for _, r := range rows {
		if r.CreatedAt%3600 != 0 {
			t.Errorf("not an hour: %d", r.CreatedAt)
		}
		count += r.Count
		quota += r.Quota
		tokens += r.TokenUsed
	}
	if count != 2 || quota != 120 || tokens != 71 { // the refund nets out; errors and tests do not count
		t.Errorf("stats: %+v", rows)
	}
}

func TestExportFile(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	id := alice.create(ch{"name": "chan"})[0]
	e.addLog(logRow{channel: id, typ: 2, at: 1000, model: `=cmd"x`, token: "t", quota: 250000, prompt: 1, output: 2, spent: 3, other: `{"cache_tokens":4}`})
	e.addLog(logRow{channel: id, typ: 6, at: 2000, model: "m", token: "t", quota: 1, other: "not json"})

	status, csv := alice.download("start=1&end=86400&tz_offset=3600")
	if status != 200 || !strings.HasPrefix(csv, "\xEF\xBB\xBF\"时间\",\"类型\",\"用户\",\"令牌\",\"分组\",\"模型\",\"渠道\"") {
		t.Fatalf("%d:\n%s", status, csv)
	}
	lines := strings.Split(csv, "\r\n")
	if len(lines) != 4 || lines[3] != "" {
		t.Fatalf("lines: %q", lines)
	}
	want := `"1970-01-01 01:33:20","退款","••••","••••","vip-group","m","#` + strconv.Itoa(id) + ` chan","是","0","0","0","0","$0.000002","","••••","req-id","c"`
	if lines[1] != want {
		t.Errorf("newest first, refund has no duration:\n%s\n%s", lines[1], want)
	}
	want = `"1970-01-01 01:16:40","消耗","••••","••••","vip-group","'=cmd""x","#` + strconv.Itoa(id) + ` chan","是","1","2","4","0","$0.5","3s","••••","req-id","c"`
	if lines[2] != want {
		t.Errorf("formula is defused, quota is money:\n%s\n%s", lines[2], want)
	}

	if status, _ := alice.download("start=1&end=86400"); status != 400 {
		t.Errorf("no tz_offset: %d", status)
	}
	var audited, rows int
	if err := e.raw.QueryRow(`SELECT count(*), COALESCE(max((detail->>'rows')::int), 0) FROM audit_logs
		WHERE action = 'log.export' AND success AND detail->>'http_route' = '/api/logs/export' AND detail->>'http_status' = '200'`).Scan(&audited, &rows); err != nil {
		t.Fatal(err)
	}
	if audited != 1 || rows != 2 {
		t.Errorf("audited %d exports of %d rows", audited, rows)
	}
	var failed int
	if err := e.raw.QueryRow(`SELECT count(*) FROM audit_logs WHERE action = 'log.export' AND NOT success AND detail->>'http_status' = '400'`).Scan(&failed); err != nil || failed != 1 {
		t.Errorf("failed exports audited: %d, %v", failed, err)
	}
}

// Time decides the order, whatever order new-api wrote the rows in, and id keeps
// the rows of one second in place from page to page.
func TestLogsReadNewestFirstByTime(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	id := alice.create(ch{})[0]
	for _, l := range []struct {
		at    int64
		model string
	}{{200, "same-second-1"}, {100, "oldest"}, {300, "newest"}, {200, "same-second-2"}} {
		e.addLog(logRow{channel: id, typ: 2, at: l.at, model: l.model, token: "t", quota: 1})
	}
	var listed []string
	for _, p := range []string{"1", "2"} {
		for _, row := range decode[struct {
			Items []struct {
				Model string `json:"model_name"`
			}
		}](t, alice.want(200, "GET", "/api/logs?"+logRange+"&page_size=2&p="+p, nil)).Items {
			listed = append(listed, row.Model)
		}
	}
	want := []string{"newest", "same-second-2", "same-second-1", "oldest"}
	if !slices.Equal(listed, want) {
		t.Errorf("list: %v", listed)
	}
	_, csv := alice.download(logRange + "&tz_offset=0")
	at := -1
	for _, model := range want {
		next := strings.Index(csv, model)
		if next <= at {
			t.Errorf("export has %q out of order:\n%s", model, csv)
		}
		at = next
	}
}

// flushRecorder notes what had been written at each flush.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes []string
}

func (f *flushRecorder) Flush() { f.flushes = append(f.flushes, f.Body.String()) }

func (f *flushRecorder) SetWriteDeadline(time.Time) error { return nil }

// The first row must not wait for a buffer to fill: a filter that matches little
// would have the proxy give up on a response that is coming.
func TestExportSendsItsFirstRowAtOnce(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	id := alice.create(ch{})[0]
	e.addLog(logRow{channel: id, typ: 2, at: 100, model: "older", token: "t", quota: 1})
	e.addLog(logRow{channel: id, typ: 2, at: 200, model: "newer", token: "t", quota: 1})
	req := httptest.NewRequest("GET", "/api/logs/export?"+logRange+"&tz_offset=0", nil)
	req.AddCookie(alice.cookie)
	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	alice.mux.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "older") {
		t.Fatalf("%d: %s", w.Code, w.Body)
	}
	if len(w.flushes) == 0 || !strings.Contains(w.flushes[0], "newer") || strings.Contains(w.flushes[0], "older") {
		t.Errorf("flushes: %q", w.flushes)
	}
}

// Exports scan new-api's database, so a process runs two at a time, whoever asks.
func TestExportsRunTwoAtATime(t *testing.T) {
	e := newEnv(t)
	first, second, third := e.asAdmin(e.super), e.asAdmin(e.a), e.asAdmin(e.b)
	query := logRange + "&tz_offset=0"

	// While this lock is held, an export waits in its query.
	lock, err := e.raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err := lock.Exec("LOCK TABLE logs IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	statuses := make(chan int, 2)
	for _, c := range []caller{first, second} {
		go func() {
			status, _ := c.download(query)
			statuses <- status
		}()
	}
	for start := time.Now(); ; time.Sleep(10 * time.Millisecond) {
		var waiting int
		if err := e.raw.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()
			AND wait_event_type = 'Lock' AND query LIKE '%FROM logs%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		if time.Since(start) > 10*time.Second {
			t.Fatalf("%d exports reached their query", waiting)
		}
	}

	if status, body := third.download(query); status != 409 || !strings.Contains(body, "稍后再试") {
		t.Errorf("a third account: %d %s", status, body)
	}
	if status, body := first.download(query); status != 409 || !strings.Contains(body, "已有一个") {
		t.Errorf("the same account: %d %s", status, body)
	}
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if status := <-statuses; status != 200 {
			t.Errorf("a running export ended with %d", status)
		}
	}
	// What ended, refused or not, gave back its place.
	for _, c := range []caller{first, second, third} {
		if status, body := c.download(query); status != 200 {
			t.Errorf("after the others: %d %s", status, body)
		}
	}
}

// A failure after the response started must not look like a complete download.
func TestFailureAfterTheResponseStartedBreaksTheConnection(t *testing.T) {
	e := newEnv(t)
	mux := http.NewServeMux()
	mux.Handle("GET /x", audit.Recorder{Store: e.st, ActorType: "admin"}.Wrap("log.export", func(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
		io.WriteString(w, "partial")
		http.NewResponseController(w).Flush()
		return io.ErrUnexpectedEOF
	}))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if body, err := io.ReadAll(resp.Body); err == nil || strings.Contains(string(body), "error") {
		t.Errorf("complete-looking body %q, err %v", body, err)
	}
	var failed int
	if err := e.raw.QueryRow(`SELECT count(*) FROM audit_logs WHERE action = 'log.export' AND NOT success`).Scan(&failed); err != nil || failed != 1 {
		t.Errorf("failed exports audited: %d, %v", failed, err)
	}
}

func TestAuditFilters(t *testing.T) {
	e := newEnv(t)
	alice, adminA, super := e.asUser(e.alice), e.asAdmin(e.a), e.asAdmin(e.super)
	alice.create(ch{})
	adminA.create(ch{})
	alice.want(404, "POST", e.channelPath(999999, "/status"), ch{"status": 1}) // a failed operation
	adminA.download(logRange + "&tz_offset=0")

	actions := func(c caller, query string) []string {
		t.Helper()
		var out []string
		for _, a := range decode[struct {
			Items []struct {
				Action string `json:"action"`
				Name   string `json:"actor_name"`
				Detail struct {
					Route string `json:"http_route"`
				} `json:"detail"`
			}
		}](t, c.want(200, "GET", "/api/audit-logs?"+query, nil)).Items {
			if a.Detail.Route == "" {
				t.Errorf("%s has no route", a.Action)
			}
			out = append(out, a.Name+":"+a.Action)
		}
		slices.Sort(out)
		return out
	}
	if got := actions(alice, "success=false"); !slices.Equal(got, []string{"alice:channel.status"}) {
		t.Errorf("failed: %v", got)
	}
	if got := actions(alice, "category=channel&success=true"); !slices.Equal(got, []string{"alice:channel.create"}) {
		t.Errorf("channel: %v", got)
	}
	if got := actions(adminA, "category=channel"); !slices.Equal(got, []string{"admin-a:channel.create", "alice:channel.create", "alice:channel.status"}) {
		t.Errorf("admin a, channel: %v", got)
	}
	if got := actions(adminA, "owner=self&category=channel"); !slices.Equal(got, []string{"admin-a:channel.create"}) {
		t.Errorf("admin a, only mine: %v", got)
	}
	if got := actions(super, "owner=self&category=channel"); got != nil {
		t.Errorf("super, only mine: %v", got)
	}
	if got := actions(super, "category=log"); !slices.Equal(got, []string{"admin-a:log.export"}) {
		t.Errorf("super, logs: %v", got)
	}
	alice.want(400, "GET", "/api/audit-logs?category=x", nil)
	alice.want(400, "GET", "/api/audit-logs?success=maybe", nil)
	adminA.want(400, "GET", "/api/audit-logs?owner=x", nil)
}

// auditRow is what the admin list returns of one row.
type auditRow struct {
	Action  string `json:"action"`
	Name    string `json:"actor_name"`
	Success bool   `json:"success"`
	IP      string `json:"ip"`
	Detail  struct {
		Route  string            `json:"http_route"`
		Status int               `json:"http_status"`
		Error  string            `json:"error"`
		Query  map[string]string `json:"query"`
		User   string            `json:"username"`
	} `json:"detail"`
}

func auditRows(t *testing.T, c caller, query string) []auditRow {
	t.Helper()
	return decode[struct{ Items []auditRow }](t, c.want(200, "GET", "/api/audit-logs?page_size=200&"+query, nil)).Items
}

func TestAuditFindsWhatAnAddressDid(t *testing.T) {
	e := newEnv(t)
	alice, adminA, adminB := e.asUser(e.alice), e.asAdmin(e.a), e.asAdmin(e.b)
	alice.from("198.51.100.7").create(ch{})
	alice.from("198.51.100.70").want(200, "GET", "/api/channels", nil)
	adminA.from("2001:db8::1").want(200, "GET", "/api/users", nil)

	ips := func(c caller, filter string) []string {
		t.Helper()
		var out []string
		for _, r := range auditRows(t, c, "ip="+filter) {
			out = append(out, r.IP)
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	if got := ips(adminA, "198.51.100.7"); !slices.Equal(got, []string{"198.51.100.7"}) {
		t.Errorf("exact: %v", got)
	}
	if got := ips(adminA, "198.51.100."); !slices.Equal(got, []string{"198.51.100.7", "198.51.100.70"}) {
		t.Errorf("prefix: %v", got)
	}
	if got := ips(adminA, "2001:DB8:"); !slices.Equal(got, []string{"2001:db8::1"}) {
		t.Errorf("ipv6 prefix in another case: %v", got)
	}
	if got := ips(adminA, "2001:DB8::1"); !slices.Equal(got, []string{"2001:db8::1"}) {
		t.Errorf("ipv6 in another case: %v", got)
	}
	if got := ips(adminB, "198.51.100."); got != nil { // alice is admin a's
		t.Errorf("out of scope: %v", got)
	}
	adminA.want(400, "GET", "/api/audit-logs?ip=198%25", nil)

	// The portal has no such filter: it answers with the user's own rows.
	all := decode[struct{ Total int }](t, alice.want(200, "GET", "/api/audit-logs", nil)).Total
	if got := decode[struct{ Total int }](t, alice.want(200, "GET", "/api/audit-logs?ip=203.0.113.200", nil)).Total; got != all || all == 0 {
		t.Errorf("portal ip filter: %d of %d", got, all)
	}
}

func TestReadsAreAuditedForAdminsOnly(t *testing.T) {
	e := newEnv(t)
	alice, adminA := e.asUser(e.alice), e.asAdmin(e.a)
	alice.want(200, "GET", "/api/me", nil)
	alice.want(200, "GET", "/api/logs?"+logRange+"&model_name=gpt&key=sk-stray-secret", nil)

	rows := auditRows(t, adminA, "category=view&actor_type=portal")
	if len(rows) != 1 || rows[0].Action != "view.logs" || !rows[0].Success || rows[0].Detail.Route != "/api/logs" || rows[0].Detail.Status != 200 {
		t.Fatalf("reads of the portal user: %+v", rows)
	}
	if q := rows[0].Detail.Query; q["model_name"] != "gpt" || q["start"] != "1" {
		t.Errorf("query: %v", q)
	}
	var stray int
	if err := e.raw.QueryRow(`SELECT count(*) FROM audit_logs WHERE detail::text LIKE '%sk-stray-secret%'`).Scan(&stray); err != nil || stray != 0 {
		t.Errorf("rows with the unknown parameter: %d, %v", stray, err)
	}

	for _, q := range []string{"", "?category=view", "?action=view.logs"} {
		if body := alice.want(200, "GET", "/api/audit-logs"+q, nil); strings.Contains(body, `"view.`) {
			t.Errorf("portal list %q shows reads: %s", q, body)
		}
	}
}

func TestDeniedAndFailedAttemptsAreAudited(t *testing.T) {
	e := newEnv(t)
	adminA, super := e.asAdmin(e.a), e.asAdmin(e.super)
	regular := adminA.from("198.51.100.9")
	regular.want(403, "GET", "/api/admins", nil)
	regular.want(403, "POST", "/api/templates", ch{"name": "x"})
	regular.want(403, "GET", "/api/stats/users?"+logRange, nil)
	e.asUser(e.alice).from("198.51.100.9").want(403, "GET", "/api/stats/users?"+logRange, nil)

	got := map[string]auditRow{}
	for _, r := range auditRows(t, super, "ip=198.51.100.9&success=false") {
		got[r.Name+" "+r.Action] = r
	}
	for key, route := range map[string]string{
		"admin-a view.admins":      "/api/admins",
		"admin-a template.create":  "/api/templates",
		"admin-a view.stats_users": "/api/stats/users",
		"alice view.stats_users":   "/api/stats/users",
	} {
		r, ok := got[key]
		if !ok || r.Detail.Route != route || r.Detail.Status != 403 || r.Detail.Error == "" {
			t.Errorf("%s: %+v of %v", key, r, got)
		}
	}

	// Nobody is logged in, so there is nothing to attribute.
	before := len(auditRows(t, super, "category=view&success=false"))
	caller{e: e, mux: e.adminMux}.want(401, "GET", "/api/admins", nil)
	if after := len(auditRows(t, super, "category=view&success=false")); after != before {
		t.Errorf("401 recorded: %d -> %d", before, after)
	}

	caller{e: e, mux: e.adminMux}.from("198.51.100.10").want(401, "POST", "/api/auth/login", ch{"username": "root", "password": "wrong-password"})
	login := auditRows(t, super, "ip=198.51.100.10")
	if len(login) != 1 || login[0].Action != "auth.login" || login[0].Success || login[0].Name != "root" {
		t.Errorf("failed login: %+v", login)
	}
	if body := super.want(200, "GET", "/api/audit-logs?ip=198.51.100.10", nil); strings.Contains(body, "wrong-password") {
		t.Errorf("password in the log: %s", body)
	}
}

// The name of the target must be readable in the row even after it is gone.
func TestAuditRowsNameTheirTarget(t *testing.T) {
	e := newEnv(t)
	adminA, super := e.asAdmin(e.a), e.asAdmin(e.super)
	adminA.want(204, "POST", "/api/users/"+strconv.FormatInt(e.alice, 10)+"/password", ch{"password": "New-Password-123"})
	super.want(204, "POST", "/api/admins/"+strconv.FormatInt(e.b, 10)+"/password", ch{"password": "New-Password-123"})
	for action, name := range map[string]string{"user.password_reset": "alice", "admin.password_reset": "admin-b"} {
		rows := auditRows(t, super, "action="+action)
		if len(rows) != 1 || rows[0].Detail.User != name {
			t.Errorf("%s: %+v", action, rows)
		}
	}
	var leaked int
	if err := e.raw.QueryRow(`SELECT count(*) FROM audit_logs WHERE detail::text LIKE '%New-Password%'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Errorf("rows with the password: %d, %v", leaked, err)
	}
}
