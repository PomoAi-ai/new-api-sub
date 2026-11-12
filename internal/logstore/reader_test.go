package logstore

import (
	"cmp"
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"newapisub/internal/newapi"
)

// logsDDL is new-api's logs table as the channels tests fake it.
const logsDDL = `CREATE TABLE logs (
  id BIGSERIAL PRIMARY KEY, user_id BIGINT, created_at BIGINT, type BIGINT, content TEXT, username TEXT,
  token_name TEXT, model_name TEXT, quota BIGINT, prompt_tokens BIGINT, completion_tokens BIGINT, use_time BIGINT,
  is_stream BOOLEAN, channel_id BIGINT, token_id BIGINT, "group" TEXT, ip TEXT, request_id VARCHAR(64), upstream_request_id VARCHAR(64), other TEXT)`

// equivalenceLogs covers what the queries tell apart: types, channel tests,
// LIKE patterns with _ and !, cache tokens in other, keys to mask, hours, the
// last minute, and a row of NULLs (null).
func equivalenceLogs() (logs []newapi.Log, null int64) {
	now := time.Now().Unix()
	base := now - now%3600 - 3*3600
	models := []string{"gpt-4o", "gpt_4o", "gpt-4o-mini", "claude!x", "o1"}
	users := []string{"alice", "al_ce", "bob"}
	others := []string{`{"cache_tokens":7,"cache_write_tokens":3}`, `{"cache_tokens": 1234567890123}`, `{"a":{"cache_tokens":2}}`,
		`not json "cache_write_tokens":4`, `{"note":"sk-abcdefghijklmnopqrst","admin_info":{"ip":"203.0.113.7"},"cache_tokens":5}`, ``}
	types := []int{2, 2, 2, 5, 6, 2, 1, 3}
	for i := range 60 {
		l := newapi.Log{
			ID: int64(i + 1), CreatedAt: base + int64(i)*170, Type: types[i%len(types)], ChannelID: 3 + i%3,
			ModelName: models[i%len(models)], Quota: int64(100 + i*7), PromptTokens: int64(10 + i), CompletionTokens: int64(i % 9),
			UseTime: int64(i % 4), IsStream: i%2 == 0, Content: fmt.Sprintf("row %d sk-abcdefghijklmnop%d", i, i),
			UserID: int64(1 + i%3), Username: users[i%len(users)], TokenID: int64(10 + i%2), TokenName: fmt.Sprintf("tk%d", i%2),
			IP: "198.51.100.1", Group: []string{"default", "vip"}[i%2], RequestID: fmt.Sprintf("req-%d", i),
			UpstreamRequestID: fmt.Sprintf("up-%d", i), Other: others[i%len(others)],
		}
		if i%11 == 0 {
			l.TokenName = newapi.TestTokenName
		}
		logs = append(logs, l)
	}
	// Two in the last minute (same second as an older one to tie the order by id), and the NULL row.
	logs = append(logs,
		newapi.Log{ID: 61, CreatedAt: now - 5, Type: 2, ChannelID: 3, ModelName: "o1", Quota: 9, PromptTokens: 4, CompletionTokens: 2, UseTime: 3},
		newapi.Log{ID: 62, CreatedAt: now - 5, Type: 2, ChannelID: 4, ModelName: "o1", Quota: 1, UseTime: 0, TokenName: newapi.TestTokenName},
		newapi.Log{ID: 63, CreatedAt: base + 100, Type: 2, ChannelID: 3},
	)
	return logs, 63
}

