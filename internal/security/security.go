// Package security is the second factor of both systems: TOTP, passkeys, backup
// codes, the login step after the password, and the one-time proofs that guard
// sensitive changes. API tokens never pass through here.
package security

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/httpx"
	"newapisub/internal/secret"
	"newapisub/internal/store"
)

const (
	loginTTL     = 5 * time.Minute
	challengeTTL = 2 * time.Minute
	setupTTL     = 10 * time.Minute
	proofTTL     = 60 * time.Second

	backupCodes = 10
	// ProofHeader carries a proof to the operation it was issued for.
	ProofHeader = "X-Security-Proof"

	badCode = "验证码无效或已使用"
)

// Purposes a proof can be issued for.
const (
	PurposeTOTPBind         = "totp_bind"
	PurposeTOTPUnbind       = "totp_unbind"
	PurposePasskeyBind      = "passkey_bind"
	PurposePasskeyUnbind    = "passkey_unbind"
	PurposeBackupRegenerate = "backup_regenerate"
	PurposeTokenCreate      = "token_create"
	PurposeNewAPIConfig     = "newapi_config"
)

var purposes = []string{PurposeTOTPBind, PurposeTOTPUnbind, PurposePasskeyBind, PurposePasskeyUnbind,
	PurposeBackupRegenerate, PurposeTokenCreate, PurposeNewAPIConfig}

// Service is the second factor of one user table.
type Service struct {
	Users store.Users
	Auth  *auth.Manager
	Audit audit.Recorder
	// Limiter counts second-step and proof failures. It is not the password
	// limiter: that one's keys are whatever a client types as a username, so a
	// shared limiter would let anyone lock a victim's second step.
	Limiter *auth.Limiter
	Box     *secret.Box
	WA      *webauthn.WebAuthn
	Issuer  string

	requests     window   // login route requests by client address
	lockRefusals window   // refusals of a locked address, to record only the first
	ipFails      failures // password failures by client address
}

// New binds passkeys to origin, which comes from the configuration: never from a
// request. name is what authenticator apps and passkey prompts show.
func New(users store.Users, m *auth.Manager, rec audit.Recorder, box *secret.Box, origin, name string) (*Service, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("security: public origin has no host")
	}
	if name == "" {
		return nil, errors.New("security: system name is empty")
	}
	timeout := webauthn.TimeoutConfig{Enforce: true, Timeout: challengeTTL, TimeoutUVD: challengeTTL}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: name,
		RPOrigins:     []string{origin},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{Login: timeout, Registration: timeout},
	})
	if err != nil {
		return nil, fmt.Errorf("security: webauthn: %w", err)
	}
	return &Service{Users: users, Auth: m, Audit: rec, Limiter: auth.NewLimiter(), Box: box, WA: wa, Issuer: name,
		requests: window{limit: ipRequests, per: ipRequestWindow}, lockRefusals: window{per: ipRequestWindow}, ipFails: failures{m: map[string]*failure{}}}, nil
}

// Register adds the login steps and the account security routes to mux. The
// password login itself is the caller's, behind Throttle.
func (s *Service) Register(mux *http.ServeMux) {
	req, a := s.Auth.Require, s.Audit.Wrap
	mux.Handle("GET /api/auth/captcha", s.Throttle(httpx.Handler(s.newCaptcha)))
	mux.Handle("POST /api/auth/login/2fa", s.Throttle(a("auth.login", s.secondStep)))
	mux.Handle("POST /api/auth/passkey/begin", s.Throttle(httpx.Handler(s.passkeyLoginBegin)))
	mux.Handle("POST /api/auth/passkey/finish", s.Throttle(a("auth.login", s.passkeyLoginFinish)))
	mux.Handle("GET /api/me/security", req(httpx.Handler(s.status)))
	mux.Handle("POST /api/me/security/proof", req(a("auth.proof", s.proof)))
	mux.Handle("POST /api/me/security/proof/passkey/begin", req(httpx.Handler(s.proofPasskeyBegin)))
	mux.Handle("POST /api/me/security/proof/passkey/finish", req(a("auth.proof", s.proofPasskeyFinish)))
	mux.Handle("POST /api/me/security/totp/setup", req(httpx.Handler(s.totpSetup)))
	mux.Handle("POST /api/me/security/totp/confirm", req(a("auth.totp_bind", s.totpConfirm)))
	mux.Handle("DELETE /api/me/security/totp", req(a("auth.totp_unbind", s.unbind(store.FactorTOTP, PurposeTOTPUnbind))))
	mux.Handle("POST /api/me/security/passkey/begin", req(httpx.Handler(s.passkeyBindBegin)))
	mux.Handle("POST /api/me/security/passkey/finish", req(a("auth.passkey_bind", s.passkeyBindFinish)))
	mux.Handle("DELETE /api/me/security/passkey", req(a("auth.passkey_unbind", s.unbind(store.FactorPasskey, PurposePasskeyUnbind))))
	mux.Handle("POST /api/me/security/backup-codes", req(a("auth.backup_regenerate", s.regenerateBackup)))
}

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newFlow stores f and returns its token. A random string, so that nothing but
// this table can accept it: the session cookie's parser cannot.
func (s *Service) newFlow(ctx context.Context, f store.Flow, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	rand.Read(b) // never fails: crypto/rand crashes instead
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, s.Users.CreateFlow(ctx, digest(token), f, ttl)
}

