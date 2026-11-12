package newapi

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"time"
)

// otherInt reads an integer field of logs.other, which new-api writes as a JSON
// object but which can be empty or hold anything in other rows. A JSON cast
// would fail the whole query on one such row, so PostgreSQL (no safe cast before
// 16) matches the text and MySQL checks JSON_VALID first, in a CASE because only
// that fixes the order. Anything but a non-negative integer of at most 12 digits
// is 0, which also keeps a bad row from overflowing a sum.
func (d dialect) otherInt(field string) string {
	if d.pg {
		// shortcut: the text match also finds a same-named field inside a nested object, upgrade to pg_input_is_valid once PostgreSQL 16 is the minimum.
		return `COALESCE(substring(other from '"` + field + `":[[:space:]]*([0-9]{1,12})([^0-9]|$)')::bigint, 0)`
	}
	v := `JSON_EXTRACT(other, '$.` + field + `')`
	return `CASE WHEN JSON_VALID(other) THEN CASE WHEN JSON_TYPE(` + v + `) = 'INTEGER' AND ` + v + ` BETWEEN 0 AND 999999999999 ` +
		`THEN CAST(` + v + ` AS SIGNED) ELSE 0 END ELSE 0 END`
}

// The fields new-api itself feeds its dashboard from (model/log.go).
const (
	otherCacheRead  = "cache_tokens"
	otherCacheWrite = "cache_write_tokens"
)

