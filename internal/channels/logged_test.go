package channels

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A worker log line names the pool, never where its new-api lives.
func TestLoggedHidesTheAddress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:1/api/status", nil)
	_, err := http.DefaultClient.Do(req)
	if err == nil {
		t.Skip("something listens on port 1")
	}
	if s := logged(err); strings.Contains(s, "127.0.0.1") || strings.Contains(s, ":1/") {
		t.Errorf("logged = %q", s)
	}
}
