package store

import "context"

// KeyBalance is the last balance check of one key of a multi-key channel.
// Balance is nil when that check failed, and Error says why.
type KeyBalance struct {
	Balance   *float64
	Error     string
	CheckedAt int64
}

// KeyBalances returns the stored checks of a channel's keys by key index.
func (s *Store) KeyBalances(ctx context.Context, channelID int) (map[int]KeyBalance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT key_index, balance, error, EXTRACT(EPOCH FROM checked_at)::BIGINT
		FROM channel_key_balances WHERE channel_id = $1`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]KeyBalance{}
	for rows.Next() {
		var i int
		var b KeyBalance
		if err := rows.Scan(&i, &b.Balance, &b.Error, &b.CheckedAt); err != nil {
			return nil, err
		}
		out[i] = b
	}
	return out, rows.Err()
}

// SaveKeyBalance records a check: a balance, or nil and why it failed.
func (s *Store) SaveKeyBalance(ctx context.Context, channelID, index int, balance *float64, errText string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO channel_key_balances (channel_id, key_index, balance, error) VALUES ($1, $2, $3, $4)
		ON CONFLICT (channel_id, key_index) DO UPDATE
		SET balance = EXCLUDED.balance, error = EXCLUDED.error, checked_at = now()`,
		channelID, index, balance, errText)
	return err
}

// PruneKeyBalances drops the checks of keys the channel no longer has.
func (s *Store) PruneKeyBalances(ctx context.Context, channelID, keyCount int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM channel_key_balances WHERE channel_id = $1 AND key_index >= $2`,
		channelID, keyCount)
	return err
}

// KeyBalanceTotal is the sum of a channel's stored key balances and when the
// latest of them was checked.
type KeyBalanceTotal struct {
	Balance   float64
	CheckedAt int64
}

// KeyBalanceTotals returns the totals of the channels among ids that have at
// least one stored balance.
func (s *Store) KeyBalanceTotals(ctx context.Context, ids []int) (map[int]KeyBalanceTotal, error) {
	out := map[int]KeyBalanceTotal{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT channel_id, SUM(balance), EXTRACT(EPOCH FROM MAX(checked_at))::BIGINT
		FROM channel_key_balances WHERE channel_id = ANY($1) AND balance IS NOT NULL
		GROUP BY channel_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var t KeyBalanceTotal
		if err := rows.Scan(&id, &t.Balance, &t.CheckedAt); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}
