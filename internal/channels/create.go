package channels

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/httpx"
	"newapisub/internal/newapi"
	"newapisub/internal/store"
)

const (
	maxKeyBytes = 64 << 10
	typeCodex   = 57
)

// createRequest is the body of POST /api/channels; anything else in it is
// ignored. UserID counts for admins only. TemplateID 0 means none was chosen.
type createRequest struct {
	Type         int    `json:"type"`
	Name         string `json:"name"`
	Key          string `json:"key"`
	BaseURL      string `json:"base_url"`
	Mode         string `json:"mode"`
	MultiKeyMode string `json:"multi_key_mode"`
	UserID       int64  `json:"user_id"`
	Models       string `json:"models"`
	TemplateID   int64  `json:"template_id"`
}

// validName trims and checks a channel name: 1-40 characters, no control
// characters.
func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	n := utf8.RuneCountInString(name)
	if n < 1 || n > 40 || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return "", httpx.Errorf(http.StatusBadRequest, "渠道名称须为 1–40 个字符且不含控制字符")
	}
	return name, nil
}

// CleanKey trims every line and drops the empty ones, in every mode: a pasted
// trailing newline would otherwise make a key that always fails upstream.
func CleanKey(raw string) string {
	var lines []string
	for line := range strings.Lines(raw) {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// checkBaseURL accepts only a plain https origin/path that does not point at
// a private or local IP literal. Hostnames are not resolved.
func checkBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || strings.ContainsAny(raw, "?#") {
		return httpx.Errorf(http.StatusBadRequest, "base_url 须为 https:// 开头的完整地址，且不含用户信息、查询参数或片段")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil {
		ip = ip.Unmap()
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return httpx.Errorf(http.StatusBadRequest, "base_url 不能指向内网、回环或本地地址")
		}
	}
	return nil
}

// ValidURL is the format check for a base URL set by a super, who may point at
// http and internal hosts. Credentials are refused: the URL ends up in the
// audit log.
func ValidURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

// checkMode also refuses what new-api would accept but cannot work: several
// plain keys stored as one key, or a Codex JSON key split into lines. A key
// spanning lines in single mode is fine when it is JSON.
func checkMode(req createRequest, key string) error {
	switch req.Mode {
	case "single":
		if strings.Contains(key, "\n") && !strings.HasPrefix(key, "{") && !strings.HasPrefix(key, "[") {
			return httpx.Errorf(http.StatusBadRequest, "单个添加只能填一个密钥，多个密钥请选择批量添加或多密钥聚合")
		}
		return nil
	case "batch", "multi_to_single":
		if req.Type == typeCodex {
			return httpx.Errorf(http.StatusBadRequest, "Codex 渠道只支持单个添加")
		}
		// Empty is what new-api takes from a script: it then always uses the first enabled key.
		if req.Mode == "multi_to_single" && req.MultiKeyMode != "" && req.MultiKeyMode != "random" && req.MultiKeyMode != "polling" {
			return httpx.Errorf(http.StatusBadRequest, "multi_key_mode 只能为 random 或 polling")
		}
		return nil
	}
	return httpx.Errorf(http.StatusBadRequest, "mode 只能为 single、batch 或 multi_to_single")
}

// newOwner decides whose the new channel is: a portal user owns theirs; an
// admin owns it themselves or gives it to a portal user in their scope. Either
// way the channel lands in the scope of the portal user's admin.
func (s *Service) newOwner(r *http.Request, requested int64) (userID, adminID int64, err error) {
	id := auth.UserID(r.Context())
	if s.Portal {
		u, err := s.Store.PortalUsers().Get(r.Context(), id)
		return id, u.AdminID, err
	}
	if requested == 0 {
		return 0, id, nil
	}
	u, err := s.Store.PortalUsers().Get(r.Context(), requested)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !s.isSuper(r) && u.AdminID != id) {
		return 0, 0, httpx.Errorf(http.StatusNotFound, "用户不存在")
	}
	return u.ID, u.AdminID, err
}

