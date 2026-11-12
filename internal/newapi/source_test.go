package newapi

import (
	"cmp"
	"errors"
	"slices"
	"testing"
)

// poolLogs are the logs of three pools, newest first as each pool orders them,
// with seconds and ids shared across pools.
func poolLogs() [][]Log {
	var parts [][]Log
	for pool := int64(1); pool <= 3; pool++ {
		var logs []Log
		for i := range 20 {
			logs = append(logs, Log{PoolID: pool, ID: int64(100 - i), CreatedAt: int64(1000 - i/3*int(pool))})
		}
		slices.SortFunc(logs, func(a, b Log) int { return cmp.Or(cmp.Compare(b.CreatedAt, a.CreatedAt), cmp.Compare(b.ID, a.ID)) })
		parts = append(parts, logs)
	}
	return parts
}

func TestMergedPagesAreOneOrder(t *testing.T) {
	parts := poolLogs()
	all := MergeLogs(parts, 0, 1000)
	if len(all) != 60 {
		t.Fatalf("%d rows", len(all))
	}
	// Ties on the second go by pool, then by id as within a pool.
	if !slices.IsSortedFunc(all, func(a, b Log) int {
		return cmp.Or(cmp.Compare(b.CreatedAt, a.CreatedAt), cmp.Compare(a.PoolID, b.PoolID), cmp.Compare(b.ID, a.ID))
	}) {
		t.Fatalf("not in order: %+v", all)
	}
	// Page by page, each pool read up to the page's end as the caller does, the pages add up to the whole.
	for _, size := range []int{1, 7, 25} {
		var paged []Log
		for offset := 0; offset < 70; offset += size {
			var head [][]Log
			for _, p := range parts {
				head = append(head, p[:min(len(p), offset+size)])
			}
			paged = append(paged, MergeLogs(head, offset, size)...)
		}
		if !slices.Equal(paged, all) {
			t.Errorf("size %d: pages differ from the whole", size)
		}
	}
	if got := MergeLogs(nil, 0, 10); got == nil || len(got) != 0 {
		t.Errorf("no pools: %#v", got)
	}
}

func TestMergeExportMatchesTheMergedList(t *testing.T) {
	parts := poolLogs()
	exports := make([]func(func(Log) error) error, len(parts))
	for i, p := range parts {
		exports[i] = func(fn func(Log) error) error {
			for _, l := range p {
				if err := fn(l); err != nil {
					return err
				}
			}
			return nil
		}
	}
	var got []Log
	if err := MergeExport(exports, func(l Log) error { got = append(got, l); return nil }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, MergeLogs(parts, 0, 1000)) {
		t.Errorf("export order differs from the list")
	}

	// A pool failing midway fails the export; the caller stopping ends every pool.
	broken := errors.New("pool 2 went away")
	exports[1] = func(fn func(Log) error) error {
		if err := fn(parts[1][0]); err != nil {
			return err
		}
		return broken
	}
	if err := MergeExport(exports, func(Log) error { return nil }); !errors.Is(err, broken) {
		t.Errorf("pool error: %v", err)
	}
	stop := errors.New("client gone")
	n := 0
	if err := MergeExport(exports[:1], func(Log) error { n++; return stop }); !errors.Is(err, stop) || n != 1 {
		t.Errorf("caller error: %v after %d rows", err, n)
	}
}

func TestSumUsageCountsConcurrencyAcrossPools(t *testing.T) {
	const now = 2000
	// Each pool has one request at a time; together two ran at once.
	a := Usage{Quota: 10, Tokens: 100, Spans: [][2]int64{{now - 30, 10}, {now - 10, 5}}}
	b := Usage{Quota: 5, Tokens: 50, Spans: [][2]int64{{now - 28, 5}}}
	st := SumUsage([]Usage{a, b}, now)
	if st != (LogStat{Quota: 15, RPM: 3, TPM: 150, Concurrency: 2}) {
		t.Errorf("%+v", st)
	}
}

func TestMergedStatsAddUp(t *testing.T) {
	stats := MergeStats([][]StatRow{
		{{CreatedAt: 3600, ModelName: "b", Count: 1, Quota: 10}, {CreatedAt: 7200, ModelName: "a", Count: 1}},
		{{CreatedAt: 3600, ModelName: "b", Count: 2, Quota: 5, TokenUsed: 7}, {CreatedAt: 3600, ModelName: "a", Count: 1}},
	})
	if len(stats) != 3 || stats[0].ModelName != "a" || stats[1] != (StatRow{CreatedAt: 3600, ModelName: "b", Count: 3, Quota: 15, TokenUsed: 7}) {
		t.Errorf("%+v", stats)
	}
	// One channel in two pools is one row.
	ch := MergeChannelStats([][]StatRow{{{ChannelID: 10001, Quota: 3}, {ChannelID: 37, Quota: 5}}, {{ChannelID: 10001, Quota: 4}}})
	if len(ch) != 2 || ch[0] != (StatRow{ChannelID: 10001, Quota: 7}) || ch[1].ChannelID != 37 {
		t.Errorf("%+v", ch)
	}
}
