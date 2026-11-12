package logstore

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"newapisub/internal/newapi"
)

// reader is one pool's synced logs as a newapi.LogSource. Every query is the
// SQL of newapi.DB on the same rows (qb.where, statSums and the like), so the
// two sources answer alike; reader_test.go holds them to it.
type reader struct {
	s      *Store
	poolID int64
}

// Reader is the LogSource of a pool that syncs its logs here. Its log rows
// carry the channel name new-api exported; ChannelNames knows none.
func (s *Store) Reader(poolID int64) newapi.LogSource { return &reader{s, poolID} }

func e(k string, v any) bson.E { return bson.E{Key: k, Value: v} }

func d(k string, v any) bson.D { return bson.D{e(k, v)} }

// none is newapi's Filter.none: a scope without channels.
func none(f newapi.Filter) bool { return len(f.ChannelIDs) == 0 && !f.AllChannels }

// match is qb.where. Every document is of type 2, 5 or 6, and holds "" and 0
// where the SQL reads NULL through COALESCE.
func (r *reader) match(f newapi.Filter, excludeTests bool, more ...bson.D) bson.D {
	and := bson.A{d("pool_id", r.poolID)}
	if !f.AllChannels || len(f.ChannelIDs) > 0 {
		and = append(and, d("channel_id", d("$in", f.ChannelIDs)))
	}
	and = append(and, d("created_at", bson.D{e("$gte", f.Start), e("$lt", f.End)}))
	if excludeTests {
		and = append(and, d("token_name", d("$ne", newapi.TestTokenName)))
	}
	if f.Type != 0 {
		and = append(and, d("type", f.Type))
	}
	for _, c := range []struct {
		field, val string
		like       bool
	}{
		{"model_name", f.ModelName, true}, {"username", f.Username, true}, {"token_name", f.TokenName, false},
		{"group", f.Group, false}, {"request_id", f.RequestID, false}, {"upstream_request_id", f.UpstreamRequestID, false},
	} {
		switch {
		case c.val == "":
		case c.like && strings.Contains(c.val, "%"):
			and = append(and, d(c.field, bson.Regex{Pattern: likePattern(c.val), Options: "s"}))
		default:
			and = append(and, d(c.field, c.val))
		}
	}
	for _, m := range more {
		and = append(and, m)
	}
	return d("$and", and)
}

// likePattern is the SQL's LIKE of newapi's likeEscaper output: % is the only
// wildcard and matches anything, newlines too; ! and _ stand for themselves.
func likePattern(val string) string {
	parts := strings.Split(val, "%")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return `\A` + strings.Join(parts, ".*") + `\z`
}

// byNewest is ORDER BY created_at DESC, id DESC.
var byNewest = bson.D{e("created_at", -1), e("log_id", -1)}

func (r *reader) Logs(ctx context.Context, f newapi.Filter, full bool) ([]newapi.Log, int64, error) {
	out := []newapi.Log{}
	if none(f) {
		return out, 0, nil
	}
	m := r.match(f, false)
	total, err := r.s.logs.CountDocuments(ctx, m)
	if err != nil {
		return nil, 0, fmt.Errorf("count mongo logs of pool %d: %w", r.poolID, err)
	}
	cur, err := r.s.logs.Find(ctx, m, options.Find().SetSort(byNewest).SetSkip(int64(f.Offset())).SetLimit(int64(f.PageSize)))
	if err != nil {
		return nil, 0, fmt.Errorf("query mongo logs of pool %d: %w", r.poolID, err)
	}
	err = eachLog(ctx, cur, full, func(l newapi.Log) error { out = append(out, l); return nil })
	if err != nil {
		return nil, 0, fmt.Errorf("query mongo logs of pool %d: %w", r.poolID, err)
	}
	return out, total, nil
}

func eachLog(ctx context.Context, cur *mongo.Cursor, full bool, fn func(newapi.Log) error) error {
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var x doc
		if err := cur.Decode(&x); err != nil {
			return err
		}
		l := x.log()
		l.Prepare(full)
		if err := fn(l); err != nil {
			return err
		}
	}
	return cur.Err()
}

func (r *reader) ExportLogs(ctx context.Context, f newapi.Filter, full bool, fn func(newapi.Log) error) error {
	if none(f) {
		return nil
	}
	// One cursor walks the index in order and fetches in batches; no keyset paging needed.
	cur, err := r.s.logs.Find(ctx, r.match(f, false), options.Find().SetSort(byNewest).SetBatchSize(1000))
	if err != nil {
		return fmt.Errorf("query mongo logs of pool %d for export: %w", r.poolID, err)
	}
	return eachLog(ctx, cur, full, fn)
}

