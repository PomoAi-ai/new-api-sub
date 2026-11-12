package newapi

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL+"/", "nap_test")
}

func TestCreateChannelSendsPATAndReturnsID(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer nap_test" {
			t.Errorf("Authorization = %q", got)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/channel/" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body CreateRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Mode != "single" || body.Channel.Key != "sk-1" {
			t.Errorf("body = %+v", body)
		}
		w.Write([]byte(`{"success":true,"message":"","data":{"id":17,"ids":[17]}}`))
	})
	ids, err := c.CreateChannel(context.Background(), CreateRequest{Mode: "single", Channel: ChannelInput{Type: 1, Key: "sk-1"}})
	if err != nil || !slices.Equal(ids, []int{17}) {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
}

func TestCreateChannelBatchReturnsEveryIDAndUsesNewAPIFieldNames(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["mode"] != "batch" {
			t.Errorf("body = %v", body)
		}
		ch := body["channel"].(map[string]any)
		if ch["key"] != "k1\nk2" || ch["test_model"] != "m" {
			t.Errorf("channel = %v", ch)
		}
		w.Write([]byte(`{"success":true,"data":{"ids":[3,4]}}`))
	})
	ids, err := c.CreateChannel(context.Background(), CreateRequest{Mode: "batch",
		Channel: ChannelInput{Key: "k1\nk2", TestModel: "m"}})
	if err != nil || !slices.Equal(ids, []int{3, 4}) {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
}

func TestCreateChannelOmitsMultiKeyModeUnlessSetAndTestModelWhenEmpty(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["multi_key_mode"]; ok {
			t.Errorf("multi_key_mode sent: %v", body)
		}
		if _, ok := body["channel"].(map[string]any)["test_model"]; ok {
			t.Errorf("empty test_model sent: %v", body)
		}
		w.Write([]byte(`{"success":true,"message":""}`))
	})
	ids, err := c.CreateChannel(context.Background(), CreateRequest{Mode: "single", Channel: ChannelInput{Key: "k"}})
	if err != nil || len(ids) != 0 { // released new-api says nothing about the ids
		t.Fatalf("ids=%v err=%v, want none and no error", ids, err)
	}
}

func TestDataFailsRatherThanAnsweringEmpty(t *testing.T) {
	for _, tc := range []struct {
		status             int
		body, want, errHas string
	}{
		{200, `{"success":true,"data":["a"]}`, `["a"]`, ""},
		{200, `{"success":true,"data":[]}`, `[]`, ""},
		{200, `{"success":true}`, "", "no data"},
		{200, `{"success":false,"message":"refused"}`, "", "refused"},
		{403, `{"success":false,"message":"lacks group:read"}`, "", "lacks group:read"},
	} {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			w.Write([]byte(tc.body))
		})
		data, err := c.Data(context.Background(), http.MethodGet, "/api/group/", nil)
		if string(data) != tc.want || (tc.errHas == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.errHas)) {
			t.Errorf("%s: data=%s err=%v", tc.body, data, err)
		}
	}
}

func TestBusinessFailureIsErrorWithoutLeakingKey(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":false,"message":"permission denied"}`))
	})
	_, err := c.CreateChannel(context.Background(), CreateRequest{Channel: ChannelInput{Key: "sk-secret"}})
	var rej *Rejected
	if !errors.As(err, &rej) || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v, want *Rejected", err)
	}
	if strings.Contains(err.Error(), "sk-secret") {
		t.Fatal("error leaks the channel key")
	}
}

func TestHTTPErrorStatusIsError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"success":false,"message":"invalid token"}`))
	})
	if err := c.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}

func TestTestChannelFailureIsResultNotError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/channel/test/5" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if q := r.URL.Query(); q.Get("model") != "a b" || q.Get("endpoint_type") != "openai" || q.Get("stream") != "true" {
			t.Errorf("query = %v", q)
		}
		w.Write([]byte(`{"success":false,"message":"upstream 401","time":1.5}`))
	})
	ok, msg, secs, err := c.TestChannel(context.Background(), 5, TestOptions{Model: "a b", EndpointType: "openai", Stream: true})
	if err != nil || ok || msg != "upstream 401" || secs != 1.5 {
		t.Fatalf("ok=%v msg=%q secs=%v err=%v", ok, msg, secs, err)
	}
}

func TestUpdateBalanceReadsTopLevelFields(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/channel/update_balance/5" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"success":true,"message":"","balance":12.5}`))
	})
	b, err := c.UpdateBalance(context.Background(), 5)
	if err != nil || b.Balance == nil || *b.Balance != 12.5 || b.RawResponse != nil {
		t.Fatalf("balance=%+v err=%v", b, err)
	}
}

func TestUpdateBalanceRefusalIsRejected(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":false,"message":"Multi-key channels do not support balance queries"}`))
	})
	_, err := c.UpdateBalance(context.Background(), 5)
	var rej *Rejected
	if !errors.As(err, &rej) || rej.Message != "Multi-key channels do not support balance queries" {
		t.Fatalf("err = %v, want *Rejected", err)
	}
}

