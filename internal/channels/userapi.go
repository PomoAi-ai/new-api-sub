package channels

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/httpx"
	"newapisub/internal/keyprobe"
	"newapisub/internal/newapi"
)

const (
	// new-api's defaults, which a script written for it counts on.
	apiPageSize    = 10
	apiMaxPageSize = 100
	apiKeyPageSize = 50
	// logSpan is the longest range the inner log routes accept.
	logSpan = 92 * 86400
)

// apiReply is new-api's response envelope. The notices of the log routes go
// in data, except where data is a list (/api/data/self).
type apiReply struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
	notices
}

// apiChannelInfo and apiChannel are all a token sees of a channel. The inner
// answer is decoded into them, so a field added there stays out until listed here.
type apiChannelInfo struct {
	IsMultiKey           bool        `json:"is_multi_key"`
	MultiKeySize         int         `json:"multi_key_size"`
	MultiKeyStatusList   map[int]int `json:"multi_key_status_list"`
	MultiKeyPollingIndex int         `json:"multi_key_polling_index"`
	MultiKeyMode         string      `json:"multi_key_mode"`
}

type apiChannel struct {
	ID                 int            `json:"id"`
	Type               int            `json:"type"`
	Name               string         `json:"name"`
	Status             int            `json:"status"`
	Group              string         `json:"group"`
	Models             string         `json:"models"`
	Priority           *int64         `json:"priority"`
	Weight             *int64         `json:"weight"`
	UsedQuota          int64          `json:"used_quota"`
	Balance            float64        `json:"balance"`
	BalanceUpdatedTime int64          `json:"balance_updated_time"`
	BalanceFromKeys    bool           `json:"balance_from_keys"`
	ResponseTime       int64          `json:"response_time"`
	TestTime           int64          `json:"test_time"`
	CreatedTime        int64          `json:"created_time"`
	BaseURL            *string        `json:"base_url"`
	Tag                *string        `json:"tag"`
	TestModel          *string        `json:"test_model"`
	AutoBan            *int64         `json:"auto_ban"`
	ChannelInfo        apiChannelInfo `json:"channel_info"`
	Exists             bool           `json:"exists"`
	TemplateID         *int64         `json:"template_id"`
	PushState          string         `json:"push_state"` // how far the push to the pools got; the pools stay out
}

type apiType struct {
	Type               int    `json:"type"`
	TemplateID         int64  `json:"template_id"`
	TemplateName       string `json:"template_name"`
	BaseURL            string `json:"base_url"`
	AllowCustomBaseURL bool   `json:"allow_custom_base_url"`
	Models             string `json:"models"`
	TestModel          string `json:"test_model"`
}

// apiLog adds the name new-api gives the channel id, the local one. It keeps
// the pool: a log id is unique only with it.
type apiLog struct {
	newapi.Log
	Channel int `json:"channel"`
}

// capture buffers what an inner route answers.
type capture struct {
	status int
	header http.Header
	body   bytes.Buffer
}

func (c *capture) Header() http.Header { return c.header }

func (c *capture) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
}

func (c *capture) Write(b []byte) (int, error) {
	c.WriteHeader(http.StatusOK)
	return c.body.Write(b)
}

// userAPI serves new-api's paths through the inner mux, which holds the routes
// of the page. Reaching a handler only through it is what gives a token the same
// checks, scope and audit as the page. A route with no mapping here is out of a
// token's reach: password, logout, token management and the log export.
type userAPI struct {
	inner *http.ServeMux
	audit audit.Recorder
}

// refusedKeyActions are the actions of new-api's key route that have no page
// counterpart, with the audit action each counts under.
var refusedKeyActions = map[string]string{
	"delete_key": "channel.update", "delete_disabled_keys": "channel.update",
	"enable_all_keys": "channel.key_enable", "disable_all_keys": "channel.key_disable",
}

