// Package newapi integrates with new-api: writes go through its management
// API (PAT auth); reads go to its database through a read-only connection.
package newapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	callTimeout   = 20 * time.Second
	createTimeout = 60 * time.Second // a batch inserts one channel per key with its abilities
	testTimeout   = 90 * time.Second
	dataTimeout   = 60 * time.Second // fetch_models waits for the upstream
)

type Client struct {
	base string
	pat  string
	http *http.Client
}

func NewClient(baseURL, pat string) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), pat: pat, http: &http.Client{}}
}

// ChannelInput is the channel object of POST /api/channel/.
type ChannelInput struct {
	Type      int    `json:"type"`
	Key       string `json:"key"`
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	Models    string `json:"models"`
	Group     string `json:"group"`
	Priority  int64  `json:"priority"`
	Weight    int    `json:"weight"`
	Status    int    `json:"status"`
	Remark    string `json:"remark"`
	TestModel string `json:"test_model,omitempty"`
	Extra
}

// CreateRequest is the body of POST /api/channel/ (AddChannelRequest in
// new-api). Mode is single, batch (one channel per key line) or
// multi_to_single (one channel holding all key lines, using MultiKeyMode).
type CreateRequest struct {
	Mode         string       `json:"mode"`
	MultiKeyMode string       `json:"multi_key_mode,omitempty"`
	Channel      ChannelInput `json:"channel"`
}

// envelope is new-api's response shape; business errors are usually
// HTTP 200 with success=false.
type envelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Time    float64         `json:"time"`
	// update_balance answers at the top level, not in data.
	Balance     *float64 `json:"balance"`
	RawResponse *string  `json:"raw_response"`
}

// call performs a request and decodes the envelope. Request bodies (which
// may contain channel keys) are never included in errors.
func (c *Client) call(ctx context.Context, timeout time.Duration, method, path string, body any) (envelope, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return envelope{}, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return envelope{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.pat)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return envelope{}, fmt.Errorf("new-api %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return envelope{}, fmt.Errorf("new-api %s %s: read body: %w", method, path, err)
	}
	var env envelope
	jsonErr := json.Unmarshal(raw, &env)
	// What new-api says can quote an upstream, and it goes on to callers and the audit log.
	env.Message = MaskKeys(env.Message)
	if env.RawResponse != nil {
		masked := MaskKeys(*env.RawResponse)
		env.RawResponse = &masked
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// A 4xx in new-api's own words is a refusal like success=false, a PAT without the scope for one. 429 is its rate limit, which UpstreamErr words itself.
		if resp.StatusCode/100 == 4 && resp.StatusCode != http.StatusTooManyRequests && jsonErr == nil && env.Message != "" {
			return envelope{}, &Rejected{method, path, env.Message, resp.StatusCode}
		}
		msg := env.Message
		if jsonErr != nil || msg == "" {
			msg = MaskKeys(strings.TrimSpace(string(raw[:min(len(raw), 200)])))
		}
		return envelope{}, fmt.Errorf("new-api %s %s: HTTP %d: %s", method, path, resp.StatusCode, msg)
	}
	if jsonErr != nil {
		return envelope{}, fmt.Errorf("new-api %s %s: invalid JSON response: %w", method, path, jsonErr)
	}
	return env, nil
}

// Rejected is new-api's explicit business failure (success=false or a 4xx with
// its message, then in Status): the request was processed and refused, unlike
// transport errors or timeouts.
type Rejected struct {
	Method, Path, Message string
	Status                int
}

func (e *Rejected) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("new-api %s %s: HTTP %d: %s", e.Method, e.Path, e.Status, e.Message)
	}
	return fmt.Sprintf("new-api %s %s: %s", e.Method, e.Path, e.Message)
}

// do is call plus treating success=false as a *Rejected error.
func (c *Client) do(ctx context.Context, method, path string, body any) (envelope, error) {
	return c.doWithin(ctx, callTimeout, method, path, body)
}

func (c *Client) doWithin(ctx context.Context, timeout time.Duration, method, path string, body any) (envelope, error) {
	env, err := c.call(ctx, timeout, method, path, body)
	if err != nil {
		return env, err
	}
	if !env.Success {
		return env, &Rejected{Method: method, Path: path, Message: env.Message}
	}
	return env, nil
}

