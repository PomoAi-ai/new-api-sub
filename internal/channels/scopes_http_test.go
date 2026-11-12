package channels_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"newapisub/internal/auth"
	"newapisub/internal/store"
)

func TestScopes(t *testing.T) {
	e := newEnv(t)
	alice, bobby := e.asUser(e.alice), e.asUser(e.bobby)
	adminA, adminB, super := e.asAdmin(e.a), e.asAdmin(e.b), e.asAdmin(e.super)
	aliceCh := alice.create(ch{})[0]
	bobbyCh := bobby.create(ch{})[0]
	aKey := adminA.create(ch{})[0]
	superKey := super.create(ch{})[0]
	given := super.create(ch{"user_id": e.bobby})[0]

	check := func(name string, c caller, query string, want ...int) {
		t.Helper()
		if got := ids(c.list(query)); !slices.Equal(got, sorted(want...)) {
			t.Errorf("%s%s: lists %v, want %v", name, query, got, sorted(want...))
		}
	}
	check("alice", alice, "", aliceCh)
	check("bobby", bobby, "", bobbyCh, given)
	check("admin a", adminA, "", aliceCh, aKey)
	check("admin b", adminB, "", bobbyCh, given)
	check("super", super, "", aliceCh, bobbyCh, aKey, superKey, given)

	uid := func(id int64) string { return "?user_id=" + strconv.FormatInt(id, 10) }
	check("admin a", adminA, uid(e.alice), aliceCh)
	check("admin a", adminA, "?user_id=0", aKey)
	check("admin a", adminA, uid(e.bobby)) // another admin's user: nothing, not an error
	check("super", super, "?user_id=0", aKey, superKey)
	check("super", super, uid(e.bobby), bobbyCh, given)
	check("alice", alice, uid(e.bobby), aliceCh) // ignored in the portal

	// A key given to a user belongs to that user's admin, whoever added it.
	if l := adminB.list(uid(e.bobby)); l[0].Owner.AdminUsername != "admin-b" || l[0].Owner.Username != "bobby" {
		t.Errorf("owner = %+v", l[0].Owner)
	}

	// Everything outside the scope is the same 404 as a missing channel.
	foreign := []struct {
		name string
		c    caller
		id   int
	}{{"alice on bobby", alice, bobbyCh}, {"alice on a's key", alice, aKey}, {"admin a on bobby", adminA, bobbyCh},
		{"admin a on super's", adminA, superKey}, {"bobby on missing", bobby, 999999}}
	for _, f := range foreign {
		for _, op := range []string{"/test", "/status", "/balance"} {
			if rec := f.c.do("POST", e.channelPath(f.id, op), ch{"status": 2}); rec.Code != 404 {
				t.Errorf("%s %s: %d", f.name, op, rec.Code)
			}
		}
		if rec := f.c.do("DELETE", e.channelPath(f.id, ""), nil); rec.Code != 404 {
			t.Errorf("%s delete: %d", f.name, rec.Code)
		}
	}
	adminA.want(404, "POST", e.channelPath(bobbyCh, "/restore"), nil)
	adminA.want(404, "DELETE", e.channelPath(bobbyCh, "?purge=1"), nil)
	if del := e.callsTo("POST", fmt.Sprintf("/api/channel/%d/", bobbyCh)); len(del) != 0 {
		t.Errorf("out-of-scope operation reached new-api: %+v", del)
	}

	// Stats follow the same scope; logs are open (TestLogPagesAreOpen).
	alice.want(404, "GET", "/api/stats?"+logRange+"&channel_id="+strconv.Itoa(bobbyCh), nil)
	adminA.want(404, "GET", "/api/stats?"+logRange+"&channel_id="+strconv.Itoa(bobbyCh), nil)
	adminA.want(200, "GET", "/api/stats?"+logRange+"&channel_id="+strconv.Itoa(aliceCh), nil)
	super.want(200, "GET", "/api/stats?"+logRange+"&channel_id="+strconv.Itoa(bobbyCh), nil)

	// Adding a channel for someone else's user.
	adminA.want(404, "POST", "/api/channels", ch{"type": 20, "name": "n", "key": "k", "mode": "single", "user_id": e.bobby})
	adminA.want(404, "POST", "/api/channels", ch{"type": 20, "name": "n", "key": "k", "mode": "single", "user_id": 999999})
	forAlice := adminA.create(ch{"user_id": e.alice})[0]
	check("alice", alice, "", aliceCh, forAlice)
	alice.create(ch{"user_id": e.bobby}) // ignored in the portal: it stays hers
	if got := bobby.list(""); len(got) != 2 {
		t.Errorf("portal user added a channel for someone else: %+v", got)
	}
}

