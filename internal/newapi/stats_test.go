package newapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPeakConcurrency(t *testing.T) {
	const from, to = 1000, 1060
	for _, c := range []struct {
		name  string
		spans [][2]int64
		want  int64
	}{
		{"nothing", nil, 0},
		{"under a second still counts", [][2]int64{{1030, 0}}, 1},
		{"same second", [][2]int64{{1030, 0}, {1030, 1}, {1030, 5}}, 3},
		{"one ends as the next starts", [][2]int64{{1030, 10}, {1040, 10}}, 1},
		{"overlapping", [][2]int64{{1030, 10}, {1035, 10}, {1039, 3}}, 2},
		{"a long one began before the window", [][2]int64{{1005, 300}, {1003, 1}}, 2},
		{"a clock ahead of ours", [][2]int64{{1059, 5}, {1100, 50}}, 2},
		{"entirely outside", [][2]int64{{999, 5}, {1200, 5}}, 0},
	} {
		if got := peakConcurrency(c.spans, from, to); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

// streamOther is other as new-api writes it: err.Error() through json.Marshal,
// which escapes & < > and newlines, beside an integer no float64 holds.
func streamOther(t *testing.T, text string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"stream_status": map[string]any{"end_error": text, "errors": []string{text}}})
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(b), "}") + `,"big":98765432109876543210}`
}

func TestPrepareHidesQuotedKeys(t *testing.T) {
	for _, c := range []struct{ text, secret string }{
		{"Incorrect API key provided: sk-proj-********abcd. You can find your API key", "abcd"},
		{"Incorrect API key provided: 0123456789abcdef. You can find your API key", "0123456789"}, // gitleaks:allow
		{"bad key sk-or-v1-0123456789abcdef", "0123456789"},
		{"masked sk-abc...wxyz here", "wxyz"},
		{"status_code=401, gsk_0123456789 rejected", "0123456789"},
		{"pat nap_0123456789 rejected", "0123456789"},
		{"key AIzaSyA0123456789abcdef is invalid", "0123456789"},
		{"Authorization: Bearer abc.def-123", "abc.def"},
		{"Authorization: Bearer abc.def", "abc.def"},
		{"key xai-0123456789abcdefghij0123 rejected", "0123456789"},
		{"key pplx-0123456789abcdefghij0123 rejected", "0123456789"},
		{"key hf_0123456789abcdefghij0123 rejected", "0123456789"},
		{"key r8_0123456789abcdefghij0123 rejected", "0123456789"},
		{"key fw_0123456789abcdefghij0123 rejected", "0123456789"},
		{"key jina_0123456789abcdefghij0123_tail-end rejected", "tail-end"},
		{"token eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1In0.c2lnbmF0dXJl expired", "c2lnbmF0dXJl"},
		{"x-api-key: s3cret rejected", "s3cret"},
		{"X-Goog-Api-Key: s3cret rejected", "s3cret"},
		{"api_key=s3cret rejected", "s3cret"},
		{`Post "https://up.example.com/v1?a=1&api_key=s3cret&b=2": EOF`, "s3cret"},
		{`Post "https://up.example.com/v1?key=s3cret": EOF`, "s3cret"},
		{`upstream body: {"x-api-key":"s3cret"}`, "s3cret"},
		{`refresh_token: "s3cret"`, "s3cret"},
		{"first line\nsk-0123456789", "0123456789"},
		{"<sk-0123456789>", "0123456789"},
		{"a&sk-0123456789", "0123456789"},
		{`upstream body: {"error":"first line\nsk-0123456789"}`, "0123456789"},
	} {
		for _, full := range []bool{true, false} {
			l := Log{Content: c.text, Other: streamOther(t, c.text)}
			l.prepare(full)
			if strings.Contains(l.Content, c.secret) {
				t.Errorf("content of %q still shows %q: %q", c.text, c.secret, l.Content)
			}
			if strings.Contains(l.Other, c.secret) {
				t.Errorf("other of %q still shows %q: %s", c.text, c.secret, l.Other)
			}
			if !json.Valid([]byte(l.Other)) || !strings.Contains(l.Other, `"big":98765432109876543210`) {
				t.Errorf("other of %q is damaged: %s", c.text, l.Other)
			}
		}
	}

	// Ordinary errors stay as they were, in other down to the byte. The last
	// ones hold what a mask over the raw JSON text takes for a key: \n and \r
	// followed by the rest of nap_ and rk-.
	for _, plain := range []string{
		"status_code=403, skipped_requests: This model is not available in your region.",
		"Invalid bearer token",
		"Invalid bearer token.",
		"Invalid bearer token, try again",
		"无效的 Bearer 令牌",
		"Use Bearer <token> or Bearer {token}",
		"You need to provide your API key in an Authorization header using Bearer auth.",
		"You need Bearer auth)",
		"x-api-key header is required",
		"Send x-goog-api-key: <key> or ?key=<key>",
		"Invalid API key. Check your key at https://up.example.com/keys",
		"model xai-grok-beta not found",
		"model pplx-70b-online not found",
		"hf_hub_download failed",
		"risk-management and task_abcdefghij and desk-abcdefghij",
		`{"expr_b64":"eyJhIjoxfQ=="}`,
		"first line\nap_abcdefghij",
		"first line\nap-northeast-1 is down",
		"first line\rk-abcdefghij",
	} {
		for _, full := range []bool{true, false} {
			other := streamOther(t, plain)
			l := Log{Content: plain, Other: other}
			l.prepare(full)
			if l.Content != plain {
				t.Errorf("content of %q became %q", plain, l.Content)
			}
			if l.Other != other {
				t.Errorf("other of %q became %s", plain, l.Other)
			}
		}
	}

	// A word after Bearer stays, while the placeholder next to it counts as a token.
	l := Log{Content: "using Bearer auth (i.e. Authorization: Bearer YOUR_KEY), or"}
	l.prepare(true)
	if !strings.Contains(l.Content, "using Bearer auth (i.e.") || !strings.Contains(l.Content, " or") {
		t.Errorf("got %q", l.Content)
	}

	// A super reads other that is not JSON as it is, keys aside.
	l = Log{Other: "not json sk-0123456789"}
	l.prepare(true)
	if l.Other != "not json ••••" {
		t.Errorf("got %q", l.Other)
	}
}

