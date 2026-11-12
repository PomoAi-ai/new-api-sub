package channels_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"newapisub/internal/store"
)

func TestCreateIsLockedToTheTemplateForEveryRole(t *testing.T) {
	e := newEnv(t)
	tampered := ch{"group": "vip", "priority": 99, "weight": 99, "test_model": "evil", "status": 2,
		"base_url": "https://evil.example.com"}
	for name, c := range map[string]caller{"portal": e.asUser(e.alice), "admin": e.asAdmin(e.a), "super": e.asAdmin(e.super)} {
		c.create(tampered)
		got := e.lastCreate().Create.Channel
		if got.Models != "tpl-a,tpl-b" || got.Group != "tpl-group" || got.Priority != 7 || got.Weight != 3 || got.Status != 1 ||
			got.TestModel != "tpl-a" || got.BaseURL != "https://openrouter.example.com" {
			t.Errorf("%s: new-api got %+v, want the template's values", name, got)
		}
		c.want(400, "POST", "/api/channels", ch{"type": 1, "name": "n", "key": "k", "mode": "single", "models": "m"}) // no template for type 1

		c.create(ch{"models": " tpl-b ,tpl-a,tpl-b"})
		if got := e.lastCreate().Create.Channel; got.Models != "tpl-a,tpl-b" || got.TestModel != "tpl-a" {
			t.Errorf("%s: all picked: %+v", name, got)
		}
		// The test model cannot be one the channel does not serve.
		c.create(ch{"models": "tpl-b"})
		if got := e.lastCreate().Create.Channel; got.Models != "tpl-b" || got.TestModel != "tpl-b" {
			t.Errorf("%s: subset: %+v", name, got)
		}
		before := len(e.callsTo("POST", "/api/channel/"))
		for _, models := range []string{"evil", "tpl-a,evil", " , "} {
			c.want(400, "POST", "/api/channels", ch{"type": 20, "name": "n", "key": "k", "mode": "single", "models": models})
		}
		if after := len(e.callsTo("POST", "/api/channel/")); after != before {
			t.Errorf("%s: a refused create reached new-api", name)
		}
	}
}

func TestCustomBaseURLOnlyWhereTheTemplateAllowsIt(t *testing.T) {
	e := newEnv(t)
	e.template(1, "http://internal.example.com", true)
	alice := e.asUser(e.alice)
	create := func(url string) int {
		code := alice.do("POST", "/api/channels", ch{"type": 1, "name": "n", "key": "k", "mode": "single", "base_url": url}).Code
		e.push()
		return code
	}
	if code := create("https://custom.example.com/v1"); code != 201 || e.lastCreate().Create.Channel.BaseURL != "https://custom.example.com/v1" {
		t.Errorf("custom https: %d %q", code, e.lastCreate().Create.Channel.BaseURL)
	}
	if code := create(""); code != 201 || e.lastCreate().Create.Channel.BaseURL != "http://internal.example.com" {
		t.Errorf("empty: %d %q", code, e.lastCreate().Create.Channel.BaseURL)
	}
	// The form sends the prefilled template value back; it was the super's choice, so it needs no https.
	for _, own := range []string{"http://internal.example.com", "http://internal.example.com/"} {
		if code := create(own); code != 201 {
			t.Errorf("template's own value %q rejected: %d", own, code)
		}
	}
	for _, bad := range []string{"http://custom.example.com", "https://10.0.0.1", "https://u:p@custom.example.com"} {
		if code := create(bad); code != 400 {
			t.Errorf("%s: got %d, want 400", bad, code)
		}
	}
}

