package channels

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"newapisub/internal/connection"
	"newapisub/internal/httpx"
	"newapisub/internal/store"
)

// UsedUSD totals what the portal users among f's channels ran, soft-deleted
// channels included, from each pool's used_quota in that pool's unit. A channel
// gone from a pool counts for nothing there. Money is never added up from part
// of the pools: one that cannot be read fails it all with a 502. A pool without
// a database counts its channels as last read, those never read are read at
// once; asOf is the oldest such read, 0 for none.
func (s *Service) UsedUSD(ctx context.Context, f store.ChannelFilter) (used map[int64]float64, asOf int64, err error) {
	ms, err := s.Store.Channels(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	out := map[int64]float64{}
	for _, p := range s.Pools.List() {
		ids := store.InPool(ms, p.ID)
		if len(ids) == 0 {
			continue
		}
		down := httpx.Errorf(http.StatusBadGateway, "号池「%s」暂时不可用，无法计算用量", p.Name)
		if p.Reason != "" || p.QuotaPerUnit <= 0 {
			return nil, 0, down
		}
		var quota map[int]int64
		if p.DB != nil {
			if quota, err = p.DB.UsedQuota(ctx, ids); err != nil {
				log.Printf("used quota of pool %d: %v", p.ID, err)
				return nil, 0, down
			}
		} else {
			if err := s.readUnread(ctx, p, ms); err != nil {
				log.Printf("used quota of pool %d: %v", p.ID, err)
			}
			if quota, asOf, err = cachedQuota(ms, p.ID, asOf); err != nil {
				if !errors.Is(err, errNotRead) {
					log.Printf("used quota of pool %d: %v", p.ID, err)
				}
				return nil, 0, down
			}
		}
		for _, m := range ms {
			for _, pl := range m.Placements {
				if pl.PoolID == p.ID && pl.State == "created" {
					out[m.UserID] += float64(quota[pl.NewAPIID]) / p.QuotaPerUnit
				}
			}
		}
	}
	return out, asOf, nil
}

// readUnread reads now the channels of ms in a pool without a database that
// the push worker has not read yet, rather than wait for it, and puts what it
// read into ms. What it could not read stays unread.
func (s *Service) readUnread(ctx context.Context, pool *connection.Pool, ms []store.Mapping) error {
	var ids []int
	for _, m := range ms {
		for _, pl := range m.Placements {
			if pl.PoolID == pool.ID && pl.State == "created" && pl.CachedAt == 0 {
				ids = append(ids, pl.NewAPIID)
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows := map[int]string{}
	_, err := readStates(ctx, pool, ids, rows)
	if err := errors.Join(err, s.Store.CacheStates(ctx, pool.ID, rows)); err != nil {
		return err
	}
	now := time.Now().Unix()
	for i := range ms {
		for j := range ms[i].Placements {
			pl := &ms[i].Placements[j]
			if row, ok := rows[pl.NewAPIID]; ok && pl.PoolID == pool.ID {
				pl.Cached, pl.CachedAt = json.RawMessage(cmp.Or(row, "null")), now
			}
		}
	}
	return nil
}

// cachedQuota is DB.UsedQuota for a pool without a database, from the rows last
// read, and the oldest of asOf and those reads.
func cachedQuota(ms []store.Mapping, poolID, asOf int64) (map[int]int64, int64, error) {
	out := map[int]int64{}
	for _, m := range ms {
		for _, p := range m.Placements {
			if p.PoolID != poolID || p.State != "created" {
				continue
			}
			c, err := cached(p)
			if err != nil {
				return nil, 0, err
			}
			if c != nil {
				out[p.NewAPIID] = c.UsedQuota
			}
			if asOf == 0 || p.CachedAt < asOf {
				asOf = p.CachedAt
			}
		}
	}
	return out, asOf, nil
}

// SettlementView is a user's account: pending may be negative.
type SettlementView struct {
	UsedUSD    float64            `json:"used_usd"`
	SettledUSD float64            `json:"settled_usd"`
	PendingUSD float64            `json:"pending_usd"`
	Items      []store.Settlement `json:"items"`
	// AsOf is when the channels of pools without a database were read, which
	// used_usd counts as of then; absent when there are none. Stale: longer
	// than staleAfter ago.
	AsOf  *int64 `json:"as_of,omitempty"`
	Stale bool   `json:"stale,omitempty"`
}

// Settlements is the user's account. The caller has checked the scope.
func (s *Service) Settlements(ctx context.Context, userID int64) (SettlementView, error) {
	items, err := s.Store.Settlements(ctx, userID)
	if err != nil {
		return SettlementView{}, err
	}
	used, asOf, err := s.UsedUSD(ctx, store.ChannelFilter{UserID: userID})
	if err != nil {
		return SettlementView{}, err
	}
	v := SettlementView{UsedUSD: used[userID], Items: items}
	if asOf > 0 {
		v.AsOf, v.Stale = &asOf, stale(asOf)
	}
	if len(items) > 0 {
		v.SettledUSD = items[0].AmountUSD // newest first
	}
	v.PendingUSD = v.UsedUSD - v.SettledUSD
	return v, nil
}
