package security

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
	"github.com/pquerna/otp/totp"

	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/httpx"
	"newapisub/internal/store"
)

const totpPeriod = 30

var totpOpts = hotp.ValidateOpts{Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}

func (s *Service) totpField(id int64) string {
	return fmt.Sprintf("totp:%s:%d", s.Users.Realm(), id)
}

// matchStep finds the time step of code, one step either side of now for clock
// drift. Knowing the step, not just that the code fits, is what stops a replay.
func matchStep(secret, code string, now time.Time) (int64, bool) {
	cur := now.Unix() / totpPeriod
	for step := cur - 1; step <= cur+1; step++ {
		if ok, _ := hotp.ValidateCustom(code, uint64(step), secret, totpOpts); ok {
			return step, true
		}
	}
	return 0, false
}

// checkTOTP says whether code is good and unused, and uses it up. A secret that
// cannot be opened is an error and not a wrong code: the master key was lost,
// which backup codes and passkeys survive.
func (s *Service) checkTOTP(ctx context.Context, id int64, code string) (bool, error) {
	sealed, err := s.Users.TOTP(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	secret, err := s.Box.TOTP.Open(s.totpField(id), sealed)
	if err != nil {
		return false, fmt.Errorf("open the TOTP secret of %s %d: %w", s.Users.Realm(), id, err)
	}
	step, ok := matchStep(string(secret), code, time.Now())
	if !ok {
		return false, nil
	}
	return s.Users.AdvanceTOTP(ctx, id, step)
}

func (s *Service) totpSetup(w http.ResponseWriter, r *http.Request) error {
	if err := s.Require(r, PurposeTOTPBind); err != nil {
		return err
	}
	ctx := r.Context()
	u, err := s.Users.Get(ctx, auth.UserID(ctx))
	if err != nil {
		return err
	}
	st, err := s.Users.FactorStatus(ctx, u.ID)
	if err != nil {
		return err
	}
	if st.TOTP {
		return httpx.Errorf(http.StatusConflict, "已绑定验证器，请先解绑")
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: s.Issuer, AccountName: u.Username})
	if err != nil {
		return err
	}
	token, err := s.newFlow(ctx, store.Flow{Kind: store.FlowTOTPSetup, AccountID: u.ID, Version: u.SessionVersion,
		Data: s.Box.TOTP.Seal(s.totpField(u.ID), []byte(key.Secret()))}, setupTTL)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"flow_token": token, "otpauth_url": key.URL(), "secret": key.Secret()})
	return nil
}

// totpConfirm binds the secret once its first code proves the app has it. That
// code's step counts as used.
func (s *Service) totpConfirm(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	var req struct {
		FlowToken string `json:"flow_token"`
		Code      string `json:"code"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	s.target(ev)
	ctx, hash := r.Context(), digest(req.FlowToken)
	f, err := s.Users.Flow(ctx, hash, store.FlowTOTPSetup)
	if errors.Is(err, store.ErrNotFound) || (err == nil && f.AccountID != ev.ActorID) {
		return httpx.Errorf(http.StatusBadRequest, "绑定已过期，请重新开始")
	}
	if err != nil {
		return err
	}
	secret, err := s.Box.TOTP.Open(s.totpField(ev.ActorID), f.Data)
	if err != nil {
		return fmt.Errorf("open a TOTP setup of %s %d: %w", s.Users.Realm(), ev.ActorID, err)
	}
	step, ok := matchStep(string(secret), req.Code, time.Now())
	if !ok {
		return httpx.Errorf(http.StatusBadRequest, badCode)
	}
	if _, err := s.Users.TakeFlow(ctx, hash, store.FlowTOTPSetup, "", ev.ActorID); errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusBadRequest, "绑定已过期，请重新开始")
	} else if err != nil {
		return err
	}
	codes, hashes := newBackupCodes()
	ver, first, err := s.Users.BindFactor(ctx, ev.ActorID, store.FactorTOTP, nil, f.Data, step, hashes)
	if errors.Is(err, store.ErrConflict) {
		return httpx.Errorf(http.StatusConflict, "已绑定验证器，请先解绑")
	}
	if err != nil {
		return err
	}
	s.bound(w, ev.ActorID, ver, first, codes)
	return nil
}