func TestUserManagementFollowsTheScope(t *testing.T) {
	e := newEnv(t)
	adminA, super := e.asAdmin(e.a), e.asAdmin(e.super)
	var users []store.User
	json.Unmarshal([]byte(adminA.want(200, "GET", "/api/users", nil)), &users)
	if len(users) != 1 || users[0].ID != e.alice || users[0].AdminID != e.a || users[0].AdminUsername != "admin-a" {
		t.Errorf("admin a sees %+v", users)
	}
	json.Unmarshal([]byte(super.want(200, "GET", "/api/users", nil)), &users)
	if len(users) != 2 || users[1].AdminUsername != "admin-b" {
		t.Errorf("super sees %+v", users)
	}

	other := strconv.FormatInt(e.bobby, 10)
	adminA.want(404, "POST", "/api/users/"+other+"/status", ch{"status": 2})
	adminA.want(404, "POST", "/api/users/"+other+"/password", ch{"password": "new-password"})
	adminA.want(404, "DELETE", "/api/users/"+other, nil)
	super.want(204, "POST", "/api/users/"+other+"/status", ch{"status": 2})
	if rec := e.asUser(e.bobby).do("GET", "/api/me", nil); rec.Code != 401 {
		t.Errorf("disabled user's session: %d", rec.Code)
	}

	// admin_id only counts for a super.
	var created struct {
		AdminID int64 `json:"admin_id"`
	}
	json.Unmarshal([]byte(adminA.want(201, "POST", "/api/users", ch{"username": "from-a", "password": "password1", "admin_id": e.b})), &created)
	if created.AdminID != e.a {
		t.Errorf("admin gave a user to admin %d", created.AdminID)
	}
	json.Unmarshal([]byte(super.want(201, "POST", "/api/users", ch{"username": "for-b", "password": "password1", "admin_id": e.b})), &created)
	if created.AdminID != e.b {
		t.Errorf("super's admin_id ignored: %d", created.AdminID)
	}
	json.Unmarshal([]byte(super.want(201, "POST", "/api/users", ch{"username": "for-root", "password": "password1"})), &created)
	if created.AdminID != e.super {
		t.Errorf("default owner = %d", created.AdminID)
	}
	super.want(400, "POST", "/api/users", ch{"username": "for-none", "password": "password1", "admin_id": 999999})
	adminA.want(409, "POST", "/api/users", ch{"username": "alice", "password": "password1"})
}

func TestDeletingAUserRequiresNoChannelsAtAll(t *testing.T) {
	e := newEnv(t)
	alice, adminA := e.asUser(e.alice), e.asAdmin(e.a)
	id := alice.create(ch{})[0]
	path := "/api/users/" + strconv.FormatInt(e.alice, 10)
	adminA.want(409, "DELETE", path, nil)
	alice.want(204, "DELETE", e.channelPath(id, ""), nil)
	adminA.want(409, "DELETE", path, nil) // soft-deleted channels still count
	if _, err := e.raw.Exec(`DELETE FROM channels WHERE id = $1`, e.nid(id)); err != nil {
		t.Fatal(err)
	}
	adminA.want(204, "DELETE", e.channelPath(id, "?purge=1"), nil)
	adminA.want(204, "DELETE", path, nil)
}