// RegisterAPI mounts the program API, which signs in with a token of find.
func (s *Service) RegisterAPI(mux *http.ServeMux, m *auth.Manager, find auth.FindToken) {
	a := &userAPI{http.NewServeMux(), s.Audit}
	s.Register(a.inner, func(h http.Handler) http.Handler { return h })
	// A third of new-api's window, whose 360 calls the pages also draw on.
	// shortcut: per user with no site-wide total, so three scripts at full speed can still drain new-api's limit, add a shared window if that happens.
	rate := &rateWindow{limit: 120, per: 180 * time.Second}
	route := func(pattern string, h httpx.Handler) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			ctx, err := m.TokenIdentity(r, find)
			if err != nil {
				writeAPIError(w, r, err)
				return
			}
			if rate.take(int(auth.UserID(ctx)), 1) == 0 {
				w.Header().Set("Retry-After", strconv.Itoa(rate.retryAfter()))
				httpx.JSON(w, http.StatusTooManyRequests, apiReply{Message: "请求太频繁，请稍后再试"})
				return
			}
			if err := h(w, r.WithContext(ctx)); err != nil {
				writeAPIError(w, r, err)
			}
		})
	}
	// Without the bare paths the mux would answer POST /api/channel with a redirect,
	// which clients follow as a GET: a list, success:true, and no channel created.
	for _, path := range []string{"/api/channel", "/api/channel/{$}"} {
		route("GET "+path, a.list)
		route("POST "+path, a.create)
		route("PUT "+path, a.update)
	}
	route("GET /api/channel/types", a.types)
	route("GET /api/channel/{id}", a.detail)
	route("DELETE /api/channel/{id}", a.remove)
	route("POST /api/channel/{id}/status", a.status)
	route("GET /api/channel/test/{id}", a.test)
	route("GET /api/channel/update_balance/{id}", a.balance)
	route("POST /api/channel/multi_key/manage", a.manageKeys)
	route("GET /api/log/self", a.logs)
	route("GET /api/log/self/stat", a.logStat)
	route("GET /api/data/self", a.stats)
}

// writeAPIError answers as new-api does: a failure is 200 and success:false, so
// a client that retries on 5xx cannot create channels twice. The statuses kept
// are the ones a script must tell apart: the token, the scope, and 409 to try
// again later. HTTP 429 means the token's own limit only, which never comes here.
func writeAPIError(w http.ResponseWriter, r *http.Request, err error) {
	c := &capture{header: http.Header{}}
	msg := httpx.WriteError(c, r, err)
	status := http.StatusOK
	if slices.Contains([]int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict}, c.status) {
		status = c.status
	}
	httpx.JSON(w, status, apiReply{Message: msg})
}

// refuse audits a request turned down here, which reaches no inner route and so
// no Wrap. detail holds names only: never anything the caller sent as a value.
func (a *userAPI) refuse(r *http.Request, action string, channelID int64, detail map[string]any) {
	ev := &audit.Event{ActorID: auth.UserID(r.Context()), ActorName: auth.Username(r.Context()), TargetType: "channel", Detail: detail}
	if channelID > 0 {
		ev.TargetID = strconv.FormatInt(channelID, 10)
	}
	a.audit.Record(r, action, false, ev)
}

func apiOK(w http.ResponseWriter, data any) error {
	httpx.JSON(w, http.StatusOK, apiReply{Success: true, Data: data})
	return nil
}

// dispatch runs an inner route on a copy of r and decodes a 2xx answer into out,
// if any. Any other answer comes back as an error with its status and message.
// r.Clone, because the mux and the handlers must not see or change r's URL.
func (a *userAPI) dispatch(r *http.Request, method, path string, query url.Values, body, out any) error {
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return fmt.Errorf("encode %s %s: %w", method, path, err)
		}
	}
	in := r.Clone(r.Context())
	in.Method, in.URL.Path, in.URL.RawPath, in.URL.RawQuery = method, path, "", query.Encode()
	in.Body, in.ContentLength = io.NopCloser(bytes.NewReader(raw)), int64(len(raw))
	c := &capture{header: http.Header{}}
	a.inner.ServeHTTP(c, in)
	if c.status < 200 || c.status > 299 {
		var e struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(c.body.Bytes(), &e); err != nil {
			return fmt.Errorf("%s %s answered %d: %w", method, path, c.status, err)
		}
		return httpx.Errorf(c.status, "%s", e.Error)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(c.body.Bytes(), out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return nil
}

// pathID is {id}, which also catches new-api's fixed paths that are not mapped
// (/api/channel/search, models, ops...): those must be 404, not a failed lookup.
func pathID(r *http.Request) (int64, error) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return 0, httpx.Errorf(http.StatusNotFound, "接口不存在")
	}
	return id, nil
}

func channelPath(id int64, suffix string) string {
	return "/api/channels/" + strconv.FormatInt(id, 10) + suffix
}

