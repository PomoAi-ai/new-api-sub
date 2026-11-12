package newapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// q quotes the reserved word group; other columns are plain.
func (d dialect) q(col string) string {
	if col != "group" {
		return col
	}
	if d.pg {
		return `"group"`
	}
	return "`group`"
}

// qb is a tiny SQL builder that numbers placeholders per dialect.
type qb struct {
	d    dialect
	sb   strings.Builder
	args []any
}

func (b *qb) w(s string) *qb { b.sb.WriteString(s); return b }

func (b *qb) arg(v any) string {
	b.args = append(b.args, v)
	if b.d.pg {
		return "$" + strconv.Itoa(len(b.args))
	}
	return "?"
}

func (b *qb) in(ids []int) *qb {
	ph := make([]string, len(ids))
	for i, id := range ids {
		ph[i] = b.arg(id)
	}
	return b.w("(" + strings.Join(ph, ", ") + ")")
}

// jsonText reads a JSON column as text.
func (d dialect) jsonText(col string) string {
	if d.pg {
		return col + "::text"
	}
	return "CAST(" + col + " AS CHAR)"
}

// Channel has the JSON shape of new-api's channel list, minus the key, remark
// and settings. Pointers are the columns new-api itself returns as nullable.
type Channel struct {
	ID                 int             `json:"id"`
	Type               int             `json:"type"`
	Name               string          `json:"name"`
	Status             int             `json:"status"`
	Group              string          `json:"group"`
	Models             string          `json:"models"`
	Priority           *int64          `json:"priority"`
	Weight             *int64          `json:"weight"`
	UsedQuota          int64           `json:"used_quota"`
	Balance            float64         `json:"balance"`
	BalanceUpdatedTime int64           `json:"balance_updated_time"`
	ResponseTime       int64           `json:"response_time"`
	TestTime           int64           `json:"test_time"`
	CreatedTime        int64           `json:"created_time"`
	BaseURL            *string         `json:"base_url"`
	Tag                *string         `json:"tag"`
	TestModel          *string         `json:"test_model"`
	AutoBan            *int64          `json:"auto_ban"`
	ChannelInfo        json.RawMessage `json:"channel_info"`
	OtherInfo          string          `json:"other_info"`
}

// MultiKeySize is the number of keys of a multi-key channel, 0 for any other.
func (c Channel) MultiKeySize() int {
	var info struct {
		IsMultiKey   bool `json:"is_multi_key"`
		MultiKeySize int  `json:"multi_key_size"`
	}
	if json.Unmarshal(c.ChannelInfo, &info) != nil || !info.IsMultiKey {
		return 0
	}
	return info.MultiKeySize
}

func channelsSQL(d dialect, ids []int) (string, []any) {
	b := &qb{d: d}
	b.w("SELECT id, type, COALESCE(name, ''), status, COALESCE(").w(d.q("group")).
		w(", ''), COALESCE(models, ''), priority, weight, COALESCE(used_quota, 0), COALESCE(balance, 0), ").
		w("COALESCE(balance_updated_time, 0), COALESCE(response_time, 0), COALESCE(test_time, 0), COALESCE(created_time, 0), ").
		w("base_url, tag, test_model, auto_ban, COALESCE(").w(d.jsonText("channel_info")).
		w(", ''), COALESCE(other_info, '') FROM channels WHERE id IN ").in(ids)
	return b.sb.String(), b.args
}

// listChannelInfo is channel_info as new-api's channel list returns it: an
// object without the per-key disabled reasons and times, which can quote
// upstream errors. Anything unreadable becomes an empty object.
func listChannelInfo(raw string) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &m) != nil || m == nil {
		return json.RawMessage("{}")
	}
	delete(m, "multi_key_disabled_reason")
	delete(m, "multi_key_disabled_time")
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

// Channels returns the channels that still exist among ids.
func (d *DB) Channels(ctx context.Context, ids []int) (map[int]Channel, error) {
	out := map[int]Channel{}
	if len(ids) == 0 {
		return out, nil
	}
	q, args := channelsSQL(d.mainD, ids)
	rows, err := d.main.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query new-api channels: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c Channel
		var info string
		if err := rows.Scan(&c.ID, &c.Type, &c.Name, &c.Status, &c.Group, &c.Models, &c.Priority, &c.Weight,
			&c.UsedQuota, &c.Balance, &c.BalanceUpdatedTime, &c.ResponseTime, &c.TestTime, &c.CreatedTime,
			&c.BaseURL, &c.Tag, &c.TestModel, &c.AutoBan, &info, &c.OtherInfo); err != nil {
			return nil, fmt.Errorf("scan new-api channel: %w", err)
		}
		c.ChannelInfo = listChannelInfo(info)
		c.OtherInfo = maskJSON(c.OtherInfo, MaskKeys) // the reason new-api disabled it can quote the upstream
		out[c.ID] = c
	}
	return out, rows.Err()
}

