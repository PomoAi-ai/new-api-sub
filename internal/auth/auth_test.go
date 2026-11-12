package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"newapisub/internal/httpx"
)

func TestCookieRoundTripAndTampering(t *testing.T) {
	m := &Manager{Cookie: "s", Secret: []byte("0123456789abcdef0123456789abcdef")}
	now := time.Now()
	v := m.encode(42, 3, now.Add(time.Hour))

	if uid, ver, ok := m.parse(v, now); !ok || uid != 42 || ver != 3 {
		t.Fatalf("round trip: got %d %d %v", uid, ver, ok)
	}
	tampered := "A"
	if v[len(v)-1] == 'A' {
		tampered = "B"
	}
	if _, _, ok := m.parse(v[:len(v)-1]+tampered, now); ok {
		t.Error("tampered signature accepted")
	}
	forged := m.encode(1, 3, now.Add(time.Hour))
	if _, _, ok := m.parse(forged[:len(forged)-43]+v[len(v)-43:], now); ok {
		t.Error("payload swapped under another signature accepted")
	}
	other := &Manager{Secret: []byte("another-secret-another-secret-xx")}
	if _, _, ok := other.parse(v, now); ok {
		t.Error("cookie accepted under a different secret")
	}
	if _, _, ok := m.parse(v, now.Add(2*time.Hour)); ok {
		t.Error("expired cookie accepted")
	}
}

func TestRequireChecksVersionAndActive(t *testing.T) {
	var ver int
	var active bool
	m := &Manager{Cookie: "s", Secret: []byte("0123456789abcdef0123456789abcdef"),
		Lookup: func(ctx context.Context, id int64) (string, string, int, bool, error) {
			return "u", "super", ver, active, nil
		}}
	h := m.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserID(r.Context()) != 7 || Username(r.Context()) != "u" || Role(r.Context()) != "super" {
			t.Errorf("identity not in context")
		}
	}))
	call := func() int {
		req := httptest.NewRequest("GET", "/api/me", nil)
		req.AddCookie(&http.Cookie{Name: "s", Value: m.encode(7, 2, time.Now().Add(time.Hour))})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	cases := []struct {
		ver    int
		active bool
		want   int
	}{{2, true, 200}, {3, true, 401}, {2, false, 401}}
	for _, c := range cases {
		ver, active = c.ver, c.active
		if got := call(); got != c.want {
			t.Errorf("ver=%d active=%v: got %d want %d", c.ver, c.active, got, c.want)
		}
	}
}

func TestTokenIdentity(t *testing.T) {
	token, hash, _ := NewToken()
	active := true
	m := &Manager{Cookie: "s", Secret: []byte("0123456789abcdef0123456789abcdef"),
		Lookup: func(ctx context.Context, id int64) (string, string, int, bool, error) {
			return "u", "", 99, active, nil // a version no token knows: logging out must not matter
		}}
	find := func(ctx context.Context, h string) (int64, int64, bool, error) {
		return 5, 7, h == hash, nil
	}
	call := func(authz string) (context.Context, error) {
		req := httptest.NewRequest("GET", "/api/channel/", nil)
		req.AddCookie(&http.Cookie{Name: "s", Value: m.encode(7, 99, time.Now().Add(time.Hour))})
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		return m.TokenIdentity(req, find)
	}
	for _, authz := range []string{"Bearer " + token, "bearer " + token, "BEARER  " + token, token, "  " + token + " "} {
		ctx, err := call(authz)
		if err != nil || UserID(ctx) != 7 || Username(ctx) != "u" || TokenID(ctx) != 5 {
			t.Fatalf("valid token in %q: %v", authz, err)
		}
	}
	other, _, _ := NewToken()
	for name, authz := range map[string]string{"cookie only": "", "scheme alone": "Bearer", "other scheme": "Basic " + token,
		"extra word": "Bearer " + token + " x", "too short": "Bearer " + token[:20], "wrong prefix": "Bearer nap_" + token[4:], "unknown": "Bearer " + other} {
		var he *httpx.Error
		if _, err := call(authz); !errors.As(err, &he) || he.Status != http.StatusUnauthorized {
			t.Errorf("%s: %v", name, err)
		}
	}
	active = false
	var he *httpx.Error
	if _, err := call("Bearer " + token); !errors.As(err, &he) || he.Status != http.StatusUnauthorized {
		t.Errorf("disabled account: %v", err)
	}
}

func TestLimiterLocksAfterFiveFailuresAndResets(t *testing.T) {
	l := NewLimiter()
	now := time.Now()
	l.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if !l.Acquire("bob") {
			t.Fatalf("locked after %d failures", i)
		}
		l.Fail("bob")
	}
	if l.Acquire("bob") {
		t.Fatal("6th attempt allowed")
	}
	if !l.Acquire("alice") {
		t.Fatal("other user locked")
	}
	now = now.Add(lockFor)
	for i := 0; i < 4; i++ {
		if !l.Acquire("bob") {
			t.Fatal("still locked after lock period")
		}
		l.Fail("bob")
	}
	l.Acquire("bob")
	l.Reset("bob")
	l.Acquire("bob")
	l.Fail("bob")
	if !l.Acquire("bob") {
		t.Fatal("success did not reset the failure count")
	}
	l.Release("bob")
}

func TestLimiterReleaseDoesNotCountAsFailure(t *testing.T) {
	l := NewLimiter()
	for i := 0; i < 10; i++ {
		if !l.Acquire("bob") {
			t.Fatalf("locked after %d released attempts", i)
		}
		l.Release("bob")
	}
}

func TestConcurrentLoginsCannotExceedLimit(t *testing.T) {
	const n = 30
	lim := NewLimiter()
	gate := make(chan struct{})
	arrived := make(chan struct{}, n)
	var checked atomic.Int32
	find := func(ctx context.Context, name string) (Account, bool, error) {
		checked.Add(1)
		arrived <- struct{}{}
		<-gate // hold every admitted attempt open until all requests have arrived
		return Account{ID: 1, Name: name, Hash: "not-a-bcrypt-hash", Active: true}, true, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Login(context.Background(), lim, find, "Bob", "wrong"); errors.Is(err, ErrLocked) {
				arrived <- struct{}{}
			}
		}()
	}
	for i := 0; i < n; i++ {
		<-arrived
	}
	close(gate)
	wg.Wait()
	if got := checked.Load(); got != maxFailures {
		t.Fatalf("%d concurrent attempts reached the password check, want %d", got, maxFailures)
	}
	if _, err := Login(context.Background(), lim, find, "bob", "wrong"); !errors.Is(err, ErrLocked) {
		t.Fatalf("after %d failures: err = %v, want ErrLocked", maxFailures, err)
	}
}
