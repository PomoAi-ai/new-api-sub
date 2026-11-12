package keyprobe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The cases follow new-api's Channel.GetKeys line by line: index i must name
// the same key in both systems.
func TestSplitKeysNumbersKeysLikeNewAPI(t *testing.T) {
	for raw, want := range map[string][]string{
		"":                   {},
		"a":                  {"a"},
		"a\nb":               {"a", "b"},
		"\n\na\nb\n\n":       {"a", "b"},     // \n trimmed at both ends only
		"a\n\nb":             {"a", "", "b"}, // an empty line inside keeps its index
		" a \r\nb\t":         {" a \r", "b\t"},
		"  \n":               {"  "},
		` ["k1", {"x": 1}] `: {`"k1"`, `{"x": 1}`}, // raw JSON elements
		"[not json\nb":       {"[not json", "b"},
	} {
		if got := SplitKeys(raw); !slices.Equal(got, want) {
			t.Errorf("SplitKeys(%q) = %q, want %q", raw, got, want)
		}
	}
}

const key = "sk-or-v1-0123456789abcdef0123456789abcdef" // gitleaks:allow (fake)

func fakeOpenRouter(t *testing.T, h http.HandlerFunc) *Prober {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	old := OpenRouterURL
	OpenRouterURL = srv.URL
	t.Cleanup(func() { OpenRouterURL = old })
	return &Prober{http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func TestProbeReadsOpenRouterAndNeverRepeatsTheKey(t *testing.T) {
	p := fakeOpenRouter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			// Upstreams echo what they were sent, in full or in part.
			w.Write([]byte(`{"error":{"message":"No auth credentials found for ` + r.Header.Get("Authorization") + ` (sk-or-v1-0123456789…)"}}` + strings.Repeat("x", 500)))
			return
		}
		switch r.URL.Path {
		case "/api/v1/chat/completions":
			w.Write([]byte(`{"id":"` + key + `","choices":[{"message":{"content":"` + key + `"}}]}`))
		case "/api/v1/credits":
			w.Write([]byte(`{"data":{"total_credits":10,"total_usage":2.5}}`))
		}
	})
	ctx := context.Background()
	payload := []byte(`{"model":"m"}`)
	if res := p.test(ctx, " "+key+"\r", payload); !res.OK || strings.Contains(res.Message, key) {
		t.Errorf("test = %+v", res)
	}
	if b := p.balance(ctx, key); b.Balance == nil || *b.Balance != 7.5 {
		t.Errorf("balance = %+v", b)
	}
	bad := "sk-or-v1-ffffffffffffffffffffffffffffffff"
	res, b := p.test(ctx, bad, payload), p.balance(ctx, bad)
	for _, msg := range []string{res.Message, b.Error} {
		if !strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "sk-or-v1-") || strings.Contains(msg, "ffffffff") || len([]rune(msg)) > 250 {
			t.Errorf("message = %q", msg)
		}
	}
	if res.OK || b.Balance != nil {
		t.Errorf("a refused key passed: %+v %+v", res, b)
	}
	if res := p.test(ctx, "  ", payload); res.OK || res.Message != "该 Key 为空" {
		t.Errorf("empty key = %+v", res)
	}
}

// OpenRouter reports some failures, such as an exhausted key, with HTTP 200.
func TestTestPassesOnlyOnAnAnswerWithChoices(t *testing.T) {
	for answer, ok := range map[string]bool{
		`{"choices":[{"message":{"content":"x"}}]}`:                     true,
		`{"choices":[{}],"error":null}`:                                 true,
		`{"error":{"code":402,"message":"no credits for ` + key + `"}}`: false,
		`{"choices":[{}],"error":{"message":"late"}}`:                   false,
		`{"error":"plain text"}`:                                        false,
		`{"choices":[{"error":{"code":502,"message":"upstream"}}]}`:     false,
		`{"choices":[{"finish_reason":"error"}]}`:                       false,
		`{"choices":[{"finish_reason":"stop"}]}`:                        true,
		`{"choices":[]}`:                                                false,
		`{}`:                                                            false,
		`not json ` + key:                                               false,
	} {
		p := fakeOpenRouter(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(answer)) })
		res := p.test(context.Background(), key, []byte(`{}`))
		if res.OK != ok || strings.Contains(res.Message, key) {
			t.Errorf("%s: %+v", answer, res)
		}
	}
}

