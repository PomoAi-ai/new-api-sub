package security

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/dchest/captcha"

	"newapisub/internal/audit"
	"newapisub/internal/httpx"
	"newapisub/internal/store"
)

// shortcut: every count here is per process and starts over on a restart, keep them in the database if that is ever abused.
const (
	ipRequests      = 30 // per ipRequestWindow, over all login routes
	ipRequestWindow = time.Minute
	ipFailWindow    = 15 * time.Minute
	ipLockAt        = 30 // password failures per ipFailWindow
	ipCaptchaAt     = 10
	nameCaptchaAt   = 3 // of the password limiter's 5
	captchaTTL      = 2 * time.Minute
	captchaDigits   = 6
	pruneAbove      = 10000
)

// window counts per key in fixed windows. A new window drops the old counts, so
// memory holds one window's keys at most.
type window struct {
	limit int
	per   time.Duration
	mu    sync.Mutex
	start time.Time
	used  map[string]int
}

// hit counts one request and returns its number in the window and the seconds
// until the window ends.
func (w *window) hit(key string) (n, retryAfter int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if now := time.Now(); now.Sub(w.start) >= w.per {
		w.start, w.used = now, map[string]int{}
	}
	w.used[key]++
	return w.used[key], int(time.Until(w.start.Add(w.per))/time.Second) + 1
}

// failures counts failures per key from the first one for a window; at lockAt
// the key is locked for a window. Same rules as auth.Limiter, without attempts in flight.
type failures struct {
	mu sync.Mutex
	m  map[string]*failure
}

type failure struct {
	n                  int
	first, lockedUntil time.Time
}

func (f *failures) get(key string, now time.Time) *failure {
	x := f.m[key]
	if x == nil || (now.Sub(x.first) > ipFailWindow && now.After(x.lockedUntil)) {
		return &failure{first: now}
	}
	return x
}

// count is the key's failures and whether it is locked.
func (f *failures) count(key string) (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	x := f.get(key, now)
	return x.n, now.Before(x.lockedUntil)
}

// add counts a failure and returns the new count.
func (f *failures) add(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if len(f.m) > pruneAbove {
		for k, x := range f.m {
			if now.Sub(x.first) > ipFailWindow && now.After(x.lockedUntil) {
				delete(f.m, k)
			}
		}
	}
	x := f.get(key, now)
	f.m[key] = x
	x.n++
	if x.n >= ipLockAt && !now.Before(x.lockedUntil) {
		x.lockedUntil = now.Add(ipFailWindow)
	}
	return x.n
}

// Throttle caps the requests of a client address to the login routes. The
// address is the audit log's, which trusts X-Forwarded-For only from TRUSTED_PROXIES.
func (s *Service) Throttle(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := s.Audit.ClientIP(r)
		n, retry := s.requests.hit(ip)
		if n <= ipRequests {
			h.ServeHTTP(w, r)
			return
		}
		// Only the first refusal of a window is recorded, so a flood cannot flood the log.
		if n == ipRequests+1 {
			s.Audit.Record(r, "auth.rate_limited", false, &audit.Event{TargetType: "session", Detail: map[string]any{"limit": "ip_requests"}})
		}
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		httpx.WriteError(w, r, httpx.Errorf(http.StatusTooManyRequests, "请求过于频繁，请稍后再试"))
	})
}

func captchaRequired(status int, msg string) error {
	return &httpx.Error{Status: status, Msg: msg, Extra: map[string]any{"captcha_required": true}}
}

func (s *Service) needsCaptcha(ip string, fails int) bool {
	n, _ := s.ipFails.count(ip)
	return fails >= nameCaptchaAt || n >= ipCaptchaAt
}

// Admit refuses a locked-out address, and asks for a captcha once a username or
// an address has failed often. The captcha is checked before the password and
// used up either way; a wrong one is no password failure.
func (s *Service) Admit(r *http.Request, ev *audit.Event, fails int, captchaID, captchaCode string) error {
	ip := s.Audit.ClientIP(r)
	if _, locked := s.ipFails.count(ip); locked {
		ev.Action, ev.Detail["limit"] = "auth.rate_limited", "ip_failures"
		// As in Throttle, one record per address and window, so a locked flood cannot flood the log.
		n, _ := s.lockRefusals.hit(ip)
		ev.Quiet = n > 1
		return httpx.Errorf(http.StatusTooManyRequests, "登录失败次数过多，请 15 分钟后再试")
	}
	if !s.needsCaptcha(ip, fails) {
		return nil
	}
	ok, err := s.checkCaptcha(r, captchaID, captchaCode)
	if err != nil {
		return err
	}
	if !ok {
		ev.Action = "auth.captcha_failed"
		return captchaRequired(http.StatusPreconditionRequired, "请输入正确的图形验证码")
	}
	return nil
}

// Failed counts a wrong password against the address, and tells the client
// when the next try needs a captcha.
func (s *Service) Failed(r *http.Request, fails int) error {
	ip := s.Audit.ClientIP(r)
	s.ipFails.add(ip)
	if s.needsCaptcha(ip, fails) {
		return captchaRequired(http.StatusUnauthorized, "用户名或密码错误")
	}
	return httpx.Errorf(http.StatusUnauthorized, "用户名或密码错误")
}

func (s *Service) checkCaptcha(r *http.Request, id, code string) (bool, error) {
	if id == "" || code == "" {
		return false, nil
	}
	want, err := s.Users.TakeChallenge(r.Context(), digest(id), store.FlowCaptcha)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return hmac.Equal(want, captchaSum(id, []byte(code))), nil
}

// captchaSum is what the table keeps of an answer: its HMAC under the captcha id,
// which the table holds only as a hash. Six digits alone would hash back at once.
func captchaSum(id string, answer []byte) []byte {
	m := hmac.New(sha256.New, []byte(id))
	m.Write(answer)
	return m.Sum(nil)
}

func (s *Service) newCaptcha(w http.ResponseWriter, r *http.Request) error {
	digits := captcha.RandomDigits(captchaDigits)
	answer := make([]byte, len(digits))
	for i, d := range digits {
		answer[i] = '0' + d
	}
	id := rand.Text()
	err := s.Users.CreateFlow(r.Context(), digest(id), store.Flow{Kind: store.FlowCaptcha, Data: captchaSum(id, answer)}, captchaTTL)
	if errors.Is(err, store.ErrConflict) {
		return httpx.Errorf(http.StatusTooManyRequests, "请求过多，请稍后再试")
	}
	if err != nil {
		return err
	}
	var png bytes.Buffer
	if _, err := captcha.NewImage(id, digits, captcha.StdWidth, captcha.StdHeight).WriteTo(&png); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"captcha_id": id,
		"image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png.Bytes()), "expires_in": int(captchaTTL / time.Second)})
	return nil
}
