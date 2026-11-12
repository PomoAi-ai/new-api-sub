package portal

import (
	"net/url"
	"testing"
)

func TestOwnAuditFilterIgnoresClientActor(t *testing.T) {
	v, _ := url.ParseQuery("actor_type=admin&actor_id=99&actor_name=root&action=channel.create&start=10&end=20&p=2&page_size=10")
	f, err := ownAuditFilter(v, 7)
	if err != nil {
		t.Fatal(err)
	}
	if f.ActorType != "portal" || f.ActorID != 7 || f.ActorName != "" {
		t.Errorf("actor not pinned to the caller: %+v", f)
	}
	if f.Action != "channel.create" || f.Start != 10 || f.End != 20 || f.Page != 2 || f.PageSize != 10 {
		t.Errorf("filters lost: %+v", f)
	}
	v, _ = url.ParseQuery("page_size=0")
	if _, err := ownAuditFilter(v, 7); err == nil {
		t.Error("page_size=0 accepted")
	}
}