// Whatever new-api says goes on to the caller and into the audit log, so every
// way it can say something hides the keys it quotes.
func TestNewAPITextHidesQuotedKeys(t *testing.T) {
	const key = "sk-0123456789abcdef" // gitleaks:allow
	status, reply := http.StatusOK, ""
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(reply))
	})
	ctx := context.Background()

	reply = `{"success":false,"message":"upstream said ` + key + `","time":1}`
	if _, msg, _, err := c.TestChannel(ctx, 5, TestOptions{}); err != nil || msg != "upstream said ••••" {
		t.Errorf("test result: %q, %v", msg, err)
	}
	var rej *Rejected
	if _, err := c.SetStatus(ctx, 5, 1); !errors.As(err, &rej) || rej.Message != "upstream said ••••" {
		t.Errorf("refusal: %v", err)
	}

	status, reply = http.StatusForbidden, `{"success":false,"message":"upstream said `+key+`"}`
	if err := c.Ping(ctx); err == nil || strings.Contains(err.Error(), key) {
		t.Errorf("error status: %v", err)
	}
	status, reply = http.StatusBadGateway, "<html>upstream said "+key+"</html>"
	if err := c.Ping(ctx); err == nil || strings.Contains(err.Error(), key) {
		t.Errorf("error page: %v", err)
	}

	status, reply = http.StatusOK, `{"success":true,"message":"","raw_response":"{\n  \"label\": \"`+key+`\",\n  \"api_key\": \"s3cret\"\n}"}`
	b, err := c.UpdateBalance(ctx, 5)
	if err != nil || b.RawResponse == nil || *b.RawResponse != "{\n  \"label\": \"••••\",\n  \"api_key\": \"••••\"\n}" {
		t.Errorf("raw response: %v, %v", b.RawResponse, err)
	}
}

// new-api's PUT writes every non-zero field it receives, the key and base URL
// too, so nothing but these keys may ever be in the body.
func TestUpdateChannelBodyHoldsOnlyTheAllowedKeys(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/channel/" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		body = nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"success":true,"message":""}`))
	})
	if err := c.UpdateChannel(context.Background(), 7, "n", "a,b", ""); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(body, map[string]any{"id": 7.0, "name": "n", "models": "a,b"}) {
		t.Errorf("without a test model: body = %v", body)
	}
	if err := c.UpdateChannel(context.Background(), 7, "n", "a,b", "a"); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(body, map[string]any{"id": 7.0, "name": "n", "models": "a,b", "test_model": "a"}) {
		t.Errorf("with a test model: body = %v", body)
	}
}

func TestMultiKeyCallsUseOnlyTheAllowedActionsAndDropTheKeyPreview(t *testing.T) {
	var actions []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/channel/multi_key/manage" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		actions = append(actions, req["action"].(string))
		if req["action"] != "get_key_status" {
			if req["channel_id"] != float64(7) || req["key_index"] != float64(2) {
				t.Errorf("request = %v", req)
			}
			w.Write([]byte(`{"success":true,"message":""}`))
			return
		}
		// Two keys a page whatever page_size says, to make the client page through.
		page := int(req["page"].(float64))
		keys := []string{
			`{"index":0,"status":1,"key_preview":"sk-or-v1-a..."}`,
			`{"index":1,"status":3,"disabled_time":5,"reason":"401 Incorrect API key provided: sk-or-v1-abcdef0123456789","key_preview":"sk-or-v1-b..."}`, // gitleaks:allow (fake)
			`{"index":2,"status":2,"key_preview":"sk-or-v1-c..."}`,
		}[min(2*(page-1), 3):min(2*page, 3)]
		w.Write([]byte(`{"success":true,"data":{"keys":[` + strings.Join(keys, ",") + `],"total":3,"page":` + strconv.Itoa(page) + `,"total_pages":2}}`))
	})
	ctx := context.Background()
	got, err := c.KeyStatuses(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[1].Status != 3 || got[1].DisabledTime != 5 || got[2].Status != 2 {
		t.Errorf("statuses = %+v", got)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "sk-or") || strings.Contains(string(b), "preview") {
		t.Errorf("a key or its preview got through: %s", b)
	}
	if err := c.SetKeyEnabled(ctx, 7, 2, true); err != nil {
		t.Fatal(err)
	}
	if err := c.SetKeyEnabled(ctx, 7, 2, false); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"delete_key", "delete_disabled_keys", "enable_all_keys", "disable_all_keys", ""} {
		if _, err := c.manageKeys(ctx, keyRequest{ChannelID: 7, Action: action}); err == nil {
			t.Errorf("%q was sent", action)
		}
	}
	if want := []string{"get_key_status", "get_key_status", "enable_key", "disable_key"}; !slices.Equal(actions, want) {
		t.Errorf("new-api got %v, want %v", actions, want)
	}
}

