package newapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Extra holds the new-api channel fields passed through as they are, for the
// channel types that depend on them. It has none of the fields this system
// controls, so it cannot override them. An empty string means not set.
type Extra struct {
	Other              string `json:"other,omitempty"`
	Setting            string `json:"setting,omitempty"`
	Settings           string `json:"settings,omitempty"`
	ModelMapping       string `json:"model_mapping,omitempty"`
	StatusCodeMapping  string `json:"status_code_mapping,omitempty"`
	HeaderOverride     string `json:"header_override,omitempty"`
	ParamOverride      string `json:"param_override,omitempty"`
	AutoBan            *int   `json:"auto_ban,omitempty"`
	Tag                string `json:"tag,omitempty"`
	OpenAIOrganization string `json:"openai_organization,omitempty"`
}

// ParseExtra checks an extra object; none at all counts as empty. Errors are
// client errors and name keys, never values.
func ParseExtra(raw json.RawMessage) (Extra, error) {
	var x Extra
	if len(raw) == 0 {
		return x, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&x); err != nil {
		return x, fmt.Errorf("extra 不合法：%w", err)
	}
	if x.AutoBan != nil && *x.AutoBan != 0 && *x.AutoBan != 1 {
		return x, errors.New("extra.auto_ban 只能为 0 或 1")
	}
	for name, v := range map[string]string{"setting": x.Setting, "settings": x.Settings, "model_mapping": x.ModelMapping,
		"status_code_mapping": x.StatusCodeMapping, "header_override": x.HeaderOverride, "param_override": x.ParamOverride} {
		var obj map[string]any
		if v != "" && (json.Unmarshal([]byte(v), &obj) != nil || obj == nil) {
			return x, fmt.Errorf("extra.%s 须为内容是 JSON 对象的字符串", name)
		}
	}
	return x, nil
}

// MissingOther reports what new-api's form refuses: a channel type that cannot
// work without other, and no other (channel-form.ts).
func (x Extra) MissingOther(channelType int) bool {
	return slices.Contains([]int{3, 18, 21, 39, 41, 49}, channelType) && strings.TrimSpace(x.Other) == ""
}

// PublicExtra is what the form of anyone but a super gets of a template's extra:
// no field that can hold a secret, like request headers, an organization or the
// free-form other and setting. A new-api field goes here only once it is known
// to be safe, so a new one stays hidden by default.
type PublicExtra struct {
	AutoBan           *int   `json:"auto_ban,omitempty"`
	Tag               string `json:"tag,omitempty"`
	ModelMapping      string `json:"model_mapping,omitempty"`
	StatusCodeMapping string `json:"status_code_mapping,omitempty"`
	OtherSet          bool   `json:"other_set"`
	VertexKeyType     string `json:"vertex_key_type,omitempty"` // json or api_key
	AWSKeyType        string `json:"aws_key_type,omitempty"`    // ak_sk or api_key
}

// Public leaves out what PublicExtra does not hold. Of settings it reads only the
// two key types, and only their known values.
func (x Extra) Public() PublicExtra {
	var settings struct {
		Vertex any `json:"vertex_key_type"`
		AWS    any `json:"aws_key_type"`
	}
	_ = json.Unmarshal([]byte(x.Settings), &settings) // unreadable shows as not set; it was checked when saved
	p := PublicExtra{AutoBan: x.AutoBan, Tag: x.Tag, ModelMapping: x.ModelMapping, StatusCodeMapping: x.StatusCodeMapping,
		OtherSet: strings.TrimSpace(x.Other) != ""}
	if settings.Vertex == "json" || settings.Vertex == "api_key" {
		p.VertexKeyType = settings.Vertex.(string)
	}
	if settings.AWS == "ak_sk" || settings.AWS == "api_key" {
		p.AWSKeyType = settings.AWS.(string)
	}
	return p
}

// Keys names the fields that are set, for the audit log, where the values may
// not go: they can hold secrets such as request headers.
func (x Extra) Keys() []string {
	b, _ := json.Marshal(x)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return slices.Sorted(maps.Keys(m))
}
