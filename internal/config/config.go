// Package config loads process configuration from the environment and fails
// fast on anything missing or malformed.
package config

import (
	"cmp"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strings"
)

// Common is shared by the portal and admin processes.
type Common struct {
	AppDBDSN     string
	MasterKey    []byte
	CookieSecure bool
	// TrustedProxies may state the client address in X-Forwarded-For; none by default.
	TrustedProxies []netip.Prefix
	Mongo          Mongo
}

// Mongo is the store of synced logs. URI is empty when MONGO_URI is unset:
// only pools that sync need it, which the caller checks.
type Mongo struct{ URI, DB string }

type Portal struct {
	Common
	ListenAddr    string
	SessionSecret []byte
	PublicOrigin  string
}

type Admin struct {
	Common
	ListenAddr    string
	SessionSecret []byte
	PublicOrigin  string
	// Required only while admin_users is empty; checked at startup.
	BootstrapUsername string
	BootstrapPassword string
}

// NewAPI is the connection to new-api. It lives in the database; the
// environment only seeds it once.
type NewAPI struct {
	BaseURL  string
	PAT      string
	DBDSN    string
	LogDBDSN string // empty follows DBDSN
	// KeyDBDSN is optional: a role that reads only channels.id and key, for
	// testing multi-key channels' keys. Empty turns that off.
	KeyDBDSN string
}

// NormalizeBaseURL is the form addresses are stored and compared in.
func NormalizeBaseURL(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }

// Validate never quotes a value: its messages reach the settings page and the log.
// A pool that syncs its logs may go without a database, and then without the
// connections that go with one.
func (c NewAPI) Validate(sync bool) error {
	var errs []error
	u, err := url.Parse(c.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		errs = append(errs, errors.New("new-api 地址须为 http(s) 完整地址，且不含用户信息"))
	}
	if !strings.HasPrefix(c.PAT, "nap_") {
		errs = append(errs, errors.New("PAT 须为 new-api 个人访问令牌（nap_ 开头）"))
	}
	switch {
	case c.DBDSN == "" && !sync:
		errs = append(errs, errors.New("主库连接不能为空"))
	case c.DBDSN == "" && (c.LogDBDSN != "" || c.KeyDBDSN != ""):
		errs = append(errs, errors.New("日志库和 key 库须与主库一起配置"))
	}
	if c.KeyDBDSN != "" && (c.KeyDBDSN == c.DBDSN || c.KeyDBDSN == cmp.Or(c.LogDBDSN, c.DBDSN)) {
		errs = append(errs, errors.New("key 库连接须使用独立角色：其他连接不能读取 key"))
	}
	return errors.Join(errs...)
}

// NewAPIFromEnv reads the variables of the one-time import. ok is false when
// none of them is set.
func NewAPIFromEnv() (c NewAPI, ok bool) {
	c = NewAPI{
		BaseURL:  NormalizeBaseURL(os.Getenv("NEWAPI_BASE_URL")),
		PAT:      strings.TrimSpace(os.Getenv("NEWAPI_PAT")),
		DBDSN:    strings.TrimSpace(os.Getenv("NEWAPI_DB_DSN")),
		LogDBDSN: strings.TrimSpace(os.Getenv("NEWAPI_LOG_DB_DSN")),
		KeyDBDSN: strings.TrimSpace(os.Getenv("NEWAPI_KEY_DB_DSN")),
	}
	return c, c != NewAPI{}
}

type loader struct{ errs []error }

func (l *loader) fail(format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf(format, args...))
}

func (l *loader) required(name string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		l.fail("%s is required", name)
	}
	return v
}

