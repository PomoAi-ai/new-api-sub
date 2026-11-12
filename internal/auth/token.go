package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"newapisub/internal/httpx"
)

const (
	tokenPrefix = "ptl_"
	tokenLen    = len(tokenPrefix) + 43 // 32 random bytes in base64url
)

// FindToken resolves a token's SHA-256. One that does not exist or has expired
// is ok=false.
type FindToken func(ctx context.Context, hash string) (tokenID, userID int64, ok bool, err error)

// NewToken returns a token, the hash to store and a hint to tell it by. The
// token itself can never be shown again.
func NewToken() (token, hash, hint string) {
	b := make([]byte, 32)
	rand.Read(b) // never fails: crypto/rand crashes instead
	token = tokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), token[:8] + "…" + token[tokenLen-4:]
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// TokenID returns the token the request signed in with, 0 for a session.
func TokenID(ctx context.Context) int64 {
	v, _ := ctx.Value(ctxKey{}).(identity)
	return v.token
}

// headerToken reads Authorization as new-api does: Bearer in any case, or the
// token alone.
func headerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1]
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return ""
}

// TokenIdentity is Require for a Bearer token, and returns the context to go on
// with so the caller answers failures in its own format. The session version is
// not compared: logging out of the browser must not cut off a script.
func (m *Manager) TokenIdentity(r *http.Request, find FindToken) (context.Context, error) {
	invalid := httpx.Errorf(http.StatusUnauthorized, "令牌无效或已失效")
	token := headerToken(r.Header.Get("Authorization"))
	if len(token) != tokenLen || !strings.HasPrefix(token, tokenPrefix) {
		return nil, invalid
	}
	tokenID, uid, ok, err := find(r.Context(), hashToken(token))
	if err != nil {
		return nil, fmt.Errorf("token lookup: %w", err)
	}
	if !ok {
		return nil, invalid
	}
	name, role, _, active, err := m.Lookup(r.Context(), uid)
	if err != nil {
		return nil, fmt.Errorf("token owner lookup uid=%d: %w", uid, err)
	}
	if !active {
		return nil, invalid
	}
	return context.WithValue(r.Context(), ctxKey{}, identity{uid, name, role, tokenID}), nil
}
