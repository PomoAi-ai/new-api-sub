package logstore

import (
	"context"
	"errors"
	"log"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/topology"

	"newapisub/internal/newapi"
)

const (
	// downFor is how long reads fail at once after one found MongoDB
	// unreachable, rather than each wait out the server selection.
	downFor      = 10 * time.Second
	closeTimeout = 2 * time.Second
)

// ErrNotOpen is a read of the sync store while it is not connected yet, or
// for a little while after a read found it unreachable.
var ErrNotOpen = errors.New("the MongoDB log store is not connected yet")

// Lazy is the Store once it opens. MongoDB being down at start only leaves out
// what reads or writes synced logs: Dial goes on trying in the background.
type Lazy struct {
	ready chan struct{}
	s     *Store
	down  atomic.Int64 // Unix nanoseconds until which reads fail at once
}

// Dial opens uri in the background, trying again every every until it opens or
// ctx ends. Like Open, nothing it logs quotes uri.
func Dial(ctx context.Context, uri, db string, every time.Duration) *Lazy {
	l := &Lazy{ready: make(chan struct{})}
	go func() {
		for {
			s, err := Open(ctx, uri, db)
			if err == nil {
				l.s = s
				close(l.ready)
				return
			}
			if ctx.Err() != nil {
				return
			}
			log.Printf("mongo log store: %v; trying again", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(every):
			}
		}
	}()
	return l
}

// Get is the Store, nil while it is not open.
func (l *Lazy) Get() *Store {
	select {
	case <-l.ready:
		return l.s
	default:
		return nil
	}
}

// Wait is the Store once it opens, nil if ctx ends first.
func (l *Lazy) Wait(ctx context.Context) *Store {
	select {
	case <-l.ready:
		return l.s
	case <-ctx.Done():
		return nil
	}
}

// Ping is the Store when MongoDB answers now: ErrNotOpen before it opened,
// else the error of the ping.
func (l *Lazy) Ping(ctx context.Context) (*Store, error) {
	s := l.Get()
	if s == nil {
		return nil, ErrNotOpen
	}
	if err := l.seen(ctx, s.client.Ping(ctx, nil)); err != nil {
		return nil, err
	}
	l.down.Store(0)
	return s, nil
}

// Close disconnects, giving up after closeTimeout: a MongoDB that is down
// must not hold up a process that is stopping.
func (l *Lazy) Close(ctx context.Context) {
	if s := l.Get(); s != nil {
		ctx, cancel := context.WithTimeout(ctx, closeTimeout)
		defer cancel()
		s.Close(ctx)
	}
}

// seen passes err on, and notes one that could not reach MongoDB, unless the
// caller gave up first.
func (l *Lazy) seen(ctx context.Context, err error) error {
	if err != nil && ctx.Err() == nil && (errors.As(err, new(topology.ServerSelectionError)) || mongo.IsNetworkError(err)) {
		l.down.Store(time.Now().Add(downFor).UnixNano())
	}
	return err
}

// Reader is the LogSource of a pool that syncs its logs, failing with
// ErrNotOpen while the store is not open.
func (l *Lazy) Reader(poolID int64) newapi.LogSource { return lazyReader{l, poolID} }

type lazyReader struct {
	l      *Lazy
	poolID int64
}

func (r lazyReader) src() (newapi.LogSource, error) {
	s := r.l.Get()
	if s == nil || time.Now().UnixNano() < r.l.down.Load() {
		return nil, ErrNotOpen
	}
	return s.Reader(r.poolID), nil
}

func (r lazyReader) Logs(ctx context.Context, f newapi.Filter, full bool) ([]newapi.Log, int64, error) {
	src, err := r.src()
	if err != nil {
		return nil, 0, err
	}
	items, n, err := src.Logs(ctx, f, full)
	return items, n, r.l.seen(ctx, err)
}

func (r lazyReader) Usage(ctx context.Context, f newapi.Filter) (newapi.Usage, error) {
	src, err := r.src()
	if err != nil {
		return newapi.Usage{}, err
	}
	out, err := src.Usage(ctx, f)
	return out, r.l.seen(ctx, err)
}

func (r lazyReader) Stats(ctx context.Context, f newapi.Filter) ([]newapi.StatRow, error) {
	src, err := r.src()
	if err != nil {
		return nil, err
	}
	out, err := src.Stats(ctx, f)
	return out, r.l.seen(ctx, err)
}

func (r lazyReader) UserStats(ctx context.Context, f newapi.Filter) ([]newapi.StatRow, error) {
	src, err := r.src()
	if err != nil {
		return nil, err
	}
	out, err := src.UserStats(ctx, f)
	return out, r.l.seen(ctx, err)
}

func (r lazyReader) ChannelStats(ctx context.Context, f newapi.Filter) ([]newapi.StatRow, error) {
	src, err := r.src()
	if err != nil {
		return nil, err
	}
	out, err := src.ChannelStats(ctx, f)
	return out, r.l.seen(ctx, err)
}

func (r lazyReader) PerfCounts(ctx context.Context, f newapi.Filter) (map[string]newapi.PerfCount, error) {
	src, err := r.src()
	if err != nil {
		return nil, err
	}
	out, err := src.PerfCounts(ctx, f)
	return out, r.l.seen(ctx, err)
}

func (r lazyReader) Flow(ctx context.Context, f newapi.Filter, full bool) ([]newapi.FlowRow, error) {
	src, err := r.src()
	if err != nil {
		return nil, err
	}
	out, err := src.Flow(ctx, f, full)
	return out, r.l.seen(ctx, err)
}

func (r lazyReader) ExportLogs(ctx context.Context, f newapi.Filter, full bool, fn func(newapi.Log) error) error {
	src, err := r.src()
	if err != nil {
		return err
	}
	return r.l.seen(ctx, src.ExportLogs(ctx, f, full, fn))
}

func (r lazyReader) ChannelNames(context.Context) (map[int]string, error) {
	return map[int]string{}, nil
}