func TestBatchIsAChannelPerKeyFoundByItsRemark(t *testing.T) {
	e := newEnv(t) // like rc.47: the create response holds no ids
	alice := e.asUser(e.alice)
	got := alice.create(ch{"mode": "batch", "key": " sk-one \r\n\n sk-two\nsk-three\n", "batch_add_set_key_prefix_2_name": true})
	if len(got) != 3 {
		t.Fatalf("ids = %v", got)
	}
	var keys []string
	for _, c := range e.callsTo("POST", "/api/channel/") {
		// The option that would write key prefixes into the names does not reach new-api.
		if c.Create.Mode != "single" || c.Create.KeyPrefix || !strings.HasPrefix(c.Create.Channel.Remark, fmt.Sprintf("portal:u%d:", e.alice)) {
			t.Errorf("new-api got %+v", c.Create)
		}
		keys = append(keys, c.Create.Channel.Key)
	}
	if !slices.Equal(keys, []string{"sk-one", "sk-two", "sk-three"}) {
		t.Errorf("keys pushed: %q", keys)
	}
	for _, id := range got {
		if e.nid(id) == id {
			t.Errorf("local id %d is its new-api id", id)
		}
	}
	if l := alice.list(""); !slices.Equal(ids(l), sorted(got...)) {
		t.Errorf("listed %v, created %v", ids(l), got)
	}
	// All three are in the scope of alice's admin too, not of the other one.
	if l := e.asAdmin(e.a).list(""); !slices.Equal(ids(l), sorted(got...)) || l[0].Owner.UserID != e.alice || l[0].Owner.AdminID != e.a {
		t.Errorf("admin a sees %+v", l)
	}
	if l := e.asAdmin(e.b).list(""); len(l) != 0 {
		t.Errorf("admin b sees %+v", l)
	}

	e.idsInCreate = true // like new-api HEAD
	if got := alice.create(ch{"mode": "batch", "key": "k1\nk2"}); len(got) != 2 {
		t.Errorf("ids from the response = %v", got)
	}
}

func TestMultiKeyChannelIsOneChannelAndListsLikeNewAPI(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	got := alice.create(ch{"mode": "multi_to_single", "multi_key_mode": "polling", "key": "k1\n\nk2\n"})
	if len(got) != 1 {
		t.Fatalf("ids = %v", got)
	}
	c := e.lastCreate().Create
	if c.Mode != "multi_to_single" || c.MultiKeyMode != "polling" || c.Channel.Key != "k1\nk2" {
		t.Errorf("new-api got %+v", c)
	}
	body := alice.want(200, "GET", "/api/channels", nil)
	var raw []map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}
	info, _ := raw[0]["channel_info"].(map[string]any)
	if info["is_multi_key"] != true || info["multi_key_size"] != float64(2) || info["multi_key_mode"] != "polling" {
		t.Errorf("channel_info = %v, want the object new-api's frontend reads", raw[0]["channel_info"])
	}
	if other, _ := raw[0]["other_info"].(string); other != `{"status_reason":"r"}` {
		t.Errorf("other_info = %v, want new-api's string", raw[0]["other_info"])
	}
	for _, field := range []string{"balance", "balance_updated_time", "tag", "test_model", "auto_ban", "base_url", "exists", "deleted_at"} {
		if _, ok := raw[0][field]; !ok {
			t.Errorf("list item has no %q", field)
		}
	}
	if _, ok := raw[0]["owner"]; ok {
		t.Error("portal list shows the owner")
	}
	for _, leak := range []string{"k1", "remark", `"key"`, "sk-partial"} {
		if strings.Contains(body, leak) {
			t.Errorf("list leaks %q: %s", leak, body)
		}
	}
	// The reason new-api disabled a channel can quote the upstream; it stays the JSON the page parses.
	if _, err := e.raw.Exec(`UPDATE channels SET other_info = $1`, `{"status_reason":"Incorrect API key provided: sk-0123456789","status_time":1}`); err != nil { // gitleaks:allow
		t.Fatal(err)
	}
	if body := alice.want(200, "GET", "/api/channels", nil); !strings.Contains(body, `"other_info":"{\"status_reason\":\"Incorrect API key provided: ••••\",\"status_time\":1}"`) {
		t.Errorf("list: %s", body)
	}
	// new-api refuses a balance query on multi-key channels, so it is not asked.
	alice.want(400, "POST", e.channelPath(got[0], "/balance"), nil)
	if n := len(e.callsTo("GET", "/api/channel/update_balance/")); n != 0 {
		t.Errorf("new-api got %d balance queries", n)
	}
	alice.want(400, "POST", "/api/channels", ch{"type": 20, "name": "n", "key": "k", "mode": "multi_to_single", "multi_key_mode": "nope"})
	alice.want(400, "POST", "/api/channels", ch{"type": 20, "name": "n", "key": "k", "mode": "nope"})
	alice.want(400, "POST", "/api/channels", ch{"type": 20, "name": "n", "key": " \n ", "mode": "single"})
}

