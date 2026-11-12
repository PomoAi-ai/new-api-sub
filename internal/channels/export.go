package channels

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"newapisub/internal/audit"
	"newapisub/internal/auth"
	"newapisub/internal/httpx"
	"newapisub/internal/newapi"
)

const (
	// A row is written before this much time passes or the export is cut, so
	// neither the server-wide write timeout nor a stalled client decides how
	// long an export may take.
	exportWriteWindow = 5 * time.Minute
	exportFlushRows   = 256
	// A filter that matches little goes on showing progress, well inside the
	// proxy's read timeout (proxy_read_timeout in deploy/nginx.conf).
	exportFlushEvery = 30 * time.Second
	// Exports scan new-api's database, usually its primary, so they are limited
	// across accounts and in time. PostgreSQL's statement_timeout in README matches.
	exportMaxRunning = 2
	exportTimeout    = 30 * time.Minute
	exportMask       = "••••" // the page's own stand-in for hidden values
)

// exportZh translates a CSV, which does not pass through the page's i18n. The
// words are the page's own (src/i18n/locales/zh.json).
var exportZh = map[string]string{
	"Time": "时间", "Type": "类型", "User": "用户", "Token": "令牌", "Group": "分组", "Model": "模型",
	"Channel": "渠道", "Stream": "流", "Input Tokens": "输入 Token", "Output Tokens": "输出 Token",
	"Cache Read": "缓存读取", "Cache Write": "缓存写入", "Cost": "费用", "Duration": "耗时", "IP": "IP",
	"Request ID": "请求 ID", "Details": "详情", "Consume": "消耗", "Error": "错误", "Refund": "退款",
	"Yes": "是", "No": "否", "Model test": "模型测试",
}

var exportTypes = map[int]string{2: "Consume", 5: "Error", 6: "Refund"}

// exportLang returns the translator for ?lang=, in Chinese unless asked otherwise.
func exportLang(lang string) (func(string) string, error) {
	switch lang {
	case "", "zh":
		return func(s string) string { return exportZh[s] }, nil
	case "en":
		return func(s string) string { return s }, nil
	}
	return nil, httpx.Errorf(http.StatusBadRequest, "lang 只能为 zh 或 en")
}

// exportHeader has the columns of new-api's administrator view.
func exportHeader(tr func(string) string) []string {
	cols := []string{"Time", "Type", "User", "Token", "Group", "Model", "Channel", "Stream", "Input Tokens", "Output Tokens",
		"Cache Read", "Cache Write", "Cost", "Duration", "IP", "Request ID", "Details"}
	for i, c := range cols {
		cols[i] = tr(c)
	}
	return cols
}

// exportCache reads a log's cache tokens the way new-api's export does. Anything
// unreadable counts as none.
func exportCache(other string) (read, write int64) {
	var o struct {
		Read  int64 `json:"cache_tokens"`
		Write int64 `json:"cache_creation_tokens"`
		W5m   int64 `json:"cache_creation_tokens_5m"`
		W1h   int64 `json:"cache_creation_tokens_1h"`
	}
	_ = json.Unmarshal([]byte(other), &o)
	if write = o.W5m + o.W1h; write == 0 {
		write = o.Write
	}
	return o.Read, write
}

// formatCost is the log table's formatLogQuota for the USD display the portal
// and admin always use.
func formatCost(quota int64, quotaPerUnit float64) string {
	v, digits := float64(quota)/quotaPerUnit, 6
	if math.Abs(v) >= 1 {
		digits = 4
	} else if v > 0 {
		v = math.Max(v, 1e-6)
	}
	s := strconv.FormatFloat(v, 'f', digits, 64)
	return "$" + strings.TrimRight(strings.TrimRight(s, "0"), ".")
}

// exportRow writes one log, its cost in the unit of its pool. Without full the
// end user is gone from l already, and the cells say so as the page does.
func exportRow(l logItem, full bool, tzOffset int, quotaPerUnit float64, tr func(string) string) []string {
	channel := "#" + strconv.Itoa(l.ChannelID)
	if l.ChannelID == 0 { // not ours: only a super gets its id in its pool
		channel = ""
		if l.NewAPIChannelID != 0 {
			channel = l.PoolName + "#" + strconv.Itoa(l.NewAPIChannelID)
		}
	}
	if l.ChannelName != "" {
		channel = strings.TrimSpace(channel + " " + l.ChannelName)
	}
	stream, duration := "No", ""
	if l.IsStream {
		stream = "Yes"
	}
	if l.Type != 6 {
		duration = strconv.FormatInt(l.UseTime, 10) + "s"
	}
	user, token, ip := l.Username, l.TokenName, l.IP
	if l.TokenID == 0 && token == newapi.TestTokenName {
		token = tr(token)
	}
	if !full {
		user, ip = exportMask, exportMask
		if token == "" {
			token = exportMask
		}
	}
	// shortcut: only channel tests get a translated detail; new-api's content_parts need its i18n catalog, so everything else stays in English, upgrade by exporting NDJSON and rendering on the client.
	content := l.Content
	if content == newapi.TestTokenName {
		content = tr(content)
	}
	cacheRead, cacheWrite := exportCache(l.Other)
	return []string{time.Unix(l.CreatedAt+int64(tzOffset), 0).UTC().Format(time.DateTime), tr(exportTypes[l.Type]),
		user, token, l.Group, l.ModelName, channel, tr(stream), strconv.FormatInt(l.PromptTokens, 10),
		strconv.FormatInt(l.CompletionTokens, 10), strconv.FormatInt(cacheRead, 10), strconv.FormatInt(cacheWrite, 10),
		formatCost(l.Quota, quotaPerUnit), duration, ip, l.RequestID, content}
}