func (l *loader) withDefault(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

func (l *loader) secret(name string) []byte {
	v := os.Getenv(name)
	if len(v) < 32 {
		l.fail("%s is required and must be at least 32 bytes", name)
	}
	return []byte(v)
}

// trustedProxies reads a comma-separated list of IPs and CIDRs.
func (l *loader) trustedProxies() []netip.Prefix {
	var out []netip.Prefix
	for _, raw := range strings.Split(os.Getenv("TRUSTED_PROXIES"), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			addr, err := netip.ParseAddr(raw)
			if err != nil {
				l.fail("TRUSTED_PROXIES: %q is not an IP or CIDR", raw)
				continue
			}
			addr = addr.Unmap()
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		out = append(out, prefix.Masked())
	}
	return out
}

// origin reads a browser origin. It comes from the configuration, never from a
// request's Host, because passkeys are bound to it.
func (l *loader) origin(name string) string {
	v := l.required(name)
	if v == "" {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || strings.ToLower(u.Scheme+"://"+u.Host) != v || u.Hostname() == "" {
		l.fail("%s must be a lowercase origin: a scheme and a host, with no path", name)
		return v
	}
	if _, err := netip.ParseAddr(u.Hostname()); err == nil {
		l.fail("%s must name a host: passkeys do not work on an IP address", name)
	}
	if u.Scheme != "https" && (u.Scheme != "http" || u.Hostname() != "localhost") {
		l.fail("%s must be https, or http on localhost", name)
	}
	return v
}

func (l *loader) appDB() string {
	dsn := l.required("APP_DB_DSN")
	if dsn != "" && !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		l.fail("APP_DB_DSN must be a postgres:// or postgresql:// URL")
	}
	return dsn
}

// mongo never quotes MONGO_URI: it holds the password.
func (l *loader) mongo() Mongo {
	m := Mongo{URI: strings.TrimSpace(os.Getenv("MONGO_URI")), DB: l.withDefault("MONGO_DB", "newapisub")}
	if m.URI != "" && !strings.HasPrefix(m.URI, "mongodb://") && !strings.HasPrefix(m.URI, "mongodb+srv://") {
		l.fail("MONGO_URI must be a mongodb:// or mongodb+srv:// URL")
	}
	if len(m.DB) > 63 || strings.ContainsAny(m.DB, `/\. "$`) {
		l.fail("MONGO_DB must be a MongoDB database name: at most 63 bytes, none of / \\ . space \" $")
	}
	return m
}

func (l *loader) common() Common {
	c := Common{
		AppDBDSN:       l.appDB(),
		MasterKey:      l.secret("APP_MASTER_KEY"),
		TrustedProxies: l.trustedProxies(),
		Mongo:          l.mongo(),
	}

	switch v := l.required("COOKIE_SECURE"); v {
	case "true":
		c.CookieSecure = true
	case "false", "":
	default:
		l.fail("COOKIE_SECURE must be true or false, got %q", v)
	}
	return c
}

func LoadPortal() (Portal, error) {
	l := &loader{}
	cfg := Portal{
		Common:        l.common(),
		ListenAddr:    l.withDefault("PORTAL_LISTEN_ADDR", ":8081"),
		SessionSecret: l.secret("PORTAL_SESSION_SECRET"),
		PublicOrigin:  l.origin("PORTAL_PUBLIC_ORIGIN"),
	}
	return cfg, errors.Join(l.errs...)
}

func LoadAdmin() (Admin, error) {
	l := &loader{}
	cfg := Admin{
		Common:            l.common(),
		ListenAddr:        l.withDefault("ADMIN_LISTEN_ADDR", ":8082"),
		SessionSecret:     l.secret("ADMIN_SESSION_SECRET"),
		PublicOrigin:      l.origin("ADMIN_PUBLIC_ORIGIN"),
		BootstrapUsername: strings.TrimSpace(os.Getenv("ADMIN_BOOTSTRAP_USERNAME")),
		BootstrapPassword: os.Getenv("ADMIN_BOOTSTRAP_PASSWORD"),
	}
	if p := os.Getenv("PORTAL_SESSION_SECRET"); p != "" && p == string(cfg.SessionSecret) {
		l.fail("ADMIN_SESSION_SECRET must differ from PORTAL_SESSION_SECRET")
	}
	return cfg, errors.Join(l.errs...)
}

// LoadAppDB is for commands that need the database and nothing else.
func LoadAppDB() (string, error) {
	l := &loader{}
	dsn := l.appDB()
	return dsn, errors.Join(l.errs...)
}
