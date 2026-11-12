package newapi

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestParseExtraRefuses(t *testing.T) {
	for _, raw := range []string{
		// Not an object.
		`[]`, `"x"`, `1`,
		// Not on the whitelist, among them every field this system controls.
		`{"unknown":"x"}`, `{"key":"x"}`, `{"base_url":"x"}`, `{"remark":"x"}`, `{"models":"x"}`, `{"group":"x"}`,
		`{"type":1}`, `{"name":"x"}`, `{"id":1}`, `{"status":2}`, `{"priority":1}`, `{"weight":1}`, `{"test_model":"x"}`,
		// The wrong type, or a value new-api has no meaning for.
		`{"other":1}`, `{"tag":true}`, `{"openai_organization":{}}`, `{"auto_ban":"1"}`, `{"auto_ban":1.5}`,
		`{"auto_ban":2}`, `{"auto_ban":-1}`, `{"model_mapping":{"a":"b"}}`,
		// A string, but not holding a JSON object.
		`{"setting":"{"}`, `{"settings":"[]"}`, `{"model_mapping":"null"}`, `{"status_code_mapping":"1"}`,
		`{"header_override":"\"x\""}`, `{"param_override":"  "}`,
	} {
		if _, err := ParseExtra(json.RawMessage(raw)); err == nil {
			t.Errorf("%s: accepted", raw)
		}
	}
}

func TestExtraIsForwardedUnderNewAPIFieldNames(t *testing.T) {
	raw := `{"other":"o","setting":"{\"a\":1}","settings":"{\"b\":2}","model_mapping":"{\"c\":\"d\"}",
		"status_code_mapping":"{\"400\":\"500\"}","header_override":"{\"X\":\"y\"}","param_override":"{\"e\":3}",
		"auto_ban":0,"tag":"t","openai_organization":"org"}`
	x, err := ParseExtra(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(ChannelInput{Type: 3, Key: "k", Extra: x})
	if err != nil {
		t.Fatal(err)
	}
	var want, got map[string]any
	json.Unmarshal([]byte(raw), &want)
	json.Unmarshal(b, &got)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if got["type"] != 3.0 || got["key"] != "k" {
		t.Errorf("channel = %v", got)
	}
	if keys := x.Keys(); len(keys) != len(want) || !slices.IsSorted(keys) {
		t.Errorf("keys = %v", keys)
	}
}

func TestEmptyExtraFieldsAreNotSet(t *testing.T) {
	for _, raw := range []string{``, `null`, `{}`, `{"other":"","setting":"","settings":"","model_mapping":"",
		"status_code_mapping":"","header_override":"","param_override":"","tag":"","openai_organization":"","auto_ban":null}`} {
		x, err := ParseExtra(json.RawMessage(raw))
		if err != nil {
			t.Errorf("%s: %v", raw, err)
		}
		if b, _ := json.Marshal(x); string(b) != "{}" || len(x.Keys()) != 0 {
			t.Errorf("%s: %s", raw, b)
		}
	}
}

func TestMissingOther(t *testing.T) {
	for _, typ := range []int{3, 18, 21, 39, 41, 49} {
		if !(Extra{}).MissingOther(typ) || !(Extra{Other: " \n"}).MissingOther(typ) || (Extra{Other: "x"}).MissingOther(typ) {
			t.Errorf("type %d needs other", typ)
		}
	}
	for _, typ := range []int{1, 20, 57} {
		if (Extra{}).MissingOther(typ) {
			t.Errorf("type %d does not need other", typ)
		}
	}
}

func TestPublicExtraHoldsNothingThatCanBeASecret(t *testing.T) {
	zero := 0
	x := Extra{Other: "other-secret", Setting: `{"proxy":"setting-secret"}`,
		Settings:       `{"vertex_key_type":"api_key","aws_key_type":"settings-secret","project":"settings-secret"}`,
		HeaderOverride: `{"X":"header-secret"}`, ParamOverride: `{"a":"param-secret"}`, OpenAIOrganization: "org-secret",
		Tag: "t", AutoBan: &zero, ModelMapping: `{"a":"b"}`, StatusCodeMapping: `{"400":"500"}`}
	b, _ := json.Marshal(x.Public())
	const want = `{"auto_ban":0,"tag":"t","model_mapping":"{\"a\":\"b\"}","status_code_mapping":"{\"400\":\"500\"}","other_set":true,"vertex_key_type":"api_key"}`
	if string(b) != want {
		t.Errorf("public extra = %s, want %s", b, want)
	}
	if b, _ := json.Marshal(Extra{Other: " "}.Public()); string(b) != `{"other_set":false}` {
		t.Errorf("empty extra = %s", b)
	}
}
