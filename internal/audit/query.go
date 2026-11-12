package audit

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"newapisub/internal/httpx"
	"newapisub/internal/newapi"
	"newapisub/internal/store"
)

// categories are the prefixes of the audit actions: the part before the dot.
var categories = []string{"auth", "channel", "user", "admin", "template", "log", "view"}

// ParseQuery parses the audit list parameters shared by portal and admin:
// action, category, success, start, end (Unix seconds), p and page_size. Actor
// filters are left to the caller. Errors are 400s.
func ParseQuery(v url.Values) (store.AuditFilter, error) {
	f := store.AuditFilter{Action: strings.TrimSpace(v.Get("action")), Category: v.Get("category")}
	if f.Category != "" && !slices.Contains(categories, f.Category) {
		return f, httpx.Errorf(http.StatusBadRequest, "无效的 category")
	}
	if raw := v.Get("success"); raw != "" {
		ok, err := strconv.ParseBool(raw)
		if err != nil {
			return f, httpx.Errorf(http.StatusBadRequest, "无效的 success")
		}
		f.Success = &ok
	}
	for _, p := range []struct {
		name string
		dst  *int64
	}{{"start", &f.Start}, {"end", &f.End}} {
		if raw := v.Get(p.name); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n <= 0 {
				return f, httpx.Errorf(http.StatusBadRequest, "无效的 %s", p.name)
			}
			*p.dst = n
		}
	}
	var err error
	if f.Page, f.PageSize, err = newapi.ParsePage(v); err != nil {
		return f, httpx.Errorf(http.StatusBadRequest, "%s", err.Error())
	}
	return f, nil
}
