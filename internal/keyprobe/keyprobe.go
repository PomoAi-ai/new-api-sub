// Package keyprobe is the only code that reads channel keys: it reads the keys
// of a multi-key channel from new-api's database over a connection of its own
// (NEWAPI_KEY_DB_DSN) and calls OpenRouter with each one. Nothing it returns,
// error texts included, contains a key.
package keyprobe

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"newapisub/internal/newapi"
)

// OpenRouterURL is where every key goes, whatever the channel's base_url says.
// Tests point it at a fake server.
var OpenRouterURL = "https://openrouter.ai"

const (
	// MaxKeys bounds what one balance refresh sends to OpenRouter.
	MaxKeys        = 500
	balanceTimeout = 15 * time.Second
	testTimeout    = 30 * time.Second // a model answers slower than the credits route
	testMaxTokens  = 16
	concurrency    = 8
	maxBody        = 64 << 10
	maxMessage     = 200 // runes of an upstream answer kept in an error
)

var (
	ErrNoChannel   = errors.New("channel not found")
	ErrNoKey       = errors.New("key index out of range")
	ErrTooManyKeys = fmt.Errorf("more than %d keys", MaxKeys)
	// ErrOverride says why a channel's configuration cannot be tested. Its text is safe to show.
	ErrOverride = errors.New("该渠道的配置不支持按 Key 测试")
)

type Prober struct {
	db    *sql.DB
	query string
	http  *http.Client
}

