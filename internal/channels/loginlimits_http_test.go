package channels_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// captcha fetches a captcha and makes 123456 its answer: the image is for eyes.
func (c caller) captcha() string {
	c.e.t.Helper()
	got := decode[struct {
		ID        string `json:"captcha_id"`
		Image     string `json:"image"`
		ExpiresIn int    `json:"expires_in"`
	}](c.e.t, c.anon().want(200, "GET", "/api/auth/captcha", nil))
	if !strings.HasPrefix(got.Image, "data:image/png;base64,") || got.ExpiresIn != 120 {
		c.e.t.Fatalf("%.60s %d", got.Image, got.ExpiresIn)
	}
	id, answer := sha256.Sum256([]byte(got.ID)), hmac.New(sha256.New, []byte(got.ID))
	answer.Write([]byte("123456"))
	res, err := c.e.raw.Exec(`UPDATE auth_flows SET data = $1 WHERE token_hash = $2 AND kind = 'captcha'`, answer.Sum(nil), hex.EncodeToString(id[:]))
	if n, _ := res.RowsAffected(); err != nil || n != 1 {
		c.e.t.Fatalf("%d %v", n, err)
	}
	return got.ID
}

type loginAnswer struct {
	Error           string `json:"error"`
	CaptchaRequired bool   `json:"captcha_required"`
}

func (c caller) login(status int, body ch) loginAnswer {
	c.e.t.Helper()
	return decode[loginAnswer](c.e.t, c.anon().want(status, "POST", "/api/auth/login", body))
}

func TestCaptchaAfterFailuresOfAUsername(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("cap1")
	wrong := ch{"username": "cap1", "password": "wrong-password"}
	right := ch{"username": "cap1", "password": testPassword}
	for i := range 3 {
		if a := c.login(401, wrong); a.CaptchaRequired != (i == 2) {
			t.Fatalf("failure %d: %+v", i+1, a)
		}
	}
	// Without a captcha the password is not even looked at.
	if a := c.login(428, right); !a.CaptchaRequired {
		t.Errorf("%+v", a)
	}
	c.login(428, wrong)
	// Wrong captchas are no password failures: five of them do not lock the name.
	for range 5 {
		c.login(428, ch{"username": "cap1", "password": testPassword, "captcha_id": c.captcha(), "captcha_code": "654321"})
	}
	// One use each, right or wrong.
	id := c.captcha()
	c.login(401, ch{"username": "cap1", "password": "wrong-password", "captcha_id": id, "captcha_code": "123456"})
	c.login(428, ch{"username": "cap1", "password": testPassword, "captcha_id": id, "captcha_code": "123456"})
	c.login(200, ch{"username": "cap1", "password": testPassword, "captcha_id": c.captcha(), "captcha_code": "123456"})
	// Success clears the name's count.
	c.login(200, right)
	if e.auditCount("auth.captcha_failed", false) != 8 {
		t.Errorf("captcha_failed = %d", e.auditCount("auth.captcha_failed", false))
	}
	var details string
	if err := e.raw.QueryRow(`SELECT string_agg(detail::text, ' ') FROM audit_logs`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"654321", "123456", testPassword, id} {
		if strings.Contains(details, s) {
			t.Errorf("audit holds %q", s)
		}
	}
}

func TestCaptchaForAMissingUsernameToo(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("cap2")
	for i := range 3 {
		if a := c.login(401, ch{"username": "Nobody-Here", "password": "x-wrong-password"}); a.CaptchaRequired != (i == 2) {
			t.Fatalf("failure %d: %+v", i+1, a)
		}
	}
	c.login(428, ch{"username": " nobody-here", "password": "x-wrong-password"})
}

func TestCaptchaAfterFailuresOfAnAddress(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("cap3")
	for i := range 10 {
		a := c.login(401, ch{"username": "guess" + strconv.Itoa(i/2), "password": "wrong-password"})
		if a.CaptchaRequired != (i == 9) {
			t.Fatalf("failure %d: %+v", i+1, a)
		}
	}
	c.login(428, ch{"username": "cap3", "password": testPassword})
	other := c.from("198.51.100.7")
	other.cookie = nil
	other.want(200, "POST", "/api/auth/login", ch{"username": "cap3", "password": testPassword})
}

func TestLoginRoutesAreRateLimitedByAddress(t *testing.T) {
	e := newEnv(t)
	send := func(xff string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/auth/captcha", nil)
		req.Header.Set("X-Forwarded-For", xff)
		rec := httptest.NewRecorder()
		e.portalMux.ServeHTTP(rec, req)
		return rec
	}
	// The peer is no trusted proxy, so a made-up X-Forwarded-For changes nothing.
	for i := range 30 {
		if rec := send("203.0.113." + strconv.Itoa(i)); rec.Code != 200 {
			t.Fatalf("request %d: %d", i+1, rec.Code)
		}
	}
	for range 3 {
		rec := send("203.0.113.200")
		if retry, _ := strconv.Atoi(rec.Header().Get("Retry-After")); rec.Code != 429 || retry <= 0 {
			t.Fatalf("%d Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
		}
	}
	for _, path := range []string{"/api/auth/login", "/api/auth/login/2fa", "/api/auth/passkey/begin", "/api/auth/passkey/finish"} {
		if rec := (caller{e: e, mux: e.portalMux}).do("POST", path, ch{}); rec.Code != 429 {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	if n := e.auditCount("auth.rate_limited", false); n != 1 {
		t.Errorf("rate_limited recorded %d times", n)
	}
	(caller{e: e, mux: e.portalMux}).from("198.51.100.8").want(200, "GET", "/api/auth/captcha", nil)
}

// The cap on anonymous flows is per system: a flood of portal captchas must not
// leave the admin console without one.
func TestCaptchaCapIsPerSystem(t *testing.T) {
	e := newEnv(t)
	if _, err := e.raw.Exec(`INSERT INTO auth_flows (token_hash, realm, kind, expires_at)
		SELECT 'flood' || i, 'portal_users', 'captcha', now() + interval '1 minute' FROM generate_series(1, 10000) i`); err != nil {
		t.Fatal(err)
	}
	(caller{e: e, mux: e.portalMux}).want(429, "GET", "/api/auth/captcha", nil)
	(caller{e: e, mux: e.adminMux}).want(200, "GET", "/api/auth/captcha", nil)
}

// The fifth wrong password locks the name, and its answer is the lock: a
// captcha asked for then would only lead to another refusal.
func TestTheLockingFailureAnswersWithTheLock(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("lock1")
	for range 3 {
		c.login(401, ch{"username": "lock1", "password": "wrong-password"})
	}
	c.login(401, ch{"username": "lock1", "password": "wrong-password", "captcha_id": c.captcha(), "captcha_code": "123456"})
	if a := c.login(429, ch{"username": "lock1", "password": "wrong-password", "captcha_id": c.captcha(), "captcha_code": "123456"}); a.CaptchaRequired {
		t.Errorf("%+v", a)
	}
	c.login(429, ch{"username": "lock1", "password": testPassword})
}
