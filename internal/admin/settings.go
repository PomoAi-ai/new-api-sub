package admin

import (
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/brand"
	"newapisub/internal/channels"
	"newapisub/internal/httpx"
	"newapisub/internal/store"
)

const maxSettlementUSD = 1e12 // what settlements.amount_usd holds

// validText trims a short free text and refuses control characters.
func validText(label, s string, minLen, maxLen int) (string, error) {
	s = strings.TrimSpace(s)
	if n := utf8.RuneCountInString(s); n < minLen || n > maxLen || !utf8.ValidString(s) || strings.ContainsFunc(s, unicode.IsControl) {
		return "", httpx.Errorf(http.StatusBadRequest, "%s须为 %d–%d 个字符且不含控制字符", label, minLen, maxLen)
	}
	return s, nil
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) error {
	x, err := s.Store.Settings(r.Context())
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, x)
	return nil
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	var x store.Settings
	if err := httpx.Decode(w, r, &x); err != nil {
		return err
	}
	ev.TargetType = "settings"
	if x.LogScope != store.LogScopeAll && x.LogScope != store.LogScopeSelf {
		return httpx.Errorf(http.StatusBadRequest, "log_scope 只能为 all 或 self")
	}
	var err error
	if x.PortalName, err = validText("门户名称", x.PortalName, 1, 64); err != nil {
		return err
	}
	if x.AdminName, err = validText("后台名称", x.AdminName, 1, 64); err != nil {
		return err
	}
	x.PortalPublicURL = strings.TrimSpace(x.PortalPublicURL)
	if x.PortalPublicURL != "" && !channels.ValidURL(x.PortalPublicURL) {
		return httpx.Errorf(http.StatusBadRequest, "门户登录网址须为 http(s) 完整地址，且不含用户信息")
	}
	ev.Detail["to"] = x
	// A lost row makes this fail, and saving is what repairs it.
	if old, err := s.Store.Settings(r.Context()); err == nil {
		ev.Detail["from"] = old
	}
	if err := s.Store.SaveSettings(r.Context(), x); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, x)
	return nil
}

func (s *Server) putLogo(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	ev.TargetType = "settings"
	contentType, data, err := brand.ReadLogo(w, r)
	if err != nil {
		return err
	}
	ev.Detail["content_type"], ev.Detail["bytes"] = contentType, len(data)
	if err := s.Store.SaveLogo(r.Context(), contentType, data); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) deleteLogo(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	ev.TargetType = "settings"
	if err := s.Store.DeleteLogo(r.Context()); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) setUserLogScope(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := s.targetUser(r, ev)
	if err != nil {
		return err
	}
	var req struct {
		LogScope string `json:"log_scope"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	if req.LogScope != "" && req.LogScope != store.LogScopeAll && req.LogScope != store.LogScopeSelf {
		return httpx.Errorf(http.StatusBadRequest, "log_scope 只能为空、all 或 self")
	}
	ev.Detail["to"] = req.LogScope
	prev, err := s.Store.SetPortalLogScope(r.Context(), id, req.LogScope)
	if errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusNotFound, "用户不存在")
	}
	if err != nil {
		return err
	}
	ev.Detail["from"] = prev
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) userSettlements(w http.ResponseWriter, r *http.Request) error {
	// A read has no audit target of its own to fill in; View records the route.
	id, err := s.targetUser(r, &audit.Event{Detail: map[string]any{}})
	if err != nil {
		return err
	}
	v, err := s.channels.Settlements(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) addSettlement(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := s.targetUser(r, ev)
	if err != nil {
		return err
	}
	var req struct {
		AmountUSD *float64 `json:"amount_usd"`
		Note      string   `json:"note"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	if req.AmountUSD == nil || *req.AmountUSD < 0 || *req.AmountUSD >= maxSettlementUSD {
		return httpx.Errorf(http.StatusBadRequest, "结账金额须为 0 到 1 万亿（不含）美元之间的数字")
	}
	note, err := validText("备注", req.Note, 0, 200)
	if err != nil {
		return err
	}
	ev.Detail["amount_usd"], ev.Detail["note"] = *req.AmountUSD, note
	x, err := s.Store.AddSettlement(r.Context(), id, *req.AmountUSD, note, auth.Username(r.Context()))
	if err != nil {
		return err
	}
	ev.Detail["settlement_id"] = x.ID
	httpx.JSON(w, http.StatusCreated, x)
	return nil
}
