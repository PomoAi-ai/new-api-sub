package channels_test

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"testing"
)

// new-api's address is in every transport error. The user gets a fixed text, on
// both APIs and in the audit log they can read; new-api's own refusals stay.
func TestNewAPIAddressNeverReachesAUser(t *testing.T) {
	e := newEnv(t)
	alice, script := e.asUser(e.alice), e.asToken(e.alice)
	id := script.addChannel(oneChannel("sk-secret-key"))[0]
	multi := script.addChannel(ch{"mode": "multi_to_single", "multi_key_mode": "polling", "channel": ch{"type": 20, "name": "m", "key": "sk-one\nsk-two"}})[0]
	addr := e.newAPI.Listener.Addr().String()
	clean := func(where, text string) {
		t.Helper()
		for _, internal := range []string{addr, "127.0.0.1", "dial tcp", "connection refused", "EOF", "duplicate key", "relation"} {
			if strings.Contains(text, internal) {
				t.Errorf("%s: %q in %s", where, internal, text)
			}
		}
	}

	e.mu.Lock()
	e.rejectUpdate = true
	e.mu.Unlock()
	if msg := script.failed("PUT", "/api/channel/", ch{"id": id, "name": "x"}); msg != "refused by new-api" {
		t.Errorf("refusal through the token: %s", msg)
	}
	if body := alice.want(502, "PUT", e.channelPath(id, ""), ch{"name": "x"}); !strings.Contains(body, "refused by new-api") {
		t.Errorf("refusal on the page: %s", body)
	}
	if got := e.lastAudit("channel.update"); !strings.Contains(got, "refused by new-api") {
		t.Errorf("refusal in the audit log: %s", got)
	}

	// The channel is pushed, its record is gone meanwhile, and removing it fails too.
	purged := decode[struct {
		IDs []int `json:"ids"`
	}](t, string(script.ok("POST", "/api/channel/", oneChannel("sk-secret-key")).Data)).IDs[0]
	e.mu.Lock()
	e.dropDelete = true
	e.onCreate = func() { e.st.DeleteMapping(context.Background(), purged) }
	e.mu.Unlock()
	e.push()
	if got := e.lastAudit("channel.create_compensate"); !strings.HasPrefix(got, "false ") || !strings.Contains(got, "new-api 暂时不可用") {
		t.Errorf("failed cleanup audit: %s", got)
	}

	e.newAPI.Close()
	const down = "new-api 暂时不可用"
	const manage = "/api/channel/multi_key/manage"
	channel := fmt.Sprintf("/api/channel/%d", id)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/api/channel/", ch{"id": id, "name": "x"}},
		{"POST", channel + "/status", ch{"status": 2}},
		{"GET", fmt.Sprintf("/api/channel/test/%d", id), nil},
		{"GET", fmt.Sprintf("/api/channel/update_balance/%d", id), nil},
		{"DELETE", channel, nil},
		{"POST", manage, ch{"channel_id": multi, "action": "get_key_status"}},
		{"POST", manage, ch{"channel_id": multi, "action": "disable_key", "key_index": 0}},
	} {
		msg := script.failed(c.method, c.path, c.body)
		clean(c.method+" "+c.path, msg)
		if !strings.Contains(msg, down) {
			t.Errorf("%s %s: %s", c.method, c.path, msg)
		}
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"PUT", e.channelPath(id, ""), ch{"name": "x"}},
		{"POST", e.channelPath(id, "/status"), ch{"status": 2}},
		{"POST", e.channelPath(id, "/test"), nil},
		{"POST", e.channelPath(id, "/balance"), nil},
		{"DELETE", e.channelPath(id, ""), nil},
		{"GET", e.channelPath(multi, "/keys"), nil},
	} {
		body := alice.want(502, c.method, c.path, c.body)
		clean(c.method+" "+c.path, body)
		if !strings.Contains(body, down) {
			t.Errorf("%s %s: %s", c.method, c.path, body)
		}
	}
	// One key at a time is a 200 that reports how far it got.
	if r := decode[switchResp](t, alice.want(200, "POST", e.channelPath(multi, "/keys/disable"), at(0))); !strings.Contains(r.Error, down) {
		t.Errorf("key switch: %+v", r)
	}

	// An internal error is no different in the audit log.
	if _, err := e.raw.Exec(`ALTER TABLE channel_placements RENAME TO placements_gone`); err != nil {
		t.Fatal(err)
	}
	clean("internal error", alice.want(500, "POST", e.channelPath(id, "/status"), ch{"status": 2}))
	if got := e.lastAudit("channel.status"); !strings.Contains(got, "服务器内部错误") {
		t.Errorf("internal error audit: %s", got)
	}

	var rows string
	if err := e.raw.QueryRow(`SELECT string_agg(detail::text, ' ') FROM audit_logs`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	clean("audit_logs", rows)
	body := alice.want(200, "GET", "/api/audit-logs?page_size=200", nil)
	clean("/api/audit-logs", body)
	if !strings.Contains(body, down) || !strings.Contains(body, "refused by new-api") {
		t.Errorf("the audit log lost the reasons: %s", body)
	}
}

func TestRefusalsAreAuditedWithoutTheirValues(t *testing.T) {
	e := newEnv(t)
	script := e.asToken(e.alice)
	id := script.addChannel(oneChannel("sk-secret-key"))[0]
	const manage = "/api/channel/multi_key/manage"
	if msg := script.failed("PUT", "/api/channel/", ch{"id": id, "name": "x", "key": "sk-replacement-key", "base_url": "https://evil.example.com"}); !strings.Contains(msg, "key") || !strings.Contains(msg, "base_url") {
		t.Errorf("refusal names no field: %s", msg)
	}
	for _, action := range []string{"delete_key", "delete_disabled_keys", "enable_all_keys", "disable_all_keys", "sk-replacement-key"} {
		script.failed("POST", manage, ch{"channel_id": id, "action": action, "key_index": 0})
	}

	rows, err := e.raw.Query(`SELECT action, success, target_id, detail::text FROM audit_logs WHERE detail::text LIKE '%rejected_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]string{}
	for rows.Next() {
		var action, target, text string
		var success bool
		if err := rows.Scan(&action, &success, &target, &text); err != nil {
			t.Fatal(err)
		}
		detail := decode[struct {
			Fields  []string `json:"rejected_fields"`
			Action  string   `json:"rejected_action"`
			TokenID int64    `json:"token_id"`
		}](t, text)
		if success || target != strconv.Itoa(id) || detail.TokenID == 0 || strings.Contains(text, "sk-") || strings.Contains(text, "evil") {
			t.Errorf("%s success=%v target=%s: %s", action, success, target, text)
		}
		seen[detail.Action+strings.Join(detail.Fields, ",")] = action
	}
	want := map[string]string{"base_url,key": "channel.update", "delete_key": "channel.update", "delete_disabled_keys": "channel.update",
		"enable_all_keys": "channel.key_enable", "disable_all_keys": "channel.key_disable", "unknown": "channel.update"}
	if !maps.Equal(seen, want) {
		t.Errorf("audited %v, want %v", seen, want)
	}
}
