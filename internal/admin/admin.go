// Package admin serves the management API. A super admin sees everything and
// alone manages admins and channel templates; a regular admin sees their own
// portal users, their own keys and those users' channels, with the matching
// logs, stats and audit log.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"maps"
	"net/http"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/brand"
	"newapisub/internal/channels"
	"newapisub/internal/connection"
	"newapisub/internal/httpx"
	"newapisub/internal/logstore"
	"newapisub/internal/newapi"
	"newapisub/internal/secret"
	"newapisub/internal/security"
	"newapisub/internal/store"
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`)

type Server struct {
	Store    *store.Store
	Pools    *connection.Pools
	Auth     *auth.Manager
	Limiter  *auth.Limiter
	Audit    audit.Recorder
	Security *security.Service
	Box      *secret.Box
	// Mongo is the store of synced logs, nil without MONGO_URI.
	Mongo *logstore.Lazy
	// Restricted serves only login, account, settings and the pools: none is
	// stored, so Pools is nil. RestrictedReason is shown to supers.
	Restricted       bool
	RestrictedReason string

	channels *channels.Service
}

// Register adds the admin API routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	req := s.Auth.Require
	v := func(name string, fn httpx.Handler) http.Handler { return req(s.Audit.View(name, fn)) }
	a := func(action string, fn audit.Handler) http.Handler { return req(s.Audit.Wrap(action, fn)) }
	// A regular admin's attempt ends in a 403 inside the audit wrapper, so the log shows it.
	superV := func(name string, fn httpx.Handler) http.Handler {
		return v(name, func(w http.ResponseWriter, r *http.Request) error {
			if err := requireSuper(r); err != nil {
				return err
			}
			return fn(w, r)
		})
	}
	superA := func(action string, fn audit.Handler) http.Handler {
		return a(action, func(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
			if err := requireSuper(r); err != nil {
				return err
			}
			return fn(w, r, ev)
		})
	}

	mux.Handle("POST /api/auth/login", s.Security.Throttle(s.Audit.Login(s.Auth, s.Limiter, s.Store.AdminUsers(), s.Security)))
	mux.Handle("POST /api/auth/logout", s.Audit.Logout(s.Auth, s.Store.AdminUsers()))
	mux.Handle("GET /api/me", req(httpx.Handler(s.me)))
	mux.Handle("POST /api/me/password", s.Audit.ChangePassword(s.Auth, s.Store.AdminUsers()))
	mux.Handle("POST /api/me/username", superA("auth.username_change", s.changeUsername))
	s.Security.Register(mux)

	brand.Register(mux, s.Store, store.SettingAdminName)
	mux.Handle("GET /api/settings", superV("settings", s.getSettings))
	mux.Handle("PUT /api/settings", superA("settings.update", s.putSettings))
	mux.Handle("PUT /api/settings/logo", superA("settings.logo_update", s.putLogo))
	mux.Handle("DELETE /api/settings/logo", superA("settings.logo_delete", s.deleteLogo))
	mux.Handle("GET /api/pools", superV("pools", s.listPools))
	mux.Handle("POST /api/pools/test", superA("pool.test", s.testPool))
	mux.Handle("POST /api/pools", superA("pool.create", s.createPool))
	mux.Handle("PUT /api/pools/{id}", superA("pool.update", s.updatePool))
	mux.Handle("DELETE /api/pools/{id}", superA("pool.delete", s.deletePool))
	mux.Handle("PUT /api/pools/{id}/sync", superA("pool.sync_update", s.setSync))
	mux.Handle("POST /api/pools/{id}/sync/reset", superA("pool.sync_reset", s.resetSync))
	mux.Handle("GET /api/pools/{id}/sync/verify", superV("pool_sync_verify", s.verifySync))
	mux.Handle("GET /api/pools/{id}/conflicts", superV("pool_conflicts", s.listConflicts))
	mux.Handle("POST /api/pools/{id}/conflicts/resolve", superA("pool.conflict_resolve", s.resolveConflict))

	// Everything above works without new-api, which is how a super gets out of this mode.
	if s.Restricted {
		mux.Handle("/api/", httpx.Handler(func(http.ResponseWriter, *http.Request) error {
			return httpx.Errorf(http.StatusServiceUnavailable, "尚未配置 new-api 号池，请超级管理员在系统设置中添加")
		}))
		return
	}

	s.channels = &channels.Service{Store: s.Store, Pools: s.Pools, Box: s.Box, Audit: s.Audit}

	mux.Handle("GET /api/admins", superV("admins", s.listAdmins))
	mux.Handle("POST /api/admins", superA("admin.create", s.createAdmin))
	mux.Handle("POST /api/admins/{id}/status", superA("admin.status", s.setAdminStatus))
	mux.Handle("POST /api/admins/{id}/password", superA("admin.password_reset", s.resetAdminPassword))
	mux.Handle("POST /api/admins/{id}/2fa/reset", superA("admin.2fa_reset", s.resetAdmin2FA))
	mux.Handle("DELETE /api/admins/{id}", superA("admin.delete", s.deleteAdmin))

	mux.Handle("GET /api/users", v("users", s.listUsers))
	mux.Handle("POST /api/users", a("user.create", s.createUser))
	mux.Handle("POST /api/users/{id}/status", a("user.status", s.setUserStatus))
	mux.Handle("POST /api/users/{id}/password", a("user.password_reset", s.resetPassword))
	mux.Handle("POST /api/users/{id}/2fa/reset", a("user.2fa_reset", s.resetUser2FA))
	mux.Handle("DELETE /api/users/{id}", a("user.delete", s.deleteUser))
	mux.Handle("POST /api/users/{id}/log-scope", a("user.log_scope", s.setUserLogScope))
	mux.Handle("GET /api/users/{id}/settlements", v("user_settlements", s.userSettlements))
	mux.Handle("POST /api/users/{id}/settlements", a("settlement.create", s.addSettlement))

	mux.Handle("GET /api/templates", superV("templates", s.listTemplates))
	mux.Handle("POST /api/templates", superA("template.create", s.createTemplate))
	mux.Handle("PUT /api/templates/{id}", superA("template.update", s.updateTemplate))
	mux.Handle("DELETE /api/templates/{id}", superA("template.delete", s.deleteTemplate))

	s.channels.Register(mux, req)

	mux.Handle("GET /api/audit-logs", v("audit_logs", s.auditLogs))

	for name, path := range newapiReads {
		mux.Handle("GET /api/newapi/"+name, superV("newapi", s.newapiRead(path)))
	}
	mux.Handle("POST /api/newapi/fetch-models", superA("channel.fetch_models", s.fetchModels))
	mux.Handle("/api/", httpx.APINotFound)
}

func requireSuper(r *http.Request) error {
	if auth.Role(r.Context()) != store.RoleSuper {
		return httpx.Errorf(http.StatusForbidden, "仅超级管理员可用")
	}
	return nil
}

// scopeAdmin is the admin whose users and channels the caller is limited to,
// or 0 for a super.
func scopeAdmin(ctx context.Context) int64 {
	if auth.Role(ctx) == store.RoleSuper {
		return 0
	}
	return auth.UserID(ctx)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	portalURL, err := s.Store.PortalPublicURL(r.Context())
	if err != nil {
		return err
	}
	factors, err := s.Store.AdminUsers().FactorStatus(r.Context(), auth.UserID(r.Context()))
	if err != nil {
		return err
	}
	var qpu float64 // of the home pool, the unit every page shows quota in
	if s.Pools != nil {
		qpu = s.Pools.Home().QuotaPerUnit
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"id": auth.UserID(r.Context()), "username": auth.Username(r.Context()), "role": auth.Role(r.Context()),
		"quota_per_unit": qpu, "portal_url": portalURL, "restricted": s.Restricted, "mfa_enabled": factors.Any(),
	})
	return nil
}

// changeUsername renames the super who asks; the password confirms it is them.
func (s *Server) changeUsername(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	ev.TargetType, ev.TargetID = "admin", strconv.FormatInt(ev.ActorID, 10)
	if !usernameRe.MatchString(req.Username) {
		return httpx.Errorf(http.StatusBadRequest, "用户名须为 3–32 位字母、数字或 _.-")
	}
	users := s.Store.AdminUsers()
	u, err := users.Get(r.Context(), ev.ActorID)
	if err != nil {
		return err
	}
	// 400 rather than 401 so the frontend does not treat it as a logout.
	if !auth.CheckPassword(u.PasswordHash, req.Password) {
		return httpx.Errorf(http.StatusBadRequest, "密码错误")
	}
	ev.Detail["old_username"], ev.Detail["new_username"] = u.Username, req.Username
	err = users.SetUsername(r.Context(), u.ID, req.Username)
	if errors.Is(err, store.ErrConflict) {
		return httpx.Errorf(http.StatusConflict, "用户名已存在")
	}
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func badRequest(err error) error { return httpx.Errorf(http.StatusBadRequest, "%s", err.Error()) }

// target parses {id} and records it as the audit target.
func target(r *http.Request, ev *audit.Event, typ string) (int64, error) {
	ev.TargetType, ev.TargetID = typ, r.PathValue("id")
	return httpx.PathID(r, "id")
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) error {
	scope := scopeAdmin(r.Context())
	us, err := s.Store.ListPortalUsers(r.Context(), scope)
	if err != nil {
		return err
	}
	// The rest of the page works without new-api's database. Nil, not 0: zero would
	// read as nothing to settle.
	used, _, err := s.channels.UsedUSD(r.Context(), store.ChannelFilter{AdminID: scope})
	if err != nil {
		log.Printf("user list: read used quota: %v", err)
	} else {
		for i := range us {
			v := used[us[i].ID]
			us[i].UsedUSD = &v
		}
	}
	httpx.JSON(w, http.StatusOK, us)
	return nil
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		AdminID  int64  `json:"admin_id"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	ev.TargetType = "user"
	if !usernameRe.MatchString(req.Username) {
		return httpx.Errorf(http.StatusBadRequest, "用户名须为 3–32 位字母、数字或 _.-")
	}
	ev.Detail["username"] = req.Username
	if err := auth.ValidatePassword(req.Password); err != nil {
		return badRequest(err)
	}
	// Only a super may give the user to another admin.
	adminID := ev.ActorID
	if auth.Role(r.Context()) == store.RoleSuper && req.AdminID != 0 {
		adminID = req.AdminID
	}
	ev.Detail["admin_id"] = adminID
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return err
	}
	id, err := s.Store.CreatePortalUser(r.Context(), req.Username, hash, adminID)
	switch {
	case errors.Is(err, store.ErrConflict):
		return httpx.Errorf(http.StatusConflict, "用户名已存在")
	case errors.Is(err, store.ErrNotFound):
		return httpx.Errorf(http.StatusBadRequest, "所属管理员不存在")
	case err != nil:
		return err
	}
	ev.TargetID = strconv.FormatInt(id, 10)
	httpx.JSON(w, http.StatusCreated, map[string]any{"id": id, "username": req.Username, "admin_id": adminID})
	return nil
}

