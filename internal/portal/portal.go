// Package portal serves the supplier portal API: a portal user adds channels
// of the types admins have opened, and sees their own channels, stats and
// settlements, and the logs their log scope allows.
package portal

import (
	"encoding/json"
	"net/http"
	"net/url"

	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/brand"
	"newapisub/internal/channels"
	"newapisub/internal/connection"
	"newapisub/internal/httpx"
	"newapisub/internal/secret"
	"newapisub/internal/security"
	"newapisub/internal/store"
)

type Server struct {
	Store    *store.Store
	Pools    *connection.Pools
	Box      *secret.Box
	Auth     *auth.Manager
	Limiter  *auth.Limiter
	Audit    audit.Recorder
	Security *security.Service

	channels *channels.Service
}

// Register adds the portal API routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	users := s.Store.PortalUsers()
	req := s.Auth.Require
	s.channels = &channels.Service{Store: s.Store, Pools: s.Pools, Box: s.Box, Audit: s.Audit, Portal: true}
	mux.Handle("POST /api/auth/login", s.Security.Throttle(s.Audit.Login(s.Auth, s.Limiter, users, s.Security)))
	mux.Handle("POST /api/auth/logout", s.Audit.Logout(s.Auth, users))
	mux.Handle("GET /api/me", req(httpx.Handler(s.me)))
	mux.Handle("POST /api/me/password", s.Audit.ChangePassword(s.Auth, users))
	s.Security.Register(mux)
	s.channels.Register(mux, req)
	brand.Register(mux, s.Store, store.SettingPortalName)
	mux.Handle("GET /api/settlements", req(s.Audit.View("settlements", s.settlements)))
	mux.Handle("GET /api/audit-logs", req(s.Audit.View("audit_logs", s.auditLogs)))
	mux.Handle("GET /api/tokens", req(s.Audit.View("api_tokens", s.listTokens)))
	mux.Handle("POST /api/tokens", req(s.Audit.Wrap("auth.token_create", s.createToken)))
	mux.Handle("DELETE /api/tokens/{id}", req(s.Audit.Wrap("auth.token_delete", s.deleteToken)))
	s.channels.RegisterAPI(mux, s.Auth, s.Store.UseToken)
	mux.Handle("/api/", httpx.APINotFound)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	id := auth.UserID(r.Context())
	scope, err := s.Store.EffectiveLogScope(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"id": id, "username": auth.Username(r.Context()), "quota_per_unit": s.Pools.Home().QuotaPerUnit, "log_scope": scope,
	})
	return nil
}

func (s *Server) settlements(w http.ResponseWriter, r *http.Request) error {
	v, err := s.channels.Settlements(r.Context(), auth.UserID(r.Context()))
	if err != nil {
		return err
	}
	for i := range v.Items {
		v.Items[i].CreatedBy = "" // an admin's login name
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

// ownAuditFilter parses the audit list query and pins it to the caller:
// actor_type, actor_id and actor_name from the client are ignored. What the user
// read is for the admins' log only.
func ownAuditFilter(v url.Values, uid int64) (store.AuditFilter, error) {
	f, err := audit.ParseQuery(v)
	f.ActorType, f.ActorID, f.ActorName, f.NoViews = "portal", uid, "", true
	return f, err
}

// auditLogs lists the caller's own operations, without the proxy chain and
// user agent the admin view shows.
func (s *Server) auditLogs(w http.ResponseWriter, r *http.Request) error {
	f, err := ownAuditFilter(r.URL.Query(), auth.UserID(r.Context()))
	if err != nil {
		return err
	}
	logs, total, err := s.Store.ListAudit(r.Context(), f)
	if err != nil {
		return err
	}
	type view struct {
		ID         int64           `json:"id"`
		ActorType  string          `json:"actor_type"`
		ActorID    *int64          `json:"actor_id"`
		ActorName  string          `json:"actor_name"`
		Action     string          `json:"action"`
		TargetType string          `json:"target_type"`
		TargetID   string          `json:"target_id"`
		Success    bool            `json:"success"`
		Detail     json.RawMessage `json:"detail"`
		IP         string          `json:"ip"`
		CreatedAt  int64           `json:"created_at"`
	}
	items := make([]view, len(logs))
	for i, a := range logs {
		items[i] = view{a.ID, a.ActorType, a.ActorID, a.ActorName, a.Action, a.TargetType, a.TargetID, a.Success,
			a.Detail, a.IP, a.CreatedAt}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
	return nil
}