// Status returns quota_per_unit from the public /api/status.
func (c *Client) Status(ctx context.Context) (float64, error) {
	env, err := c.do(ctx, http.MethodGet, "/api/status", nil)
	if err != nil {
		return 0, err
	}
	var data struct {
		QuotaPerUnit float64 `json:"quota_per_unit"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil || data.QuotaPerUnit <= 0 {
		return 0, fmt.Errorf("new-api /api/status: missing quota_per_unit")
	}
	return data.QuotaPerUnit, nil
}

// Ping verifies the PAT can read channels.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodGet, "/api/channel/?p=1&page_size=1", nil)
	return err
}

// CreateChannel adds channels and returns their new-api ids. No ids with a
// nil error means new-api accepted the request but did not say which ids it
// used (released versions up to at least v1.0.0-rc.47 do not); the caller
// must look them up. A *Rejected error means nothing was created; any other
// error leaves the outcome unknown.
func (c *Client) CreateChannel(ctx context.Context, req CreateRequest) ([]int, error) {
	env, err := c.doWithin(ctx, createTimeout, http.MethodPost, "/api/channel/", req)
	if err != nil {
		return nil, err
	}
	var data struct {
		IDs []int `json:"ids"`
		ID  int   `json:"id"`
	}
	if json.Unmarshal(env.Data, &data) != nil {
		return nil, nil
	}
	if len(data.IDs) == 0 && data.ID > 0 {
		return []int{data.ID}, nil
	}
	return data.IDs, nil
}

// Data returns the data of a new-api response as it came. A response without
// any is an error: the caller would pass on an empty answer as if it were one.
func (c *Client) Data(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	env, err := c.doWithin(ctx, dataTimeout, method, path, body)
	if err != nil {
		return nil, err
	}
	if len(env.Data) == 0 {
		return nil, fmt.Errorf("new-api %s %s: response has no data", method, path)
	}
	return env.Data, nil
}

// UpdateChannel changes a channel's name and models and, unless empty, its test
// model; nothing else. new-api's PUT writes every non-zero field it is sent, the
// key and base URL too, and the PAT's scope does not limit that: this fixed body
// does.
func (c *Client) UpdateChannel(ctx context.Context, id int, name, models, testModel string) error {
	_, err := c.do(ctx, http.MethodPut, "/api/channel/", struct {
		ID        int    `json:"id"`
		Name      string `json:"name"`
		Models    string `json:"models"`
		TestModel string `json:"test_model,omitempty"`
	}{id, name, models, testModel})
	return err
}

// DeleteChannel is idempotent: new-api does not fail on a missing id.
func (c *Client) DeleteChannel(ctx context.Context, id int) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/channel/%d", id), nil)
	return err
}

// SetStatus sets 1 (enabled) or 2 (manually disabled). changed is new-api's
// answer, false when the channel already had that status but also, with
// success=true, when its memory cache has not learned a new channel yet and
// nothing happened.
func (c *Client) SetStatus(ctx context.Context, id, status int) (changed bool, err error) {
	path := fmt.Sprintf("/api/channel/%d/status", id)
	env, err := c.do(ctx, http.MethodPost, path, map[string]int{"status": status})
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(env.Data, &changed); err != nil {
		return false, fmt.Errorf("new-api POST %s: unexpected data: %w", path, err)
	}
	return changed, nil
}

// TestOptions are the optional parameters of a channel test.
type TestOptions struct {
	Model        string `json:"model"`
	EndpointType string `json:"endpoint_type"`
	Stream       bool   `json:"stream"`
}

// TestChannel runs new-api's channel test. A failed test (success=false)
// is a result, not an error.
func (c *Client) TestChannel(ctx context.Context, id int, opts TestOptions) (ok bool, msg string, secs float64, err error) {
	q := url.Values{}
	if opts.Model != "" {
		q.Set("model", opts.Model)
	}
	if opts.EndpointType != "" {
		q.Set("endpoint_type", opts.EndpointType)
	}
	if opts.Stream {
		q.Set("stream", "true")
	}
	path := fmt.Sprintf("/api/channel/test/%d", id)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	env, err := c.call(ctx, testTimeout, http.MethodGet, path, nil)
	if err != nil {
		return false, "", 0, err
	}
	return env.Success, env.Message, env.Time, nil
}

// Balance is the result of a balance refresh: Balance in USD for standard
// channels, RawResponse for advanced custom ones.
type Balance struct {
	Balance     *float64 `json:"balance,omitempty"`
	RawResponse *string  `json:"raw_response,omitempty"`
}

// UpdateBalance makes new-api query the upstream balance and store it. An
// unsupported channel type or a multi-key channel is a *Rejected error.
func (c *Client) UpdateBalance(ctx context.Context, id int) (Balance, error) {
	env, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/channel/update_balance/%d", id), nil)
	if err != nil {
		return Balance{}, err
	}
	return Balance{env.Balance, env.RawResponse}, nil
}

// KeyStatus is one key of a multi-key channel as new-api reports it. new-api
// also sends the first characters of the key (key_preview); there is no field
// for them, so they are dropped while decoding.
type KeyStatus struct {
	Index        int    `json:"index"`
	Status       int    `json:"status"` // 1 enabled, 2 manually disabled, 3 automatically disabled
	DisabledTime int64  `json:"disabled_time"`
	Reason       string `json:"reason"`
}

// keyActions are the multi-key actions this system may send. new-api's others
// delete keys or switch all of them at once.
var keyActions = []string{"get_key_status", "enable_key", "disable_key"}

type keyRequest struct {
	ChannelID int    `json:"channel_id"`
	Action    string `json:"action"`
	KeyIndex  *int   `json:"key_index,omitempty"`
	Page      int    `json:"page,omitempty"`
	PageSize  int    `json:"page_size,omitempty"`
}

const keyPageSize = 500

func (c *Client) manageKeys(ctx context.Context, req keyRequest) (envelope, error) {
	if !slices.Contains(keyActions, req.Action) {
		return envelope{}, fmt.Errorf("multi-key action %q is not allowed", req.Action)
	}
	return c.do(ctx, http.MethodPost, "/api/channel/multi_key/manage", req)
}

// KeyStatuses returns every key of a multi-key channel in index order.
func (c *Client) KeyStatuses(ctx context.Context, channelID int) ([]KeyStatus, error) {
	var out []KeyStatus
	for page := 1; ; page++ {
		env, err := c.manageKeys(ctx, keyRequest{ChannelID: channelID, Action: "get_key_status", Page: page, PageSize: keyPageSize})
		if err != nil {
			return nil, err
		}
		var data struct {
			Keys       []KeyStatus `json:"keys"`
			TotalPages int         `json:"total_pages"`
		}
		if err := json.Unmarshal(env.Data, &data); err != nil {
			return nil, fmt.Errorf("new-api multi-key status of channel %d: %w", channelID, err)
		}
		for _, k := range data.Keys {
			// new-api clamps a page past the end to the last one; the keys may also change between pages.
			if k.Index != len(out) {
				return nil, fmt.Errorf("new-api multi-key status of channel %d: key %d out of order, the key list changed while reading", channelID, k.Index)
			}
			k.Reason = MaskKeys(k.Reason) // why new-api disabled it can quote the upstream
			out = append(out, k)
		}
		if page >= data.TotalPages || len(data.Keys) == 0 {
			return out, nil
		}
	}
}

// SetKeyEnabled enables or manually disables one key of a multi-key channel.
func (c *Client) SetKeyEnabled(ctx context.Context, channelID, index int, enabled bool) error {
	action := "disable_key"
	if enabled {
		action = "enable_key"
	}
	_, err := c.manageKeys(ctx, keyRequest{ChannelID: channelID, Action: action, KeyIndex: &index})
	return err
}

// ListedChannel is a channel of new-api's channel list, with its remark.
type ListedChannel struct {
	Channel
	Remark string `json:"remark"`
}

// ChannelPageSize is how many channels a page of RecentChannels holds.
const ChannelPageSize = 100

// RecentChannels returns page (from 1) of new-api's channels, newest id first,
// and how many there are in all.
func (c *Client) RecentChannels(ctx context.Context, page int) ([]ListedChannel, int, error) {
	path := fmt.Sprintf("/api/channel/?p=%d&page_size=%d&id_sort=true", page, ChannelPageSize)
	env, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, 0, err
	}
	var data struct {
		Items []ListedChannel `json:"items"`
		Total int             `json:"total"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil, 0, fmt.Errorf("new-api GET /api/channel/: unexpected data: %w", err)
	}
	for i := range data.Items {
		ch := &data.Items[i].Channel
		ch.ChannelInfo = listChannelInfo(string(ch.ChannelInfo))
		ch.OtherInfo = maskJSON(ch.OtherInfo, MaskKeys) // as Channels reads it
	}
	return data.Items, data.Total, nil
}

