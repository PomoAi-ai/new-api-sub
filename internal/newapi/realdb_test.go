package newapi

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestChannelQueriesAgainstRealSchema runs the channel queries on databases
// new-api itself migrated, because a wrong column or cast only fails there.
// Set TEST_NEWAPI_DSNS to a comma-separated list of scratch PostgreSQL and
// MySQL DSNs. It inserts and removes rows in their channels tables.
func TestChannelQueriesAgainstRealSchema(t *testing.T) {
	dsns := os.Getenv("TEST_NEWAPI_DSNS")
	if dsns == "" {
		t.Skip("TEST_NEWAPI_DSNS not set")
	}
	for _, dsn := range strings.Split(dsns, ",") {
		driver, dia, err := detectDialect(dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			raw, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			d, err := OpenDB(ctx, dsn, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()

			key := "`key`" // only this test writes the column; production never touches it
			if dia.pg {
				key = `"key"`
			}
			const remark = "realdb-test"
			const full, bare = 2147483001, 2147483002
			clean := func() {
				if _, err := raw.ExecContext(ctx, "DELETE FROM channels WHERE id IN (2147483001, 2147483002)"); err != nil {
					t.Fatal(err)
				}
			}
			clean()
			defer clean()
			b := &qb{d: dia}
			b.w("INSERT INTO channels (id, " + key + ", type, name, status, " + dia.q("group") + ", models, priority, weight, " +
				"used_quota, balance, balance_updated_time, response_time, test_time, created_time, base_url, tag, test_model, " +
				"auto_ban, channel_info, other_info, remark) VALUES (" +
				b.arg(full) + ", 'secret-key', 20, 'n', 1, 'g', 'm1,m2', 5, 3, 7, 1.25, 11, 12, 13, 14, 'https://x', 't', 'm1', 1, " +
				`'{"is_multi_key":true,"multi_key_size":2,"multi_key_disabled_reason":{"0":"boom"}}', '{"status_reason":"r"}', ` + b.arg(remark) + ")")
			if _, err := raw.ExecContext(ctx, b.sb.String(), b.args...); err != nil {
				t.Fatal(err)
			}
			// What gorm leaves NULL when a value was never set.
			b = &qb{d: dia}
			b.w("INSERT INTO channels (id, " + key + ", type, status, priority, weight, base_url, auto_ban, " + dia.q("group") + ", remark) VALUES (" +
				b.arg(bare) + ", 'secret-key', 1, 2, NULL, NULL, NULL, NULL, NULL, " + b.arg(remark) + ")")
			if _, err := raw.ExecContext(ctx, b.sb.String(), b.args...); err != nil {
				t.Fatal(err)
			}

			chs, err := d.Channels(ctx, []int{full, bare})
			if err != nil {
				t.Fatal(err)
			}
			c := chs[full]
			if c.Name != "n" || c.Group != "g" || *c.Priority != 5 || *c.Weight != 3 || c.Balance != 1.25 ||
				c.BalanceUpdatedTime != 11 || *c.BaseURL != "https://x" || *c.Tag != "t" || *c.TestModel != "m1" ||
				*c.AutoBan != 1 || c.OtherInfo != `{"status_reason":"r"}` {
				t.Errorf("full row: %+v", c)
			}
			var info map[string]any
			if err := json.Unmarshal(c.ChannelInfo, &info); err != nil || info["multi_key_size"] != float64(2) || info["multi_key_disabled_reason"] != nil {
				t.Errorf("channel_info = %s (%v)", c.ChannelInfo, err)
			}
			n := chs[bare]
			if n.Priority != nil || n.Weight != nil || n.BaseURL != nil || n.AutoBan != nil || n.Tag != nil || n.TestModel != nil ||
				string(n.ChannelInfo) != "{}" || n.Group != "" {
				t.Errorf("NULL columns: %+v", n)
			}

			names, err := d.ChannelNames(ctx)
			if err != nil || names[full] != "n" || names[bare] != "" {
				t.Errorf("channel names, one NULL: %v %v", names, err)
			}

			ids, err := d.ChannelIDsByRemark(ctx, remark)
			if err != nil || !slices.Equal(ids, []int{full, bare}) {
				t.Errorf("by remark: %v %v", ids, err)
			}
			if ids, err := d.ChannelIDsByRemark(ctx, "no-such-remark"); err != nil || len(ids) != 0 {
				t.Errorf("by unknown remark: %v %v", ids, err)
			}
		})
	}
}

