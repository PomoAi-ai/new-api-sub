package newapi

import (
	"context"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	pg = dialect{pg: true}
	my = dialect{}
)

func TestLogsSQLPerDialect(t *testing.T) {
	f := Filter{ChannelIDs: []int{3, 4}, Start: 100, End: 200, Type: 5, Page: 3, PageSize: 50}

	q, args := logsSQL(pg, f)
	for _, want := range []string{"channel_id IN ($1, $2)", "created_at >= $3 AND created_at < $4", "type = $5",
		`COALESCE("group", '')`, "ORDER BY created_at DESC, id DESC LIMIT $6 OFFSET $7"} {
		if !strings.Contains(q, want) {
			t.Errorf("pg sql missing %q:\n%s", want, q)
		}
	}
	if !reflect.DeepEqual(args, []any{3, 4, int64(100), int64(200), 5, 50, 100}) {
		t.Errorf("pg args = %v", args)
	}

	q, _ = logsSQL(my, f)
	if !strings.Contains(q, "channel_id IN (?, ?)") || !strings.Contains(q, "COALESCE(`group`, '')") ||
		!strings.Contains(q, "LIMIT ? OFFSET ?") || strings.Contains(q, "$") {
		t.Errorf("mysql sql:\n%s", q)
	}
}

func TestLogsCountSharesThePageFilter(t *testing.T) {
	f := Filter{ChannelIDs: []int{3, 4}, Start: 100, End: 200, Type: 5, Page: 3, PageSize: 50}
	page, pageArgs := logsSQL(pg, f)
	count, countArgs := logsCountSQL(pg, f)
	where := page[strings.Index(page, " FROM "):strings.Index(page, " ORDER BY ")]
	if count != "SELECT COUNT(*)"+where || !reflect.DeepEqual(countArgs, pageArgs[:len(pageArgs)-2]) {
		t.Errorf("count differs from page filter:\n%s\n%s %v", count, page, countArgs)
	}
}

const excludeTestLogs = "AND COALESCE(token_name, '') <> 'Model test'"

func TestTestLogsAreListedButNotInStatistics(t *testing.T) {
	f := Filter{ChannelIDs: []int{1}, Start: 1, End: 2, Page: 1, PageSize: 10}
	if q, _ := logsSQL(pg, f); strings.Contains(q, "Model test") {
		t.Errorf("logs hide channel-test logs:\n%s", q)
	}
	for _, d := range []dialect{pg, my} {
		if q, _ := statsSQL(d, f, "model_name"); !strings.Contains(q, excludeTestLogs) {
			t.Errorf("stats include channel-test logs:\n%s", q)
		}
		if q, _ := perfSQL(d, f); !strings.Contains(q, excludeTestLogs) {
			t.Errorf("perf includes channel-test logs:\n%s", q)
		}
		if q, _ := flowSQL(d, f, true); !strings.Contains(q, excludeTestLogs) {
			t.Errorf("flow includes channel-test logs:\n%s", q)
		}
	}
}

func TestBasicFlowSQLHasNoEndUserColumns(t *testing.T) {
	flow, _ := flowSQL(pg, Filter{ChannelIDs: []int{1}, Start: 1, End: 2}, false)
	q := flow[:strings.Index(flow, " FROM ")]
	for _, col := range []string{"user_id", "username", "token_id", "token_name", ", ip", "group", "request_id", "other"} {
		if strings.Contains(q, col) {
			t.Errorf("basic flow reads %q:\n%s", col, flow)
		}
	}
}

func TestDropEndUserSearchKeepsTheRest(t *testing.T) {
	f := Filter{Username: "u", TokenName: "t", Group: "g", RequestID: "r", UpstreamRequestID: "up", ModelName: "m"}
	f.DropEndUserSearch()
	if f.Username != "" || f.TokenName != "" || f.Group != "g" || f.RequestID != "r" || f.UpstreamRequestID != "up" || f.ModelName != "m" {
		t.Errorf("%+v", f)
	}
}

func TestLikeFilterMatchesNewAPI(t *testing.T) {
	for _, c := range []struct{ input, want string }{
		{"gpt-4", " AND model_name = $"}, // no %: exact, and _ needs no escape
		{"gpt_4%", " AND model_name LIKE $"},
	} {
		q, _ := logsSQL(pg, Filter{ChannelIDs: []int{1}, Start: 1, End: 2, Page: 1, PageSize: 10, ModelName: c.input})
		if !strings.Contains(q, c.want) {
			t.Errorf("%q: %s", c.input, q)
		}
	}
	_, args := logsSQL(pg, Filter{ChannelIDs: []int{1}, Start: 1, End: 2, Page: 1, PageSize: 10, ModelName: "a_b!%"})
	if !slices.Contains(args, any("a!_b!!%")) {
		t.Errorf("escaped pattern missing from %v", args)
	}
	for bad, ok := range map[string]bool{"gpt%": true, "%gpt%": true, "%%gpt": false, "%a%b%": false, "a%": false, "gpt": true, "%": false} {
		if err := checkLike(bad); (err == nil) != ok {
			t.Errorf("checkLike(%q) = %v", bad, err)
		}
	}
}

