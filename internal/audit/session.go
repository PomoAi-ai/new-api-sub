package audit

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"newapisub/internal/auth"
	"newapisub/internal/httpx"
	"newapisub/internal/store"
)

// accountFinder adapts a user table to auth.FindAccount.
func accountFinder(users store.Users) auth.FindAccount {
	return func(ctx context.Context, name string) (auth.Account, bool, error) {
		u, err := users.ByName(ctx, name)
		if errors.Is(err, store.ErrNotFound) {
			return auth.Account{}, false, nil
		}
		if err != nil {
			return auth.Account{}, false, err
		}
		return auth.Account{ID: u.ID, Name: u.Username, Hash: u.PasswordHash, Version: u.SessionVersion, Active: u.Status == 1}, true, nil
	}
}

// Guard stands around Login's password check. fails is the username's count of
// recent failures in Login's limiter.
type Guard interface {
	// Admit decides whether a password may be tried at all. It sets ev.Action
	// for a refusal of its own.
	Admit(r *http.Request, ev *Event, fails int, captchaID, captchaCode string) error
	// Failed counts a wrong password and returns the answer.
	Failed(r *http.Request, fails int) error
	// Gate decides whether a correct password is enough. A non-nil answer is
	// sent instead of a session.
	Gate(ctx context.Context, acc auth.Account) (any, error)
}

// Login is POST /api/auth/login for the given user table: {username,
// password} -> {id, username} plus session cookie, or the guard's answer without one.
func (rec Recorder) Login(m *auth.Manager, lim *auth.Limiter, users store.Users, guard Guard) http.Handler {
	find := accountFinder(users)
	return rec.Wrap("auth.login", func(w http.ResponseWriter, r *http.Request, ev *Event) error {
		var req struct {
			Username    string `json:"username"`
			Password    string `json:"password"`
			CaptchaID   string `json:"captcha_id"`
			CaptchaCode string `json:"captcha_code"`
		}
		if err := httpx.Decode(w, r, &req); err != nil {
			return err
		}
		ev.ActorName = req.Username
		ev.TargetType = "session"
		key := auth.LoginKey(req.Username)
		if err := guard.Admit(r, ev, lim.Failures(key), req.CaptchaID, req.CaptchaCode); err != nil {
			return err
		}
		acc, err := auth.Login(r.Context(), lim, find, req.Username, req.Password)
		if acc.ID > 0 {
			ev.ActorID, ev.ActorName = acc.ID, acc.Name
		}
		locked := httpx.Errorf(http.StatusTooManyRequests, "登录失败次数过多，请 15 分钟后再试")
		switch {
		case errors.Is(err, auth.ErrLocked):
			ev.Detail["reason"] = "locked"
			return locked
		case errors.Is(err, auth.ErrBadCredentials):
			failed := guard.Failed(r, lim.Failures(key))
			// The failure that locks the name says so: asking for a captcha would be no use.
			if lim.Locked(key) {
				return locked
			}
			return failed
		case errors.Is(err, auth.ErrDisabled):
			return httpx.Errorf(http.StatusForbidden, "账号已禁用")
		case err != nil:
			return err
		}
		ev.TargetID = strconv.FormatInt(acc.ID, 10)
		pending, err := guard.Gate(r.Context(), acc)
		if err != nil {
			return err
		}
		if pending != nil {
			ev.Action = "auth.login_pending"
			httpx.JSON(w, http.StatusOK, pending)
			return nil
		}
		m.Issue(w, acc.ID, acc.Version)
		httpx.JSON(w, http.StatusOK, map[string]any{"id": acc.ID, "username": acc.Name})
		return nil
	})
}

// Logout is POST /api/auth/logout (behind Require): invalidates every
// session of the user and clears the cookie.
func (rec Recorder) Logout(m *auth.Manager, users store.Users) http.Handler {
	return m.Require(rec.Wrap("auth.logout", func(w http.ResponseWriter, r *http.Request, ev *Event) error {
		ev.TargetType, ev.TargetID = "session", strconv.FormatInt(ev.ActorID, 10)
		if err := users.BumpVersion(r.Context(), ev.ActorID); err != nil {
			return err
		}
		m.Clear(w)
		w.WriteHeader(http.StatusNoContent)
		return nil
	}))
}

// ChangePassword is the signed-in user's own password change, on either system.
func (rec Recorder) ChangePassword(m *auth.Manager, users store.Users) http.Handler {
	return m.Require(rec.Wrap("auth.password_change", func(w http.ResponseWriter, r *http.Request, ev *Event) error {
		var req struct {
			OldPassword string `json:"old_password"`
			NewPassword string `json:"new_password"`
		}
		if err := httpx.Decode(w, r, &req); err != nil {
			return err
		}
		ev.TargetType, ev.TargetID = "user", strconv.FormatInt(ev.ActorID, 10)
		if rec.ActorType == "admin" {
			ev.TargetType = "admin"
		}
		if err := auth.ValidatePassword(req.NewPassword); err != nil {
			return httpx.Errorf(http.StatusBadRequest, "%s", err.Error())
		}
		u, err := users.Get(r.Context(), ev.ActorID)
		if err != nil {
			return err
		}
		// 400 rather than 401 so the frontend does not treat it as a logout.
		if !auth.CheckPassword(u.PasswordHash, req.OldPassword) {
			return httpx.Errorf(http.StatusBadRequest, "原密码错误")
		}
		hash, err := auth.HashPassword(req.NewPassword)
		if err != nil {
			return err
		}
		ver, err := users.SetPassword(r.Context(), u.ID, hash)
		if err != nil {
			return err
		}
		m.Issue(w, u.ID, ver)
		w.WriteHeader(http.StatusNoContent)
		return nil
	}))
}