func TestKeysThatCouldNeverWorkAreRefused(t *testing.T) {
	e := newEnv(t)
	e.template(57, "", false)
	alice := e.asUser(e.alice)
	const codexKey = "{\n  \"access_token\": \"a\",\n  \"account_id\": \"b\"\n}\n"
	created := len(e.callsTo("POST", "/api/channel/"))

	alice.want(400, "POST", "/api/channels", ch{"type": 20, "name": "n", "key": "sk-1\nsk-2", "mode": "single"})
	alice.want(400, "POST", "/api/channels", ch{"type": 57, "name": "n", "key": codexKey, "mode": "batch"})
	alice.want(400, "POST", "/api/channels", ch{"type": 57, "name": "n", "key": codexKey, "mode": "multi_to_single", "multi_key_mode": "random"})
	if n := len(e.callsTo("POST", "/api/channel/")); n != created {
		t.Errorf("refused keys reached new-api: %d calls", n-created)
	}

	alice.create(ch{"type": 57, "key": codexKey})
	if got := e.lastCreate().Create.Channel.Key; got != "{\n\"access_token\": \"a\",\n\"account_id\": \"b\"\n}" {
		t.Errorf("JSON key = %q", got)
	}
}

func TestPortalAuditDetailDoesNotRevealTemplates(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	alice.create(ch{})
	if body := alice.want(200, "GET", "/api/audit-logs", nil); strings.Contains(body, "template") || strings.Contains(body, "admin_id") {
		t.Errorf("portal audit log: %s", body)
	}
}