func TestChannelsSQLNeverReadsSecretColumns(t *testing.T) {
	for _, d := range []dialect{pg, my} {
		q, _ := channelsSQL(d, []int{1})
		for _, col := range []string{"key", "remark", "other", "setting", "settings", "param_override", "header_override"} {
			if regexp.MustCompile(`(?i)[^a-z_]` + col + `[^a-z_]`).MatchString(q) {
				t.Errorf("channels query reads %q:\n%s", col, q)
			}
		}
	}
}

func TestChannelInfoReadAsTextPerDialect(t *testing.T) {
	if q, _ := channelsSQL(pg, []int{1}); !strings.Contains(q, "COALESCE(channel_info::text, '')") {
		t.Errorf("pg: %s", q)
	}
	if q, _ := channelsSQL(my, []int{1}); !strings.Contains(q, "COALESCE(CAST(channel_info AS CHAR), '')") {
		t.Errorf("mysql: %s", q)
	}
}

func TestMaskKeysHidesPortalTokens(t *testing.T) {
	token := "ptl_" + strings.Repeat("aB3-_", 8) + "xyz"
	if got := MaskKeys("rejected " + token + " here"); strings.Contains(got, "aB3") {
		t.Errorf("token not hidden: %s", got)
	}
}

func TestListChannelInfoDropsPerKeyErrors(t *testing.T) {
	got := string(listChannelInfo(`{"is_multi_key":true,"multi_key_size":2,"multi_key_status_list":{"0":3},` +
		`"multi_key_disabled_reason":{"0":"Incorrect API key sk-abc"},"multi_key_disabled_time":{"0":1}}`))
	if strings.Contains(got, "sk-abc") || strings.Contains(got, "disabled_time") {
		t.Errorf("per-key errors kept: %s", got)
	}
	if !strings.Contains(got, `"multi_key_size":2`) || !strings.Contains(got, `"multi_key_status_list":{"0":3}`) {
		t.Errorf("multi-key counts lost: %s", got)
	}
	for _, unreadable := range []string{"", "null", "not json"} {
		if got := string(listChannelInfo(unreadable)); got != "{}" {
			t.Errorf("listChannelInfo(%q) = %s", unreadable, got)
		}
	}
}

func TestEmptyScopeSkipsSQL(t *testing.T) {
	d := &DB{} // nil connections: any query would panic
	if items, total, err := d.Logs(context.Background(), Filter{Page: 1, PageSize: 10}, false); err != nil || len(items) != 0 || total != 0 {
		t.Fatalf("logs: %v %v %v", items, total, err)
	}
	if rows, err := d.Stats(context.Background(), Filter{}); err != nil || len(rows) != 0 {
		t.Fatalf("stats: %v %v", rows, err)
	}
	if chs, err := d.Channels(context.Background(), nil); err != nil || len(chs) != 0 {
		t.Fatalf("channels: %v %v", chs, err)
	}
}

func TestDetectDialect(t *testing.T) {
	cases := map[string]string{
		"postgres://u:p@h/db":                 "pgx",
		"postgresql://u:p@h/db":               "pgx",
		"root:pw@tcp(127.0.0.1:3306)/new-api": "mysql",
		"":                                    "",
		"local":                               "",
		"clickhouse://h:9000/db":              "",
		"tcp://h:9000":                        "",
		"https://h":                           "",
	}
	for dsn, want := range cases {
		driver, _, err := detectDialect(dsn)
		if driver != want || (want == "") != (err != nil) {
			t.Errorf("%q: driver=%q err=%v, want %q", dsn, driver, err, want)
		}
	}
}

func TestParseFilterBounds(t *testing.T) {
	ok := func(q string) Filter {
		v, _ := url.ParseQuery(q)
		f, err := ParseFilter(v)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		return f
	}
	bad := func(q string) {
		v, _ := url.ParseQuery(q)
		if _, err := ParseFilter(v); err == nil {
			t.Errorf("%q accepted", q)
		}
	}
	f := ok("start=0&end=7948800&channel_id=3&channel_id=4")
	if f.Page != 1 || f.PageSize != 50 || f.Offset() != 0 || !reflect.DeepEqual(f.ChannelIDs, []int{3, 4}) {
		t.Errorf("defaults: %+v", f)
	}
	ok("start=0&end=1&type=1") // these logs are never of that type, so it finds nothing
	if f = ok("start=0&end=1&type=6&p=3&page_size=200"); f.Offset() != 400 {
		t.Errorf("offset of p=3 page_size=200: %d", f.Offset())
	}
	bad("end=1")
	bad("start=5&end=5")
	bad("start=0&end=1&model_name=%25%25gpt")
	bad("start=0&end=1&username=a%25")
	bad("start=0&end=7948801") // 92 days + 1s
	bad("start=-1&end=1")
	bad("start=-9223372036854775808&end=9223372036854775807") // the span overflows past the limit
	bad("start=0&end=1&type=-1")
	bad("start=0&end=1&page_size=201")
	bad("start=0&end=1&page_size=0")
	bad("start=0&end=1&p=0")
	bad("start=0&end=1&p=x")
	bad("start=0&end=1&p=2147483648")
	bad("start=0&end=1&channel_id=x")

	if _, err := ParseTZOffset(url.Values{}); err == nil {
		t.Error("missing tz_offset accepted")
	}
	if _, err := ParseTZOffset(url.Values{"tz_offset": {"50401"}}); err == nil {
		t.Error("tz_offset out of range accepted")
	}
}
