package admin

import (
	"errors"
	"net/http"
	"strconv"

	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/httpx"
	"newapisub/internal/store"
)

// All routes in this file are super-only.

func (s *Server) listAdmins(w http.ResponseWriter, r *http.Request) error {
	as, err := s.Store.ListAdmins(r.Context())
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, as)
	return nil
}

// createAdmin always makes a regular admin; the only super comes from the
// bootstrap configuration.
func (s *Server) createAdmin(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	ev.TargetType = "admin"
	if !usernameRe.MatchString(req.Username) {
		return httpx.Errorf(http.StatusBadRequest, "用户名须为 3–32 位字母、数字或 _.-")
	}
	ev.Detail["username"] = req.Username
	if err := auth.ValidatePassword(req.Password); err != nil {
		return badRequest(err)
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return err
	}
	id, err := s.Store.CreateAdmin(r.Context(), req.Username, hash, store.RoleAdmin)
	if errors.Is(err, store.ErrConflict) {
		return httpx.Errorf(http.StatusConflict, "用户名已存在")
	}
	if err != nil {
		return err
	}
	ev.TargetID = strconv.FormatInt(id, 10)
	httpx.JSON(w, http.StatusCreated, map[string]any{"id": id, "username": req.Username, "role": store.RoleAdmin})
	return nil
}

// targetAdmin is target for an admin, with the name for the audit log. A failed
// lookup leaves it out: the operation itself reports what is wrong.
func (s *Server) targetAdmin(r *http.Request, ev *audit.Event) (int64, error) {
	id, err := target(r, ev, "admin")
	if err != nil {
		return 0, err
	}
	if u, err := s.Store.AdminUsers().Get(r.Context(), id); err == nil {
		ev.Detail["username"] = u.Username
	}
	return id, nil
}

func (s *Server) setAdminStatus(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := s.targetAdmin(r, ev)
	if err != nil {
		return err
	}
	status, err := httpx.DecodeStatus(w, r)
	if err != nil {
		return err
	}
	ev.Detail["to"] = status
	if id == ev.ActorID {
		return httpx.Errorf(http.StatusBadRequest, "不能修改自己的状态")
	}
	prev, err := s.Store.SetAdminStatus(r.Context(), id, status)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return httpx.Errorf(http.StatusNotFound, "管理员不存在")
	case errors.Is(err, store.ErrLastSuper):
		return httpx.Errorf(http.StatusConflict, "至少保留一个启用的超级管理员")
	case err != nil:
		return err
	}
	ev.Detail["from"] = prev
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) resetAdminPassword(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := s.targetAdmin(r, ev)
	if err != nil {
		return err
	}
	hash, err := decodeNewPassword(w, r)
	if err != nil {
		return err
	}
	ver, err := s.Store.AdminUsers().SetPassword(r.Context(), id, hash)
	if errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusNotFound, "管理员不存在")
	}
	if err != nil {
		return err
	}
	if id == ev.ActorID { // the reset logged out every session, this one included
		s.Auth.Issue(w, id, ver)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// resetAdmin2FA is for an admin who lost their device. Not one's own: that would
// let a stolen session drop the second factor. A lone super uses reset-2fa on the server.
func (s *Server) resetAdmin2FA(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := s.targetAdmin(r, ev)
	if err != nil {
		return err
	}
	if id == ev.ActorID {
		return httpx.Errorf(http.StatusBadRequest, "不能重置自己的两步验证")
	}
	if err := s.Store.AdminUsers().ResetFactors(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusNotFound, "管理员不存在")
	} else if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) deleteAdmin(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := s.targetAdmin(r, ev)
	if err != nil {
		return err
	}
	if id == ev.ActorID {
		return httpx.Errorf(http.StatusBadRequest, "不能删除自己")
	}
	_, err = s.Store.DeleteAdmin(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return httpx.Errorf(http.StatusNotFound, "管理员不存在")
	case errors.Is(err, store.ErrLastSuper):
		return httpx.Errorf(http.StatusConflict, "至少保留一个启用的超级管理员")
	case errors.Is(err, store.ErrConflict):
		return httpx.Errorf(http.StatusConflict, "该管理员名下仍有用户或渠道（含已删除的渠道），不能删除")
	case err != nil:
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