// ModelList splits a comma-separated model list, trimmed and without repeats.
func ModelList(models string) []string {
	var out []string
	for m := range strings.SplitSeq(models, ",") {
		if m = strings.TrimSpace(m); m != "" && !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

// pickModels checks that requested names at least one model of the template and
// no other, and returns them in the template's order.
func pickModels(template, requested string) (string, error) {
	allowed, want := ModelList(template), ModelList(requested)
	if len(want) == 0 {
		return "", httpx.Errorf(http.StatusBadRequest, "至少选择一个模型")
	}
	for _, m := range want {
		if !slices.Contains(allowed, m) {
			return "", httpx.Errorf(http.StatusBadRequest, "模型 %s 不在可选范围内", m)
		}
	}
	allowed = slices.DeleteFunc(allowed, func(m string) bool { return !slices.Contains(want, m) })
	return strings.Join(allowed, ","), nil
}

// pickTemplate applies the caller's choice among the enabled templates of a
// type. A type with only one needs no choice.
func pickTemplate(enabled []store.Template, id int64) (store.Template, error) {
	switch {
	case len(enabled) == 0:
		return store.Template{}, httpx.Errorf(http.StatusBadRequest, "该类型未开放")
	case id != 0:
		i := slices.IndexFunc(enabled, func(t store.Template) bool { return t.ID == id })
		if i < 0 {
			return store.Template{}, httpx.Errorf(http.StatusBadRequest, "所选模板已停用或不属于该类型，请重新选择")
		}
		return enabled[i], nil
	case len(enabled) > 1:
		return store.Template{}, httpx.Errorf(http.StatusBadRequest, "该类型有多个模板，请选择一个")
	}
	return enabled[0], nil
}

// settings decides the channel's fields and template. Every role is bound to an
// enabled template of the type: only the models, as a subset of its own, and a
// base URL where it allows one are the caller's. No models means all of them.
func (s *Service) settings(r *http.Request, req createRequest) (newapi.ChannelInput, store.Template, error) {
	var ch newapi.ChannelInput
	enabled, err := s.Store.EnabledTemplates(r.Context(), req.Type)
	if err != nil {
		return ch, store.Template{}, err
	}
	tpl, err := pickTemplate(enabled, req.TemplateID)
	if err != nil {
		return ch, tpl, err
	}
	models := strings.Join(ModelList(tpl.Models), ",")
	if strings.TrimSpace(req.Models) != "" {
		if models, err = pickModels(tpl.Models, req.Models); err != nil {
			return ch, tpl, err
		}
	}
	selected := ModelList(models)
	if len(selected) == 0 { // saved before templates were checked for this
		return ch, tpl, httpx.Errorf(http.StatusBadRequest, "该类型未配置可用模型")
	}
	testModel := tpl.TestModel
	if testModel != "" && !slices.Contains(selected, testModel) {
		testModel = selected[0]
	}
	baseURL := tpl.BaseURL
	custom := strings.TrimSpace(req.BaseURL)
	// The form sends the prefilled template value back, without trailing slashes.
	if tpl.AllowCustomBaseURL && custom != "" && strings.TrimRight(custom, "/") != strings.TrimRight(tpl.BaseURL, "/") {
		if err := checkBaseURL(custom); err != nil {
			return ch, tpl, err
		}
		baseURL = custom
	}
	extra, err := newapi.ParseExtra(tpl.Extra)
	if err != nil { // it was checked when the template was saved
		return ch, tpl, err
	}
	ch = newapi.ChannelInput{Type: tpl.ChannelType, BaseURL: baseURL, Models: models, Group: tpl.Group,
		Priority: tpl.Priority, Weight: tpl.Weight, Status: 1, TestModel: testModel, Extra: extra}
	return ch, tpl, nil
}

func (s *Service) create(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	var req createRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	ev.TargetType = "channel"
	ev.Detail["type"], ev.Detail["mode"] = req.Type, req.Mode

	name, err := validName(req.Name)
	if err != nil {
		return err
	}
	ev.Detail["name"] = name
	key := CleanKey(req.Key)
	if key == "" || len(key) > maxKeyBytes {
		return httpx.Errorf(http.StatusBadRequest, "密钥不能为空且不超过 64KB")
	}
	if err := checkMode(req, key); err != nil {
		return err
	}
	userID, adminID, err := s.newOwner(r, req.UserID)
	if err != nil {
		return err
	}
	ch, tpl, err := s.settings(r, req)
	if err != nil {
		return err
	}
	ev.Detail["base_url"], ev.Detail["models"] = ch.BaseURL, ch.Models
	if !s.Portal { // portal users read their own audit log: it names no owner, template or pool
		ev.Detail["user_id"], ev.Detail["admin_id"], ev.Detail["template_id"] = userID, adminID, tpl.ID
		ev.Detail["pool_ids"] = tpl.PoolIDs
	}

	// A batch is a channel per key, each pushed on its own.
	sp := spec{ChannelInput: ch, Mode: req.Mode}
	keys := []string{key}
	switch req.Mode {
	case "batch":
		sp.Mode, keys = "single", strings.Split(key, "\n")
	case "multi_to_single":
		sp.MultiKeyMode = req.MultiKeyMode
	}
	raw, err := json.Marshal(sp)
	if err != nil {
		return err
	}
	ctx := context.WithoutCancel(r.Context())
	ids, err := s.Store.NextChannelIDs(ctx, len(keys))
	if err != nil {
		return err
	}
	chs := make([]store.NewChannel, len(ids))
	for i, id := range ids {
		chs[i] = store.NewChannel{ID: id, UserID: userID, AdminID: adminID, TemplateID: tpl.ID, Name: name, Spec: raw,
			KeySealed: s.Box.ChannelKey.Seal(KeyField(id), []byte(keys[i])), PoolIDs: tpl.PoolIDs}
	}
	idStrs := make([]string, len(ids))
	for i, id := range ids {
		idStrs[i] = strconv.Itoa(id)
	}
	ev.TargetID = strings.Join(idStrs, ",")
	if err := s.Store.CreateChannels(ctx, chs); errors.Is(err, store.ErrNoPool) {
		return httpx.Errorf(http.StatusBadRequest, "该模板的号池都已停止接收新渠道，请联系管理员")
	} else if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"ids": ids})
	return nil
}

// KeyField is what a channel's key is sealed to.
func KeyField(channelID int) string { return "channel:" + strconv.Itoa(channelID) + ":key" }