func TestNoKeyInAuditLog(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	id := alice.create(ch{"key": "sk-secret-key"})[0]
	alice.want(204, "POST", e.channelPath(id, "/status"), ch{"status": 2})
	alice.want(204, "DELETE", e.channelPath(id, ""), nil)
	var n int
	if err := e.raw.QueryRow(`SELECT count(*) FROM audit_logs WHERE detail::text LIKE '%sk-secret%'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("audit rows with the key: %d (%v)", n, err)
	}
	var total int
	e.raw.QueryRow(`SELECT count(*) FROM audit_logs WHERE action IN ('channel.create','channel.status','channel.delete')`).Scan(&total)
	if total != 3 {
		t.Errorf("audit rows = %d, want the 3 portal operations", total)
	}
}

func TestAChannelPurgedWhilePushedIsDeletedAgain(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	id := alice.submit(ch{})[0]
	e.onCreate = func() {
		if err := e.st.DeleteMapping(context.Background(), id); err != nil {
			t.Error(err)
		}
	}
	e.push()
	deleted := e.callsTo("DELETE", "/api/channel/")
	if len(deleted) != 1 {
		t.Fatalf("new-api DELETE calls = %+v, want the channel just made", deleted)
	}
	var n int
	if e.raw.QueryRow(`SELECT count(*) FROM channels`).Scan(&n); n != 0 {
		t.Errorf("%d channels left in new-api", n)
	}
	if n := e.auditCount("channel.create_compensate", true); n != 1 {
		t.Errorf("%d compensation records", n)
	}
}

// new-api HEAD answers with ids; one more than asked for means the answer is
// not to be trusted, and the remark says which channel is ours.
func TestAnAnswerWithStrayIDsFallsBackToTheRemark(t *testing.T) {
	e := newEnv(t)
	e.idsInCreate, e.extraIDs = true, []int{999}
	id := e.asUser(e.alice).create(ch{})[0]
	var remark string
	if err := e.raw.QueryRow(`SELECT remark FROM channels WHERE id = $1`, e.nid(id)).Scan(&remark); err != nil || !strings.HasPrefix(remark, "portal:u") {
		t.Errorf("recorded %d, remark %q (%v)", e.nid(id), remark, err)
	}
}

func TestSoftDeleteKeepsTheChannelInNewAPIAndItsHistory(t *testing.T) {
	e := newEnv(t)
	alice, adminA := e.asUser(e.alice), e.asAdmin(e.a)
	id := alice.create(ch{})[0]
	other := alice.create(ch{})[0]
	path := e.channelPath(id, "")
	if _, err := e.raw.Exec(`INSERT INTO logs (created_at, type, channel_id, model_name, quota, prompt_tokens, completion_tokens,
		use_time, is_stream, content, token_name) VALUES (100, 2, $1, 'm', 5, 1, 2, 3, false, '', 'tok')`, e.nid(id)); err != nil {
		t.Fatal(err)
	}

	alice.want(204, "DELETE", path, nil)
	if del := e.callsTo("DELETE", "/api/channel/"); len(del) != 0 {
		t.Errorf("soft delete called new-api DELETE: %+v", del)
	}
	var status int
	if err := e.raw.QueryRow(`SELECT status FROM channels WHERE id = $1`, e.nid(id)).Scan(&status); err != nil || status != 2 {
		t.Errorf("new-api status = %d (%v), want it kept and disabled", status, err)
	}
	var by string
	e.raw.QueryRow(`SELECT deleted_by FROM portal_channels WHERE newapi_channel_id = $1`, id).Scan(&by)
	if by != "portal:alice" {
		t.Errorf("deleted_by = %q", by)
	}
	// Losing a race with another delete must not overwrite who deleted it.
	if err := e.st.SoftDeleteChannel(context.Background(), id, "admin:late"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second soft delete: %v", err)
	}
	e.raw.QueryRow(`SELECT deleted_by FROM portal_channels WHERE newapi_channel_id = $1`, id).Scan(&by)
	if by != "portal:alice" {
		t.Errorf("deleted_by overwritten: %q", by)
	}

	if l := alice.list(""); !slices.Equal(ids(l), []int{other}) {
		t.Errorf("portal lists %v, want only the other channel", ids(l))
	}
	for _, op := range []string{"/test", "/status", "/balance"} {
		alice.want(404, "POST", e.channelPath(id, op), ch{"status": 1})
		adminA.want(404, "POST", e.channelPath(id, op), ch{"status": 1})
	}
	alice.want(404, "DELETE", path, nil)

	if body := alice.want(200, "GET", "/api/logs?"+logRange+"&channel_id="+strconv.Itoa(id), nil); !strings.Contains(body, `"total":1`) {
		t.Errorf("portal lost the logs of the deleted channel: %s", body)
	}
	if body := alice.want(200, "GET", "/api/stats?"+logRange, nil); !strings.Contains(body, `"quota":5`) {
		t.Errorf("portal lost the stats of the deleted channel: %s", body)
	}

	if l := adminA.list(""); !slices.Equal(ids(l), []int{other}) {
		t.Errorf("active view lists %v", ids(l))
	}
	l := adminA.list("?view=deleted")
	if len(l) != 1 || l[0].ID != id || l[0].DeletedAt == nil || !l[0].Exists {
		t.Errorf("deleted view: %+v", l)
	}
	adminA.want(400, "GET", "/api/channels?view=nope", nil)

	adminA.want(204, "POST", e.channelPath(id, "/restore"), nil)
	adminA.want(409, "POST", e.channelPath(id, "/restore"), nil)
	l = alice.list("?view=deleted") // the portal has no such view
	if !slices.Equal(ids(l), sorted(id, other)) {
		t.Errorf("after restore: %v", ids(l))
	}
	for _, x := range l {
		if x.ID == id && (x.DeletedAt != nil || x.Status != 2) {
			t.Errorf("after restore: %+v, want it active and still disabled", x)
		}
	}
	alice.want(204, "POST", e.channelPath(id, "/status"), ch{"status": 1})
}

// new-api says success=true, data=false and does nothing for a channel its
// memory cache has not learned yet. Deleting then must not hide a channel that
// keeps routing traffic.
func TestStatusNewAPIDidNotApplyIsNotReportedAsDone(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	id := alice.create(ch{})[0]
	e.mu.Lock()
	e.notInCache = true
	e.mu.Unlock()

	alice.want(409, "POST", e.channelPath(id, "/status"), map[string]int{"status": 2})
	alice.want(409, "DELETE", e.channelPath(id, ""), nil)
	var deleted bool
	if err := e.raw.QueryRow(`SELECT deleted_at IS NOT NULL FROM portal_channels WHERE newapi_channel_id = $1`, id).Scan(&deleted); err != nil || deleted {
		t.Errorf("marked deleted = %v (%v), want not deleted while new-api keeps it enabled", deleted, err)
	}

	// Asking for the status it already has is not a failure.
	alice.want(204, "POST", e.channelPath(id, "/status"), map[string]int{"status": 1})
}

func TestPortalCannotRestoreOrPurge(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	id := alice.create(ch{})[0]
	alice.want(404, "POST", e.channelPath(id, "/restore"), nil)
	alice.want(204, "DELETE", e.channelPath(id, "?purge=1"), nil) // just a soft delete
	if l := e.asAdmin(e.a).list("?view=deleted"); len(l) != 1 {
		t.Errorf("record gone: %+v", l)
	}
}

func TestPurgeOnlyRemovesOrphans(t *testing.T) {
	e := newEnv(t)
	alice, adminA := e.asUser(e.alice), e.asAdmin(e.a)
	id := alice.create(ch{"name": "my-channel"})[0]
	purge := e.channelPath(id, "?purge=1")

	adminA.want(409, "DELETE", purge, nil)
	if del := e.callsTo("DELETE", "/api/channel/"); len(del) != 0 {
		t.Errorf("purge called new-api DELETE: %+v", del)
	}

	if _, err := e.raw.Exec(`DELETE FROM channels WHERE id = $1`, e.nid(id)); err != nil { // removed in new-api's panel
		t.Fatal(err)
	}
	l := alice.list("")
	if len(l) != 1 || l[0].Exists || l[0].Name != "my-channel" || string(l[0].ChannelInfo) != "{}" {
		t.Errorf("orphan: %+v", l)
	}
	alice.want(404, "POST", e.channelPath(id, "/test"), nil)
	alice.want(404, "POST", e.channelPath(id, "/status"), ch{"status": 1})
	alice.want(404, "POST", e.channelPath(id, "/balance"), nil)
	alice.want(204, "DELETE", e.channelPath(id, ""), nil) // soft delete works on orphans

	adminA.want(204, "DELETE", purge, nil)
	adminA.want(404, "DELETE", purge, nil)
	if del := e.callsTo("DELETE", "/api/channel/"); len(del) != 0 {
		t.Errorf("purge called new-api DELETE: %+v", del)
	}
}

func TestOperationsReachNewAPIWithTheirParameters(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	id := alice.create(ch{})[0]
	body := alice.want(200, "POST", e.channelPath(id, "/test"), ch{"model": "m 1", "endpoint_type": "openai", "stream": true})
	if !strings.Contains(body, `"success":false`) || !strings.Contains(body, `"message":"upstream 401"`) || !strings.Contains(body, `"time":1.5`) {
		t.Errorf("test result = %s", body)
	}
	cs := e.callsTo("GET", "/api/channel/test/")
	if len(cs) != 1 || cs[0].Query != "endpoint_type=openai&model=m+1&stream=true" {
		t.Errorf("test call = %+v", cs)
	}
	alice.want(200, "POST", e.channelPath(id, "/test"), nil) // the body is optional
	alice.want(200, "POST", e.channelPath(id, "/test"), ch{"model": "pass"})
	if body := alice.want(200, "POST", e.channelPath(id, "/balance"), nil); !strings.Contains(body, `"balance":12.5`) {
		t.Errorf("balance = %s", body)
	}
	alice.want(400, "POST", e.channelPath(id, "/status"), ch{"status": 3})
	alice.want(204, "POST", e.channelPath(id, "/status"), ch{"status": 2})
	var actions []string
	rows, err := e.raw.Query(`SELECT action || ':' || CASE WHEN success THEN 'ok' ELSE 'failed' END FROM audit_logs WHERE actor_type = 'portal' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		rows.Scan(&a)
		actions = append(actions, a)
	}
	// a test the upstream failed answers 200 but is audited as failed
	want := []string{"channel.create:ok", "channel.test:failed", "channel.test:failed", "channel.test:ok", "channel.balance:ok", "channel.status:failed", "channel.status:ok"}
	if !slices.Equal(actions, want) {
		t.Errorf("audited %v, want %v", actions, want)
	}
}
