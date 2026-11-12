package channels_test

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// azure is a template of a type that cannot work without other.
func azure(enabled bool, extra any) ch {
	return ch{"name": "azure", "channel_type": 3, "base_url": "https://azure.example.com", "models": "m", "group": "g",
		"enabled": enabled, "extra": extra, "pool_ids": []int{1}}
}

func TestTemplateExtraReachesNewAPIButOnlySupersReadIt(t *testing.T) {
	e := newEnv(t)
	super := e.asAdmin(e.super)
	super.want(201, "POST", "/api/templates", azure(true, ch{"other": "secret-other", "tag": "tpl-tag", "auto_ban": 0,
		"param_override": `{"a":"secret-param"}`, "header_override": `{"X":"secret-header"}`, "setting": `{"proxy":"secret-setting"}`,
		"openai_organization": "secret-org", "settings": `{"aws_key_type":"api_key","project":"secret-settings"}`,
		"model_mapping": `{"a":"b"}`, "status_code_mapping": `{"400":"500"}`}))
	forged := ch{"type": 3, "status": 2, "extra": ch{"other": "forged", "tag": "forged", "auto_ban": 1, "key": "sk-forged", "unknown": 1}}
	callers := map[string]caller{"portal": e.asUser(e.alice), "admin": e.asAdmin(e.a), "super": super}
	for name, c := range callers {
		c.create(forged)
		got := e.lastCreate().Create.Channel
		if got.Other != "secret-other" || got.Tag != "tpl-tag" || got.AutoBan == nil || *got.AutoBan != 0 ||
			got.ParamOverride != `{"a":"secret-param"}` || got.HeaderOverride != `{"X":"secret-header"}` ||
			got.Setting != `{"proxy":"secret-setting"}` || got.OpenAIOrganization != "secret-org" ||
			got.Settings != `{"aws_key_type":"api_key","project":"secret-settings"}` {
			t.Errorf("%s: new-api got %+v", name, got)
		}
		if got.Status != 1 || got.Key != "sk-secret-key" {
			t.Errorf("%s: status=%d key=%q", name, got.Status, got.Key)
		}

		body := c.want(200, "GET", "/api/channel-types", nil)
		var types []map[string]any
		json.Unmarshal([]byte(body), &types)
		i := slices.IndexFunc(types, func(x map[string]any) bool { return x["type"] == 3.0 })
		if i < 0 {
			t.Fatalf("%s: channel-types = %v", name, types)
		}
		extra, _ := types[i]["extra"].(map[string]any)
		if name == "super" {
			if extra["header_override"] != `{"X":"secret-header"}` || extra["other"] != "secret-other" {
				t.Errorf("super: extra = %v", extra)
			}
			continue
		}
		if strings.Contains(body, "secret") {
			t.Errorf("%s: channel-types holds a template secret: %s", name, body)
		}
		if !slices.Equal(slices.Sorted(maps.Keys(extra)), []string{"auto_ban", "aws_key_type", "model_mapping", "other_set", "status_code_mapping", "tag"}) ||
			extra["other_set"] != true || extra["aws_key_type"] != "api_key" || extra["tag"] != "tpl-tag" {
			t.Errorf("%s: extra = %v", name, extra)
		}
	}
}

func TestExtraIsCheckedWhereverItIsTaken(t *testing.T) {
	e := newEnv(t)
	super := e.asAdmin(e.super)
	var tpl struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal([]byte(super.want(201, "POST", "/api/templates", azure(false, nil))), &tpl)
	path := "/api/templates/" + strconv.FormatInt(tpl.ID, 10)
	for _, bad := range []any{ch{"key": "sk-x"}, ch{"unknown": "x"}, ch{"tag": 1}, ch{"auto_ban": 2}, ch{"param_override": "{"}, "x", []string{"tag"}} {
		template := azure(false, bad)
		template["name"] = "bad"
		super.want(400, "POST", "/api/templates", template)
		super.want(400, "PUT", path, template)
	}
}

func TestOtherIsRequiredWhereNewAPIRequiresIt(t *testing.T) {
	e := newEnv(t)
	super := e.asAdmin(e.super)
	for _, extra := range []any{nil, ch{}, ch{"other": ""}, ch{"other": " \n"}, ch{"tag": "t"}} {
		if body := super.want(400, "POST", "/api/templates", azure(true, extra)); !strings.Contains(body, "该类型需要额外配置") {
			t.Errorf("%v: %s", extra, body)
		}
	}
	// A disabled one is a draft, until it gets enabled.
	var tpl struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal([]byte(super.want(201, "POST", "/api/templates", azure(false, nil))), &tpl)
	path := "/api/templates/" + strconv.FormatInt(tpl.ID, 10)
	super.want(400, "PUT", path, azure(true, nil))
	if body := super.want(200, "PUT", path, azure(true, ch{"other": "2024-02-01", "tag": "t"})); !strings.Contains(body, "2024-02-01") {
		t.Errorf("updated template = %s", body)
	}
	// An update replaces it: what is left out is gone.
	if body := super.want(200, "PUT", path, azure(true, ch{"other": "2024-02-01"})); strings.Contains(body, `"tag"`) {
		t.Errorf("updated template = %s", body)
	}
	// Moving an enabled template to such a type counts too.
	super.want(400, "PUT", "/api/templates/1", ch{"name": "tpl-20", "channel_type": 18, "models": "m", "group": "g", "enabled": true})
}

func TestExtraValuesStayOutOfTheAuditLog(t *testing.T) {
	e := newEnv(t)
	super := e.asAdmin(e.super)
	var tpl struct {
		ID int64 `json:"id"`
	}
	template := azure(true, ch{"other": "v1", "header_override": `{"X-Secret":"hdr-secret-1"}`})
	json.Unmarshal([]byte(super.want(201, "POST", "/api/templates", template)), &tpl)
	path := "/api/templates/" + strconv.FormatInt(tpl.ID, 10)
	template["extra"] = ch{"other": "v1", "header_override": `{"X-Secret":"hdr-secret-2"}`}
	super.want(200, "PUT", path, template)
	// A failed update is audited as well.
	template["name"] = "tpl-20"
	super.want(409, "PUT", path, template)
	super.create(ch{"type": 3})

	var leaked int
	if err := e.raw.QueryRow(`SELECT count(*) FROM audit_logs WHERE detail::text LIKE '%hdr-secret%' OR detail::text LIKE '%v1%'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Errorf("audit rows with an extra value: %d (%v)", leaked, err)
	}

	var update struct {
		Changes struct {
			Extra *struct {
				To struct {
					Keys []string `json:"keys"`
				} `json:"to"`
			} `json:"extra"`
		} `json:"changes"`
	}
	var raw string
	e.raw.QueryRow(`SELECT detail::text FROM audit_logs WHERE action = 'template.update' AND success`).Scan(&raw)
	json.Unmarshal([]byte(raw), &update)
	// The same names with another value still shows up as a change.
	if update.Changes.Extra == nil || !slices.Equal(update.Changes.Extra.To.Keys, []string{"header_override", "other"}) {
		t.Errorf("template.update detail = %s", raw)
	}
}

func TestVertexTemplateNeedsRegionNewAPIAccepts(t *testing.T) {
	e := newEnv(t)
	super := e.asAdmin(e.super)
	vertex := func(other string) ch {
		body := azure(true, ch{"other": other})
		body["channel_type"] = 41
		return body
	}
	for _, other := range []string{"us-central1", `{"region":"us-central1"}`} {
		super.want(400, "POST", "/api/templates", vertex(other))
	}
	super.want(201, "POST", "/api/templates", vertex(`{"default":"us-central1"}`))
}