// factorMethods is how an account with factors can prove itself.
func factorMethods(st store.FactorStatus) []string {
	methods := []string{}
	if st.TOTP {
		methods = append(methods, "totp")
	}
	if st.Passkey {
		methods = append(methods, "passkey")
	}
	if st.BackupLeft > 0 {
		methods = append(methods, "backup")
	}
	return methods
}

// Gate turns a correct password into a pending login when the account has a
// factor. The token goes in the body: it is not a session.
func (s *Service) Gate(ctx context.Context, acc auth.Account) (any, error) {
	st, err := s.Users.FactorStatus(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	if !st.Any() {
		return nil, nil
	}
	token, err := s.newFlow(ctx, store.Flow{Kind: store.FlowLogin, AccountID: acc.ID, Version: acc.Version}, loginTTL)
	if err != nil {
		return nil, err
	}
	return map[string]any{"mfa_required": true, "flow_token": token, "methods": factorMethods(st),
		"expires_in": int(loginTTL / time.Second)}, nil
}

func tooMany() error {
	return httpx.Errorf(http.StatusTooManyRequests, "验证失败次数过多，请 15 分钟后再试")
}

// secondStep finishes a pending login. The flow is only read at first: a
// mistyped code must not send the user back to the password.
func (s *Service) secondStep(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	var req struct {
		FlowToken  string `json:"flow_token"`
		Code       string `json:"code"`
		BackupCode string `json:"backup_code"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	ev.Action, ev.TargetType = "auth.2fa_failed", "session"
	ctx, hash := r.Context(), digest(req.FlowToken)
	f, err := s.Users.Flow(ctx, hash, store.FlowLogin)
	if errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusUnauthorized, "登录已过期，请重新登录")
	}
	if err != nil {
		return err
	}
	u, err := s.Users.Get(ctx, f.AccountID)
	if err != nil {
		return err
	}
	ev.ActorID, ev.ActorName, ev.TargetID = u.ID, u.Username, strconv.FormatInt(u.ID, 10)
	key := "login:" + ev.TargetID
	if !s.Limiter.Acquire(key) {
		return tooMany()
	}
	settle := s.Limiter.Release
	defer func() { settle(key) }()

	method := "totp"
	if req.BackupCode != "" {
		method = "backup"
		err = s.Users.LoginBackup(ctx, hash, u.ID, backupHash(req.BackupCode))
	} else {
		var ok bool
		if ok, err = s.checkTOTP(ctx, u.ID, req.Code); err == nil && !ok {
			err = store.ErrNotFound
		}
		if err == nil {
			_, err = s.Users.TakeFlow(ctx, hash, store.FlowLogin, "", u.ID)
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		settle = s.Limiter.Fail
		return httpx.Errorf(http.StatusUnauthorized, badCode)
	}
	if err != nil {
		return err
	}
	settle = s.Limiter.Reset
	ev.Action, ev.Detail["method"] = "", method
	s.Auth.Issue(w, u.ID, u.SessionVersion)
	httpx.JSON(w, http.StatusOK, map[string]any{"id": u.ID, "username": u.Username})
	return nil
}

func (s *Service) status(w http.ResponseWriter, r *http.Request) error {
	st, err := s.Users.FactorStatus(r.Context(), auth.UserID(r.Context()))
	if err != nil {
		return err
	}
	methods := factorMethods(st)
	if !st.Any() {
		methods = []string{"password"}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"totp": st.TOTP, "passkey": st.Passkey,
		"backup_codes_left": st.BackupLeft, "methods": methods})
	return nil
}

// target records the caller's own account as the audit target.
func (s *Service) target(ev *audit.Event) {
	ev.TargetType, ev.TargetID = "user", strconv.FormatInt(ev.ActorID, 10)
	if s.Audit.ActorType == "admin" {
		ev.TargetType = "admin"
	}
}

func validPurpose(p string) error {
	if !slices.Contains(purposes, p) {
		return httpx.Errorf(http.StatusBadRequest, "无效的 purpose")
	}
	return nil
}

// issueProof answers with a proof of purpose for u, as of u's session version.
func (s *Service) issueProof(w http.ResponseWriter, r *http.Request, u store.User, purpose string) error {
	token, err := s.newFlow(r.Context(), store.Flow{Kind: store.FlowProof, AccountID: u.ID, Version: u.SessionVersion, Purpose: purpose}, proofTTL)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"proof": token, "expires_in": int(proofTTL / time.Second)})
	return nil
}

// proof verifies the caller again. An account with a factor must use one, the
// password is what it no longer relies on; one without has only the password.
// Failures are 400: a 401 would read as a logout.
func (s *Service) proof(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	var req struct {
		Purpose    string `json:"purpose"`
		Password   string `json:"password"`
		Code       string `json:"code"`
		BackupCode string `json:"backup_code"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	s.target(ev)
	if err := validPurpose(req.Purpose); err != nil {
		return err
	}
	ev.Detail["purpose"] = req.Purpose
	ctx := r.Context()
	u, err := s.Users.Get(ctx, ev.ActorID)
	if err != nil {
		return err
	}
	st, err := s.Users.FactorStatus(ctx, u.ID)
	if err != nil {
		return err
	}
	var method string
	switch {
	case st.Any() && req.BackupCode != "":
		method = "backup"
	case st.Any() && req.Code != "":
		method = "totp"
	case !st.Any() && req.Password != "":
		method = "password"
	default:
		return httpx.Errorf(http.StatusBadRequest, "请使用账号可用的验证方式")
	}
	ev.Detail["method"] = method
	key := "proof:" + ev.TargetID
	if !s.Limiter.Acquire(key) {
		return tooMany()
	}
	settle := s.Limiter.Release
	defer func() { settle(key) }()

	ok := false
	switch method {
	case "backup":
		err = s.Users.UseBackup(ctx, u.ID, backupHash(req.BackupCode))
		ok = err == nil
		if errors.Is(err, store.ErrNotFound) {
			err = nil
		}
	case "totp":
		ok, err = s.checkTOTP(ctx, u.ID, req.Code)
	case "password":
		ok = auth.CheckPassword(u.PasswordHash, req.Password)
	}
	if err != nil {
		return err
	}
	if !ok {
		settle = s.Limiter.Fail
		return httpx.Errorf(http.StatusBadRequest, "验证失败")
	}
	settle = s.Limiter.Reset
	return s.issueProof(w, r, u, req.Purpose)
}

// Require uses up the request's proof of purpose. One SQL statement checks that
// it is live, for this purpose, of the caller and at the current session
// version. 403 and not 401: a 401 would read as a logout.
func (s *Service) Require(r *http.Request, purpose string) error {
	_, err := s.Users.TakeFlow(r.Context(), digest(r.Header.Get(ProofHeader)), store.FlowProof, purpose, auth.UserID(r.Context()))
	if errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusForbidden, "身份验证已失效，请重新验证")
	}
	return err
}