// Open connects with the dedicated read-only role. The pool is small: a few
// requests at a time read one row each.
func Open(ctx context.Context, dsn string) (*Prober, error) {
	db, pg, err := newapi.OpenConn(ctx, "NEWAPI_KEY_DB_DSN", dsn, 2, 1)
	if err != nil {
		return nil, err
	}
	// key is a reserved word in MySQL.
	query := "SELECT `key`, model_mapping, param_override FROM channels WHERE id = ?"
	if pg {
		query = `SELECT "key", model_mapping, param_override FROM channels WHERE id = $1`
	}
	return &Prober{db: db, query: query, http: &http.Client{
		// A key is only ever sent to OpenRouterURL itself.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (p *Prober) Close() error { return p.db.Close() }

// SetMaxOpenConns caps the pool, for a short-lived Prober.
func (p *Prober) SetMaxOpenConns(n int) { p.db.SetMaxOpenConns(n) }

// Check runs the query of a key read without reading a key: no channel has id 0.
func (p *Prober) Check(ctx context.Context) error {
	if _, err := p.channel(ctx, 0); !errors.Is(err, ErrNoChannel) {
		return err
	}
	return nil
}

// SplitKeys is new-api's Channel.GetKeys, so that index i here is key i there:
// a JSON array gives its raw elements, anything else is split on \n after
// trimming \n at both ends, with no other trimming and empty lines kept.
func SplitKeys(raw string) []string {
	if raw == "" {
		return []string{}
	}
	if trimmed := strings.TrimSpace(raw); strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if json.Unmarshal([]byte(trimmed), &arr) == nil {
			out := make([]string, len(arr))
			for i, v := range arr {
				out[i] = string(v)
			}
			return out
		}
	}
	return strings.Split(strings.Trim(raw, "\n"), "\n")
}

// channel is what a probe needs of a new-api channel.
type channel struct {
	keys          []string
	modelMapping  string
	paramOverride string
}

func (p *Prober) channel(ctx context.Context, channelID int) (channel, error) {
	var raw string
	var mapping, override sql.NullString
	err := p.db.QueryRowContext(ctx, p.query, channelID).Scan(&raw, &mapping, &override)
	if errors.Is(err, sql.ErrNoRows) {
		return channel{}, ErrNoChannel
	}
	if err != nil {
		return channel{}, fmt.Errorf("read the keys of channel %d: %w", channelID, err)
	}
	keys := SplitKeys(raw)
	if len(keys) > MaxKeys {
		return channel{}, ErrTooManyKeys
	}
	return channel{keys, mapping.String, override.String}, nil
}

// checkIndexes is ErrNoKey for an index the channel has no key at.
func (c channel) checkIndexes(indexes []int) error {
	for _, i := range indexes {
		if i < 0 || i >= len(c.keys) {
			return ErrNoKey
		}
	}
	return nil
}

// each runs fn for every index, a few at a time. Indexes not yet started when
// ctx ends are left out.
func each[T any](ctx context.Context, indexes []int, fn func(int) T) []T {
	out := make([]T, 0, len(indexes))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
start:
	for _, i := range indexes {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break start
		}
		if ctx.Err() != nil { // select picks at random when both are ready
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			r := fn(i)
			mu.Lock()
			out = append(out, r)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// TestResult is one key's call of a model. A key OpenRouter refuses, or a
// failed call, is a result with OK false.
type TestResult struct {
	Index     int    `json:"index"`
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	LatencyMS int64  `json:"latency_ms"`
}

// Tests calls model once with each key of indexes, sorted by index, sending what
// new-api would send: model_mapping and param_override applied. A channel
// configured in a way this cannot reproduce is ErrOverride before any call:
// the provider routing it may hold is what keeps the call on the right upstream.
func (p *Prober) Tests(ctx context.Context, channelID int, indexes []int, model string) ([]TestResult, error) {
	c, err := p.channel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if err := c.checkIndexes(indexes); err != nil {
		return nil, err
	}
	payload, err := chatRequest(model, c.modelMapping, c.paramOverride)
	if err != nil {
		return nil, err
	}
	out := each(ctx, indexes, func(i int) TestResult {
		r := p.test(ctx, c.keys[i], payload)
		r.Index = i
		return r
	})
	slices.SortFunc(out, func(a, b TestResult) int { return cmp.Compare(a.Index, b.Index) })
	return out, nil
}

func (p *Prober) test(ctx context.Context, key string, payload []byte) TestResult {
	// The answer's text is not read: it may repeat anything.
	var body struct {
		Error   *json.RawMessage `json:"error"`
		Choices []struct {
			Error        *json.RawMessage `json:"error"`
			FinishReason string           `json:"finish_reason"`
		} `json:"choices"`
	}
	key = strings.TrimSpace(key)
	start := time.Now()
	err := p.call(ctx, key, http.MethodPost, "/api/v1/chat/completions", payload, testTimeout, &body)
	r := TestResult{LatencyMS: time.Since(start).Milliseconds()}
	switch {
	case err != nil:
		r.Message = err.Error()
	case body.Error != nil:
		r.Message = "OpenRouter 返回错误: " + clean(string(*body.Error), key)
	case len(body.Choices) == 0:
		r.Message = "OpenRouter 的响应没有 choices"
	default:
		// An upstream that fails mid-way is reported inside the choice, with HTTP 200.
		for _, c := range body.Choices {
			if c.Error != nil {
				r.Message = "OpenRouter 返回错误: " + clean(string(*c.Error), key)
				return r
			}
			if c.FinishReason == "error" {
				r.Message = "OpenRouter 返回错误: finish_reason 为 error"
				return r
			}
		}
		r.OK, r.Message = true, "调用成功"
	}
	return r
}

// chatRequest is the body of a test call.
func chatRequest(model, modelMapping, paramOverride string) ([]byte, error) {
	upstream, err := mapModel(model, modelMapping)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"model":      upstream,
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
		"max_tokens": testMaxTokens,
		"stream":     false,
	}
	if err := applyOverride(body, paramOverride); err != nil {
		return nil, err
	}
	return json.Marshal(body)
}

// mapModel follows model_mapping to the end of the chain, as new-api does.
// shortcut: new-api also tries the name without its reasoning suffix, which depends on its settings; add that if a mapping ever relies on it.
func mapModel(model, mapping string) (string, error) {
	if strings.TrimSpace(mapping) == "" {
		return model, nil
	}
	var m map[string]string
	if json.Unmarshal([]byte(mapping), &m) != nil {
		return "", overrideErr("model_mapping 不是有效的映射")
	}
	seen := map[string]bool{model: true}
	for {
		next := m[model]
		if next == "" || next == model {
			return model, nil
		}
		if seen[next] {
			return "", overrideErr("model_mapping 有循环")
		}
		seen[next] = true
		model = next
	}
}

func overrideErr(why string) error { return fmt.Errorf("%w：%s", ErrOverride, why) }

// overridePath is a path that means the same here and in new-api, whose paths
// also know array indexes, wildcards and escapes.
var overridePath = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*(\.[A-Za-z_][A-Za-z0-9_-]*)*$`)

// applyOverride applies param_override to body. Of new-api's two formats it
// takes a plain object, merged at the top level with its keys taken literally,
// and {"operations":[...]} made of nothing but unconditional sets. Whatever else
// new-api would do to the request cannot be reproduced, so it is an error. The
// errors never quote the override: it may hold secrets.
func applyOverride(body map[string]any, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber() // numbers go out as written
	var v any
	if !json.Valid([]byte(raw)) || dec.Decode(&v) != nil {
		return overrideErr("param_override 不是有效的 JSON")
	}
	if v == nil {
		return nil
	}
	// Maps, not structs: encoding/json matches struct fields ignoring case, new-api does not.
	obj, ok := v.(map[string]any)
	if !ok {
		return overrideErr("param_override 不是 JSON 对象")
	}
	operations, found := obj["operations"]
	if !found {
		maps.Copy(body, obj)
		return nil
	}
	list, ok := operations.([]any)
	if !ok || len(obj) != 1 {
		return overrideErr("param_override 的 operations 须为数组，且不能与其他字段同时出现")
	}
	for _, item := range list {
		op, ok := item.(map[string]any)
		if !ok || op["mode"] != "set" {
			return overrideErr("param_override 只支持 mode 为 set 的操作")
		}
		for field := range op {
			// description is the editor's note; new-api ignores it too.
			if field != "mode" && field != "path" && field != "value" && field != "description" {
				return overrideErr("param_override 的操作只能含 mode、path、value、description，不支持条件等其他字段")
			}
		}
		path, _ := op["path"].(string)
		if !overridePath.MatchString(path) {
			return overrideErr("param_override 的 path 只支持点号分隔的字段名")
		}
		if err := setPath(body, path, op["value"]); err != nil {
			return err
		}
	}
	return nil
}

// setPath sets a dotted path, creating the objects on the way.
func setPath(obj map[string]any, path string, value any) error {
	parts := strings.Split(path, ".")
	last := len(parts) - 1
	for _, part := range parts[:last] {
		next, found := obj[part]
		if !found {
			next = map[string]any{}
			obj[part] = next
		}
		child, ok := next.(map[string]any)
		if !ok {
			return overrideErr("param_override 的 path 经过了不是对象的字段")
		}
		obj = child
	}
	obj[parts[last]] = value
	return nil
}

// Balance is one key's check: a balance in USD, or why there is none.
type Balance struct {
	Index   int
	Balance *float64
	Error   string
}

// Balances asks OpenRouter for the balance (GET /api/v1/credits, total_credits
// - total_usage, as new-api computes it) of the keys of indexes, or all when
// indexes is nil, a few at a time. Keys not yet started when ctx ends are left out.
func (p *Prober) Balances(ctx context.Context, channelID int, indexes []int) ([]Balance, error) {
	c, err := p.channel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if err := c.checkIndexes(indexes); err != nil {
		return nil, err
	}
	if indexes == nil {
		indexes = make([]int, len(c.keys))
		for i := range indexes {
			indexes[i] = i
		}
	}
	return each(ctx, indexes, func(i int) Balance {
		b := p.balance(ctx, c.keys[i])
		b.Index = i
		return b
	}), nil
}

func (p *Prober) balance(ctx context.Context, key string) Balance {
	var body struct {
		Data *struct {
			TotalCredits float64 `json:"total_credits"`
			TotalUsage   float64 `json:"total_usage"`
		} `json:"data"`
	}
	if err := p.call(ctx, key, http.MethodGet, "/api/v1/credits", nil, balanceTimeout, &body); err != nil {
		return Balance{Error: err.Error()}
	}
	if body.Data == nil {
		return Balance{Error: "OpenRouter 的响应没有 data"}
	}
	b := body.Data.TotalCredits - body.Data.TotalUsage
	return Balance{Balance: &b}
}

// call calls OpenRouter with key. Its errors are safe to show: the key and
// anything shaped like one are masked, the upstream's answer is cut short.
func (p *Prober) call(ctx context.Context, key, method, path string, payload []byte, timeout time.Duration, out any) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("该 Key 为空")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, OpenRouterURL+path, bytes.NewReader(payload))
	if err != nil {
		return errors.New(clean(err.Error(), key))
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return errors.New("OpenRouter 请求失败: " + clean(err.Error(), key))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return errors.New("读取 OpenRouter 响应失败: " + clean(err.Error(), key))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("OpenRouter HTTP %d: %s", resp.StatusCode, clean(strings.TrimSpace(string(raw)), key))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errors.New("OpenRouter 的响应不是有效的 JSON")
	}
	return nil
}

var skKey = regexp.MustCompile(`sk-[A-Za-z0-9_\-]{8,}`)

// clean masks key, anything shaped like a key, and cuts s short.
func clean(s, key string) string {
	s = strings.ReplaceAll(s, key, "••••")
	if len(key) >= 16 { // upstreams also quote the end of a key
		s = strings.ReplaceAll(s, key[len(key)-8:], "••••")
	}
	s = skKey.ReplaceAllString(newapi.MaskKeys(s), "••••")
	if utf8.RuneCountInString(s) > maxMessage {
		s = string([]rune(s)[:maxMessage]) + "…"
	}
	return strings.ToValidUTF8(s, "")
}