// apiPage reads new-api's GetPageQuery: p, page_size and its aliases. Anything
// unusable falls back to the default rather than failing.
func apiPage(v url.Values) (page, size int) {
	page, size = 1, apiPageSize
	if n, err := strconv.Atoi(v.Get("p")); err == nil && n > 0 {
		page = n
	}
	for _, name := range []string{"page_size", "ps", "size"} {
		if n, err := strconv.Atoi(v.Get(name)); err == nil && n > 0 {
			return page, min(n, apiMaxPageSize)
		}
	}
	return page, size
}

func pageOf[T any](items []T, page, size int) []T {
	if page-1 > len(items)/size { // also keeps (page-1)*size from overflowing
		return items[:0]
	}
	lo := (page - 1) * size
	return items[lo:min(lo+size, len(items))]
}

func (a *userAPI) channels(r *http.Request) ([]apiChannel, error) {
	var out []apiChannel
	err := a.dispatch(r, http.MethodGet, "/api/channels", nil, nil, &out)
	return out, err
}

// unwanted applies new-api's status, type and group filters. They must work: a
// script that deletes what status=disabled lists would otherwise delete everything.
func unwanted(v url.Values) func(apiChannel) bool {
	status := strings.ToLower(v.Get("status"))
	typ, typErr := strconv.Atoi(v.Get("type"))
	group := strings.TrimSpace(v.Get("group"))
	if strings.EqualFold(group, "all") || strings.EqualFold(group, "null") {
		group = ""
	}
	return func(c apiChannel) bool {
		switch status {
		case "enabled", "1":
			if c.Status != 1 {
				return true
			}
		case "disabled", "0":
			if c.Status == 1 {
				return true
			}
		}
		return (typErr == nil && typ >= 0 && c.Type != typ) || (group != "" && !slices.Contains(ModelList(c.Group), group))
	}
}

func (a *userAPI) list(w http.ResponseWriter, r *http.Request) error {
	v := r.URL.Query()
	if tagMode, _ := strconv.ParseBool(v.Get("tag_mode")); tagMode {
		return httpx.Errorf(http.StatusBadRequest, "不支持 tag_mode")
	}
	items, err := a.channels(r)
	if err != nil {
		return err
	}
	items = slices.DeleteFunc(items, unwanted(v))
	page, size := apiPage(v)
	return apiOK(w, map[string]any{"items": pageOf(items, page, size), "total": len(items), "page": page, "page_size": size})
}

func (a *userAPI) detail(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	items, err := a.channels(r)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(items, func(c apiChannel) bool { return int64(c.ID) == id })
	if i < 0 {
		return httpx.Errorf(http.StatusNotFound, "渠道不存在")
	}
	return apiOK(w, items[i])
}

func (a *userAPI) types(w http.ResponseWriter, r *http.Request) error {
	var out []apiType
	if err := a.dispatch(r, http.MethodGet, "/api/channel-types", nil, nil, &out); err != nil {
		return err
	}
	return apiOK(w, out)
}

// create takes new-api's nested body. What the template decides is not read.
func (a *userAPI) create(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Mode         string `json:"mode"`
		MultiKeyMode string `json:"multi_key_mode"`
		TemplateID   int64  `json:"template_id"`
		Channel      *struct {
			Type       int    `json:"type"`
			Name       string `json:"name"`
			Key        string `json:"key"`
			BaseURL    string `json:"base_url"`
			Models     string `json:"models"`
			TemplateID int64  `json:"template_id"`
		} `json:"channel"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	if req.Channel == nil {
		return httpx.Errorf(http.StatusBadRequest, "缺少 channel")
	}
	c := req.Channel
	body := createRequest{Type: c.Type, Name: c.Name, Key: c.Key, BaseURL: c.BaseURL, Mode: req.Mode,
		MultiKeyMode: req.MultiKeyMode, Models: c.Models, TemplateID: cmp.Or(req.TemplateID, c.TemplateID)}
	var res struct {
		IDs []int `json:"ids"`
	}
	if err := a.dispatch(r, http.MethodPost, "/api/channels", nil, body, &res); err != nil {
		return err
	}
	return apiOK(w, res)
}

// update refuses what new-api's PUT would change and the page never does, rather
// than drop it: the script's author believes it was applied. "" and null are
// what a form or a serializer sends for a field nobody set. Other fields are ignored.
func (a *userAPI) update(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ID           int64           `json:"id"`
		Name         string          `json:"name"`
		Models       *string         `json:"models"`
		Key          json.RawMessage `json:"key"`
		BaseURL      json.RawMessage `json:"base_url"`
		KeyMode      json.RawMessage `json:"key_mode"`
		MultiKeyMode json.RawMessage `json:"multi_key_mode"`
		Status       json.RawMessage `json:"status"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	var refused []string
	for name, v := range map[string]json.RawMessage{"key": req.Key, "base_url": req.BaseURL, "key_mode": req.KeyMode,
		"multi_key_mode": req.MultiKeyMode, "status": req.Status} {
		if s := string(v); s != "" && s != "null" && s != `""` {
			refused = append(refused, name)
		}
	}
	if refused != nil {
		slices.Sort(refused)
		a.refuse(r, "channel.update", req.ID, map[string]any{"rejected_fields": refused})
		return httpx.Errorf(http.StatusBadRequest, "不支持修改 %s", strings.Join(refused, "、"))
	}
	if req.ID <= 0 {
		return httpx.Errorf(http.StatusBadRequest, "缺少渠道 id")
	}
	// new-api leaves out of the update what a PUT does not carry; the page's route always wants a name.
	if req.Name == "" {
		items, err := a.channels(r)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(items, func(c apiChannel) bool { return int64(c.ID) == req.ID })
		if i < 0 {
			return httpx.Errorf(http.StatusNotFound, "渠道不存在")
		}
		req.Name = items[i].Name
	}
	body := map[string]any{"name": req.Name, "models": req.Models}
	if err := a.dispatch(r, http.MethodPut, channelPath(req.ID, ""), nil, body, nil); err != nil {
		return err
	}
	return apiOK(w, nil)
}

