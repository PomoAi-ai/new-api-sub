package audit

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIP(t *testing.T) {
	behindProxy := Recorder{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("2001:db8::/32")}}
	direct := Recorder{}
	for _, c := range []struct {
		name   string
		rec    Recorder
		remote string
		xff    []string
		want   string
	}{
		{"no proxies configured: the header is ignored", direct, "10.0.0.1:1", []string{"6.6.6.6"}, "10.0.0.1"},
		{"untrusted peer forging the header", behindProxy, "198.51.100.1:1", []string{"6.6.6.6"}, "198.51.100.1"},
		{"trusted proxy names the client", behindProxy, "10.0.0.1:1", []string{"203.0.113.9"}, "203.0.113.9"},
		{"client forging behind a trusted proxy", behindProxy, "10.0.0.1:1", []string{"6.6.6.6, 203.0.113.9"}, "203.0.113.9"},
		{"chain of trusted proxies", behindProxy, "10.0.0.1:1", []string{"6.6.6.6, 203.0.113.9, 10.0.0.2"}, "203.0.113.9"},
		{"several header lines", behindProxy, "10.0.0.1:1", []string{"6.6.6.6", "203.0.113.9, 10.0.0.2"}, "203.0.113.9"},
		{"every hop trusted", behindProxy, "10.0.0.1:1", []string{"10.0.0.2, 10.0.0.3"}, "10.0.0.1"},
		{"no header", behindProxy, "10.0.0.1:1", nil, "10.0.0.1"},
		{"garbage from the proxy", behindProxy, "10.0.0.1:1", []string{"unknown"}, "10.0.0.1"},
		{"mapped IPv4 peer is still the proxy", behindProxy, "[::ffff:10.0.0.1]:1", []string{"203.0.113.9"}, "203.0.113.9"},
		{"mapped IPv4 prints as IPv4", direct, "[::ffff:198.51.100.1]:1", nil, "198.51.100.1"},
		{"IPv6 proxy and client", behindProxy, "[2001:db8::1]:1", []string{"2606:4700::1111"}, "2606:4700::1111"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = c.remote
			for _, v := range c.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := c.rec.ClientIP(r); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}