func (r *reader) ChannelNames(context.Context) (map[int]string, error) { return map[int]string{}, nil }

func (r *reader) aggregate(ctx context.Context, what string, pipeline bson.A, out any) error {
	cur, err := r.s.logs.Aggregate(ctx, pipeline)
	if err != nil {
		return fmt.Errorf("query mongo %s of pool %d: %w", what, r.poolID, err)
	}
	if err := cur.All(ctx, out); err != nil {
		return fmt.Errorf("read mongo %s of pool %d: %w", what, r.poolID, err)
	}
	return nil
}

// when is CASE WHEN type = typ THEN then.
func when(typ int, then any, otherwise any) bson.D {
	return d("$cond", bson.A{d("$eq", bson.A{"$type", typ}), then, otherwise})
}

func sum(v any) bson.D { return d("$sum", v) }

// statSums is newapi's statSums.
var statSums = bson.D{
	e("count", sum(when(2, 1, 0))),
	e("quota", sum(when(2, "$quota", when(6, d("$subtract", bson.A{0, "$quota"}), 0)))),
	e("token_used", sum(when(2, d("$add", bson.A{"$prompt_tokens", "$completion_tokens"}), 0))),
	e("cache_read", sum(when(2, "$cache_read", 0))),
	e("cache_write", sum(when(2, "$cache_write", 0))),
}

type statSum struct {
	Count      int64 `bson:"count"`
	Quota      int64 `bson:"quota"`
	TokenUsed  int64 `bson:"token_used"`
	CacheRead  int64 `bson:"cache_read"`
	CacheWrite int64 `bson:"cache_write"`
}

func (s statSum) row(r newapi.StatRow) newapi.StatRow {
	r.Count, r.Quota, r.TokenUsed, r.CacheReadTokens, r.CacheCreationTokens = s.Count, s.Quota, s.TokenUsed, s.CacheRead, s.CacheWrite
	return r
}

func group(id any, sums bson.D) bson.D { return d("$group", append(bson.D{e("_id", id)}, sums...)) }

var notErrors = d("type", d("$ne", 5))

func (r *reader) stats(ctx context.Context, f newapi.Filter, dim string, set func(*newapi.StatRow, string)) ([]newapi.StatRow, error) {
	out := []newapi.StatRow{}
	if len(f.ChannelIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ID struct {
			H   int64  `bson:"h"`
			Dim string `bson:"dim"`
		} `bson:"_id"`
		S statSum `bson:",inline"`
	}
	hour := d("$subtract", bson.A{"$created_at", d("$mod", bson.A{"$created_at", 3600})})
	err := r.aggregate(ctx, "stats", bson.A{
		d("$match", r.match(f, true, notErrors)),
		group(bson.D{e("h", hour), e("dim", "$"+dim)}, statSums),
		d("$sort", bson.D{e("_id.h", 1), e("_id.dim", 1)}),
	}, &rows)
	for _, x := range rows {
		row := x.S.row(newapi.StatRow{CreatedAt: x.ID.H})
		set(&row, x.ID.Dim)
		out = append(out, row)
	}
	return out, err
}

func (r *reader) Stats(ctx context.Context, f newapi.Filter) ([]newapi.StatRow, error) {
	return r.stats(ctx, f, "model_name", func(s *newapi.StatRow, v string) { s.ModelName = v })
}

func (r *reader) UserStats(ctx context.Context, f newapi.Filter) ([]newapi.StatRow, error) {
	return r.stats(ctx, f, "username", func(s *newapi.StatRow, v string) { s.Username = v })
}

func (r *reader) ChannelStats(ctx context.Context, f newapi.Filter) ([]newapi.StatRow, error) {
	out := []newapi.StatRow{}
	if len(f.ChannelIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ID int     `bson:"_id"`
		S  statSum `bson:",inline"`
	}
	err := r.aggregate(ctx, "channel stats", bson.A{
		d("$match", r.match(f, true, notErrors)),
		group("$channel_id", statSums),
		d("$sort", bson.D{e("quota", -1), e("_id", 1)}),
	}, &rows)
	for _, x := range rows {
		out = append(out, x.S.row(newapi.StatRow{CreatedAt: f.Start, ChannelID: x.ID}))
	}
	return out, err
}

