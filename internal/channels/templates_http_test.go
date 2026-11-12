package channels_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"newapisub/internal/store"
)

func (e *env) addTemplate(name string, typ int, models string, enabled bool) int64 {
	e.t.Helper()
	id, err := e.st.CreateTemplate(context.Background(), store.Template{Name: name, ChannelType: typ, Models: models,
		Group: "g-" + name, TestModel: strings.Split(models, ",")[0], Extra: json.RawMessage(`{"param_override":"{\"a\":\"secret-param\"}"}`), Enabled: enabled, PoolIDs: []int64{1}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) setTemplateEnabled(id int64, enabled bool) {
	e.t.Helper()
	if _, err := e.raw.Exec(`UPDATE channel_templates SET enabled = $1 WHERE id = $2`, enabled, id); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) firstTemplate() int64 {
	e.t.Helper()
	var id int64
	if err := e.raw.QueryRow(`SELECT id FROM channel_templates WHERE name = 'tpl-20'`).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func TestATypeCanHaveSeveralEnabledTemplates(t *testing.T) {
	e := newEnv(t)
	// A database from before this was allowed loses the index at the next start.
	if _, err := e.raw.Exec(`CREATE UNIQUE INDEX channel_templates_enabled_type_idx ON channel_templates(channel_type) WHERE enabled`); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), e.dsn)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	super := e.asAdmin(e.super)
	tpl := ch{"name": "second", "channel_type": 20, "models": "m", "group": "g", "enabled": true, "test_model": "m", "pool_ids": []int{1}}
	var created store.Template
	json.Unmarshal([]byte(super.want(201, "POST", "/api/templates", tpl)), &created)
	if created.TestModel != "m" {
		t.Errorf("test_model = %q", created.TestModel)
	}
	tpl["enabled"] = false
	path := "/api/templates/" + strconv.FormatInt(created.ID, 10)
	super.want(200, "PUT", path, tpl)
	tpl["enabled"] = true
	super.want(200, "PUT", path, tpl)
	tpl["name"] = "tpl-20"
	if b := super.want(409, "POST", "/api/templates", tpl); !strings.Contains(b, "模板名称已存在") {
		t.Errorf("duplicate name: %s", b)
	}
	if b := super.want(409, "PUT", path, tpl); !strings.Contains(b, "模板名称已存在") {
		t.Errorf("duplicate name: %s", b)
	}
}

func TestTemplateKeepsPassThroughWithParamOverride(t *testing.T) {
	e := newEnv(t)
	super := e.asAdmin(e.super)
	b := super.want(201, "POST", "/api/templates", ch{"name": "routed", "channel_type": 20, "models": "m", "group": "g", "enabled": true, "pool_ids": []int{1}, "extra": ch{
		"setting":        `{"pass_through_body_enabled":true}`,
		"param_override": `{"provider":{"only":["openai"]}}`}})
	if !strings.Contains(b, "pass_through_body_enabled") || !strings.Contains(b, "provider") {
		t.Errorf("both: %s", b)
	}
}

func TestChannelTypesListEveryEnabledTemplateOfAType(t *testing.T) {
	e := newEnv(t)
	second := e.addTemplate("second", 20, "m1,m2", true)
	e.addTemplate("off", 20, "m3", false)
	for name, c := range map[string]caller{"portal": e.asUser(e.alice), "admin": e.asAdmin(e.a)} {
		body := c.want(200, "GET", "/api/channel-types", nil)
		got := decode[[]map[string]any](t, body)
		if len(got) != 2 || got[0]["type"] != 20.0 || got[1]["type"] != 20.0 || got[0]["template_id"] != float64(e.firstTemplate()) ||
			got[1]["template_id"] != float64(second) || got[1]["template_name"] != "second" || got[1]["models"] != "m1,m2" {
			t.Errorf("%s: %s", name, body)
		}
		if strings.Contains(body, "secret") {
			t.Errorf("%s: channel-types holds a template secret: %s", name, body)
		}
	}
}

func TestCreateBindsToTheChosenTemplate(t *testing.T) {
	e := newEnv(t)
	alice, adminA := e.asUser(e.alice), e.asAdmin(e.a)
	first := e.firstTemplate()
	second := e.addTemplate("second", 20, "m1,m2", true)
	other := e.addTemplate("other-type", 1, "m1", true)
	off := e.addTemplate("off", 20, "m1", false)
	body := func(id int64) ch {
		return ch{"type": 20, "name": "n", "key": "sk-secret-key", "mode": "single", "template_id": id}
	}

	for _, c := range []caller{alice, adminA, e.asAdmin(e.super)} {
		if b := c.want(400, "POST", "/api/channels", ch{"type": 20, "name": "n", "key": "sk-secret-key", "mode": "single"}); !strings.Contains(b, "多个模板") {
			t.Errorf("no template: %s", b)
		}
		for _, id := range []int64{other, off, 999999, -1} {
			if b := c.want(400, "POST", "/api/channels", body(id)); !strings.Contains(b, "模板") {
				t.Errorf("template %d: %s", id, b)
			}
		}
	}
	if n := len(e.callsTo("POST", "/api/channel/")); n != 0 {
		t.Errorf("new-api got %d creates", n)
	}

	id := alice.create(ch{"template_id": second})[0]
	if got := e.lastCreate().Create.Channel; got.Models != "m1,m2" || got.Group != "g-second" || got.TestModel != "m1" {
		t.Errorf("new-api got %+v", got)
	}
	list := decode[[]struct {
		ID         int    `json:"id"`
		TemplateID *int64 `json:"template_id"`
	}](t, alice.want(200, "GET", "/api/channels", nil))
	if len(list) != 1 || list[0].ID != id || list[0].TemplateID == nil || *list[0].TemplateID != second {
		t.Errorf("list = %+v", list)
	}
	if a := e.lastAudit("channel.create"); strings.Contains(a, "template_id") {
		t.Errorf("portal users read this audit row: %s", a)
	}
	adminA.create(ch{"template_id": first})
	if got := e.lastCreate().Create.Channel; got.Models != "tpl-a,tpl-b" {
		t.Errorf("new-api got %+v", got)
	}
	if a := e.lastAudit("channel.create"); !strings.Contains(a, `"template_id": `+strconv.FormatInt(first, 10)) {
		t.Errorf("admin audit = %s", a)
	}

	// Back to one: nothing to choose, and choosing it anyway is fine.
	e.setTemplateEnabled(second, false)
	alice.create(ch{})
	alice.create(ch{"template_id": first})
	alice.want(400, "POST", "/api/channels", body(second))
}

func TestEditChoosesModelsFromTheChannelsOwnTemplate(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	second := e.addTemplate("second", 20, "m1,m2", true)
	id := alice.create(ch{"template_id": second})[0]
	path := e.channelPath(id, "")

	alice.want(400, "PUT", path, ch{"name": "n", "models": "tpl-a"}) // the other enabled template's
	alice.want(204, "PUT", path, ch{"name": "n", "models": "m2"})
	if _, models, _, _ := e.channelRow(id); models != "m2" {
		t.Errorf("models = %q", models)
	}

	// Its template stopped: the only other one takes over.
	e.setTemplateEnabled(second, false)
	alice.want(204, "PUT", path, ch{"name": "n", "models": "tpl-a"})

	// With two others there is no telling which: only the name is left.
	third := e.addTemplate("third", 20, "m3", true)
	alice.want(400, "PUT", path, ch{"name": "n", "models": "tpl-b"})
	alice.want(400, "PUT", path, ch{"name": "n", "models": "m3"})
	alice.want(204, "PUT", path, ch{"name": "renamed"})
	alice.want(204, "PUT", path, ch{"name": "renamed", "models": "tpl-a"})

	// No record of a template at all, as for a channel from before templates were kept.
	e.setTemplateEnabled(third, false)
	if _, err := e.raw.Exec(`UPDATE portal_channels SET template_id = NULL WHERE newapi_channel_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	alice.want(204, "PUT", path, ch{"name": "renamed", "models": "tpl-b"})
	e.setTemplateEnabled(third, true)
	alice.want(400, "PUT", path, ch{"name": "renamed", "models": "tpl-a"})
}