// StatRow is one hour of use of a model or by an end user, shaped like a row of
// new-api's quota_data. Count covers consumption only; refunds reduce Quota.
type StatRow struct {
	CreatedAt           int64  `json:"created_at"`             // start of the hour; for ChannelStats, of the range
	ChannelID           int    `json:"channel_id,omitempty"`   // ChannelStats only
	ChannelName         string `json:"channel_name,omitempty"` // ChannelStats only, filled by the caller
	ModelName           string `json:"model_name,omitempty"`
	Username            string `json:"username,omitempty"`
	Count               int64  `json:"count"`
	Quota               int64  `json:"quota"`
	TokenUsed           int64  `json:"token_used"`
	CacheReadTokens     int64  `json:"cache_read_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
}

// sum0 is SUM(x), 0 rather than NULL over rows where x is only NULL: old rows
// hold NULL quota and tokens.
func sum0(x string) string { return "COALESCE(SUM(" + x + "), 0)" }

// statSums are the aggregates StatRow holds, in its order. Quota is named q.
func statSums(d dialect) string {
	return sum0("CASE WHEN type = 2 THEN 1 ELSE 0 END") + ", " +
		sum0("CASE WHEN type = 2 THEN quota WHEN type = 6 THEN -quota ELSE 0 END") + " AS q, " +
		sum0("CASE WHEN type = 2 THEN prompt_tokens + completion_tokens ELSE 0 END") + ", " +
		sum0("CASE WHEN type = 2 THEN "+d.otherInt(otherCacheRead)+" ELSE 0 END") + ", " +
		sum0("CASE WHEN type = 2 THEN "+d.otherInt(otherCacheWrite)+" ELSE 0 END")
}

func statsSQL(d dialect, f Filter, dim string) (string, []any) {
	b := &qb{d: d}
	b.w("SELECT created_at - created_at % 3600 AS h, COALESCE(" + dim + ", '') AS dim, " + statSums(d))
	b.where(f, true)
	b.w(" AND type <> 5 GROUP BY h, dim ORDER BY h, dim")
	return b.sb.String(), b.args
}

func (d *DB) queryStats(ctx context.Context, f Filter, dim string, field func(*StatRow) *string) ([]StatRow, error) {
	out := []StatRow{}
	if len(f.ChannelIDs) == 0 {
		return out, nil
	}
	q, args := statsSQL(d.logsD, f, dim)
	rows, err := d.logs.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query new-api stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r StatRow
		if err := rows.Scan(&r.CreatedAt, field(&r), &r.Count, &r.Quota, &r.TokenUsed,
			&r.CacheReadTokens, &r.CacheCreationTokens); err != nil {
			return nil, fmt.Errorf("scan new-api stats: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Stats aggregates logs per hour and model.
func (d *DB) Stats(ctx context.Context, f Filter) ([]StatRow, error) {
	return d.queryStats(ctx, f, "model_name", func(r *StatRow) *string { return &r.ModelName })
}

// UserStats aggregates logs per hour and end user, so only a super may see it.
func (d *DB) UserStats(ctx context.Context, f Filter) ([]StatRow, error) {
	return d.queryStats(ctx, f, "username", func(r *StatRow) *string { return &r.Username })
}

// ChannelStats totals the whole range per channel, biggest consumer first.
func (d *DB) ChannelStats(ctx context.Context, f Filter) ([]StatRow, error) {
	out := []StatRow{}
	if len(f.ChannelIDs) == 0 {
		return out, nil
	}
	b := &qb{d: d.logsD}
	b.w("SELECT channel_id, " + statSums(d.logsD))
	b.where(f, true)
	b.w(" AND type <> 5 GROUP BY channel_id ORDER BY q DESC, channel_id")
	rows, err := d.logs.QueryContext(ctx, b.sb.String(), b.args...)
	if err != nil {
		return nil, fmt.Errorf("query new-api channel stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		r := StatRow{CreatedAt: f.Start}
		if err := rows.Scan(&r.ChannelID, &r.Count, &r.Quota, &r.TokenUsed, &r.CacheReadTokens, &r.CacheCreationTokens); err != nil {
			return nil, fmt.Errorf("scan new-api channel stats: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LogStat is the usage strip above the log table.
type LogStat struct {
	Quota int64 `json:"quota"`
	RPM   int64 `json:"rpm"`
	TPM   int64 `json:"tpm"`
	// Concurrency is the most requests in flight at once in the last 60 seconds.
	Concurrency int64 `json:"concurrency"`
}

// Usage is what LogStat is made of, for one source: consumption whatever type
// f asks for, and the tokens and {created_at, use_time} of every log of the
// last 60 seconds whatever range it asks for. Channel tests count, as they are
// in the list above it.
type Usage struct {
	Quota, Tokens int64
	Spans         [][2]int64
}

// Usage is new-api's SumUsedQuota for f; SumUsage turns it into a LogStat.
func (d *DB) Usage(ctx context.Context, f Filter) (Usage, error) {
	var u Usage
	if f.none() {
		return u, nil
	}
	f.Type = 2
	b := &qb{d: d.logsD}
	b.w("SELECT COALESCE(SUM(quota), 0)")
	b.where(f, false)
	if err := d.logs.QueryRowContext(ctx, b.sb.String(), b.args...).Scan(&u.Quota); err != nil {
		return u, fmt.Errorf("query new-api log stat: %w", err)
	}
	f.Start, f.End = time.Now().Unix()-60, math.MaxInt64
	b = &qb{d: d.logsD}
	b.w("SELECT created_at, COALESCE(use_time, 0), COALESCE(prompt_tokens, 0) + COALESCE(completion_tokens, 0)")
	b.where(f, false)
	rows, err := d.logs.QueryContext(ctx, b.sb.String(), b.args...)
	if err != nil {
		return u, fmt.Errorf("query new-api log rate: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var end, took, tokens int64
		if err := rows.Scan(&end, &took, &tokens); err != nil {
			return u, fmt.Errorf("scan new-api log rate: %w", err)
		}
		u.Tokens += tokens
		u.Spans = append(u.Spans, [2]int64{end, took})
	}
	return u, rows.Err()
}

// peakConcurrency is the most requests in flight during any second of [from, to].
// Each span is a log's {created_at, use_time}: it ran for use_time seconds up to
// created_at, 0 meaning under a second. new-api writes a log when a request ends
// and keeps no count of the running ones, so this cannot see those yet.
func peakConcurrency(spans [][2]int64, from, to int64) int64 {
	delta := make([]int64, to-from+2)
	for _, s := range spans {
		first, last := max(s[0]-max(s[1], 1)+1, from), min(s[0], to)
		if first <= last {
			delta[first-from]++
			delta[last-from+1]--
		}
	}
	var running, peak int64
	for _, d := range delta {
		running += d
		peak = max(peak, running)
	}
	return peak
}

// PerfSummary and the types around it have the JSON shape of new-api's
// /api/perf-metrics/summary.
type PerfSummary struct {
	AvgLatencyMs int64   `json:"avg_latency_ms"`
	SuccessRate  float64 `json:"success_rate"`
	AvgTPS       float64 `json:"avg_tps"`
}

type PerfModel struct {
	ModelName string `json:"model_name"`
	PerfSummary
	requests int64
}

type Perf struct {
	Summary     *PerfSummary `json:"summary"` // nil without requests, rather than a made-up 0%
	WindowStart int64        `json:"window_start"`
	WindowEnd   int64        `json:"window_end"`
	Models      []PerfModel  `json:"models"`
}

// PerfCount is what logs offer in place of new-api's in-memory samples, for a
// model: requests are consumption and error rows (errors count per failed
// attempt), latency has the second resolution of use_time, and throughput only
// counts requests that took a measurable time.
type PerfCount struct{ Requests, Success, UseTime, OutTokens, OutTime int64 }

func (c *PerfCount) add(o PerfCount) {
	c.Requests += o.Requests
	c.Success += o.Success
	c.UseTime += o.UseTime
	c.OutTokens += o.OutTokens
	c.OutTime += o.OutTime
}

func (c PerfCount) summary() PerfSummary {
	round := func(x float64) float64 { return math.Round(x*100) / 100 }
	s := PerfSummary{
		AvgLatencyMs: c.UseTime * 1000 / c.Requests,
		SuccessRate:  round(float64(c.Success) / float64(c.Requests) * 100),
	}
	if c.OutTime > 0 {
		s.AvgTPS = round(float64(c.OutTokens) / float64(c.OutTime))
	}
	return s
}

func perfSQL(d dialect, f Filter) (string, []any) {
	b := &qb{d: d}
	b.w("SELECT COALESCE(model_name, '') AS m, COUNT(*), " + sum0("CASE WHEN type = 2 THEN 1 ELSE 0 END") + ", " +
		sum0("use_time") + ", " + sum0("CASE WHEN type = 2 AND use_time > 0 THEN completion_tokens ELSE 0 END") + ", " +
		sum0("CASE WHEN type = 2 AND use_time > 0 THEN use_time ELSE 0 END"))
	b.where(f, true)
	b.w(" AND type <> 6 GROUP BY m")
	return b.sb.String(), b.args
}

// PerfCounts counts request health per model over f's range; BuildPerf
// summarizes them.
func (d *DB) PerfCounts(ctx context.Context, f Filter) (map[string]PerfCount, error) {
	out := map[string]PerfCount{}
	if len(f.ChannelIDs) == 0 {
		return out, nil
	}
	q, args := perfSQL(d.logsD, f)
	rows, err := d.logs.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query new-api perf: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var c PerfCount
		if err := rows.Scan(&name, &c.Requests, &c.Success, &c.UseTime, &c.OutTokens, &c.OutTime); err != nil {
			return nil, fmt.Errorf("scan new-api perf: %w", err)
		}
		out[name] = c
	}
	return out, rows.Err()
}

// BuildPerf summarizes counts over f's range, busiest model first.
func BuildPerf(f Filter, counts map[string]PerfCount) Perf {
	out := Perf{WindowStart: f.Start, WindowEnd: f.End - 1, Models: []PerfModel{}}
	var all PerfCount
	for name, c := range counts {
		all.add(c)
		out.Models = append(out.Models, PerfModel{ModelName: name, PerfSummary: c.summary(), requests: c.Requests})
	}
	slices.SortFunc(out.Models, func(a, b PerfModel) int {
		return cmp.Or(cmp.Compare(b.requests, a.requests), cmp.Compare(a.ModelName, b.ModelName))
	})
	if all.Requests > 0 {
		s := all.summary()
		out.Summary = &s
	}
	return out
}

// FlowRow has the JSON shape of new-api's /api/data/flow. Only full rows carry
// who sent the requests.
type FlowRow struct {
	UserID      int64  `json:"user_id,omitempty"`
	Username    string `json:"username,omitempty"`
	TokenID     int64  `json:"token_id,omitempty"`
	TokenName   string `json:"token_name,omitempty"`
	UseGroup    string `json:"use_group,omitempty"`
	ChannelID   int    `json:"channel_id"`
	ChannelName string `json:"channel_name,omitempty"` // filled by the caller from its own mapping
	ModelName   string `json:"model_name"`
	TokenUsed   int64  `json:"token_used"`
	Count       int64  `json:"count"`
	Quota       int64  `json:"quota"`
}

func flowSQL(d dialect, f Filter, full bool) (string, []any) {
	cols, group := "", "channel_id, m"
	if full {
		cols = "COALESCE(user_id, 0) AS uid, COALESCE(username, '') AS un, COALESCE(token_id, 0) AS tid, " +
			"COALESCE(token_name, '') AS tn, COALESCE(" + d.q("group") + ", '') AS ug, "
		group = "uid, un, tid, tn, ug, " + group
	}
	b := &qb{d: d}
	b.w("SELECT " + cols + "channel_id, COALESCE(model_name, '') AS m, " +
		sum0("CASE WHEN type = 2 THEN prompt_tokens + completion_tokens ELSE 0 END") + ", " +
		sum0("CASE WHEN type = 2 THEN 1 ELSE 0 END") + ", " +
		sum0("CASE WHEN type = 2 THEN quota WHEN type = 6 THEN -quota ELSE 0 END") + " AS q")
	b.where(f, true)
	b.w(" AND type <> 5 GROUP BY " + group + " ORDER BY q DESC")
	return b.sb.String(), b.args
}

// Flow aggregates logs along the columns of the flow chart. Without full the
// query does not read any column about end users.
func (d *DB) Flow(ctx context.Context, f Filter, full bool) ([]FlowRow, error) {
	out := []FlowRow{}
	if len(f.ChannelIDs) == 0 {
		return out, nil
	}
	q, args := flowSQL(d.logsD, f, full)
	rows, err := d.logs.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query new-api flow: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r FlowRow
		dest := []any{&r.ChannelID, &r.ModelName, &r.TokenUsed, &r.Count, &r.Quota}
		if full {
			dest = append([]any{&r.UserID, &r.Username, &r.TokenID, &r.TokenName, &r.UseGroup}, dest...)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan new-api flow: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// exportBatchRows keeps every export query a LIMIT query, which the planner starts
// fast: without a LIMIT it reads and sorts every match before the first row. The
// SQL holds it as a literal because a generic plan cannot see a bound LIMIT.
const exportBatchRows = 5000

// ExportLogs calls fn with each log of f, newest first, one row at a time so an
// export never holds the result in memory. Without full the rows are what
// hideEndUser leaves.
func (d *DB) ExportLogs(ctx context.Context, f Filter, full bool, fn func(Log) error) error {
	if f.none() {
		return nil
	}
	var last Log
	for {
		b := &qb{d: d.logsD}
		b.w(logColumns(d.logsD))
		b.where(f, false) // the same rows as the log table
		if last.ID != 0 {
			b.w(" AND (created_at < " + b.arg(last.CreatedAt) + " OR id < " + b.arg(last.ID) + ")")
		}
		// By id, a past range walks the primary key back from today; this order is new-api's idx_created_at_id.
		b.w(fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT %d", exportBatchRows))
		n, err := d.exportBatch(ctx, b, &last, full, fn)
		if err != nil || n < exportBatchRows {
			return err
		}
		f.End = last.CreatedAt + 1 // so the next batch enters the index at this second, not at the range's end
	}
}

// exportBatch runs one query of ExportLogs and leaves its last row in last.
func (d *DB) exportBatch(ctx context.Context, b *qb, last *Log, full bool, fn func(Log) error) (int, error) {
	rows, err := d.logs.QueryContext(ctx, b.sb.String(), b.args...)
	if err != nil {
		return 0, fmt.Errorf("query new-api logs for export: %w", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var l Log
		if err := rows.Scan(l.dest()...); err != nil {
			return n, fmt.Errorf("scan new-api log for export: %w", err)
		}
		*last = l
		n++
		l.prepare(full)
		if err := fn(l); err != nil {
			return n, err
		}
	}
	return n, rows.Err()
}
