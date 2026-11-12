package newapi

import (
	"encoding/json"
	"slices"
	"strings"
)

// hiddenOther are the keys of logs.other only a super reads. new-api keeps the
// first lines from a log's owner too (model/log_other.go): the scoped blocks and
// legacySensitiveLogOtherKeys. The last line, with subscription_*, it shows the
// owner (service/log_info_generate.go), but these rows are read across users, and
// how someone pays, down to one subscription's balance, ties their logs together.
var hiddenOther = []string{
	"admin_info", "root_info", "audit_info",
	"channel_id", "channel_name", "channel_type", "reject_reason",
	"billing_source", "billing_preference", "wallet_quota_deducted",
}

// OwnerOther is new-api's formatLogOtherJSON for logOtherVisibilityUser, less
// what ties a log to one end user. The values stay raw, so integers beyond a
// float64's range survive.
func OwnerOther(other string) string {
	if other == "" {
		return ""
	}
	var values map[string]json.RawMessage
	if json.Unmarshal([]byte(other), &values) != nil {
		return "{}"
	}
	changed := false
	for key := range values {
		if slices.Contains(hiddenOther, key) || strings.HasPrefix(key, "subscription_") {
			delete(values, key)
			changed = true
		}
	}
	if !changed {
		return other
	}
	out, err := json.Marshal(values)
	if err != nil {
		return "{}"
	}
	return string(out)
}
