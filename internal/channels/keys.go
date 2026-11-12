package channels

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"newapisub/internal/audit"
	"newapisub/internal/httpx"
	"newapisub/internal/keyprobe"
	"newapisub/internal/newapi"
)

const (
	typeOpenRouter = 20
	// keysBudget keeps a batch of calls within the server's write timeout.
	keysBudget = 100 * time.Second
	maxBatch   = 100
)

var (
	probeBusy sync.Map // local channel id → batch in progress
	// new-api limits this host to 360 calls in 180s across portal and admin: batches leave room for the rest. Per pool.
	toggleRate = rateWindow{limit: 170, per: 180 * time.Second}
	// Tests spend the key's money and leave no trace in new-api.
	testRate = rateWindow{limit: 1000, per: time.Hour}
)

// rateWindow hands out limit units per key in each fixed window.
// shortcut: counted per process, so portal and admin each get the limit; move it to the store if that is ever too much.
type rateWindow struct {
	limit int
	per   time.Duration
	mu    sync.Mutex
	start time.Time
	used  map[int]int
}

// take counts up to n units of key and returns how many were granted.
func (w *rateWindow) take(key, n int) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if now := time.Now(); now.Sub(w.start) >= w.per {
		w.start, w.used = now, map[int]int{}
	}
	n = min(n, w.limit-w.used[key])
	w.used[key] += n
	return n
}

// retryAfter is the whole seconds left of the current window.
func (w *rateWindow) retryAfter() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return int(time.Until(w.start.Add(w.per))/time.Second) + 1
}

func (s *Service) registerKeys(mux *http.ServeMux, a func(string, audit.Handler) http.Handler) {
	mux.Handle("GET /api/channels/{id}/keys", a("view.channel_keys", s.keys))
	mux.Handle("POST /api/channels/{id}/keys/enable", a("channel.key_enable", s.setKeys(true)))
	mux.Handle("POST /api/channels/{id}/keys/disable", a("channel.key_disable", s.setKeys(false)))
	mux.Handle("POST /api/channels/{id}/keys/test", a("channel.key_test", s.testKeys))
	mux.Handle("POST /api/channels/{id}/keys/balance", a("channel.key_balance", s.keyBalance))
}

// probeSupported: only OpenRouter channels whose keys are OpenRouter's own.
// The probe always calls OpenRouterURL, never the channel's base_url.
func probeSupported(c *newapi.Channel) bool {
	if c.Type != typeOpenRouter {
		return false
	}
	if c.BaseURL == nil || *c.BaseURL == "" {
		return true
	}
	u, err := url.Parse(*c.BaseURL)
	return err == nil && u.Host == "openrouter.ai"
}

// multiKey is operable for a live multi-key channel.
func (s *Service) multiKey(r *http.Request, ev *audit.Event) (target, error) {
	t, err := s.operable(r, ev)
	if err == nil {
		err = t.need()
	}
	if err != nil {
		return t, err
	}
	if t.lives[0].c.MultiKeySize() == 0 {
		return t, httpx.Errorf(http.StatusBadRequest, "不是多 Key 渠道")
	}
	return t, nil
}

// keyIndexes checks the indexes of a request and records them for the audit log.
// Repeats are dropped, the order is kept.
func keyIndexes(requested []int, ev *audit.Event, size int) ([]int, error) {
	if len(requested) == 0 || len(requested) > maxBatch {
		return nil, httpx.Errorf(http.StatusBadRequest, "一次须选择 1–%d 个 Key", maxBatch)
	}
	if slices.Min(requested) < 0 {
		return nil, httpx.Errorf(http.StatusBadRequest, "无效的 Key 编号")
	}
	indexes := make([]int, 0, len(requested))
	for _, i := range requested {
		if !slices.Contains(indexes, i) {
			indexes = append(indexes, i)
		}
	}
	ev.Detail["indexes"] = indexes
	if slices.Max(indexes) >= size {
		return nil, httpx.Errorf(http.StatusNotFound, "Key 不存在")
	}
	return indexes, nil
}

// claimProbe takes the channel's one batch slot: a test or balance batch runs up
// to keysBudget with 8 outbound calls, and overlapping switches overwrite each
// other in new-api, which reads the channel before taking its lock.
func claimProbe(channelID int) (release func(), err error) {
	if _, busy := probeBusy.LoadOrStore(channelID, struct{}{}); busy {
		return nil, httpx.Errorf(http.StatusConflict, "该渠道有批量操作正在进行，请稍后再试")
	}
	return func() { probeBusy.Delete(channelID) }, nil
}