// TestLogQueriesAgainstRealSchema runs the log aggregates on new-api's own
// schema. Whatever other holds must not fail a query: it is free text to the
// database, and a failed cast would blank the whole page.
func TestLogQueriesAgainstRealSchema(t *testing.T) {
	dsns := os.Getenv("TEST_NEWAPI_DSNS")
	if dsns == "" {
		t.Skip("TEST_NEWAPI_DSNS not set")
	}
	for _, dsn := range strings.Split(dsns, ",") {
		driver, dia, err := detectDialect(dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			raw, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			d, err := OpenDB(ctx, dsn, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()

			const channel = 2147483003
			clean := func() {
				if _, err := raw.ExecContext(ctx, "DELETE FROM logs WHERE model_name = 'legacy-null-row' OR channel_id = "+strconv.Itoa(channel)); err != nil {
					t.Fatal(err)
				}
			}
			clean()
			defer clean()
			const hour = 1_000_000_000 - 1_000_000_000%3600
			add := func(typ, at int, token, model, other string) {
				t.Helper()
				b := &qb{d: dia}
				b.w("INSERT INTO logs (user_id, created_at, type, content, username, token_name, model_name, quota, prompt_tokens, " +
					"completion_tokens, use_time, is_stream, channel_id, token_id, " + dia.q("group") + ", ip, request_id, upstream_request_id, other) VALUES (7, " +
					b.arg(at) + ", " + b.arg(typ) + ", 'c', 'end-user', " + b.arg(token) + ", " + b.arg(model) +
					", 10, 1, 2, 3, " + b.arg(true) + ", " + b.arg(channel) + ", 8, 'vip', '203.0.113.9', 'req', 'up-req', " + b.arg(other) + ")")
				if _, err := raw.ExecContext(ctx, b.sb.String(), b.args...); err != nil {
					t.Fatal(err)
				}
			}
			good := `{"cache_tokens":100,"cache_write_tokens":20,"x":1}`
			add(2, hour+1, "t", "real_model", good)
			add(2, hour+2, "t", "real_model", `{"cache_tokens": 100, "cache_write_tokens": 20}`) // not how gorm spaces it, but valid
			for _, junk := range []string{"", "not json", "{", "null", "[1,2]", `"text"`, "12",
				`{"cache_tokens":null,"cache_write_tokens":"abc"}`, `{"cache_tokens":{"a":1},"cache_write_tokens":[1]}`,
				`{"cache_tokens":-5}`, `{"cache_tokens":99999999999999999999999}`} {
				add(2, hour+3, "t", "junk", junk)
			}
			add(6, hour+3, "t", "real_model", good)          // nets out, its tokens do not count
			add(5, hour+3, "t", "real_model", good)          // an error: neither quota nor count
			add(2, hour+3, "Model test", "real_model", good) // in the list, never in statistics
			add(2, hour+3600, "t", "realxmodel", good)       // next hour, and _ must not match the x
			f := Filter{ChannelIDs: []int{channel}, Start: hour, End: hour + 7200}

			rows, err := d.Stats(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			byKey := map[string]StatRow{}
			for _, r := range rows {
				byKey[strconv.FormatInt(r.CreatedAt, 10)+"/"+r.ModelName] = r
			}
			real := byKey[strconv.Itoa(hour)+"/real_model"]
			if real.Count != 2 || real.Quota != 10 || real.TokenUsed != 6 || real.CacheReadTokens != 200 || real.CacheCreationTokens != 40 {
				t.Errorf("real_model: %+v", real)
			}
			junk := byKey[strconv.Itoa(hour)+"/junk"]
			if junk.Count != 11 || junk.Quota != 110 || junk.TokenUsed != 33 {
				t.Errorf("junk: %+v", junk)
			}
			if junk.CacheReadTokens != 0 || junk.CacheCreationTokens != 0 {
				t.Errorf("junk other counted as tokens: %+v", junk)
			}
			next := byKey[strconv.Itoa(hour+3600)+"/realxmodel"]
			if next.Count != 1 || next.CacheReadTokens != 100 || next.CacheCreationTokens != 20 {
				t.Errorf("next hour: %+v", next)
			}

			users, err := d.UserStats(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			if len(users) != 2 || users[0].Username != "end-user" || users[0].ModelName != "" || users[0].Count != 13 {
				t.Errorf("users: %+v", users)
			}

			like := f
			like.ModelName = "real_%"
			if rows, err = d.Stats(ctx, like); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].ModelName != "real_model" {
				t.Errorf("real_%%: %+v", rows)
			}
			like.ModelName = "%model"
			if rows, err = d.Stats(ctx, like); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 2 {
				t.Errorf("%%model: %+v", rows)
			}

			only := f
			only.Username, only.TokenName, only.Group, only.RequestID, only.UpstreamRequestID = "end%", "t", "vip", "req", "up-req"
			stat, err := d.Usage(ctx, only)
			if err != nil {
				t.Fatal(err)
			}
			if stat.Quota != 140 || len(stat.Spans) != 0 || stat.Tokens != 0 { // 14 consumption rows; these logs are long past 60s
				t.Errorf("stat: %+v", stat)
			}

			counts, err := d.PerfCounts(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			perf := BuildPerf(f, counts)
			if perf.Summary == nil || perf.Summary.AvgLatencyMs != 3000 || len(perf.Models) != 3 || perf.Models[0].ModelName != "junk" {
				t.Errorf("perf: %+v", perf)
			}

			flow, err := d.Flow(ctx, f, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(flow) != 3 || flow[0].Username != "end-user" || flow[0].TokenName != "t" || flow[0].UseGroup != "vip" || flow[0].UserID != 7 || flow[0].TokenID != 8 {
				t.Errorf("full flow: %+v", flow)
			}
			flow, err = d.Flow(ctx, f, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(flow) != 3 || flow[0].Username != "" || flow[0].UseGroup != "" || flow[0].UserID != 0 || flow[0].ChannelID != channel {
				t.Errorf("basic flow: %+v", flow)
			}

			for _, full := range []bool{true, false} {
				var got []Log
				if err := d.ExportLogs(ctx, f, full, func(l Log) error {
					got = append(got, l)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if len(got) != 17 || !slices.IsSortedFunc(got, func(a, b Log) int {
					return cmp.Or(cmp.Compare(b.CreatedAt, a.CreatedAt), cmp.Compare(b.ID, a.ID))
				}) {
					t.Fatalf("full=%v: %d rows", full, len(got))
				}
				page := f
				page.Page, page.PageSize = 1, 17
				items, total, err := d.Logs(ctx, page, full)
				if err != nil {
					t.Fatal(err)
				}
				if total != 17 || !slices.Equal(items, got) {
					t.Errorf("full=%v: the list has other rows than the export: %d\n%+v", full, total, items)
				}
				test := slices.IndexFunc(got, func(l Log) bool { return l.TokenName == TestTokenName })
				if test < 0 {
					t.Errorf("full=%v: no test log: %+v", full, got)
				}
				newest := got[0]
				if newest.Group != "vip" || newest.RequestID != "req" || newest.UpstreamRequestID != "up-req" {
					t.Errorf("full=%v newest: %+v", full, newest)
				}
				hidden := newest.Username == "" && newest.IP == "" && newest.TokenName == "" && newest.UserID == 0 && newest.TokenID == 0
				if hidden == full {
					t.Errorf("full=%v newest: %+v", full, newest)
				}
			}
			open := f
			open.ChannelIDs, open.AllChannels, open.Page, open.PageSize = nil, true, 1, 200
			if logs, total, err := d.Logs(ctx, open, false); err != nil || total == 0 || len(logs) == 0 {
				t.Errorf("open logs: %d rows of %d, %v", len(logs), total, err)
			}
			if stat, err := d.Usage(ctx, open); err != nil || stat.Quota == 0 {
				t.Errorf("open stat: %+v, %v", stat, err)
			}
			// A row from before these columns existed: the open pages read it too.
			if _, err := raw.ExecContext(ctx, "INSERT INTO logs (created_at, type, model_name) VALUES ("+strconv.Itoa(hour)+", 2, 'legacy-null-row')"); err != nil {
				t.Fatal(err)
			}
			open.ModelName = "legacy-null-row"
			if logs, total, err := d.Logs(ctx, open, false); err != nil || total != 1 || logs[0].ChannelID != 0 || logs[0].IsStream || logs[0].Quota != 0 {
				t.Errorf("row with NULL columns: %+v, %d, %v", logs, total, err)
			}
			if _, err := d.ChannelNames(ctx); err != nil {
				t.Errorf("channel names: %v", err)
			}
			if _, err := d.AllChannelGroups(ctx); err != nil {
				t.Errorf("channel groups: %v", err)
			}
			if _, err := d.ChannelGroups(ctx, []int{1}); err != nil {
				t.Errorf("channel groups of ids: %v", err)
			}
			if _, err := d.UsedQuota(ctx, []int{1}); err != nil {
				t.Errorf("used quota: %v", err)
			}
		})
	}
}

// TestExportCrossesBatchesWithoutLosingRows puts a batch boundary inside one
// second, and has the rows of the older second carry higher ids, so a cursor that
// compares only one of created_at and id repeats or drops rows.
func TestExportCrossesBatchesWithoutLosingRows(t *testing.T) {
	dsns := os.Getenv("TEST_NEWAPI_DSNS")
	if dsns == "" {
		t.Skip("TEST_NEWAPI_DSNS not set")
	}
	for _, dsn := range strings.Split(dsns, ",") {
		driver, _, err := detectDialect(dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			raw, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			d, err := OpenDB(ctx, dsn, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()

			const channel = 2147483004
			clean := func() {
				if _, err := raw.ExecContext(ctx, "DELETE FROM logs WHERE channel_id = "+strconv.Itoa(channel)); err != nil {
					t.Fatal(err)
				}
			}
			clean()
			defer clean()

			const newer, second, older = 1_000_000_002, 1_000_000_001, 1_000_000_000
			const firstID = 2147000000
			type row struct{ id, at int64 }
			var want []row
			add := func(at int64, ids ...int64) {
				t.Helper()
				var values strings.Builder
				for i, id := range ids {
					if i > 0 {
						values.WriteString(", ")
					}
					fmt.Fprintf(&values, "(%d, %d, 2, %d)", id, at, channel)
				}
				if _, err := raw.ExecContext(ctx, "INSERT INTO logs (id, created_at, type, channel_id) VALUES "+values.String()); err != nil {
					t.Fatal(err)
				}
			}
			// Newest first, as the export must return them.
			wantSecond := func(at int64, ids []int64) {
				add(at, ids...)
				for _, id := range ids {
					want = append(want, row{id, at})
				}
			}
			wantSecond(newer, []int64{firstID + 2, firstID + 1})
			var same []int64
			for i := range int64(exportBatchRows + 10) {
				same = append(same, firstID+100+exportBatchRows+10-i)
			}
			wantSecond(second, same)
			wantSecond(older, []int64{firstID + 200_000, firstID + 100_000})

			export := func(f Filter) []row {
				t.Helper()
				var got []row
				if err := d.ExportLogs(ctx, f, true, func(l Log) error {
					got = append(got, row{l.ID, l.CreatedAt})
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				return got
			}
			f := Filter{ChannelIDs: []int{channel}, Start: older, End: newer + 1}
			if got := export(f); !slices.Equal(got, want) {
				t.Errorf("across batches: %d rows, want %d", len(got), len(want))
			}

			// The last batch full: the query after it finds nothing and ends the export.
			clean()
			want = nil
			wantSecond(second, same[:exportBatchRows])
			if got := export(f); !slices.Equal(got, want) {
				t.Errorf("a full last batch: %d rows, want %d", len(got), len(want))
			}
		})
	}
}