func (r *reader) PerfCounts(ctx context.Context, f newapi.Filter) (map[string]newapi.PerfCount, error) {
	out := map[string]newapi.PerfCount{}
	if len(f.ChannelIDs) == 0 {
		return out, nil
	}
	timed := d("$and", bson.A{d("$eq", bson.A{"$type", 2}), d("$gt", bson.A{"$use_time", 0})})
	var rows []struct {
		ID        string `bson:"_id"`
		Requests  int64  `bson:"requests"`
		Success   int64  `bson:"success"`
		UseTime   int64  `bson:"use_time"`
		OutTokens int64  `bson:"out_tokens"`
		OutTime   int64  `bson:"out_time"`
	}
	err := r.aggregate(ctx, "perf", bson.A{
		d("$match", r.match(f, true, d("type", d("$ne", 6)))),
		group("$model_name", bson.D{
			e("requests", sum(1)), e("success", sum(when(2, 1, 0))), e("use_time", sum("$use_time")),
			e("out_tokens", sum(d("$cond", bson.A{timed, "$completion_tokens", 0}))),
			e("out_time", sum(d("$cond", bson.A{timed, "$use_time", 0}))),
		}),
	}, &rows)
	for _, x := range rows {
		out[x.ID] = newapi.PerfCount{Requests: x.Requests, Success: x.Success, UseTime: x.UseTime, OutTokens: x.OutTokens, OutTime: x.OutTime}
	}
	return out, err
}

func (r *reader) Flow(ctx context.Context, f newapi.Filter, full bool) ([]newapi.FlowRow, error) {
	out := []newapi.FlowRow{}
	if len(f.ChannelIDs) == 0 {
		return out, nil
	}
	id := bson.D{e("channel_id", "$channel_id"), e("model_name", "$model_name")}
	if full { // without full, no column about end users is read
		id = append(id, e("user_id", "$user_id"), e("username", "$username"), e("token_id", "$token_id"),
			e("token_name", "$token_name"), e("group", "$group"))
	}
	var rows []struct {
		ID struct {
			ChannelID int    `bson:"channel_id"`
			ModelName string `bson:"model_name"`
			UserID    int64  `bson:"user_id"`
			Username  string `bson:"username"`
			TokenID   int64  `bson:"token_id"`
			TokenName string `bson:"token_name"`
			Group     string `bson:"group"`
		} `bson:"_id"`
		S statSum `bson:",inline"`
	}
	err := r.aggregate(ctx, "flow", bson.A{
		d("$match", r.match(f, true, notErrors)),
		group(id, statSums[:3]), // count, quota and token_used
		d("$sort", d("quota", -1)),
	}, &rows)
	for _, x := range rows {
		out = append(out, newapi.FlowRow{UserID: x.ID.UserID, Username: x.ID.Username, TokenID: x.ID.TokenID,
			TokenName: x.ID.TokenName, UseGroup: x.ID.Group, ChannelID: x.ID.ChannelID, ModelName: x.ID.ModelName,
			TokenUsed: x.S.TokenUsed, Count: x.S.Count, Quota: x.S.Quota})
	}
	return out, err
}

func (r *reader) Usage(ctx context.Context, f newapi.Filter) (newapi.Usage, error) {
	var u newapi.Usage
	if none(f) {
		return u, nil
	}
	f.Type = 2
	var total []struct {
		Quota int64 `bson:"quota"`
	}
	if err := r.aggregate(ctx, "log stat", bson.A{d("$match", r.match(f, false)), group(nil, d("quota", sum("$quota")))}, &total); err != nil {
		return u, err
	}
	if len(total) > 0 {
		u.Quota = total[0].Quota
	}
	f.Start, f.End = time.Now().Unix()-60, math.MaxInt64
	cur, err := r.s.logs.Find(ctx, r.match(f, false),
		options.Find().SetProjection(bson.D{e("created_at", 1), e("use_time", 1), e("prompt_tokens", 1), e("completion_tokens", 1)}))
	if err != nil {
		return u, fmt.Errorf("query mongo log rate of pool %d: %w", r.poolID, err)
	}
	var rows []struct {
		CreatedAt        int64 `bson:"created_at"`
		UseTime          int64 `bson:"use_time"`
		PromptTokens     int64 `bson:"prompt_tokens"`
		CompletionTokens int64 `bson:"completion_tokens"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return u, fmt.Errorf("read mongo log rate of pool %d: %w", r.poolID, err)
	}
	for _, x := range rows {
		u.Tokens += x.PromptTokens + x.CompletionTokens
		u.Spans = append(u.Spans, [2]int64{x.CreatedAt, x.UseTime})
	}
	return u, nil
}
