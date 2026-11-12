// Package auth implements signed session cookies, password hashing and
// login rate limiting. It is shared by portal and admin, each with its own
// cookie name, secret and user table.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"newapisub/internal/httpx"
)

const sessionTTL = 7 * 24 * time.Hour

// Lookup returns the current username, role (admins only), session version
// and whether the account may still log in. A missing user is active=false.
type Lookup func(ctx context.Context, id int64) (name, role string, version int, active bool, err error)

type Manager struct {
	Cookie string
	Secret []byte
	Secure bool
	Lookup Lookup
}

type ctxKey struct{}

type identity struct {
	id    int64
	name  string
	role  string
	token int64 // set by TokenIdentity only
}

// UserID returns the authenticated user id, or 0 outside Require.
func UserID(ctx context.Context) int64 {
	v, _ := ctx.Value(ctxKey{}).(identity)
	return v.id
}

// Username returns the authenticated username, or "" outside Require.
func Username(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(identity)
	return v.name
}

// Role returns the authenticated admin's role, or "" for portal users and
// outside Require.
func Role(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(identity)
	return v.role
}

func (m *Manager) sign(payload string) string {
	mac := hmac.New(sha256.New, m.Secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *Manager) encode(uid int64, ver int, exp time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "%d.%d.%d", uid, ver, exp.Unix()))
	return payload + "." + m.sign(payload)
}

// parse verifies the signature and expiry of a cookie value.
func (m *Manager) parse(value string, now time.Time) (uid int64, ver int, ok bool) {
	payload, sig, found := strings.Cut(value, ".")
	if !found || !hmac.Equal([]byte(sig), []byte(m.sign(payload))) {
		return 0, 0, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return 0, 0, false
	}
	parts := strings.Split(string(raw), ".")
	if len(parts) != 3 {
		return 0, 0, false
	}
	uid, err1 := strconv.ParseInt(parts[0], 10, 64)
	ver, err2 := strconv.Atoi(parts[1])
	exp, err3 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || uid <= 0 || now.Unix() >= exp {
		return 0, 0, false
	}
	return uid, ver, true
}

func (m *Manager) Issue(w http.ResponseWriter, uid int64, ver int) {
	http.SetCookie(w, &http.Cookie{
		Name:     m.Cookie,
		Value:    m.encode(uid, ver, time.Now().Add(sessionTTL)),
		Path:     "/",
		MaxAge:   int(sessionTTL / time.Second),
		HttpOnly: true,
		Secure:   m.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *Manager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: m.Cookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: m.Secure, SameSite: http.SameSiteLaxMode,
	})
}

// Require rejects requests without a valid cookie whose version matches
// the stored session_version of an active user.
func (m *Manager) Require(next http.Handler) http.Handler {
	return httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
		c, err := r.Cookie(m.Cookie)
		if err != nil {
			return httpx.Errorf(http.StatusUnauthorized, "未登录")
		}
		uid, ver, ok := m.parse(c.Value, time.Now())
		if !ok {
			return httpx.Errorf(http.StatusUnauthorized, "登录已失效")
		}
		name, role, cur, active, err := m.Lookup(r.Context(), uid)
		if err != nil {
			return fmt.Errorf("session lookup uid=%d: %w", uid, err)
		}
		if !active || cur != ver {
			return httpx.Errorf(http.StatusUnauthorized, "登录已失效")
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, identity{uid, name, role, 0})))
		return nil
	})
}
