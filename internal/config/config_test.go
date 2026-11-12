package config

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func TestTrustedProxies(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", " 10.0.0.0/8, 192.0.2.1 ,2001:db8::/32,,::ffff:198.51.100.1")
	l := &loader{}
	want := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.0.2.1/32"),
		netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("198.51.100.1/32"),
	}
	if got := l.trustedProxies(); !slices.Equal(got, want) || len(l.errs) != 0 {
		t.Errorf("%v, %v", got, l.errs)
	}

	t.Setenv("TRUSTED_PROXIES", "")
	l = &loader{}
	if got := l.trustedProxies(); got != nil || len(l.errs) != 0 {
		t.Errorf("empty: %v, %v", got, l.errs)
	}

	for _, bad := range []string{"proxy.example.com", "10.0.0.0/33", "10.0.0.1, nope"} {
		t.Setenv("TRUSTED_PROXIES", bad)
		l = &loader{}
		if l.trustedProxies(); len(l.errs) != 1 {
			t.Errorf("%q: %v", bad, l.errs)
		}
	}
}

func TestOrigin(t *testing.T) {
	for v, ok := range map[string]bool{
		"https://portal.example.com":      true,
		"https://portal.example.com:8443": true,
		"http://localhost:5181":           true,
		"":                                false,
		"http://portal.example.com":       false,
		"https://portal.example.com/":     false,
		"https://portal.example.com/app":  false,
		"https://portal.example.com?a=1":  false,
		"https://u:p@portal.example.com":  false,
		"https://Portal.example.com":      false,
		"https://192.0.2.1":               false,
		"http://127.0.0.1:5181":           false,
		"portal.example.com":              false,
	} {
		t.Setenv("X_ORIGIN", v)
		l := &loader{}
		l.origin("X_ORIGIN")
		if (len(l.errs) == 0) != ok {
			t.Errorf("%q: %v", v, l.errs)
		}
	}
}

func TestStartRefusesMissingMasterKeyAndOrigin(t *testing.T) {
	t.Setenv("APP_DB_DSN", "postgres://x")
	t.Setenv("COOKIE_SECURE", "false")
	t.Setenv("PORTAL_SESSION_SECRET", strings.Repeat("p", 32))
	t.Setenv("ADMIN_SESSION_SECRET", strings.Repeat("a", 32))
	t.Setenv("APP_MASTER_KEY", strings.Repeat("m", 32))
	t.Setenv("PORTAL_PUBLIC_ORIGIN", "https://portal.example.com")
	t.Setenv("ADMIN_PUBLIC_ORIGIN", "https://admin.example.com")
	if _, err := LoadPortal(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAdmin(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"APP_MASTER_KEY", "PORTAL_PUBLIC_ORIGIN"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "")
			if _, err := LoadPortal(); err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("portal: %v", err)
			}
		})
	}
	t.Setenv("ADMIN_PUBLIC_ORIGIN", "")
	if _, err := LoadAdmin(); err == nil || !strings.Contains(err.Error(), "ADMIN_PUBLIC_ORIGIN") {
		t.Errorf("admin: %v", err)
	}
}

func TestNewAPIValidateDoesNotQuoteValues(t *testing.T) {
	c := NewAPI{BaseURL: "ftp://u:hunter2@host", PAT: "hunter2", DBDSN: "postgres://u:hunter2@h/db", KeyDBDSN: "postgres://u:hunter2@h/db"}
	err := c.Validate(false)
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("%v", err)
	}
	ok := NewAPI{BaseURL: "http://new-api:3000", PAT: "nap_x", DBDSN: "postgres://a", KeyDBDSN: "postgres://b"}
	if err := ok.Validate(false); err != nil {
		t.Error(err)
	}
	same := ok
	same.LogDBDSN, same.KeyDBDSN = "postgres://l", "postgres://l"
	if same.Validate(false) == nil {
		t.Error("key connection equal to the log connection accepted")
	}
}

func TestOnlyASyncingPoolGoesWithoutADatabase(t *testing.T) {
	bare := NewAPI{BaseURL: "http://new-api:3000", PAT: "nap_x"}
	if bare.Validate(false) == nil {
		t.Error("a pool reading its logs directly accepted without a database")
	}
	if err := bare.Validate(true); err != nil {
		t.Error(err)
	}
	keys := bare
	keys.KeyDBDSN = "postgres://k"
	if keys.Validate(true) == nil {
		t.Error("a key connection accepted without the database it is checked against")
	}
}

func TestMongoIsOptionalAndNeverQuoted(t *testing.T) {
	t.Setenv("MONGO_URI", "")
	t.Setenv("MONGO_DB", "")
	l := &loader{}
	if m := l.mongo(); m != (Mongo{DB: "newapisub"}) || len(l.errs) != 0 {
		t.Errorf("unset: %+v, %v", m, l.errs)
	}
	for uri, ok := range map[string]bool{
		"mongodb://app:hunter2@mongo:27017/?authSource=admin": true,
		"mongodb+srv://app:hunter2@cluster.example.com":       true,
		"postgres://app:hunter2@mongo":                        false,
		"mongo:27017":                                         false,
	} {
		t.Setenv("MONGO_URI", uri)
		l = &loader{}
		m := l.mongo()
		if (len(l.errs) == 0) != ok || m.URI != uri {
			t.Errorf("%q: %+v, %v", uri, m, l.errs)
		}
		if err := errors.Join(l.errs...); err != nil && strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%q: error quotes the URI: %v", uri, err)
		}
	}
	t.Setenv("MONGO_URI", "")
	for db, ok := range map[string]bool{"logs_2": true, "a.b": false, "a/b": false, "a$": false, strings.Repeat("x", 64): false} {
		t.Setenv("MONGO_DB", db)
		l = &loader{}
		if m := l.mongo(); (len(l.errs) == 0) != ok || m.DB != db {
			t.Errorf("MONGO_DB %q: %+v, %v", db, m, l.errs)
		}
	}
}