func TestSuperOnlyRoutesAre403ForRegularAdmins(t *testing.T) {
	e := newEnv(t)
	adminA, super := e.asAdmin(e.a), e.asAdmin(e.super)
	routes := [][2]string{
		{"GET", "/api/admins"}, {"POST", "/api/admins"}, {"POST", "/api/admins/1/status"}, {"POST", "/api/admins/1/password"},
		{"DELETE", "/api/admins/1"}, {"GET", "/api/templates"}, {"POST", "/api/templates"}, {"PUT", "/api/templates/1"},
		{"DELETE", "/api/templates/1"},
	}
	for _, r := range routes {
		if rec := adminA.do(r[0], r[1], ch{}); rec.Code != 403 {
			t.Errorf("admin %s %s: %d", r[0], r[1], rec.Code)
		}
		if rec := super.do(r[0], r[1], ch{}); rec.Code == 403 {
			t.Errorf("super %s %s: 403", r[0], r[1])
		}
	}
	var n int
	e.raw.QueryRow(`SELECT count(*) FROM admin_users`).Scan(&n)
	if n != 3 {
		t.Errorf("admin_users = %d", n)
	}
	if body := super.want(200, "GET", "/api/me", nil); !strings.Contains(body, `"role":"super"`) {
		t.Errorf("me = %s", body)
	}
	if body := adminA.want(200, "GET", "/api/me", nil); !strings.Contains(body, `"role":"admin"`) {
		t.Errorf("me = %s", body)
	}
	// Templates do not exist as far as the portal is concerned.
	e.asUser(e.alice).want(404, "GET", "/api/templates", nil)
}