// targetUser is target for a portal user, which outside the caller's scope
// answers the same 404 as one that does not exist.
func (s *Server) targetUser(r *http.Request, ev *audit.Event) (int64, error) {
	id, err := target(r, ev, "user")
	if err != nil {
		return 0, err
	}
	u, err := s.Store.PortalUsers().Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return 0, httpx.Errorf(http.StatusNotFound, "用户不存在")
	}
	if err != nil {
		return 0, err
	}
	if scope := scopeAdmin(r.Context()); scope != 0 && u.AdminID != scope {
		return 0, httpx.Errorf(http.StatusNotFound, "用户不存在")
	}
	ev.Detail["username"] = u.Username
	return id, nil
}

func (s *Server) setUserStatus(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := s.targetUser(r, ev)
	if err != nil {
		return err
	}
	status, err := httpx.DecodeStatus(w, r)
	if err != nil {
		return err
	}
	ev.Detail["to"] = status
	prev, err := s.Store.SetPortalStatus(r.Context(), id, status)
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

// decodeNewPassword reads {password} and returns its hash.
func decodeNewPassword(w http.ResponseWriter, r *http.Request) (string, error) {
	var req struct {
		Password string `json:"password"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return "", err
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		return "", badRequest(err)
	}
	return auth.HashPassword(req.Password)
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := s.targetUser(r, ev)
	if err != nil {
		return err
	}
	hash, err := decodeNewPassword(w, r)
	if err != nil {
		return err
	}
	if _, err := s.Store.PortalUsers().SetPassword(r.Context(), id, hash); errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusNotFound, "用户不存在")
	} else if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) resetUser2FA(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := s.targetUser(r, ev)
	if err != nil {
		return err
	}
	if err := s.Store.PortalUsers().ResetFactors(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusNotFound, "用户不存在")
	} else if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := s.targetUser(r, ev)
	if err != nil {
		return err
	}
	_, err = s.Store.DeletePortalUser(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrConflict):
		return httpx.Errorf(http.StatusConflict, "该用户名下仍有渠道（含已删除的渠道）或结账记录，不能删除，可改为禁用")
	case errors.Is(err, store.ErrNotFound):
		return httpx.Errorf(http.StatusNotFound, "用户不存在")
	case err != nil:
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) error {
	ts, err := s.Store.ListTemplates(r.Context(), false)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, ts)
	return nil
}

const channelTypeVertexAI = 41

// decodeTemplate reads and validates a template body.
func decodeTemplate(w http.ResponseWriter, r *http.Request) (store.Template, error) {
	var t store.Template
	if err := httpx.Decode(w, r, &t); err != nil {
		return t, err
	}
	t.Name = strings.TrimSpace(t.Name)
	t.BaseURL = strings.TrimSpace(t.BaseURL)
	t.Models = strings.TrimSpace(t.Models)
	t.Group = strings.TrimSpace(t.Group)
	t.TestModel = strings.TrimSpace(t.TestModel)
	switch {
	case t.Name == "" || len([]rune(t.Name)) > 64:
		return t, httpx.Errorf(http.StatusBadRequest, "模板名称须为 1–64 个字符")
	case t.ChannelType <= 0:
		return t, httpx.Errorf(http.StatusBadRequest, "channel_type 须为正整数")
	case len(channels.ModelList(t.Models)) == 0:
		return t, httpx.Errorf(http.StatusBadRequest, "models 不能为空")
	case t.Group == "":
		return t, httpx.Errorf(http.StatusBadRequest, "group 不能为空")
	case t.Weight < 0:
		return t, httpx.Errorf(http.StatusBadRequest, "weight 不能为负数")
	}
	if t.BaseURL != "" && !channels.ValidURL(t.BaseURL) {
		return t, httpx.Errorf(http.StatusBadRequest, "base_url 须为 http(s) 完整地址，且不含用户信息")
	}
	extra, err := newapi.ParseExtra(t.Extra)
	if err != nil {
		return t, httpx.Errorf(http.StatusBadRequest, "%s", err.Error())
	}
	if t.Enabled && extra.MissingOther(t.ChannelType) {
		return t, httpx.Errorf(http.StatusBadRequest, "该类型需要额外配置")
	}
	if t.Enabled && t.ChannelType == channelTypeVertexAI {
		// new-api rejects anything else when the channel is created, which would fail every later add.
		var region map[string]any
		if json.Unmarshal([]byte(extra.Other), &region) != nil || region["default"] == nil {
			return t, httpx.Errorf(http.StatusBadRequest, `Vertex AI 的部署地区须为含 default 的 JSON，例如 {"default":"us-central1"}`)
		}
	}
	t.Extra, err = json.Marshal(extra)
	return t, err
}

// templateFields returns the editable fields of t keyed by JSON name.
// auditedExtra is a template's extra in the audit log: its values may hold
// secrets such as request headers, so a diff can compare them but only the
// names get marshalled.
type auditedExtra struct {
	Keys   []string `json:"keys"`
	values map[string]any
}

func templateFields(t store.Template) map[string]any {
	b, _ := json.Marshal(t)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	delete(m, "id")
	delete(m, "created_at")
	delete(m, "updated_at")
	values, _ := m["extra"].(map[string]any)
	m["extra"] = auditedExtra{slices.Sorted(maps.Keys(values)), values}
	return m
}

// templateConflict gives a duplicate template name its own 409; other errors
// pass through.
func templateConflict(err error) error {
	if errors.Is(err, store.ErrConflict) {
		return httpx.Errorf(http.StatusConflict, "模板名称已存在")
	}
	return err
}

// checkTemplatePools refuses a template that sends its channels nowhere, to a
// pool twice, or to a pool that does not exist.
func (s *Server) checkTemplatePools(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return httpx.Errorf(http.StatusBadRequest, "至少选择一个号池")
	}
	rows, err := s.Store.Pools(ctx)
	if err != nil {
		return err
	}
	for i, id := range ids {
		if slices.Contains(ids[:i], id) {
			return httpx.Errorf(http.StatusBadRequest, "号池重复")
		}
		if !slices.ContainsFunc(rows, func(p store.Pool) bool { return p.ID == id }) {
			return httpx.Errorf(http.StatusBadRequest, "号池 %d 不存在", id)
		}
	}
	return nil
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	ev.TargetType = "template"
	t, err := decodeTemplate(w, r)
	if err != nil {
		return err
	}
	if err := s.checkTemplatePools(r.Context(), t.PoolIDs); err != nil {
		return err
	}
	ev.Detail["name"], ev.Detail["fields"] = t.Name, templateFields(t)
	id, err := s.Store.CreateTemplate(r.Context(), t)
	if err := templateConflict(err); err != nil {
		return err
	}
	ev.TargetID = strconv.FormatInt(id, 10)
	t, err = s.Store.Template(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, t)
	return nil
}

func (s *Server) updateTemplate(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := target(r, ev, "template")
	if err != nil {
		return err
	}
	t, err := decodeTemplate(w, r)
	if err != nil {
		return err
	}
	if err := s.checkTemplatePools(r.Context(), t.PoolIDs); err != nil {
		return err
	}
	t.ID = id
	old, err := s.Store.Template(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusNotFound, "模板不存在")
	}
	if err != nil {
		return err
	}
	ev.Detail["name"] = old.Name
	before, after := templateFields(old), templateFields(t)
	changes := map[string]any{}
	for k, v := range after {
		if !reflect.DeepEqual(before[k], v) {
			changes[k] = map[string]any{"from": before[k], "to": v}
		}
	}
	ev.Detail["changes"] = changes
	err = s.Store.UpdateTemplate(r.Context(), t)
	if errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusNotFound, "模板不存在")
	}
	if err := templateConflict(err); err != nil {
		return err
	}
	if t, err = s.Store.Template(r.Context(), id); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, t)
	return nil
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	id, err := target(r, ev, "template")
	if err != nil {
		return err
	}
	if old, err := s.Store.Template(r.Context(), id); err == nil {
		ev.Detail["name"] = old.Name
	}
	if err := s.Store.DeleteTemplate(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		return httpx.Errorf(http.StatusNotFound, "模板不存在")
	} else if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ipFilterRe keeps LIKE's wildcards out of a prefix.
var ipFilterRe = regexp.MustCompile(`^[0-9a-fA-F:.]{1,45}$`)

// parseIPFilter reads ?ip=: a complete address matches exactly, in the form the
// log stores it, and anything else is the start of an address, like 192.168.
func parseIPFilter(raw string) (ip, prefix string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil
	}
	if !ipFilterRe.MatchString(raw) {
		return "", "", httpx.Errorf(http.StatusBadRequest, "无效的 ip")
	}
	if addr, err := netip.ParseAddr(raw); err == nil {
		return addr.Unmap().String(), "", nil
	}
	return "", strings.ToLower(raw), nil
}

func (s *Server) auditLogs(w http.ResponseWriter, r *http.Request) error {
	v := r.URL.Query()
	f, err := audit.ParseQuery(v)
	if err != nil {
		return err
	}
	f.ActorType, f.ActorName = v.Get("actor_type"), strings.TrimSpace(v.Get("actor_name"))
	if f.ActorType != "" && f.ActorType != "portal" && f.ActorType != "admin" {
		return httpx.Errorf(http.StatusBadRequest, "actor_type 只能为 portal 或 admin")
	}
	if raw := v.Get("actor_id"); raw != "" {
		if f.ActorID, err = strconv.ParseInt(raw, 10, 64); err != nil || f.ActorID <= 0 {
			return httpx.Errorf(http.StatusBadRequest, "无效的 actor_id")
		}
	}
	switch v.Get("owner") {
	case "":
	case "self":
		f.ActorType, f.ActorID = "admin", auth.UserID(r.Context())
	default:
		return httpx.Errorf(http.StatusBadRequest, "owner 只能为 self")
	}
	if f.IP, f.IPPrefix, err = parseIPFilter(v.Get("ip")); err != nil {
		return err
	}
	f.AdminID = scopeAdmin(r.Context())
	items, total, err := s.Store.ListAudit(r.Context(), f)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
	return nil
}