var csvNumber = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// writeCSVLine quotes every cell. Text a spreadsheet would run as a formula gets
// a leading apostrophe, since logs carry names end users typed.
func writeCSVLine(w *bufio.Writer, cells []string) error {
	for i, cell := range cells {
		if cell != "" && strings.ContainsRune("=+-@\t\r", rune(cell[0])) && !csvNumber.MatchString(cell) {
			cell = "'" + cell
		}
		if i > 0 {
			w.WriteByte(',')
		}
		w.WriteByte('"')
		w.WriteString(strings.ReplaceAll(cell, `"`, `""`))
		w.WriteByte('"')
	}
	_, err := w.WriteString("\r\n")
	return err // bufio.Writer keeps the first error
}

var errExportPoolDown = httpx.Errorf(http.StatusServiceUnavailable, "有号池暂时不可用，导出会不完整，请稍后再试")

// exportLogs streams the logs of the current filter as a UTF-8 CSV. As in
// new-api there is no limit on rows: the time range, the limits on running
// exports and exportTimeout bound the work. Failing after the first row goes
// through Wrap, which breaks the connection instead of ending a short file
// normally.
func (s *Service) exportLogs(w http.ResponseWriter, r *http.Request, ev *audit.Event) error {
	v := r.URL.Query()
	tz, err := newapi.ParseTZOffset(v)
	if err != nil {
		return httpx.Errorf(http.StatusBadRequest, "%s", err.Error())
	}
	tr, err := exportLang(v.Get("lang"))
	if err != nil {
		return err
	}
	f, q, err := s.logView(r)
	if err != nil {
		return err
	}
	// A file says nothing of what it lacks.
	if q.Retention != nil {
		return httpx.Errorf(http.StatusBadRequest, "同步号池只保留 30 天的日志，请缩小时间范围")
	}
	if len(q.Degraded) > 0 {
		return errExportPoolDown
	}
	names, errs := each(q.qs, func(pq poolQuery) (map[int]string, error) { return pq.pool.Logs.ChannelNames(r.Context()) })
	if err := errors.Join(errs...); err != nil {
		log.Printf("export logs: channel names: %v", err)
		return errExportPoolDown
	}
	ev.TargetType = "log"
	ev.Detail["start"], ev.Detail["end"] = f.Start, f.End
	id := auth.UserID(r.Context())
	if _, busy := s.exports.LoadOrStore(id, struct{}{}); busy {
		return httpx.Errorf(http.StatusConflict, "已有一个日志导出正在进行，请等它结束后再试")
	}
	defer s.exports.Delete(id)
	defer s.exportsRunning.Add(-1)
	if s.exportsRunning.Add(1) > exportMaxRunning {
		return httpx.Errorf(http.StatusConflict, "同时进行的日志导出已达上限，请稍后再试")
	}
	ctx, cancel := context.WithTimeout(r.Context(), exportTimeout)
	defer cancel()

	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Now().Add(exportWriteWindow)); err != nil {
		return fmt.Errorf("extend the export's write deadline: %w", err)
	}
	full := s.isSuper(r)
	// The header waits for the first row, so a query that fails can still answer with an error.
	var bw *bufio.Writer
	open := func() error {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="usage-logs-%s-%s.csv"`,
			time.Unix(f.Start+int64(tz), 0).UTC().Format("20060102"), time.Unix(f.End+int64(tz), 0).UTC().Format("20060102")))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		bw = bufio.NewWriter(w)
		bw.WriteString("\xEF\xBB\xBF") // the BOM spreadsheet apps need to read UTF-8
		return writeCSVLine(bw, exportHeader(tr))
	}
	rows := 0
	var flushed time.Time // never yet, so the first row goes out at once
	exports := make([]func(func(newapi.Log) error) error, len(q.qs))
	pools := make(map[int64]int, len(q.qs))
	for i, pq := range q.qs {
		pools[pq.pool.ID] = i
		exports[i] = func(fn func(newapi.Log) error) error {
			return pq.pool.Logs.ExportLogs(ctx, pq.f, full, func(l newapi.Log) error {
				l.PoolID, l.ChannelName = pq.pool.ID, cmp.Or(names[i][l.ChannelID], l.ChannelName) // a sync reader names them itself
				return fn(l)
			})
		}
	}
	err = newapi.MergeExport(exports, func(l newapi.Log) error {
		if bw == nil {
			if err := open(); err != nil {
				return err
			}
		}
		qpu := q.qs[pools[l.PoolID]].pool.QuotaPerUnit
		if err := writeCSVLine(bw, exportRow(s.logItem(r, q, l), full, tz, qpu, tr)); err != nil {
			return err
		}
		if rows++; rows%exportFlushRows != 0 && time.Since(flushed) < exportFlushEvery {
			return nil
		}
		if err := bw.Flush(); err != nil {
			return err
		}
		if err := rc.SetWriteDeadline(time.Now().Add(exportWriteWindow)); err != nil {
			return err
		}
		flushed = time.Now()
		return rc.Flush()
	})
	ev.Detail["rows"] = rows
	if err != nil && bw == nil && ctx.Err() == nil { // no row went out: a pool failed before the file began
		log.Printf("export logs: %v", err)
		return errExportPoolDown
	}
	if err != nil {
		return err
	}
	if bw == nil { // no rows: still a valid file
		if err := open(); err != nil {
			return err
		}
	}
	return bw.Flush()
}