func (a *userAPI) remove(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := a.dispatch(r, http.MethodDelete, channelPath(id, ""), nil, nil, nil); err != nil {
		return err
	}
	return apiOK(w, nil)
}

func (a *userAPI) status(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var body json.RawMessage
	if err := httpx.Decode(w, r, &body); err != nil {
		return err
	}
	if err := a.dispatch(r, http.MethodPost, channelPath(id, "/status"), nil, body, nil); err != nil {
		return err
	}
	return apiOK(w, true)
}

// test keeps new-api's shape: the result and time at the top level, and a failed
// test is success:false like any other failure.
func (a *userAPI) test(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	v := r.URL.Query()
	stream, _ := strconv.ParseBool(v.Get("stream"))
	opts := newapi.TestOptions{Model: v.Get("model"), EndpointType: v.Get("endpoint_type"), Stream: stream}
	var res struct {
		Success bool    `json:"success"`
		Message string  `json:"message"`
		Time    float64 `json:"time"`
	}
	if err := a.dispatch(r, http.MethodPost, channelPath(id, "/test"), nil, opts, &res); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (a *userAPI) balance(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var res struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		newapi.Balance
	}
	if err := a.dispatch(r, http.MethodPost, channelPath(id, "/balance"), nil, nil, &res.Balance); err != nil {
		return err
	}
	res.Success = true
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// manageKeys is new-api's one route for key actions, plus the page's test and
// balance. Everything else, the deleting and all-at-once actions included, is
// refused before it reaches any handler.
func (a *userAPI) manageKeys(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ChannelID  int64  `json:"channel_id"`
		Action     string `json:"action"`
		KeyIndex   *int   `json:"key_index"`
		KeyIndexes []int  `json:"key_indexes"`
		Model      string `json:"model"`
		Page       int    `json:"page"`
		PageSize   int    `json:"page_size"`
		Status     *int   `json:"status"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	keys := channelPath(req.ChannelID, "/keys")
	indexes := req.KeyIndexes
	if indexes == nil && req.KeyIndex != nil {
		indexes = []int{*req.KeyIndex}
	}
	switch req.Action {
	case "get_key_status":
		var v keysView
		if err := a.dispatch(r, http.MethodGet, keys, nil, nil, &v); err != nil {
			return err
		}
		return apiOK(w, keyPage(v, req.Status, req.Page, req.PageSize))
	case "enable_key", "disable_key":
		if req.KeyIndex == nil {
			return httpx.Errorf(http.StatusBadRequest, "缺少 key_index")
		}
		var res struct {
			Error string `json:"error"`
		}
		body := map[string][]int{"indexes": {*req.KeyIndex}}
		if err := a.dispatch(r, http.MethodPost, keys+"/"+strings.TrimSuffix(req.Action, "_key"), nil, body, &res); err != nil {
			return err
		}
		if res.Error != "" { // the inner route answers 200 so a batch can report what it did
			return httpx.Errorf(http.StatusBadRequest, "%s", res.Error)
		}
		return apiOK(w, nil)
	case "test_keys":
		var res struct {
			Results []keyprobe.TestResult `json:"results"`
		}
		body := map[string]any{"indexes": indexes, "model": req.Model}
		if err := a.dispatch(r, http.MethodPost, keys+"/test", nil, body, &res); err != nil {
			return err
		}
		return apiOK(w, res)
	case "get_key_balance":
		var body any
		if indexes != nil {
			body = map[string][]int{"indexes": indexes}
		}
		var v keysView
		if err := a.dispatch(r, http.MethodPost, keys+"/balance", nil, body, &v); err != nil {
			return err
		}
		return apiOK(w, v)
	}
	// An action not listed is free text from the caller, so its name stays out of the log.
	name, action := req.Action, refusedKeyActions[req.Action]
	if action == "" {
		name, action = "unknown", "channel.update"
	}
	a.refuse(r, action, req.ChannelID, map[string]any{"rejected_action": name})
	return httpx.Errorf(http.StatusBadRequest, "不支持该操作")
}

// keyPage is new-api's get_key_status over all of a channel's keys: the counts
// ignore the filter, and a page past the end is the last one.
func keyPage(v keysView, status *int, page, size int) map[string]any {
	counts := map[int]int{}
	for _, k := range v.Keys {
		counts[k.Status]++
	}
	keys := v.Keys
	if status != nil {
		keys = slices.DeleteFunc(keys, func(k keyView) bool { return k.Status != *status })
	}
	if size <= 0 {
		size = apiKeyPageSize
	}
	pages := max((len(keys)+size-1)/size, 1)
	page = min(max(page, 1), pages)
	return map[string]any{"keys": pageOf(keys, page, size), "total": len(keys), "page": page, "page_size": size, "total_pages": pages,
		"enabled_count": counts[1], "manual_disabled_count": counts[2], "auto_disabled_count": counts[3],
		"probe_enabled": v.ProbeEnabled, "probe_supported": v.ProbeSupported, "total_balance": v.TotalBalance}
}

// logQuery translates new-api's log parameters. Its end is inclusive and the
// inner one is not; a range it leaves open has to fit the inner limit.
func logQuery(v url.Values) url.Values {
	end, _ := strconv.ParseInt(v.Get("end_timestamp"), 10, 64)
	if end <= 0 {
		end = time.Now().Unix()
	}
	end++
	start, _ := strconv.ParseInt(v.Get("start_timestamp"), 10, 64)
	if start <= 0 {
		start = max(end-logSpan, 0)
	}
	page, size := apiPage(v)
	q := url.Values{}
	q.Set("start", strconv.FormatInt(start, 10))
	q.Set("end", strconv.FormatInt(end, 10))
	q.Set("p", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(size))
	for _, name := range []string{"type", "model_name", "group", "request_id", "upstream_request_id"} {
		if s := v.Get(name); s != "" {
			q.Set(name, s)
		}
	}
	if c := v.Get("channel"); c != "" && c != "0" { // new-api's 0 is no filter
		q.Set("channel_id", c)
	}
	return q
}

func (a *userAPI) logs(w http.ResponseWriter, r *http.Request) error {
	var res struct {
		Items []apiLog `json:"items"`
		Total int64    `json:"total"`
		notices
	}
	if err := a.dispatch(r, http.MethodGet, "/api/logs", logQuery(r.URL.Query()), nil, &res); err != nil {
		return err
	}
	for i := range res.Items {
		res.Items[i].Channel = res.Items[i].ChannelID
	}
	page, size := apiPage(r.URL.Query())
	return apiOK(w, struct {
		Items    []apiLog `json:"items"`
		Total    int64    `json:"total"`
		Page     int      `json:"page"`
		PageSize int      `json:"page_size"`
		notices
	}{res.Items, res.Total, page, size, res.notices})
}

func (a *userAPI) logStat(w http.ResponseWriter, r *http.Request) error {
	var stat struct {
		newapi.LogStat
		notices
	}
	if err := a.dispatch(r, http.MethodGet, "/api/logs/stat", logQuery(r.URL.Query()), nil, &stat); err != nil {
		return err
	}
	return apiOK(w, stat)
}

func (a *userAPI) stats(w http.ResponseWriter, r *http.Request) error {
	var res struct {
		Rows []newapi.StatRow `json:"rows"`
		notices
	}
	if err := a.dispatch(r, http.MethodGet, "/api/stats", logQuery(r.URL.Query()), nil, &res); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, apiReply{Success: true, Data: res.Rows, notices: res.notices})
	return nil
}
