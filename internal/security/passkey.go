package security

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/httpx"
	"newapisub/internal/store"
)

const badPasskey = "通用密钥验证失败"

// passkeyUser is an account as webauthn sees it. The user handle is what finds
// the account in a login without a username: see handlePrefix.
type passkeyUser struct {
	store.User
	handle []byte
	creds  []webauthn.Credential
}

func (u passkeyUser) WebAuthnID() []byte                         { return u.handle }
func (u passkeyUser) WebAuthnName() string                       { return u.Username }
func (u passkeyUser) WebAuthnDisplayName() string                { return u.Username }
func (u passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// handlePrefix starts a user handle, "p:" or "a:" before the decimal id. The two
// tables' ids overlap, and on one host name an authenticator keeps one passkey
// per handle: a bare id would let one system's passkey replace the other's.
func (s *Service) handlePrefix() string { return s.Users.Realm()[:1] + ":" }

func (s *Service) passkeyUser(u store.User) passkeyUser {
	return passkeyUser{User: u, handle: []byte(s.handlePrefix() + strconv.FormatInt(u.ID, 10))}
}

// loadPasskey adds the account's credential, if it has one.
func (s *Service) loadPasskey(ctx context.Context, u store.User) (passkeyUser, error) {
	user := s.passkeyUser(u)
	raw, err := s.Users.Passkey(ctx, u.ID)
	if errors.Is(err, store.ErrNotFound) {
		return user, nil
	}
	if err != nil {
		return passkeyUser{}, err
	}
	var c webauthn.Credential
	if err := json.Unmarshal(raw, &c); err != nil {
		return passkeyUser{}, err
	}
	user.creds = []webauthn.Credential{c}
	return user, nil
}

// challenge stores a ceremony's session under a flow and answers with its options.
func (s *Service) challenge(w http.ResponseWriter, r *http.Request, f store.Flow, options any, session *webauthn.SessionData) error {
	var err error
	if f.Data, err = json.Marshal(session); err != nil {
		return err
	}
	token, err := s.newFlow(r.Context(), f, challengeTTL)
	if errors.Is(err, store.ErrConflict) {
		return httpx.Errorf(http.StatusTooManyRequests, "请求过多，请稍后再试")
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"flow_token": token, "options": options})
	return nil
}

// used stores what an assertion changed. The library only flags a counter that
// went backwards, a sign of a cloned authenticator, and still accepts it.
func (s *Service) used(r *http.Request, ev *audit.Event, u passkeyUser, after *webauthn.Credential) error {
	raw, err := json.Marshal(after)
	if err != nil {
		return err
	}
	if err := s.Users.SavePasskey(r.Context(), u.ID, raw); err != nil {
		return err
	}
	if after.Authenticator.CloneWarning && !u.creds[0].Authenticator.CloneWarning {
		s.Audit.Record(r, "auth.passkey_clone_warning", false, &audit.Event{ActorID: u.ID, ActorName: u.Username,
			TargetType: ev.TargetType, TargetID: strconv.FormatInt(u.ID, 10), Detail: map[string]any{}})
	}
	return nil
}

type passkeyFinish struct {
	FlowToken  string          `json:"flow_token"`
	Credential json.RawMessage `json:"credential"`
}

func (s *Service) passkeyLoginBegin(w http.ResponseWriter, r *http.Request) error {
	options, session, err := s.WA.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return err
	}
	return s.challenge(w, r, store.Flow{Kind: store.FlowPasskeyLogin}, options, session)
}

