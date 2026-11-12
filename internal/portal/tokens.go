package portal

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/httpx"
	"newapisub/internal/security"
	"newapisub/internal/store"
)

// maxTokenExpiry is 9999-12-31, which keeps the value within what PostgreSQL stores.
const maxTokenExpiry = 253402300799

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) error {
	tokens, err := s.Store.ListTokens(r.Context(), auth.UserID(r.Context()))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, tokens)
	return nil
}

// createToken is the one answer that holds the token itself.
func (s *Server) createToken(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	var req struct {
		Name      string `json:"name"`
		ExpiresAt *int64 `json:"expires_at"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 64 || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return httpx.Errorf(http.StatusBadRequest, "令牌名称须为 1–64 个字符且不含控制字符")
	}
	var expires *time.Time
	if req.ExpiresAt != nil {
		t := time.Unix(*req.ExpiresAt, 0)
		if !t.After(time.Now()) || *req.ExpiresAt > maxTokenExpiry {
			return httpx.Errorf(http.StatusBadRequest, "expires_at 须为将来的 Unix 秒")
		}
		expires = &t
	}
	ev.TargetType, ev.Detail["name"] = "api_token", name
	if err := s.Security.Require(r, security.PurposeTokenCreate); err != nil {
		return err
	}
	token, hash, hint := auth.NewToken()
	t, err := s.Store.CreateToken(r.Context(), ev.ActorID, name, hash, hint, expires)
	if errors.Is(err, store.ErrConflict) {
		return httpx.Errorf(http.StatusConflict, "令牌数量已达上限，请先撤销不用的令牌")
	}
	if err != nil {
		return err
	}
	ev.TargetID = strconv.FormatInt(t.ID, 10)
	httpx.JSON(w, http.StatusCreated, map[string]any{"id": t.ID, "name": t.Name, "hint": t.Hint,
		"expires_at": t.ExpiresAt, "created_at": t.CreatedAt, "token": token})
	return nil
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	ev.TargetType, ev.TargetID = "api_token", r.PathValue("id")
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	name, err := s.Store.DeleteToken(r.Context(), id, ev.ActorID)
	if errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusNotFound, "令牌不存在")
	}
	if err != nil {
		return err
	}
	ev.Detail["name"] = name
	w.WriteHeader(http.StatusNoContent)
	return nil
}