// ErrNoChannel is new-api's answer for a channel id it does not have.
var ErrNoChannel = errors.New("new-api has no such channel")

// Channel reads one channel by id, as RecentChannels lists it, with the same
// scope (channel read). ErrNoChannel when new-api has no channel of that id.
func (c *Client) Channel(ctx context.Context, id int) (Channel, error) {
	path := fmt.Sprintf("/api/channel/%d", id)
	env, err := c.do(ctx, http.MethodGet, path, nil)
	var rej *Rejected
	if errors.As(err, &rej) && rej.Status == 0 && rej.Message == "record not found" { // GetChannel passes gorm's ErrRecordNotFound on as it is
		return Channel{}, ErrNoChannel
	}
	if err != nil {
		return Channel{}, err
	}
	var ch Channel
	if err := json.Unmarshal(env.Data, &ch); err != nil || ch.ID != id {
		return Channel{}, fmt.Errorf("new-api GET %s: unexpected data", path)
	}
	ch.ChannelInfo = listChannelInfo(string(ch.ChannelInfo))
	ch.OtherInfo = maskJSON(ch.OtherInfo, MaskKeys)
	return ch, nil
}

const (
	exportPath    = "/api/log/export"
	exportTimeout = 120 * time.Second
	exportLineMax = 16 << 20 // content and other are TEXT: a line can be long
)

