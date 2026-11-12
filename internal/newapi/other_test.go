package newapi

import "testing"

func TestOwnerOther(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"not json", "{}"},
		{"[1]", "{}"},
		{`{"frt":12}`, `{"frt":12}`},
		{`{"admin_info":{"use_channel":[1]},"root_info":{},"audit_info":{},"channel_id":1,"channel_name":"x","channel_type":2,"reject_reason":"r","frt":12}`, `{"frt":12}`},
		{`{"admin_info":{},"big":12345678901234567890}`, `{"big":12345678901234567890}`},
		// Written by appendBillingInfo, and tying an end user's logs together.
		{`{"billing_source":"subscription","billing_preference":"subscription_first","wallet_quota_deducted":0,` +
			`"subscription_id":7,"subscription_pre_consumed":1,"subscription_post_delta":1,"subscription_plan_id":2,"subscription_plan_title":"Pro",` +
			`"subscription_total":9,"subscription_used":8,"subscription_remain":1,"subscription_consumed":2,"frt":12}`, `{"frt":12}`},
	} {
		if got := OwnerOther(c.in); got != c.want {
			t.Errorf("OwnerOther(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
