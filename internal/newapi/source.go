package newapi

import (
	"cmp"
	"context"
	"errors"
	"iter"
	"slices"
)

// LogSource is where a pool's logs are read from. Rows come with the pool's own
// channel ids; translating them and setting PoolID is the caller's.
type LogSource interface {
	Logs(ctx context.Context, f Filter, full bool) ([]Log, int64, error) // f.Page and f.PageSize set by the caller
	Usage(ctx context.Context, f Filter) (Usage, error)
	Stats(ctx context.Context, f Filter) ([]StatRow, error)
	UserStats(ctx context.Context, f Filter) ([]StatRow, error)
	ChannelStats(ctx context.Context, f Filter) ([]StatRow, error)
	PerfCounts(ctx context.Context, f Filter) (map[string]PerfCount, error)
	Flow(ctx context.Context, f Filter, full bool) ([]FlowRow, error)
	ExportLogs(ctx context.Context, f Filter, full bool, fn func(Log) error) error
	ChannelNames(ctx context.Context) (map[int]string, error)
}

var _ LogSource = (*DB)(nil)

// Prepare is the last step of every log a LogSource outside this package
// returns, as of the ones *DB does.
func (l *Log) Prepare(full bool) { l.prepare(full) }

// MaskJSON hides the keys quoted in the strings of a JSON text, as logs.other
// leaves this package.
func MaskJSON(s string) string { return maskJSON(s, MaskKeys) }

// MaxMergeRows bounds offset+size of a log page merged from several pools: each
// is read from its first row up to there.
const MaxMergeRows = 10000

// logOrder is the order of a log page across pools: newest first, then by pool,
// then as within a pool.
func logOrder(a, b Log) int {
	return cmp.Or(cmp.Compare(b.CreatedAt, a.CreatedAt), cmp.Compare(a.PoolID, b.PoolID), cmp.Compare(b.ID, a.ID))
}

// MergeLogs is rows [offset, offset+size) of the pools' logs together. Each
// part is that pool's first offset+size rows, PoolID set.
func MergeLogs(parts [][]Log, offset, size int) []Log {
	all := slices.Concat(parts...)
	slices.SortFunc(all, logOrder)
	all = all[min(offset, len(all)):min(offset+size, len(all))]
	if all == nil {
		return []Log{}
	}
	return all
}

// SumUsage is the strip above the log table for usage of several sources, as
// of now.
func SumUsage(parts []Usage, now int64) LogStat {
	var st LogStat
	var spans [][2]int64
	for _, u := range parts {
		st.Quota += u.Quota
		st.TPM += u.Tokens
		st.RPM += int64(len(u.Spans))
		spans = append(spans, u.Spans...)
	}
	st.Concurrency = peakConcurrency(spans, now-60, now)
	return st
}

type statKey struct {
	hour        int64
	channel     int
	model, user string
}

func sumStats(parts [][]StatRow) []StatRow {
	at := map[statKey]int{}
	out := []StatRow{}
	for _, rows := range parts {
		for _, r := range rows {
			k := statKey{r.CreatedAt, r.ChannelID, r.ModelName, r.Username}
			i, ok := at[k]
			if !ok {
				at[k] = len(out)
				out = append(out, r)
				continue
			}
			o := &out[i]
			o.Count += r.Count
			o.Quota += r.Quota
			o.TokenUsed += r.TokenUsed
			o.CacheReadTokens += r.CacheReadTokens
			o.CacheCreationTokens += r.CacheCreationTokens
		}
	}
	return out
}

// MergeStats adds up the Stats or UserStats rows of several pools by hour and
// model or end user, in their order.
func MergeStats(parts [][]StatRow) []StatRow {
	out := sumStats(parts)
	slices.SortFunc(out, func(a, b StatRow) int {
		return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ModelName, b.ModelName), cmp.Compare(a.Username, b.Username))
	})
	return out
}

// MergeChannelStats adds up ChannelStats rows by channel, biggest consumer
// first. Their ids must be local already: a channel in several pools is one row.
func MergeChannelStats(parts [][]StatRow) []StatRow {
	out := sumStats(parts)
	slices.SortFunc(out, func(a, b StatRow) int {
		return cmp.Or(cmp.Compare(b.Quota, a.Quota), cmp.Compare(a.ChannelID, b.ChannelID))
	})
	return out
}

// MergeFlow adds up Flow rows that agree in every column but the sums,
// biggest first. Their channel ids must be local already.
func MergeFlow(parts [][]FlowRow) []FlowRow {
	type key struct {
		uid, tid               int64
		user, token, group, md string
		channel                int
	}
	at := map[key]int{}
	out := []FlowRow{}
	for _, rows := range parts {
		for _, r := range rows {
			k := key{r.UserID, r.TokenID, r.Username, r.TokenName, r.UseGroup, r.ModelName, r.ChannelID}
			i, ok := at[k]
			if !ok {
				at[k] = len(out)
				out = append(out, r)
				continue
			}
			out[i].TokenUsed += r.TokenUsed
			out[i].Count += r.Count
			out[i].Quota += r.Quota
		}
	}
	slices.SortStableFunc(out, func(a, b FlowRow) int { return cmp.Compare(b.Quota, a.Quota) })
	return out
}

// MergePerfCounts adds up the PerfCounts of several pools by model.
func MergePerfCounts(parts []map[string]PerfCount) map[string]PerfCount {
	out := map[string]PerfCount{}
	for _, counts := range parts {
		for name, c := range counts {
			sum := out[name]
			sum.add(c)
			out[name] = sum
		}
	}
	return out
}

// errStopExport ends an export MergeExport no longer reads.
var errStopExport = errors.New("export stopped")

// MergeExport runs exports, each a pool's ExportLogs with PoolID set, and calls
// fn with all their rows in the order of MergeLogs. The first error of an
// export or of fn ends it.
func MergeExport(exports []func(fn func(Log) error) error, fn func(Log) error) error {
	type head struct {
		next func() (Log, bool)
		err  *error
		cur  Log
		ok   bool
	}
	heads := make([]*head, len(exports))
	for i, export := range exports {
		h := &head{err: new(error)}
		next, stop := iter.Pull(func(yield func(Log) bool) {
			err := export(func(l Log) error {
				if !yield(l) {
					return errStopExport
				}
				return nil
			})
			if !errors.Is(err, errStopExport) {
				*h.err = err
			}
		})
		defer stop()
		h.next = next
		h.cur, h.ok = next()
		heads[i] = h
	}
	for {
		var first *head
		for _, h := range heads {
			if !h.ok {
				if *h.err != nil {
					return *h.err
				}
				continue
			}
			if first == nil || logOrder(h.cur, first.cur) < 0 {
				first = h
			}
		}
		if first == nil {
			return nil
		}
		if err := fn(first.cur); err != nil {
			return err
		}
		first.cur, first.ok = first.next()
	}
}