// newBackupCodes returns the codes to show once, 80 bits each, and their hashes.
// Plain SHA-256 is enough at that entropy, and one DELETE can then use a code up
// atomically, which a salted hash cannot. Not an HMAC: backup codes must keep
// working after the master key is lost.
func newBackupCodes() (codes []string, hashes [][]byte) {
	for range backupCodes {
		c := rand.Text()[:16]
		codes = append(codes, c[:4]+"-"+c[4:8]+"-"+c[8:12]+"-"+c[12:])
		hashes = append(hashes, backupHash(c))
	}
	return codes, hashes
}

func backupHash(code string) []byte {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(code))))
	return sum[:]
}

// bound finishes a bind: the cookie follows the new session version, and the
// first factor's backup codes are shown this once.
func (s *Service) bound(w http.ResponseWriter, id int64, ver int, first bool, codes []string) {
	s.Auth.Issue(w, id, ver)
	out := map[string]any{}
	if first {
		out["backup_codes"] = codes
	}
	httpx.JSON(w, http.StatusOK, out)
}

// unbind removes a factor of the caller. Other sessions end, this one goes on.
func (s *Service) unbind(kind, purpose string) audit.Handler {
	return func(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
		s.target(ev)
		if err := s.Require(r, purpose); err != nil {
			return err
		}
		ver, err := s.Users.UnbindFactor(r.Context(), ev.ActorID, kind)
		if errors.Is(err, store.ErrNotFound) {
			return httpx.Errorf(http.StatusNotFound, "尚未绑定")
		}
		if err != nil {
			return err
		}
		s.Auth.Issue(w, ev.ActorID, ver)
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

func (s *Service) regenerateBackup(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	s.target(ev)
	if err := s.Require(r, PurposeBackupRegenerate); err != nil {
		return err
	}
	codes, hashes := newBackupCodes()
	ver, err := s.Users.ReplaceBackup(r.Context(), ev.ActorID, hashes)
	if errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusBadRequest, "请先绑定验证器或通用密钥")
	}
	if err != nil {
		return err
	}
	s.bound(w, ev.ActorID, ver, true, codes)
	return nil
}