// ErrRateLimited is new-api's rate limit (HTTP 429): try again later.
var ErrRateLimited = errors.New("new-api rate limit (HTTP 429)")

// exportLine is a line of /api/log/export: a log, which names its channel
// "channel", or the trailer, an envelope with the number of logs before it.
type exportLine struct {
	Log
	Channel int    `json:"channel"`
	Success *bool  `json:"success"`
	Message string `json:"message"`
	Data    *struct {
		Count *int `json:"count"`
	} `json:"data"`
}

// ExportLogs streams every log with created_at in [start, end], end included as
// new-api has it, newest first, to fn. count is the number of logs; served is
// new-api's clock (its Date header), zero if it sent none. An error means the
// window was not read whole, though fn may have seen some of it: a stream cut
// before its trailer, a trailer whose count differs, success=false (a
// *Rejected, as is another export of the same user running) or ErrRateLimited.
func (c *Client) ExportLogs(ctx context.Context, start, end int64, fn func(Log) error) (count int, served time.Time, err error) {
	ctx, cancel := context.WithTimeout(ctx, exportTimeout)
	defer cancel()
	q := url.Values{"start_timestamp": {strconv.FormatInt(start, 10)}, "end_timestamp": {strconv.FormatInt(end, 10)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+exportPath+"?"+q.Encode(), nil)
	if err != nil {
		return 0, served, err
	}
	req.Header.Set("Authorization", "Bearer "+c.pat)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, served, fmt.Errorf("new-api GET %s: %w", exportPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, served, exportStatusErr(resp)
	}
	served, _ = http.ParseTime(resp.Header.Get("Date"))
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), exportLineMax)
	done := false
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if done {
			return count, served, fmt.Errorf("new-api GET %s: a line after the trailer", exportPath)
		}
		var l exportLine
		if err := json.Unmarshal(line, &l); err != nil {
			return count, served, fmt.Errorf("new-api GET %s: line %d: %w", exportPath, count+1, err)
		}
		if l.Success != nil {
			if !*l.Success {
				return count, served, &Rejected{Method: http.MethodGet, Path: exportPath, Message: MaskKeys(l.Message)}
			}
			if l.Data == nil || l.Data.Count == nil || *l.Data.Count != count {
				return count, served, fmt.Errorf("new-api GET %s: the trailer does not count the %d logs read", exportPath, count)
			}
			done = true
			continue
		}
		l.Log.ChannelID = l.Channel
		count++
		if err := fn(l.Log); err != nil {
			return count, served, err
		}
	}
	if err := sc.Err(); err != nil {
		return count, served, fmt.Errorf("new-api GET %s: read: %w", exportPath, err)
	}
	if !done {
		return count, served, fmt.Errorf("new-api GET %s: the stream ended before its trailer", exportPath)
	}
	return count, served, nil
}

// exportStatusErr is call's reading of a failed status, 429 apart.
func exportStatusErr(resp *http.Response) error {
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("new-api GET %s: %w", exportPath, ErrRateLimited)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	var env envelope
	jsonErr := json.Unmarshal(raw, &env)
	if msg := MaskKeys(env.Message); resp.StatusCode/100 == 4 && jsonErr == nil && msg != "" {
		return &Rejected{http.MethodGet, exportPath, msg, resp.StatusCode}
	}
	return fmt.Errorf("new-api GET %s: HTTP %d: %s", exportPath, resp.StatusCode, MaskKeys(strings.TrimSpace(string(raw[:min(len(raw), 200)]))))
}
