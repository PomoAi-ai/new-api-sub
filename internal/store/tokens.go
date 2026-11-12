package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const maxTokensPerUser = 10

// APIToken is a token as its owner sees it: never the token itself.
type APIToken struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Hint       string `json:"hint"`
	ExpiresAt  *int64 `json:"expires_at"`
	LastUsedAt *int64 `json:"last_used_at"`
	CreatedAt  int64  `json:"created_at"`
}

// CreateToken stores a token of userID, which fails with ErrConflict at
// maxTokensPerUser. expiresAt nil never expires.
// shortcut: concurrent creates can each pass the count and go a little over the limit, take a row lock on the user if that ever matters.
func (s *Store) CreateToken(ctx context.Context, userID int64, name, hash, hint string, expiresAt *time.Time) (APIToken, error) {
	t := APIToken{Name: name, Hint: hint}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO api_tokens (user_id, name, token_hash, hint, expires_at)
		SELECT $1::BIGINT, $2::TEXT, $3::TEXT, $4::TEXT, $5::TIMESTAMPTZ WHERE (SELECT count(*) FROM api_tokens WHERE user_id = $1) < $6
		RETURNING id, EXTRACT(EPOCH FROM expires_at)::BIGINT, EXTRACT(EPOCH FROM created_at)::BIGINT`,
		userID, name, hash, hint, expiresAt, maxTokensPerUser).Scan(&t.ID, &t.ExpiresAt, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrConflict
	}
	return t, err
}

// ListTokens returns the tokens of userID, newest first.
func (s *Store) ListTokens(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, hint, EXTRACT(EPOCH FROM expires_at)::BIGINT, EXTRACT(EPOCH FROM last_used_at)::BIGINT,
		       EXTRACT(EPOCH FROM created_at)::BIGINT
		FROM api_tokens WHERE user_id = $1 ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Hint, &t.ExpiresAt, &t.LastUsedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteToken revokes a token of userID and returns its name: someone else's
// is ErrNotFound.
func (s *Store) DeleteToken(ctx context.Context, id, userID int64) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx, `DELETE FROM api_tokens WHERE id = $1 AND user_id = $2 RETURNING name`, id, userID).Scan(&name)
	return name, notFound(err)
}

// UseToken implements auth.FindToken. last_used_at is written at most once a
// minute, not on every call of a script.
func (s *Store) UseToken(ctx context.Context, hash string) (tokenID, userID int64, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `
		WITH hit AS (
		  SELECT id, user_id FROM api_tokens WHERE token_hash = $1 AND (expires_at IS NULL OR expires_at > now())
		), touched AS (
		  UPDATE api_tokens t SET last_used_at = now() FROM hit
		  WHERE t.id = hit.id AND (t.last_used_at IS NULL OR t.last_used_at < now() - interval '1 minute')
		)
		SELECT id, user_id FROM hit`, hash).Scan(&tokenID, &userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	return tokenID, userID, err == nil, err
}