// probeChannel is multiKey for the routes that read keys: the switch must be on,
// the channel OpenRouter's, and a pool it is in must have the key connection.
// The keys are read in the first such pool; they are the same in every one.
func (s *Service) probeChannel(r *http.Request, ev *audit.Event) (target, live, error) {
	t, err := s.multiKey(r, ev)
	if err != nil {
		return t, live{}, err
	}
	on, err := s.Store.KeyProbe(r.Context())
	if err != nil {
		return t, live{}, err
	}
	if !on {
		return t, live{}, httpx.Errorf(http.StatusForbidden, "按 Key 测试和查余额未开启")
	}
	if !probeSupported(&t.lives[0].c) {
		return t, live{}, httpx.Errorf(http.StatusBadRequest, "只支持 API 地址为 openrouter.ai 的 OpenRouter 渠道")
	}
	for _, l := range t.lives {
		if l.pool.Probe != nil {
			return t, l, nil
		}
	}
	return t, live{}, httpx.Errorf(http.StatusServiceUnavailable, "未配置 key 读取连接（NEWAPI_KEY_DB_DSN）")
}

func probeErr(err error) error {
	switch {
	case errors.Is(err, keyprobe.ErrNoKey):
		return httpx.Errorf(http.StatusNotFound, "Key 不存在")
	case errors.Is(err, keyprobe.ErrNoChannel):
		return httpx.Errorf(http.StatusNotFound, "渠道已在 new-api 删除")
	case errors.Is(err, keyprobe.ErrTooManyKeys):
		return httpx.Errorf(http.StatusBadRequest, "Key 超过 %d 个，不支持按 Key 测试和查余额", keyprobe.MaxKeys)
	case errors.Is(err, keyprobe.ErrOverride):
		return httpx.Errorf(http.StatusBadRequest, "%s", err.Error())
	}
	return err
}

type keyView struct {
	Index        int      `json:"index"`
	Status       int      `json:"status"`
	DisabledTime int64    `json:"disabled_time"`
	Reason       string   `json:"reason"`
	Balance      *float64 `json:"balance"`
	BalanceAt    *int64   `json:"balance_at"`
	BalanceError string   `json:"balance_error"`
}

type keysView struct {
	ProbeEnabled   bool      `json:"probe_enabled"`
	ProbeSupported bool      `json:"probe_supported"`
	TotalBalance   *float64  `json:"total_balance"`
	Keys           []keyView `json:"keys"`
}

// keysView joins new-api's key states with the stored balances. A key shows
// as the lead pool has it, unless another pool has it disabled.
func (s *Service) keysView(ctx context.Context, t target) (keysView, error) {
	lead := t.lives[0]
	statuses, err := lead.pool.API.KeyStatuses(ctx, lead.p.NewAPIID)
	if err != nil {
		return keysView{}, UpstreamErr(err)
	}
	for _, l := range t.lives[1:] {
		other, err := l.pool.API.KeyStatuses(ctx, l.p.NewAPIID)
		if err != nil {
			log.Printf("keys of channel %d in pool %d: %v", t.m.ChannelID, l.pool.ID, err)
			continue
		}
		for i := range min(len(statuses), len(other)) {
			if statuses[i].Status == 1 && other[i].Status != 1 {
				statuses[i] = other[i]
			}
		}
	}
	on, err := s.Store.KeyProbe(ctx)
	if err != nil {
		return keysView{}, err
	}
	balances, err := s.Store.KeyBalances(ctx, t.m.ChannelID)
	if err != nil {
		return keysView{}, err
	}
	v := keysView{ProbeEnabled: on, ProbeSupported: probeSupported(&lead.c), Keys: make([]keyView, len(statuses))}
	for i, k := range statuses {
		v.Keys[i] = keyView{Index: k.Index, Status: k.Status, DisabledTime: k.DisabledTime, Reason: k.Reason}
		b, ok := balances[k.Index]
		if !ok {
			continue
		}
		v.Keys[i].Balance, v.Keys[i].BalanceAt, v.Keys[i].BalanceError = b.Balance, &b.CheckedAt, b.Error
		if b.Balance != nil {
			total := *b.Balance
			if v.TotalBalance != nil {
				total += *v.TotalBalance
			}
			v.TotalBalance = &total
		}
	}
	return v, nil
}

