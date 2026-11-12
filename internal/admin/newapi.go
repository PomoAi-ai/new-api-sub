package admin

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"newapisub/internal/audit"
	"newapisub/internal/channels"
	"newapisub/internal/connection"
	"newapisub/internal/httpx"
)

// newapiReads are the new-api routes a super's template form reads, by their
// path under /api/newapi/, of the pool of ?pool_id=.
var newapiReads = map[string]string{
	"models":            "/api/channel/models",
	"default-base-urls": "/api/channel/default_base_urls",
	"groups":            "/api/group/",
	"prefill-groups":    "/api/prefill_group/",
}

// newapiPool is the pool of ?pool_id=, the home pool without one.
func (s *Server) newapiPool(r *http.Request) (*connection.Pool, error) {
	p := s.Pools.Home()
	if raw := r.URL.Query().Get("pool_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, httpx.Errorf(http.StatusBadRequest, "无效的 pool_id")
		}
		if p = s.Pools.Get(id); p == nil {
			return nil, httpx.Errorf(http.StatusNotFound, "号池不存在")
		}
	}
	if p.Reason != "" {
		return nil, httpx.Errorf(http.StatusServiceUnavailable, "号池「%s」不可用：%s", p.Name, p.Reason)
	}
	return p, nil
}

// newapiRead answers with new-api's data as it is. A failure, such as a PAT
// without the route's scope, is a 502 saying so: an empty list would read as
// having no options.
func (s *Server) newapiRead(path string) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		p, err := s.newapiPool(r)
		if err != nil {
			return err
		}
		target := path
		if t := r.URL.Query().Get("type"); t != "" { // prefill_group filters on it, the others ignore it
			target += "?type=" + url.QueryEscape(t)
		}
		data, err := p.API.Data(r.Context(), http.MethodGet, target, nil)
		if err != nil {
			return channels.UpstreamErr(err)
		}
		httpx.JSON(w, http.StatusOK, data)
		return nil
	}
}

// fetchModels has new-api list an upstream's models for a form not saved yet,
// using the key typed into it.
func (s *Server) fetchModels(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	var req struct {
		Type    int    `json:"type"`
		BaseURL string `json:"base_url"`
		Key     string `json:"key"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	ev.TargetType = "channel"
	baseURL := strings.TrimSpace(req.BaseURL)
	switch {
	case req.Type <= 0:
		return httpx.Errorf(http.StatusBadRequest, "type 须为正整数")
	case baseURL != "" && !channels.ValidURL(baseURL):
		return httpx.Errorf(http.StatusBadRequest, "base_url 须为 http(s) 完整地址，且不含用户信息")
	}
	ev.Detail["type"], ev.Detail["base_url"] = req.Type, baseURL
	p, err := s.newapiPool(r)
	if err != nil {
		return err
	}
	ev.Detail["pool_id"] = p.ID
	key := channels.CleanKey(req.Key)
	data, err := p.API.Data(r.Context(), http.MethodPost, "/api/channel/fetch_models",
		map[string]any{"type": req.Type, "base_url": baseURL, "key": key})
	if err != nil {
		// The message goes to the audit log too, and may quote the upstream on the key.
		msg := channels.UpstreamErr(err).Error()
		for _, part := range strings.Fields(key) {
			msg = strings.ReplaceAll(msg, part, "***")
		}
		return httpx.Errorf(http.StatusBadGateway, "%s", msg)
	}
	httpx.JSON(w, http.StatusOK, data)
	return nil
}
