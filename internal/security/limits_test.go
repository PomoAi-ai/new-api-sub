package security

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"newapisub/internal/audit"
	"newapisub/internal/httpx"
)

func TestAddressLocksAtThirtyFailures(t *testing.T) {
	f := failures{m: map[string]*failure{}}
	for range ipLockAt - 1 {
		f.add("192.0.2.1")
	}
	if _, locked := f.count("192.0.2.1"); locked {
		t.Fatal("locked early")
	}
	f.add("192.0.2.1")
	if n, locked := f.count("192.0.2.1"); !locked || n != ipLockAt {
		t.Errorf("%d %v", n, locked)
	}
	if _, locked := f.count("192.0.2.2"); locked {
		t.Error("another address locked")
	}
}

func TestALockedAddressIsRecordedOncePerWindow(t *testing.T) {
	s := &Service{lockRefusals: window{per: time.Minute}, ipFails: failures{m: map[string]*failure{}}}
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	for range ipLockAt {
		s.ipFails.add(s.Audit.ClientIP(r))
	}
	recorded := 0
	for range 5 {
		ev := &audit.Event{Detail: map[string]any{}}
		var he *httpx.Error
		if err := s.Admit(r, ev, 0, "", ""); !errors.As(err, &he) || he.Status != http.StatusTooManyRequests {
			t.Fatalf("%v", err)
		}
		if !ev.Quiet {
			recorded++
		}
	}
	if recorded != 1 {
		t.Errorf("recorded %d of 5 refusals", recorded)
	}
}