// ChannelIDsByRemark finds the channels carrying remark (a one-time value):
// several for a batch create, none if nothing was created.
func (d *DB) ChannelIDsByRemark(ctx context.Context, remark string) ([]int, error) {
	b := &qb{d: d.mainD}
	b.w("SELECT id FROM channels WHERE remark = " + b.arg(remark) + " ORDER BY id")
	rows, err := d.main.QueryContext(ctx, b.sb.String(), b.args...)
	if err != nil {
		return nil, fmt.Errorf("query new-api channel by remark: %w", err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan new-api channel id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// UsedQuota returns the used_quota of the channels that still exist among ids.
func (d *DB) UsedQuota(ctx context.Context, ids []int) (map[int]int64, error) {
	out := map[int]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	b := &qb{d: d.mainD}
	b.w("SELECT id, COALESCE(used_quota, 0) FROM channels WHERE id IN ").in(ids)
	rows, err := d.main.QueryContext(ctx, b.sb.String(), b.args...)
	if err != nil {
		return nil, fmt.Errorf("query new-api used quota: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var quota int64
		if err := rows.Scan(&id, &quota); err != nil {
			return nil, fmt.Errorf("scan new-api used quota: %w", err)
		}
		out[id] = quota
	}
	return out, rows.Err()
}

// TestTokenName is the token_name of the logs new-api writes for channel tests
// (controller/channel-test.go).
const TestTokenName = "Model test"

// Log is a row of new-api's logs table. ChannelName, PoolID and PoolName are
// filled by the caller from its own mapping.
type Log struct {
	PoolID            int64  `json:"pool_id"`
	PoolName          string `json:"pool_name,omitempty"`
	ID                int64  `json:"id"`
	CreatedAt         int64  `json:"created_at"`
	Type              int    `json:"type"`
	ChannelID         int    `json:"channel_id"`
	ChannelName       string `json:"channel_name"`
	ModelName         string `json:"model_name"`
	Quota             int64  `json:"quota"`
	PromptTokens      int64  `json:"prompt_tokens"`
	CompletionTokens  int64  `json:"completion_tokens"`
	UseTime           int64  `json:"use_time"`
	IsStream          bool   `json:"is_stream"`
	Content           string `json:"content"`
	UserID            int64  `json:"user_id"`
	Username          string `json:"username"`
	TokenID           int64  `json:"token_id"`
	TokenName         string `json:"token_name"`
	IP                string `json:"ip"`
	Group             string `json:"group"`
	RequestID         string `json:"request_id"`
	UpstreamRequestID string `json:"upstream_request_id"`
	Other             string `json:"other"`
}

// peerAddr matches the addresses in Go's network errors. new-api stores those as
// they are in other.stream_status, and the far end of a failed write to a client
// is that client: write tcp 172.18.0.3:3000->203.0.113.7:51324: write: broken pipe.
var peerAddr = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b|\[[0-9A-Fa-f.]*:[0-9A-Fa-f.]*:[0-9A-Fa-f:.]*(?:%[^\]\s]*)?\]`)

// hideEndUser leaves what anyone but a super may see of a log: nothing about who
// made the request, and other as OwnerOther has it, without addresses. A channel
// test has no end user, so its token name stays.
func (l *Log) hideEndUser() {
	l.UserID, l.Username, l.TokenID, l.IP = 0, "", 0, ""
	if l.TokenName != TestTokenName {
		l.TokenName = ""
	}
	l.Other = maskJSON(OwnerOther(l.Other), func(s string) string { return peerAddr.ReplaceAllString(s, hidden) })
}

const (
	hidden = "••••" // the page's own stand-in for hidden values
	// keyChar is what a key is made of: printable ASCII without quotes or
	// backslashes, so quoted JSON around a key and text in another script end it.
	keyChar = `[^\x00-\x20\x7f-\x{10FFFF}"'\\]`
)

// quotedKey matches what upstream errors quote of an API key, masked or not.
// shortcut: known key formats only, add a format here when an upstream quotes another.
var quotedKey = regexp.MustCompile(
	// An escape of JSON quoted in the text starts a word too: \n ends in a letter.
	`(?:\b|\\[bfnrt]|\\u[0-9a-fA-F]{4})(?:` +
		// Masked forms count: sk-proj-****abcd, sk-abc...wxyz.
		`(?:sk[-_]|rk[-_]|gsk_|nap_|ptl_)[-*.\w]{5,}[-*\w]` +
		// These prefixes also start model and library names: xai-grok-beta, pplx-70b-online, hf_hub_download.
		`|(?:xai-|pplx-|hf_|r8_|fw_|jina_)[A-Za-z0-9]{20}[-\w]*` +
		`|AIza[-\w]{10,}` +
		// A JWT, which a Codex access_token is.
		`|eyJ[-\w]+(?:\.[-\w]+)+` +
		// A word is no token (Bearer auth, bearer token.), nor is a placeholder (Bearer <token>).
		`|(?i:bearer\s+)` + keyChar + `*(?:[-0-9#$%&*+/=@^_|~]|[.,:;!?)\]}>][A-Za-z0-9])` + keyChar + `*` +
		`)` +
		// A value named as a secret, whose name stays: x-api-key: v, api_key=v, "api_key":"v", ?key=v,
		// and OpenAI's "Incorrect API key provided: v", which relays copy.
		`|((?i:(?:api|access|secret)[-_]?key|(?:access|refresh|auth)[-_]?token|(?:api|app|client)[-_]?secret|api key provided|[?&](?:key|token))` +
		`[\\"']*\s*[:=]\s*[\\"']*)[^\x00-\x20\x7f-\x{10FFFF}"'\\,;&<>(){}\[\]]+`)

// MaskKeys hides the API keys that text from new-api or an upstream quotes. Text
// that is JSON and must stay so goes through maskJSON.
func MaskKeys(s string) string {
	return quotedKey.ReplaceAllString(s, "${1}"+hidden)
}

// jsonString matches a string of a valid JSON text.
var jsonString = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// maskJSON runs mask over the strings of a JSON text and leaves the rest as
// written, so integers beyond a float64's range survive. Masking the raw text
// would not do: \n and \u003e end in a word character, which hides the start of
// what follows, and a match can start on the letter of an escape and invalidate
// it. Text that is not JSON is masked as a whole.
func maskJSON(s string, mask func(string) string) string {
	if !json.Valid([]byte(s)) {
		return mask(s)
	}
	return jsonString.ReplaceAllStringFunc(s, func(tok string) string {
		var text string
		_ = json.Unmarshal([]byte(tok), &text) // cannot fail on a string of valid JSON
		masked := mask(text)
		if masked == text {
			return tok
		}
		out, _ := json.Marshal(masked) // cannot fail on a string
		return string(out)
	})
}

// prepare is the last step of every log leaving this package. Nobody reads a
// key, not even a super, and these rows are open to every role.
func (l *Log) prepare(full bool) {
	l.Content = MaskKeys(l.Content)
	l.Other = maskJSON(l.Other, MaskKeys)
	if !full {
		l.hideEndUser()
	}
}

// where appends the shared log/stats predicate. excludeTests drops the logs of
// channel tests, which new-api's dashboard leaves out and its log list does not.
func (b *qb) where(f Filter, excludeTests bool) {
	if f.AllChannels && len(f.ChannelIDs) == 0 {
		b.w(" FROM logs WHERE 1 = 1")
	} else {
		b.w(" FROM logs WHERE channel_id IN ").in(f.ChannelIDs)
	}
	b.w(" AND created_at >= " + b.arg(f.Start) + " AND created_at < " + b.arg(f.End) + " AND type IN (2, 5, 6)")
	if excludeTests {
		b.w(" AND COALESCE(token_name, '') <> '" + TestTokenName + "'")
	}
	if f.Type != 0 {
		b.w(" AND type = " + b.arg(f.Type))
	}
	b.like("model_name", f.ModelName)
	b.like("username", f.Username)
	b.eq("token_name", f.TokenName)
	b.eq(b.d.q("group"), f.Group)
	b.eq("request_id", f.RequestID)
	b.eq("upstream_request_id", f.UpstreamRequestID)
}

func (b *qb) eq(col, val string) {
	if val != "" {
		b.w(" AND " + col + " = " + b.arg(val))
	}
}

// like is new-api's applyExplicitLogTextFilter: exact unless val holds a %.
func (b *qb) like(col, val string) {
	if strings.Contains(val, "%") {
		b.w(" AND " + col + " LIKE " + b.arg(likeEscaper.Replace(val)) + " ESCAPE '!'")
		return
	}
	b.eq(col, val)
}

// logColumns are the columns Log.dest scans. Open log pages read rows no channel
// of ours wrote, and the columns are nullable: rows from before a column existed
// hold NULL, and one such row must not fail the page.
func logColumns(d dialect) string {
	return "SELECT id, created_at, type, COALESCE(channel_id, 0), COALESCE(model_name, ''), COALESCE(quota, 0), COALESCE(prompt_tokens, 0), " +
		"COALESCE(completion_tokens, 0), COALESCE(use_time, 0), COALESCE(is_stream, FALSE), COALESCE(content, ''), COALESCE(user_id, 0), COALESCE(username, ''), COALESCE(token_id, 0), " +
		"COALESCE(token_name, ''), COALESCE(ip, ''), COALESCE(" + d.q("group") + ", ''), COALESCE(request_id, ''), " +
		"COALESCE(upstream_request_id, ''), COALESCE(other, '')"
}

func (l *Log) dest() []any {
	return []any{&l.ID, &l.CreatedAt, &l.Type, &l.ChannelID, &l.ModelName, &l.Quota, &l.PromptTokens,
		&l.CompletionTokens, &l.UseTime, &l.IsStream, &l.Content, &l.UserID, &l.Username, &l.TokenID,
		&l.TokenName, &l.IP, &l.Group, &l.RequestID, &l.UpstreamRequestID, &l.Other}
}

func logsSQL(d dialect, f Filter) (string, []any) {
	b := &qb{d: d}
	b.w(logColumns(d))
	b.where(f, false)
	// By id, a past range walks the primary key back from today; this order is new-api's idx_created_at_id.
	b.w(" ORDER BY created_at DESC, id DESC LIMIT " + b.arg(f.PageSize) + " OFFSET " + b.arg(f.Offset()))
	return b.sb.String(), b.args
}

// logsCountSQL counts the rows logsSQL pages through.
func logsCountSQL(d dialect, f Filter) (string, []any) {
	b := &qb{d: d}
	b.w("SELECT COUNT(*)")
	b.where(f, false)
	return b.sb.String(), b.args
}

// Logs returns one page of logs and the total count. Without full the rows are
// what hideEndUser leaves.
func (d *DB) Logs(ctx context.Context, f Filter, full bool) ([]Log, int64, error) {
	out := []Log{}
	if f.none() {
		return out, 0, nil
	}
	var total int64
	q, args := logsCountSQL(d.logsD, f)
	if err := d.logs.QueryRowContext(ctx, q, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count new-api logs: %w", err)
	}
	q, args = logsSQL(d.logsD, f)
	rows, err := d.logs.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query new-api logs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var l Log
		if err := rows.Scan(l.dest()...); err != nil {
			return nil, 0, fmt.Errorf("scan new-api log: %w", err)
		}
		l.prepare(full)
		out = append(out, l)
	}
	return out, total, rows.Err()
}

// ChannelNames maps every channel to its name; logs only hold the id.
func (d *DB) ChannelNames(ctx context.Context) (map[int]string, error) {
	rows, err := d.main.QueryContext(ctx, "SELECT id, COALESCE(name, '') FROM channels")
	if err != nil {
		return nil, fmt.Errorf("query new-api channel names: %w", err)
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("scan new-api channel name: %w", err)
		}
		out[id] = name
	}
	return out, rows.Err()
}

// AllChannelGroups lists the groups channels serve, for the group filter of a
// caller who cannot read new-api's own list. Reading them off the logs would scan
// months of rows on every page load, now that nothing narrows those to a few
// channels.
func (d *DB) AllChannelGroups(ctx context.Context) ([]string, error) {
	b := &qb{d: d.mainD}
	b.w("SELECT DISTINCT " + d.mainD.q("group") + " FROM channels")
	return d.channelGroups(ctx, b)
}

// ChannelGroups is AllChannelGroups limited to ids; no ids means no groups.
func (d *DB) ChannelGroups(ctx context.Context, ids []int) ([]string, error) {
	if len(ids) == 0 {
		return []string{}, nil
	}
	b := &qb{d: d.mainD}
	b.w("SELECT DISTINCT " + d.mainD.q("group") + " FROM channels WHERE id IN ").in(ids)
	return d.channelGroups(ctx, b)
}

func (d *DB) channelGroups(ctx context.Context, b *qb) ([]string, error) {
	rows, err := d.main.QueryContext(ctx, b.sb.String(), b.args...)
	if err != nil {
		return nil, fmt.Errorf("query new-api channel groups: %w", err)
	}
	defer rows.Close()
	var lists []string
	for rows.Next() {
		var groups sql.NullString
		if err := rows.Scan(&groups); err != nil {
			return nil, fmt.Errorf("scan new-api channel group: %w", err)
		}
		lists = append(lists, groups.String)
	}
	return Groups(lists...), rows.Err()
}

// Groups is the sorted set of the groups in channels' comma-separated lists.
func Groups(lists ...string) []string {
	out := []string{}
	for _, l := range lists {
		for g := range strings.SplitSeq(l, ",") {
			if g = strings.TrimSpace(g); g != "" {
				out = append(out, g)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