func TestChatRequestSendsWhatTheChannelIsConfiguredToSend(t *testing.T) {
	const routing = `"provider":{"only":["openai"],"allow_fallbacks":false}`
	base := func(extra string) string {
		return `{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":16,"stream":false` + extra + `}`
	}
	for name, c := range map[string]struct{ mapping, override, want string }{
		"nothing configured":              {"", "", base("")},
		"empty override":                  {"{}", " {} ", base("")},
		"null override":                   {"", "null", base("")},
		"no operations":                   {"", `{"operations":[]}`, base("")},
		"flat":                            {"", `{` + routing + `,"temperature":0.10}`, base(`,` + routing + `,"temperature":0.10`)},
		"flat keeps a dotted key literal": {"", `{"a.b":1}`, base(`,"a.b":1`)},
		"operations": {"", `{"operations":[{"mode":"set","path":"provider.only","value":["openai"]},
			{"mode":"set","path":"provider.allow_fallbacks","value":false}]}`, base(`,` + routing)},
		"set without a value is null":    {"", `{"operations":[{"mode":"set","path":"seed"}]}`, base(`,"seed":null`)},
		"a set's description is ignored": {"", `{"operations":[{"mode":"set","path":"seed","value":1,"description":"note"}]}`, base(`,"seed":1`)},
		"mapping":                        {`{"m":"openai/m"}`, "", `{"model":"openai/m","messages":[{"role":"user","content":"hi"}],"max_tokens":16,"stream":false}`},
		"mapping chain":                  {`{"m":"x","x":"y","y":"y"}`, "", `{"model":"y","messages":[{"role":"user","content":"hi"}],"max_tokens":16,"stream":false}`},
		"mapping to itself":              {`{"m":"m"}`, "", base("")},
		"override beats mapping":         {`{"m":"x"}`, `{"model":"forced"}`, `{"model":"forced","messages":[{"role":"user","content":"hi"}],"max_tokens":16,"stream":false}`},
	} {
		got, err := chatRequest("m", c.mapping, c.override)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var a, b any
		dec := json.NewDecoder(strings.NewReader(string(got)))
		dec.UseNumber()
		dec.Decode(&a)
		dec = json.NewDecoder(strings.NewReader(c.want))
		dec.UseNumber()
		dec.Decode(&b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: got %s, want %s", name, got, c.want)
		}
	}
}

// Everything here makes new-api send something other than what a plain set
// would, so a test would go out without the channel's routing.
func TestChatRequestRefusesWhatItCannotReproduce(t *testing.T) {
	const secret = "secret-in-the-override"
	for _, override := range []string{
		`{"operations":[{"mode":"delete","path":"provider"}]}`,
		`{"operations":[{"mode":"SET","path":"a","value":1}]}`,
		`{"operations":[{"mode":"set","path":"a","value":1},{"mode":"append","path":"b","value":"` + secret + `"}]}`,
		`{"operations":[{"mode":"set","path":"a","value":1,"conditions":[{"path":"model","mode":"full","value":"m"}]}]}`,
		`{"operations":[{"mode":"set","path":"a","value":1,"keep_origin":true}]}`,
		`{"operations":[{"mode":"set","path":"a","value":1,"logic":"AND"}]}`,
		`{"operations":[{"Mode":"set","mode":"delete","path":"a"}]}`,
		`{"operations":[{"Mode":"set","path":"a"}]}`,
		`{"operations":[{"mode":"set","value":1}]}`,
		`{"operations":[{"mode":"set","path":"","value":1}]}`,
		`{"operations":[{"mode":"set","path":"a..b","value":1}]}`,
		`{"operations":[{"mode":"set","path":"messages.0.content","value":"x"}]}`,
		`{"operations":[{"mode":"set","path":"messages.-1.content","value":"x"}]}`,
		`{"operations":[{"mode":"set","path":"*.x","value":1}]}`,
		`{"operations":[{"mode":"set","path":"a\\.b","value":1}]}`,
		`{"operations":[{"mode":"set","path":"model.x","value":1}]}`,
		`{"operations":["set"]}`,
		`{"operations":{"mode":"set"}}`,
		`{"operations":"` + secret + `"}`,
		`{"operations":null}`,
		`{"operations":[],"temperature":1}`,
		`["a"]`, `"a"`, `1`, `true`, `{`, `{} {}`, `{}}`,
	} {
		_, err := chatRequest("m", "", override)
		if !errors.Is(err, ErrOverride) {
			t.Errorf("%s: err = %v", override, err)
		} else if strings.Contains(err.Error(), secret) {
			t.Errorf("%s: quoted the override: %v", override, err)
		}
	}
	for _, mapping := range []string{`{"m":"x","x":"m"}`, `{"m":1}`, `[]`, `{`} {
		if _, err := chatRequest("m", mapping, ""); !errors.Is(err, ErrOverride) {
			t.Errorf("%s: err = %v", mapping, err)
		}
	}
}

func TestEachLeavesOutWhatHasNotStartedWhenTimeIsUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	got := make(chan []int)
	go func() {
		got <- each(ctx, make([]int, 3*concurrency), func(int) int {
			<-release
			return 1
		})
	}()
	cancel()
	close(release)
	if n := len(<-got); n > concurrency {
		t.Errorf("%d calls ran after the deadline", n-concurrency)
	}
}

func TestPayloadReachesOpenRouter(t *testing.T) {
	var got string
	p := fakeOpenRouter(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = r.Method + " " + r.URL.Path + " " + r.Header.Get("Content-Type") + " " + string(b)
		w.Write([]byte(`{"choices":[{}]}`))
	})
	if res := p.test(context.Background(), key, []byte(`{"model":"m"}`)); !res.OK {
		t.Fatalf("test = %+v", res)
	}
	if got != `POST /api/v1/chat/completions application/json {"model":"m"}` {
		t.Errorf("OpenRouter got %s", got)
	}
}

func TestProbeDoesNotFollowRedirects(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("followed the redirect with %q", r.Header.Get("Authorization"))
	}))
	t.Cleanup(elsewhere.Close)
	p := fakeOpenRouter(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/steal", http.StatusTemporaryRedirect)
	})
	if res := p.test(context.Background(), key, []byte(`{}`)); res.OK || !strings.Contains(res.Message, "HTTP 307") {
		t.Errorf("test = %+v", res)
	}
}

func TestCleanMasksTheEndOfTheKey(t *testing.T) {
	if got := clean("rejected ..."+key[len(key)-8:], key); strings.Contains(got, key[len(key)-8:]) {
		t.Errorf("clean = %q", got)
	}
}