func exportClient(t *testing.T, status int, body string) *Client {
	return newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/log/export" || r.URL.Query().Get("start_timestamp") != "100" || r.URL.Query().Get("end_timestamp") != "199" {
			t.Errorf("unexpected %s", r.URL)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	})
}

func TestExportLogsReadsTheChannelAndChecksTheTrailer(t *testing.T) {
	const rows = `{"id":9,"created_at":150,"type":2,"channel":37,"quota":5,"request_id":"r9"}` + "\n" +
		`{"id":8,"created_at":120,"type":6,"channel":4,"quota":1}` + "\n"
	var got []Log
	collect := func(l Log) error { got = append(got, l); return nil }
	n, served, err := exportClient(t, 200, rows+`{"success":true,"message":"","data":{"count":2}}`+"\n").ExportLogs(context.Background(), 100, 199, collect)
	if served.IsZero() {
		t.Error("no server time from the Date header")
	}
	if err != nil || n != 2 || len(got) != 2 || got[0].ChannelID != 37 || got[0].ID != 9 || got[0].RequestID != "r9" || got[1].ChannelID != 4 {
		t.Fatalf("n=%d err=%v got=%+v", n, err, got)
	}
	for name, body := range map[string]string{
		"no trailer":     rows,
		"cut mid-line":   rows + `{"id":7,"crea`,
		"count mismatch": rows + `{"success":true,"data":{"count":3}}`,
		"no count":       rows + `{"success":true,"data":{}}`,
		"after trailer":  rows + `{"success":true,"data":{"count":2}}` + "\n" + `{"id":7}`,
	} {
		if _, _, err := exportClient(t, 200, body).ExportLogs(context.Background(), 100, 199, func(Log) error { return nil }); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestExportLogsRefusalIsRejectedAndRateLimitIsNot(t *testing.T) {
	var rejected *Rejected
	_, _, err := exportClient(t, 200, `{"success":false,"message":"Another log export is running. Try again after it finishes."}`).
		ExportLogs(context.Background(), 100, 199, func(Log) error { t.Error("row from an envelope"); return nil })
	if !errors.As(err, &rejected) || !strings.Contains(rejected.Message, "Another log export") {
		t.Errorf("success=false: %v", err)
	}
	_, _, err = exportClient(t, 403, `{"success":false,"message":"no scope"}`).ExportLogs(context.Background(), 100, 199, nil)
	if !errors.As(err, &rejected) || rejected.Status != 403 {
		t.Errorf("403: %v", err)
	}
	_, _, err = exportClient(t, 429, `{"success":false,"message":"too many"}`).ExportLogs(context.Background(), 100, 199, nil)
	if !errors.Is(err, ErrRateLimited) || errors.As(err, &rejected) {
		t.Errorf("429: %v", err)
	}
}

func TestRecentChannelsReadsRemarkAndHidesPerKeyErrors(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); r.URL.Path != "/api/channel/" || q.Get("p") != "2" || q.Get("page_size") != "100" || q.Get("id_sort") != "true" {
			t.Errorf("unexpected %s", r.URL)
		}
		w.Write([]byte(`{"success":true,"data":{"total":101,"items":[{"id":5,"name":"n","status":1,"used_quota":42,"remark":"rm",
			"channel_info":{"is_multi_key":true,"multi_key_disabled_reason":{"0":"sk-abcdefghijklmnop"}}},{"id":4,"remark":null}]}}`))
	})
	chs, total, err := c.RecentChannels(context.Background(), 2)
	if err != nil || total != 101 || len(chs) != 2 || chs[0].Remark != "rm" || chs[0].UsedQuota != 42 || chs[1].Remark != "" {
		t.Fatalf("chs=%+v total=%d err=%v", chs, total, err)
	}
	if strings.Contains(string(chs[0].ChannelInfo), "sk-") || string(chs[1].ChannelInfo) != "{}" {
		t.Errorf("channel_info = %s, %s", chs[0].ChannelInfo, chs[1].ChannelInfo)
	}
}

func TestChannelTellsAMissingChannelFromAFailure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel/5":
			w.Write([]byte(`{"success":true,"data":{"id":5,"status":2,"used_quota":42,"channel_info":{"is_multi_key":true,"multi_key_disabled_reason":{"0":"sk-abcdefghijklmnop"}}}}`))
		case "/api/channel/6":
			w.Write([]byte(`{"success":false,"message":"record not found"}`))
		default:
			w.Write([]byte(`{"success":false,"message":"无权进行此操作"}`))
		}
	})
	ch, err := c.Channel(context.Background(), 5)
	if err != nil || ch.ID != 5 || ch.Status != 2 || ch.UsedQuota != 42 || strings.Contains(string(ch.ChannelInfo), "sk-") {
		t.Errorf("ch=%+v err=%v", ch, err)
	}
	if _, err := c.Channel(context.Background(), 6); !errors.Is(err, ErrNoChannel) {
		t.Errorf("missing: %v", err)
	}
	if _, err := c.Channel(context.Background(), 7); err == nil || errors.Is(err, ErrNoChannel) {
		t.Errorf("refused: %v", err)
	}
}