func (s *Service) keys(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	t, err := s.multiKey(r, ev)
	if err != nil {
		return err
	}
	v, err := s.keysView(r.Context(), t)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

// setKeys enables or disables keys in every pool of the channel, pool by pool,
// one key at a time and in the order given, stopping in a pool at its first
// failure. done is what every pool did; what was done stays done, so the
// answer is 200 with done and the reasons, which also leaves new-api's words
// out of the audit log. A pool that fell short, or could not be reached, is
// brought in line with the others by the push worker.
func (s *Service) setKeys(enabled bool) audit.Handler {
	return func(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
		t, err := s.multiKey(r, ev)
		if err != nil {
			return err
		}
		var req struct {
			Indexes []int `json:"indexes"`
		}
		if err := httpx.Decode(w, r, &req); err != nil {
			return err
		}
		indexes, err := keyIndexes(req.Indexes, ev, t.lives[0].c.MultiKeySize())
		if err != nil {
			return err
		}
		release, err := claimProbe(t.m.ChannelID)
		if err != nil {
			return err
		}
		defer release()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), keysBudget)
		defer cancel()
		var done []int
		var reasons []string
		behind := slices.Clone(t.down)
		for n, l := range t.lives {
			// One key at a time is someone clicking: a batch must not lock them out.
			granted, reason := len(indexes), ""
			if granted > 1 {
				granted = toggleRate.take(int(l.pool.ID), granted)
			}
			if granted < len(indexes) {
				reason = "开关太频繁：3 分钟内最多 " + strconv.Itoa(toggleRate.limit) + " 个，请稍后再试"
			}
			did := make([]int, 0, granted)
			for _, i := range indexes[:granted] {
				if err := l.pool.API.SetKeyEnabled(ctx, l.p.NewAPIID, i, enabled); err != nil {
					reason = UpstreamErr(err).Error()
					break
				}
				did = append(did, i)
			}
			if n == 0 {
				done = did
			} else {
				done = slices.DeleteFunc(done, func(i int) bool { return !slices.Contains(did, i) })
			}
			if reason != "" {
				behind = append(behind, l.pool.ID)
				if len(t.lives) > 1 {
					reason = "号池「" + l.pool.Name + "」：" + reason
				}
				reasons = append(reasons, reason)
			}
		}
		if len(behind) > 0 {
			if err := s.Store.MarkDirty(ctx, t.m.ChannelID, behind, false, false, true); err != nil {
				return err
			}
		}
		ev.Detail["checked"], ev.Detail["failed"] = len(done), len(indexes)-len(done)
		ev.Failed = len(reasons) > 0
		httpx.JSON(w, http.StatusOK, map[string]any{"done": done, "error": strings.Join(reasons, "；")})
		return nil
	}
}

// testKeys calls one of the channel's models with each key. The call costs
// money, so the model must be the channel's and a client that leaves stops it.
func (s *Service) testKeys(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	t, l, err := s.probeChannel(r, ev)
	if err != nil {
		return err
	}
	var req struct {
		Indexes []int  `json:"indexes"`
		Model   string `json:"model"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	indexes, err := keyIndexes(req.Indexes, ev, l.c.MultiKeySize())
	if err != nil {
		return err
	}
	if !slices.Contains(ModelList(l.c.Models), req.Model) {
		return httpx.Errorf(http.StatusBadRequest, "模型不属于该渠道")
	}
	ev.Detail["model"] = req.Model
	release, err := claimProbe(t.m.ChannelID)
	if err != nil {
		return err
	}
	defer release()
	indexes = indexes[:testRate.take(t.m.ChannelID, len(indexes))]
	if len(indexes) == 0 {
		return httpx.Errorf(http.StatusTooManyRequests, "该渠道每小时最多测试 %d 次，请稍后再试", testRate.limit)
	}
	ctx, cancel := context.WithTimeout(r.Context(), keysBudget)
	defer cancel()
	results, err := l.pool.Probe.Tests(ctx, l.p.NewAPIID, indexes, req.Model)
	if err != nil {
		return probeErr(err)
	}
	failed := 0
	for _, res := range results {
		if !res.OK {
			failed++
		}
	}
	ev.Detail["checked"], ev.Detail["failed"] = len(results), failed
	ev.Failed = failed > 0
	httpx.JSON(w, http.StatusOK, map[string]any{"results": results})
	return nil
}

// keyBalance refreshes the balance of the keys of indexes, or all of them,
// stores the results and answers with the keys as GET does.
func (s *Service) keyBalance(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	t, l, err := s.probeChannel(r, ev)
	if err != nil {
		return err
	}
	var req struct {
		Indexes []int `json:"indexes"`
	}
	if r.ContentLength != 0 { // the body is optional
		if err := httpx.Decode(w, r, &req); err != nil {
			return err
		}
	}
	var indexes []int
	if req.Indexes != nil {
		if indexes, err = keyIndexes(req.Indexes, ev, l.c.MultiKeySize()); err != nil {
			return err
		}
	}
	release, err := claimProbe(t.m.ChannelID)
	if err != nil {
		return err
	}
	defer release()
	// The results are worth keeping even if the client goes away.
	ctx := context.WithoutCancel(r.Context())
	probeCtx, cancel := context.WithTimeout(ctx, keysBudget)
	defer cancel()
	results, err := l.pool.Probe.Balances(probeCtx, l.p.NewAPIID, indexes)
	if err != nil {
		return probeErr(err)
	}
	failed := 0
	for _, b := range results {
		if b.Balance == nil {
			failed++
		}
		if err := s.Store.SaveKeyBalance(ctx, t.m.ChannelID, b.Index, b.Balance, b.Error); err != nil {
			return err
		}
	}
	ev.Detail["checked"], ev.Detail["failed"] = len(results), failed
	ev.Failed = failed > 0
	v, err := s.keysView(ctx, t)
	if err != nil {
		return err
	}
	if err := s.Store.PruneKeyBalances(ctx, t.m.ChannelID, len(v.Keys)); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}