func TestMongoReaderAnswersLikeTheSQL(t *testing.T) {
	ctx := context.Background()
	raw, dsn := testPG(t)
	if _, err := raw.ExecContext(ctx, logsDDL); err != nil {
		t.Fatal(err)
	}
	logs, null := equivalenceLogs()
	for _, l := range logs {
		v := []any{l.ID, l.CreatedAt, l.Type, l.ChannelID, l.ModelName, l.Quota, l.PromptTokens, l.CompletionTokens, l.UseTime,
			l.IsStream, l.Content, l.UserID, l.Username, l.TokenID, l.TokenName, l.IP, l.Group, l.RequestID, l.UpstreamRequestID, l.Other}
		if l.ID == null { // alone in its hour and model, so the sums are over NULL only
			for i := 4; i < len(v); i++ {
				v[i] = nil
			}
		}
		if _, err := raw.ExecContext(ctx, `INSERT INTO logs (id, created_at, type, channel_id, model_name, quota, prompt_tokens,
			completion_tokens, use_time, is_stream, content, user_id, username, token_id, token_name, ip, "group", request_id,
			upstream_request_id, other) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`, v...); err != nil {
			t.Fatal(err)
		}
	}
	sqlSrc, err := newapi.OpenDB(ctx, dsn, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sqlSrc.Close)
	mongo := testMongo(t)
	if _, _, err := mongo.Insert(ctx, 7, logs); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mongo.Insert(ctx, 8, logs[:5]); err != nil { // another pool's logs stay out
		t.Fatal(err)
	}
	mongoSrc := mongo.Reader(7)

	now := time.Now().Unix()
	all := newapi.Filter{Start: now - 5*3600, End: now + 1, Page: 1, PageSize: 7}
	with := func(edit func(*newapi.Filter)) newapi.Filter { f := all; edit(&f); return f }
	filters := map[string]newapi.Filter{
		"open pages":          with(func(f *newapi.Filter) { f.AllChannels = true }),
		"open pages, one":     with(func(f *newapi.Filter) { f.AllChannels, f.ChannelIDs = true, []int{4} }),
		"scope":               with(func(f *newapi.Filter) { f.ChannelIDs = []int{3, 4} }),
		"empty scope":         with(func(f *newapi.Filter) {}),
		"consumption, page 3": with(func(f *newapi.Filter) { f.ChannelIDs, f.Type, f.Page = []int{3, 4, 5}, 2, 3 }),
		"refunds":             with(func(f *newapi.Filter) { f.ChannelIDs, f.Type = []int{3, 4, 5}, 6 }),
		"model exact":         with(func(f *newapi.Filter) { f.ChannelIDs, f.ModelName = []int{3, 4, 5}, "gpt-4o" }),
		"model _ is literal":  with(func(f *newapi.Filter) { f.ChannelIDs, f.ModelName = []int{3, 4, 5}, "gpt_%" }),
		"model suffix":        with(func(f *newapi.Filter) { f.ChannelIDs, f.ModelName = []int{3, 4, 5}, "%mini" }),
		"model ! is literal":  with(func(f *newapi.Filter) { f.AllChannels, f.ModelName = true, "claude!%" }),
		"model infix":         with(func(f *newapi.Filter) { f.AllChannels, f.ModelName = true, "%4o%" }),
		"user like":           with(func(f *newapi.Filter) { f.ChannelIDs, f.Username = []int{3, 4, 5}, "al_%" }),
		"token, group":        with(func(f *newapi.Filter) { f.ChannelIDs, f.TokenName, f.Group = []int{3, 4, 5}, "tk1", "vip" }),
		"request ids":         with(func(f *newapi.Filter) { f.AllChannels, f.RequestID, f.UpstreamRequestID = true, "req-5", "up-5" }),
		"channel test token":  with(func(f *newapi.Filter) { f.ChannelIDs, f.TokenName = []int{3, 4, 5}, newapi.TestTokenName }),
		"an hour, page 2": with(func(f *newapi.Filter) {
			f.ChannelIDs, f.Start, f.End, f.Page = []int{3, 4, 5}, all.Start+3600, all.Start+7200, 2
		}),
		"past the last page":   with(func(f *newapi.Filter) { f.ChannelIDs, f.Page = []int{3, 4, 5}, 99 }),
		"channel out of reach": with(func(f *newapi.Filter) { f.ChannelIDs = []int{99} }),
	}
	for name, f := range filters {
		t.Run(name, func(t *testing.T) {
			for _, full := range []bool{true, false} {
				same(t, "Logs", full, func(s newapi.LogSource) (any, error) {
					rows, total, err := s.Logs(ctx, f, full)
					return []any{rows, total}, err
				}, sqlSrc, mongoSrc)
				same(t, "ExportLogs", full, func(s newapi.LogSource) (any, error) {
					var rows []newapi.Log
					err := s.ExportLogs(ctx, f, full, func(l newapi.Log) error { rows = append(rows, l); return nil })
					return rows, err
				}, sqlSrc, mongoSrc)
				same(t, "Flow", full, func(s newapi.LogSource) (any, error) {
					rows, err := s.Flow(ctx, f, full)
					slices.SortFunc(rows, func(a, b newapi.FlowRow) int { return cmp.Compare(fmt.Sprint(a), fmt.Sprint(b)) }) // ties have no order
					return rows, err
				}, sqlSrc, mongoSrc)
			}
			same(t, "Usage", false, func(s newapi.LogSource) (any, error) {
				u, err := s.Usage(ctx, f)
				slices.SortFunc(u.Spans, func(a, b [2]int64) int { return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1])) })
				return u, err
			}, sqlSrc, mongoSrc)
			byKey := func(rows []newapi.StatRow) []newapi.StatRow { // the SQL sorts names by the database's collation
				slices.SortFunc(rows, func(a, b newapi.StatRow) int {
					return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ModelName, b.ModelName), cmp.Compare(a.Username, b.Username))
				})
				return rows
			}
			same(t, "Stats", false, func(s newapi.LogSource) (any, error) { rows, err := s.Stats(ctx, f); return byKey(rows), err }, sqlSrc, mongoSrc)
			same(t, "UserStats", false, func(s newapi.LogSource) (any, error) { rows, err := s.UserStats(ctx, f); return byKey(rows), err }, sqlSrc, mongoSrc)
			same(t, "ChannelStats", false, func(s newapi.LogSource) (any, error) { return s.ChannelStats(ctx, f) }, sqlSrc, mongoSrc)
			same(t, "PerfCounts", false, func(s newapi.LogSource) (any, error) { return s.PerfCounts(ctx, f) }, sqlSrc, mongoSrc)
		})
	}
}

func same(t *testing.T, what string, full bool, query func(newapi.LogSource) (any, error), sqlSrc, mongoSrc newapi.LogSource) {
	t.Helper()
	want, err := query(sqlSrc)
	if err != nil {
		t.Fatalf("%s SQL: %v", what, err)
	}
	got, err := query(mongoSrc)
	if err != nil {
		t.Fatalf("%s Mongo: %v", what, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s (full %v):\nmongo %+v\nsql   %+v", what, full, got, want)
	}
}