func TestPeerAddressesAreForASuperOnly(t *testing.T) {
	other := streamOther(t, "write tcp 172.18.0.3:3000->203.0.113.7:51324: write: broken pipe; [2001:db8::1]:3000 and a[1:2]")
	l := Log{Other: other}
	l.prepare(false)
	for _, addr := range []string{"172.18.0.3", "203.0.113.7", "2001:db8"} {
		if strings.Contains(l.Other, addr) {
			t.Errorf("still shows %q: %s", addr, l.Other)
		}
	}
	if !strings.Contains(l.Other, "write: broken pipe") || !strings.Contains(l.Other, "a[1:2]") || !json.Valid([]byte(l.Other)) ||
		!strings.Contains(l.Other, `"big":98765432109876543210`) {
		t.Errorf("got %s", l.Other)
	}
	l = Log{Other: other}
	l.prepare(true)
	if l.Other != other {
		t.Errorf("a super's other changed: %s", l.Other)
	}
}

func TestAllChannelsQuery(t *testing.T) {
	f := Filter{Start: 1, End: 2, Page: 1, PageSize: 10}
	if !f.none() {
		t.Error("a scope without channels must match nothing")
	}
	f.AllChannels = true
	if f.none() {
		t.Error("all channels must match")
	}
	q, _ := logsSQL(dialect{pg: true}, f)
	if strings.Contains(q, "channel_id IN") {
		t.Errorf("no channel limit expected: %s", q)
	}
	f.ChannelIDs = []int{7}
	q, args := logsSQL(dialect{pg: true}, f)
	if !strings.Contains(q, "channel_id IN") || args[0] != 7 {
		t.Errorf("channel_id narrows the open view: %s %v", q, args)
	}
}