// passkeyLoginFinish signs in whoever owns the credential. It serves as a second
// step too: user verification is required, so the passkey alone is two factors,
// and the pending password login simply expires.
func (s *Service) passkeyLoginFinish(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	var req passkeyFinish
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	ev.TargetType, ev.Detail["method"] = "session", "passkey"
	bad := httpx.Errorf(http.StatusUnauthorized, badPasskey)
	parsed, err := protocol.ParseCredentialRequestResponseBytes(req.Credential)
	if err != nil {
		return bad
	}
	ctx := r.Context()
	raw, err := s.Users.TakeChallenge(ctx, digest(req.FlowToken), store.FlowPasskeyLogin)
	if errors.Is(err, store.ErrNotFound) {
		return bad
	}
	if err != nil {
		return err
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(raw, &session); err != nil {
		return err
	}
	var owner passkeyUser
	var lookupErr error
	find := func(_, handle []byte) (webauthn.User, error) {
		digits, ok := strings.CutPrefix(string(handle), s.handlePrefix())
		if !ok {
			return nil, store.ErrNotFound // the other system's passkey
		}
		id, err := strconv.ParseInt(digits, 10, 64)
		if err != nil {
			return nil, err
		}
		u, err := s.Users.Get(ctx, id)
		if err == nil && u.Status == 1 {
			owner, err = s.loadPasskey(ctx, u)
		} else if err == nil {
			err = store.ErrNotFound
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			lookupErr = err
		}
		return owner, err
	}
	_, cred, err := s.WA.ValidatePasskeyLogin(find, session, parsed)
	if lookupErr != nil {
		return lookupErr
	}
	if err != nil {
		return bad
	}
	if err := s.used(r, ev, owner, cred); err != nil {
		return err
	}
	ev.ActorID, ev.ActorName, ev.TargetID = owner.ID, owner.Username, strconv.FormatInt(owner.ID, 10)
	s.Auth.Issue(w, owner.ID, owner.SessionVersion)
	httpx.JSON(w, http.StatusOK, map[string]any{"id": owner.ID, "username": owner.Username})
	return nil
}

func (s *Service) proofPasskeyBegin(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Purpose string `json:"purpose"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	if err := validPurpose(req.Purpose); err != nil {
		return err
	}
	ctx := r.Context()
	u, err := s.Users.Get(ctx, auth.UserID(ctx))
	if err != nil {
		return err
	}
	user, err := s.loadPasskey(ctx, u)
	if err != nil {
		return err
	}
	if len(user.creds) == 0 {
		return httpx.Errorf(http.StatusBadRequest, "尚未绑定通用密钥")
	}
	options, session, err := s.WA.BeginLogin(user, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return err
	}
	return s.challenge(w, r, store.Flow{Kind: store.FlowPasskeyProof, AccountID: u.ID, Version: u.SessionVersion, Purpose: req.Purpose}, options, session)
}

func (s *Service) proofPasskeyFinish(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	var req passkeyFinish
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	s.target(ev)
	ev.Detail["method"] = "passkey"
	bad := httpx.Errorf(http.StatusBadRequest, badPasskey)
	parsed, err := protocol.ParseCredentialRequestResponseBytes(req.Credential)
	if err != nil {
		return bad
	}
	ctx, hash := r.Context(), digest(req.FlowToken)
	f, err := s.Users.Flow(ctx, hash, store.FlowPasskeyProof)
	if errors.Is(err, store.ErrNotFound) || (err == nil && f.AccountID != ev.ActorID) {
		return bad
	}
	if err != nil {
		return err
	}
	ev.Detail["purpose"] = f.Purpose
	key := "proof:" + ev.TargetID
	if !s.Limiter.Acquire(key) {
		return tooMany()
	}
	settle := s.Limiter.Release
	defer func() { settle(key) }()
	if _, err := s.Users.TakeFlow(ctx, hash, store.FlowPasskeyProof, f.Purpose, ev.ActorID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return bad
		}
		return err
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(f.Data, &session); err != nil {
		return err
	}
	u, err := s.Users.Get(ctx, ev.ActorID)
	if err != nil {
		return err
	}
	user, err := s.loadPasskey(ctx, u)
	if err != nil {
		return err
	}
	cred, err := s.WA.ValidateLogin(user, session, parsed)
	if err != nil {
		settle = s.Limiter.Fail
		return bad
	}
	if err := s.used(r, ev, user, cred); err != nil {
		return err
	}
	settle = s.Limiter.Reset
	return s.issueProof(w, r, u, f.Purpose)
}

func (s *Service) passkeyBindBegin(w http.ResponseWriter, r *http.Request) error {
	if err := s.Require(r, PurposePasskeyBind); err != nil {
		return err
	}
	ctx := r.Context()
	u, err := s.Users.Get(ctx, auth.UserID(ctx))
	if err != nil {
		return err
	}
	user, err := s.loadPasskey(ctx, u)
	if err != nil {
		return err
	}
	if len(user.creds) > 0 {
		return httpx.Errorf(http.StatusConflict, "已绑定通用密钥，请先解绑")
	}
	options, session, err := s.WA.BeginRegistration(user)
	if err != nil {
		return err
	}
	return s.challenge(w, r, store.Flow{Kind: store.FlowPasskeySetup, AccountID: u.ID, Version: u.SessionVersion}, options, session)
}

func (s *Service) passkeyBindFinish(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	var req passkeyFinish
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	s.target(ev)
	bad := httpx.Errorf(http.StatusBadRequest, badPasskey)
	parsed, err := protocol.ParseCredentialCreationResponseBytes(req.Credential)
	if err != nil {
		return bad
	}
	ctx := r.Context()
	data, err := s.Users.TakeFlow(ctx, digest(req.FlowToken), store.FlowPasskeySetup, "", ev.ActorID)
	if errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusBadRequest, "绑定已过期，请重新开始")
	}
	if err != nil {
		return err
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(data, &session); err != nil {
		return err
	}
	u, err := s.Users.Get(ctx, ev.ActorID)
	if err != nil {
		return err
	}
	cred, err := s.WA.CreateCredential(s.passkeyUser(u), session, parsed)
	if err != nil {
		return bad
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	codes, hashes := newBackupCodes()
	ver, first, err := s.Users.BindFactor(ctx, u.ID, store.FactorPasskey, cred.ID, raw, 0, hashes)
	if errors.Is(err, store.ErrConflict) {
		return httpx.Errorf(http.StatusConflict, "已绑定通用密钥，请先解绑")
	}
	if err != nil {
		return err
	}
	s.bound(w, u.ID, ver, first, codes)
	return nil
}
