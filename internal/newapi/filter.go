package newapi

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	maxSpan         = 92 * 86400
	defaultPageSize = 50
	maxPageSize     = 200
	maxTZOffset     = 50400
)

// ErrOutOfScope means a requested channel is not in the caller's scope;
// handlers answer 404 so foreign channel ids are not confirmed.
var ErrOutOfScope = errors.New("channel not found")

// Filter is the shared log/stats query. ChannelIDs must be set through
// Restrict before querying, unless AllChannels opts out of that.
type Filter struct {
	ChannelIDs []int
	// AllChannels is the open log pages: no scope, so ChannelIDs only narrows.
	AllChannels bool
	Start, End  int64  // Unix seconds, [Start, End)
	Type        int    // 0 = 2, 5 and 6; these logs are never of another type
	ModelName   string // exact, or a LIKE pattern if it holds a %
	// Username and TokenName are the end user; see DropEndUserSearch.
	Username, TokenName, Group, RequestID, UpstreamRequestID string
	Page                                                     int // logs page, from 1
	PageSize                                                 int // logs page size
}

// DropEndUserSearch is for callers who may not see end users: searching by one
// would confirm that it exists.
func (f *Filter) DropEndUserSearch() {
	f.Username, f.TokenName = "", ""
}

// likeEscaper is new-api's sanitizeLikePattern: % stays the wildcard, _ is
// literal, and ! escapes rather than \, which MySQL treats differently.
var likeEscaper = strings.NewReplacer("!", "!!", "_", "!_")

// checkLike applies new-api's validateLikePattern to a value holding a %.
func checkLike(s string) error {
	if !strings.Contains(s, "%") {
		return nil
	}
	s = likeEscaper.Replace(s)
	switch {
	case strings.Contains(s, "%%"):
		return errors.New("搜索模式中不允许包含连续的 % 通配符")
	case strings.Count(s, "%") > 2:
		return errors.New("搜索模式中最多允许包含 2 个 % 通配符")
	case len(strings.ReplaceAll(s, "%", "")) < 2:
		return errors.New("使用模糊搜索时，关键词长度至少为 2 个字符")
	}
	return nil
}

// none says f can match nothing: a scope without channels.
func (f Filter) none() bool { return len(f.ChannelIDs) == 0 && !f.AllChannels }

// Offset is the number of rows before Page.
func (f Filter) Offset() int { return (f.Page - 1) * f.PageSize }

// ParsePage parses p (from 1, default 1) and page_size (1-200, default 50).
// Errors are client errors.
func ParsePage(v url.Values) (page, size int, err error) {
	page, size = 1, defaultPageSize
	if s := v.Get("p"); s != "" {
		// 32-bit bound keeps the offset far from overflowing.
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil || n < 1 {
			return 0, 0, errors.New("p 须为正整数")
		}
		page = int(n)
	}
	if s := v.Get("page_size"); s != "" {
		if size, err = strconv.Atoi(s); err != nil || size < 1 || size > maxPageSize {
			return 0, 0, fmt.Errorf("page_size 须为 1–%d", maxPageSize)
		}
	}
	return page, size, nil
}

// ParseFilter parses start, end, type, channel_id (repeatable), model_name,
// username, token_name, group, request_id, upstream_request_id, p and
// page_size. Errors are client errors.
func ParseFilter(v url.Values) (Filter, error) {
	var f Filter
	var err error
	// Not negative: end-start below would overflow past the span limit.
	if f.Start, err = strconv.ParseInt(v.Get("start"), 10, 64); err != nil || f.Start < 0 {
		return f, errors.New("start 必填且为 Unix 秒")
	}
	if f.End, err = strconv.ParseInt(v.Get("end"), 10, 64); err != nil {
		return f, errors.New("end 必填且为 Unix 秒")
	}
	if f.End <= f.Start {
		return f, errors.New("end 必须大于 start")
	}
	if f.End-f.Start > maxSpan {
		return f, errors.New("时间跨度不能超过 92 天")
	}
	if s := v.Get("type"); s != "" {
		if f.Type, err = strconv.Atoi(s); err != nil || f.Type < 0 {
			return f, errors.New("type 须为非负整数")
		}
	}
	for _, s := range v["channel_id"] {
		id, err := strconv.Atoi(s)
		if err != nil || id <= 0 {
			return f, fmt.Errorf("无效的 channel_id %q", s)
		}
		f.ChannelIDs = append(f.ChannelIDs, id)
	}
	f.ModelName, f.Username = v.Get("model_name"), v.Get("username")
	if err := checkLike(f.ModelName); err != nil {
		return f, err
	}
	if err := checkLike(f.Username); err != nil {
		return f, err
	}
	f.TokenName, f.Group = v.Get("token_name"), v.Get("group")
	f.RequestID, f.UpstreamRequestID = v.Get("request_id"), v.Get("upstream_request_id")
	f.Page, f.PageSize, err = ParsePage(v)
	return f, err
}

// ParseTZOffset parses the required tz_offset (seconds east of UTC).
func ParseTZOffset(v url.Values) (int, error) {
	tz, err := strconv.Atoi(v.Get("tz_offset"))
	if err != nil || tz < -maxTZOffset || tz > maxTZOffset {
		return 0, errors.New("tz_offset 必填，范围 ±50400 秒")
	}
	return tz, nil
}

// Restrict limits the filter to owned channels: no requested ids means all
// owned ones; any requested id outside owned is ErrOutOfScope. These are local
// ids, never to be queried as they are: ForPool turns them into a pool's.
func (f *Filter) Restrict(owned []int) error {
	if len(f.ChannelIDs) == 0 {
		f.ChannelIDs = slices.Clone(owned)
		return nil
	}
	for _, id := range f.ChannelIDs {
		if !slices.Contains(owned, id) {
			return ErrOutOfScope
		}
	}
	return nil
}

// ForPool is f for one pool, where ids are the new-api ids of the channels f
// asks for. The open log pages asking for no channel in particular take every
// channel of every pool. false: the pool has none of them, skip it.
func (f Filter) ForPool(ids []int) (Filter, bool) {
	if f.AllChannels && len(f.ChannelIDs) == 0 {
		return f, true
	}
	if len(ids) == 0 {
		return f, false
	}
	f.ChannelIDs = slices.Clone(ids)
	return f, true
}

// ParsePerfWindow reads ?hours= like new-api's queryWindow: whole hours back from
// the current one, 24 by default and 30 days at most. The filter ends now.
func ParsePerfWindow(v url.Values) (Filter, error) {
	hours := 24
	if raw := v.Get("hours"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return Filter{}, errors.New("无效的 hours")
		}
		hours = min(n, 24*30)
	}
	now := time.Now().Unix()
	return Filter{Start: now - now%3600 - int64(hours-1)*3600, End: now + 1}, nil
}