func TestChannelTypesListEnabledTemplatesToEveryone(t *testing.T) {
	e := newEnv(t)
	_, err := e.st.CreateTemplate(context.Background(), store.Template{Name: "off", ChannelType: 8, Models: "m", Group: "g", Extra: json.RawMessage("{}")})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]caller{"portal": e.asUser(e.alice), "admin": e.asAdmin(e.a), "super": e.asAdmin(e.super)} {
		var got []map[string]any
		json.Unmarshal([]byte(c.want(200, "GET", "/api/channel-types", nil)), &got)
		wantExtra := map[string]any{"other_set": false}
		if name == "super" {
			wantExtra = map[string]any{}
		}
		if len(got) != 1 || got[0]["type"] != float64(20) || got[0]["models"] != "tpl-a,tpl-b" || got[0]["group"] != "tpl-group" ||
			got[0]["priority"] != float64(7) || got[0]["weight"] != float64(3) || got[0]["test_model"] != "tpl-a" ||
			!reflect.DeepEqual(got[0]["extra"], wantExtra) {
			t.Errorf("%s: %v", name, got)
		}
		if got[0]["template_name"] != "tpl-20" || got[0]["template_id"] == nil {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestRegularAdminLogsLeaveOutTheEndUsers(t *testing.T) {
	e := newEnv(t)
	id := e.asUser(e.alice).create(ch{})[0]
	for _, token := range []string{"end-user-token", "Model test"} {
		if _, err := e.raw.Exec(`INSERT INTO logs (user_id, created_at, type, content, username, token_name, model_name, quota,
			prompt_tokens, completion_tokens, use_time, is_stream, channel_id, token_id, "group", ip, request_id, other)
			VALUES (9, 100, 2, '', 'end-user', $1, 'm', 5, 1, 2, 3, false, $2, 7, 'vip', '203.0.113.9', 'req', '{"a":1}')`, token, e.nid(id)); err != nil {
			t.Fatal(err)
		}
	}
	logs := func(c caller) (items []map[string]any, total int) {
		t.Helper()
		var res struct {
			Items []map[string]any `json:"items"`
			Total int              `json:"total"`
		}
		json.Unmarshal([]byte(c.want(200, "GET", "/api/logs?"+logRange+"&channel_id="+strconv.Itoa(id), nil)), &res)
		return res.Items, res.Total
	}
	endUser := map[string]any{"user_id": 9.0, "username": "end-user", "token_id": 7.0, "token_name": "Model test",
		"ip": "203.0.113.9", "group": "vip", "request_id": "req", "other": `{"a":1}`}

	full, total := logs(e.asAdmin(e.super))
	if total != 2 || len(full) != 2 { // the channel test is there too
		t.Fatalf("super: total=%d %v", total, full)
	}
	for k, v := range endUser {
		if full[0][k] != v {
			t.Errorf("super: %s = %v, want %v", k, full[0][k], v)
		}
	}

	for name, c := range map[string]caller{"admin": e.asAdmin(e.a), "portal": e.asUser(e.alice)} {
		items, total := logs(c)
		if total != 2 || len(items) != 2 || items[0]["channel_name"] != "n" || items[0]["model_name"] != "m" {
			t.Fatalf("%s: total=%d %v", name, total, items)
		}
		if items[0]["token_name"] != "Model test" || items[1]["token_name"] != "" {
			t.Errorf("%s token names: %v, %v", name, items[0]["token_name"], items[1]["token_name"])
		}
		want := map[string]any{"user_id": 0.0, "username": "", "token_id": 0.0, "ip": "", "group": "vip", "request_id": "req", "other": `{"a":1}`}
		for k, v := range want {
			if items[1][k] != v {
				t.Errorf("%s: %s = %v, want %v", name, k, items[1][k], v)
			}
		}
	}
}

func TestNewAPIReadsAndFetchModelsAreForSupers(t *testing.T) {
	e := newEnv(t)
	super, adminA, alice := e.asAdmin(e.super), e.asAdmin(e.a), e.asUser(e.alice)
	for path, want := range map[string]string{
		"models":            `[{"id":"gpt-x"}]`,
		"default-base-urls": `{"1":"https://api.example.com"}`,
		"groups":            `["default","vip"]`,
		"prefill-groups":    `[{"id":1,"type":"model"}]`,
	} {
		path = "/api/newapi/" + path
		adminA.want(403, "GET", path, nil)
		alice.want(404, "GET", path, nil)
		if got := strings.TrimSpace(super.want(200, "GET", path, nil)); got != want {
			t.Errorf("%s = %s, want new-api's data %s", path, got, want)
		}
	}

	// The query does not pass through as a whole.
	super.want(200, "GET", "/api/newapi/prefill-groups?type=mo%26del&page_size=1", nil)
	if cs := e.callsTo("GET", "/api/prefill_group/"); cs[len(cs)-1].Query != "type=mo%26del" {
		t.Errorf("query = %q", cs[len(cs)-1].Query)
	}

	// A scope the PAT lacks is an error saying so, not an empty list.
	e.denied = "/api/group/"
	if body := super.want(502, "GET", "/api/newapi/groups", nil); !strings.Contains(body, "lacks the required permission: group:read") {
		t.Errorf("denied = %s", body)
	}

	fetch := "/api/newapi/fetch-models"
	body := ch{"type": 20, "base_url": " https://up.example.com ", "key": " sk-fetch-secret \r\n\n"}
	adminA.want(403, "POST", fetch, body)
	alice.want(404, "POST", fetch, body)
	if len(e.callsTo("POST", "/api/channel/fetch_models")) != 0 {
		t.Error("new-api was called for a regular admin")
	}
	if got := strings.TrimSpace(super.want(200, "POST", fetch, body)); got != `["m1","m2"]` {
		t.Errorf("fetched = %s", got)
	}
	var sent map[string]any
	json.Unmarshal([]byte(e.callsTo("POST", "/api/channel/fetch_models")[0].Body), &sent)
	if sent["type"] != 20.0 || sent["base_url"] != "https://up.example.com" || sent["key"] != "sk-fetch-secret" {
		t.Errorf("new-api got %v", sent)
	}
	super.want(400, "POST", fetch, ch{"key": "k"})
	super.want(400, "POST", fetch, ch{"type": 20, "base_url": "https://user:pw@up.example.com", "key": "k"})
	// An upstream quoting the key does not get it into the response or the audit log.
	refused := super.want(502, "POST", fetch, ch{"type": 20, "key": "sk-refused"})
	if strings.Contains(refused, "sk-refused") || !strings.Contains(refused, "Failed to fetch models") {
		t.Errorf("refused = %s", refused)
	}

	var ok, failed, withKey int
	if err := e.raw.QueryRow(`SELECT count(*) FILTER (WHERE success), count(*) FILTER (WHERE NOT success),
		count(*) FILTER (WHERE detail::text LIKE '%sk-%' OR detail::text LIKE '%pw@%')
		FROM audit_logs WHERE action = 'channel.fetch_models'`).Scan(&ok, &failed, &withKey); err != nil {
		t.Fatal(err)
	}
	if ok != 1 || failed != 4 || withKey != 0 { // the regular admin's 403 is one of them
		t.Errorf("audited ok=%d failed=%d with the key=%d", ok, failed, withKey)
	}
	var base string
	e.raw.QueryRow(`SELECT detail->>'base_url' FROM audit_logs WHERE action = 'channel.fetch_models' AND success`).Scan(&base)
	if base != "https://up.example.com" {
		t.Errorf("audited base_url = %q", base)
	}
}

func TestAdminManagementGuards(t *testing.T) {
	e := newEnv(t)
	super, adminA, adminB := e.asAdmin(e.super), e.asAdmin(e.a), e.asAdmin(e.b)
	me := "/api/admins/" + strconv.FormatInt(e.super, 10)
	super.want(400, "POST", me+"/status", ch{"status": 2})
	super.want(400, "DELETE", me, nil)

	var admins []store.AdminInfo
	json.Unmarshal([]byte(super.want(200, "GET", "/api/admins", nil)), &admins)
	if len(admins) != 3 || admins[0].Role != "super" || admins[1].UserCount != 1 {
		t.Errorf("admins = %+v", admins)
	}

	// New admins are regular whatever the request says.
	var created struct {
		ID   int64  `json:"id"`
		Role string `json:"role"`
	}
	json.Unmarshal([]byte(super.want(201, "POST", "/api/admins", ch{"username": "new-admin", "password": "password1", "role": "super"})), &created)
	var role string
	e.raw.QueryRow(`SELECT role FROM admin_users WHERE id = $1`, created.ID).Scan(&role)
	if created.Role != "admin" || role != "admin" {
		t.Errorf("role = %q, stored %q", created.Role, role)
	}
	super.want(409, "POST", "/api/admins", ch{"username": "new-admin", "password": "password1"})
	super.want(204, "DELETE", "/api/admins/"+strconv.FormatInt(created.ID, 10), nil)

	a := "/api/admins/" + strconv.FormatInt(e.a, 10)
	super.want(409, "DELETE", a, nil) // alice belongs to admin a
	super.want(204, "POST", a+"/password", ch{"password": "new-password"})
	if rec := adminA.do("GET", "/api/me", nil); rec.Code != 401 {
		t.Errorf("session after password reset: %d", rec.Code)
	}

	b := "/api/admins/" + strconv.FormatInt(e.b, 10)
	super.want(204, "POST", b+"/status", ch{"status": 2})
	if rec := adminB.do("GET", "/api/me", nil); rec.Code != 401 {
		t.Errorf("disabled admin's session: %d", rec.Code)
	}
	super.want(204, "POST", b+"/status", ch{"status": 1})

	// Resetting one's own password keeps the session going.
	if rec := super.do("POST", me+"/password", ch{"password": "new-password"}); rec.Code != 204 || len(rec.Result().Cookies()) != 1 {
		t.Errorf("own password: %d %v", rec.Code, rec.Result().Cookies())
	}
}

func TestSessionChecksTheAdminStatusItself(t *testing.T) {
	e := newEnv(t)
	adminA := e.asAdmin(e.a)
	adminA.want(200, "GET", "/api/me", nil)
	// Without the session_version bump that disabling through the API also does.
	if _, err := e.raw.Exec(`UPDATE admin_users SET status = 2 WHERE id = $1`, e.a); err != nil {
		t.Fatal(err)
	}
	adminA.want(401, "GET", "/api/me", nil)
}

func TestDisabledAdminCannotLogIn(t *testing.T) {
	e := newEnv(t)
	hash, err := auth.HashPassword("password1")
	if err != nil {
		t.Fatal(err)
	}
	id, err := e.st.CreateAdmin(context.Background(), "off-admin", hash, store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	login := func() int {
		req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"off-admin","password":"password1"}`))
		rec := httptest.NewRecorder()
		e.adminMux.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := login(); code != 200 {
		t.Fatalf("login: %d", code)
	}
	if _, err := e.st.SetAdminStatus(context.Background(), id, 2); err != nil {
		t.Fatal(err)
	}
	if code := login(); code != 403 {
		t.Errorf("disabled login: %d", code)
	}
}

func TestLastSuperCannotBeRemovedEvenConcurrently(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.st.SetAdminStatus(ctx, e.super, 2); !errors.Is(err, store.ErrLastSuper) {
		t.Errorf("disable the only super: %v", err)
	}
	if _, err := e.st.DeleteAdmin(ctx, e.super); !errors.Is(err, store.ErrLastSuper) {
		t.Errorf("delete the only super: %v", err)
	}
	second := e.mustAdmin("root2", store.RoleSuper)
	for range 20 { // two supers disabling each other at once
		var wg sync.WaitGroup
		for _, id := range []int64{e.super, second} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := e.st.SetAdminStatus(ctx, id, 2); err != nil && !errors.Is(err, store.ErrLastSuper) {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		var enabled int
		e.raw.QueryRow(`SELECT count(*) FROM admin_users WHERE role = 'super' AND status = 1`).Scan(&enabled)
		if enabled != 1 {
			t.Fatalf("enabled supers = %d", enabled)
		}
		for _, id := range []int64{e.super, second} {
			if _, err := e.st.SetAdminStatus(ctx, id, 1); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestAuditLogFollowsTheScope(t *testing.T) {
	e := newEnv(t)
	alice, bobby := e.asUser(e.alice), e.asUser(e.bobby)
	adminA, adminB, super := e.asAdmin(e.a), e.asAdmin(e.b), e.asAdmin(e.super)
	alice.create(ch{})
	bobby.create(ch{})
	adminA.create(ch{})
	adminB.create(ch{})

	actors := func(c caller, query string) []string {
		t.Helper()
		var res struct {
			Items []store.AuditLog `json:"items"`
			Total int              `json:"total"`
		}
		json.Unmarshal([]byte(c.want(200, "GET", "/api/audit-logs?category=channel&"+strings.TrimPrefix(query, "?"), nil)), &res)
		var out []string
		for _, a := range res.Items {
			out = append(out, a.ActorType+":"+a.ActorName)
		}
		slices.Sort(out)
		if res.Total != len(out) {
			t.Errorf("total %d, items %d", res.Total, len(out))
		}
		return out
	}
	if got := actors(adminA, ""); !slices.Equal(got, []string{"admin:admin-a", "portal:alice"}) {
		t.Errorf("admin a sees %v", got)
	}
	if got := actors(adminA, "?actor_type=portal"); !slices.Equal(got, []string{"portal:alice"}) {
		t.Errorf("admin a filtered: %v", got)
	}
	if got := actors(adminA, "?actor_name=bobby"); len(got) != 0 {
		t.Errorf("admin a sees bobby: %v", got)
	}
	if got := slices.DeleteFunc(actors(super, ""), func(a string) bool { return a == "admin:system" }); len(got) != 4 { // and the push worker
		t.Errorf("super sees %v", got)
	}
	if got := actors(alice, "?actor_name=bobby"); !slices.Equal(got, []string{"portal:alice"}) {
		t.Errorf("alice sees %v", got)
	}
}
