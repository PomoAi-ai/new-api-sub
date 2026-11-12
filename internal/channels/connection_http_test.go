package channels_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"newapisub/internal/connection"
	"newapisub/internal/secret"
)

// connectionBody is a form that passes against the fakes.
func (e *env) connectionBody(extra ch) ch {
	body := ch{"base_url": e.newAPI.URL, "pat": "nap_test", "db_dsn": e.dsn}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// saveConnection adds the first pool through the pools page.
func (e *env) saveConnection() {
	e.t.Helper()
	e.asAdmin(e.super).withProof("newapi_config").want(201, "POST", "/api/pools", e.connectionBody(ch{"name": "号池 1"}))
}

func (e *env) storedConnection() (url string, pat string, version int64) {
	e.t.Helper()
	rows, err := e.st.Pools(context.Background())
	if err != nil || len(rows) == 0 {
		e.t.Fatal(rows, err)
	}
	row := rows[0]
	c, err := connection.Decrypt(e.box, row)
	if err != nil {
		e.t.Fatal(err)
	}
	return c.BaseURL, c.PAT, row.Version
}

func dbPassword(t *testing.T, dsn string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := u.User.Password()
	return p
}

type poolResp struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	BaseURL    string `json:"base_url"`
	PATSet     bool   `json:"pat_set"`
	Available  bool   `json:"available"`
	Placements int    `json:"placements"`
	Templates  int    `json:"templates"`
	DB         struct {
		Set  bool   `json:"set"`
		Host string `json:"host"`
	} `json:"db"`
	LogDB struct {
		Set bool `json:"set"`
	} `json:"log_db"`
	Version int64 `json:"version"`
}

func TestSavingAPool(t *testing.T) {
	e := bareEnv(t)
	root := e.asAdmin(e.super)
	if body := root.want(200, "GET", "/api/pools", nil); body != "[]\n" {
		t.Errorf("pools: %s", body)
	}

	// Testing needs no proof, saving does.
	res := decode[struct {
		OK    bool `json:"ok"`
		Steps []struct {
			Name string `json:"name"`
			OK   bool   `json:"ok"`
		} `json:"steps"`
	}](t, root.want(200, "POST", "/api/pools/test", e.connectionBody(ch{"name": "a", "id": 0})))
	if !res.OK || len(res.Steps) != 5 { // and the newest log, looked for among the other pools
		t.Errorf("%+v", res)
	}
	root.want(403, "POST", "/api/pools", e.connectionBody(ch{"name": "a"}))
	root.withProof("token_create").want(403, "POST", "/api/pools", e.connectionBody(ch{"name": "a"}))
	root.withProof("newapi_config").want(400, "POST", "/api/pools", e.connectionBody(nil)) // a pool needs a name

	body := root.withProof("newapi_config").want(201, "POST", "/api/pools", e.connectionBody(ch{"name": "a", "log_dsn": e.dsn + "&application_name=logs"}))
	v := decode[poolResp](t, body)
	if v.ID != 1 || v.BaseURL != e.newAPI.URL || !v.PATSet || !v.DB.Set || !v.LogDB.Set || v.Version != 1 || !strings.Contains(v.DB.Host, "/") || v.Available {
		t.Errorf("%+v", v) // available once the processes restart on it
	}
	password := dbPassword(t, e.dsn)
	for _, secret := range []string{"nap_test", password} {
		if strings.Contains(body, secret) {
			t.Errorf("view shows %q: %s", secret, body)
		}
	}
	var pat, dsn []byte
	if err := e.raw.QueryRow(`SELECT pat, db_dsn FROM newapi_pools`).Scan(&pat, &dsn); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(pat, []byte("nap_test")) || bytes.Contains(dsn, []byte(password)) {
		t.Error("stored in plain text")
	}

	// Empty secrets keep what is stored; only what changes is named.
	root.withProof("newapi_config").want(200, "PUT", "/api/pools/1", ch{"clear_log_dsn": true})
	if _, p, ver := e.storedConnection(); p != "nap_test" || ver != 2 {
		t.Errorf("pat %q version %d", p, ver)
	}
	e.serve()
	pools := decode[[]poolResp](t, e.asAdmin(e.super).want(200, "GET", "/api/pools", nil))
	if len(pools) != 1 || pools[0].LogDB.Set || !pools[0].Available {
		t.Errorf("%+v", pools)
	}
	var detail string
	if err := e.raw.QueryRow(`SELECT detail::text FROM audit_logs WHERE action = 'pool.update' AND success ORDER BY id DESC LIMIT 1`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "log_dsn") || strings.Contains(detail, "\"pat\"") || strings.Contains(detail, password) {
		t.Errorf("audit: %s", detail)
	}
}

func TestAPoolInUseCannotBeDeleted(t *testing.T) {
	e := newEnv(t)
	root := e.asAdmin(e.super)
	e.asUser(e.alice).create(ch{})
	pools := decode[[]poolResp](t, root.want(200, "GET", "/api/pools", nil))
	if len(pools) != 2 || pools[0].Placements != 1 || pools[0].Templates != 1 || pools[1].Placements != 0 || !pools[1].Available {
		t.Errorf("%+v", pools)
	}
	root.withProof("newapi_config").want(409, "DELETE", "/api/pools/1", nil)
	root.want(403, "DELETE", "/api/pools/2", nil)
	root.withProof("newapi_config").want(204, "DELETE", "/api/pools/2", nil)
	root.withProof("newapi_config").want(404, "DELETE", "/api/pools/2", nil)
	// Another pool on the same new-api would count its logs twice.
	if body := root.withProof("newapi_config").want(400, "POST", "/api/pools", e.connectionBody(ch{"name": "again"})); !strings.Contains(body, "号池 1") {
		t.Error(body)
	}
}

func TestOnlyASuperTouchesThePools(t *testing.T) {
	e := newEnv(t)
	regular := e.asAdmin(e.a)
	regular.want(403, "GET", "/api/pools", nil)
	regular.want(403, "POST", "/api/pools/test", e.connectionBody(nil))
	regular.withProof("newapi_config").want(403, "POST", "/api/pools", e.connectionBody(nil))
	regular.withProof("newapi_config").want(403, "PUT", "/api/pools/1", e.connectionBody(nil))
	regular.withProof("newapi_config").want(403, "DELETE", "/api/pools/2", nil)
}

func TestAFailedCheckSavesNothing(t *testing.T) {
	e := bareEnv(t)
	root := e.asAdmin(e.super)
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	if body := root.withProof("newapi_config").want(400, "POST", "/api/pools", e.connectionBody(ch{"name": "a", "base_url": down.URL})); !strings.Contains(body, "校验未通过") {
		t.Error(body)
	}
	if v, err := e.st.PoolsVersion(context.Background()); err != nil || v != "" {
		t.Errorf("saved: %q, %v", v, err)
	}
}

func TestNothingSecretInErrorsAuditOrLog(t *testing.T) {
	e := bareEnv(t)
	root := e.asAdmin(e.super)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	const user, pass = "leakuser", "leak@pa:ss/word"
	for name, body := range map[string]ch{
		"postgres, not a URL":  e.connectionBody(ch{"db_dsn": "postgres://" + user + ":" + pass + "@host:notaport/db"}),
		"postgres, wrong host": e.connectionBody(ch{"db_dsn": "postgres://" + user + ":leakpass@127.0.0.1:1/db?sslmode=disable"}),
		"mysql, not a DSN":     e.connectionBody(ch{"db_dsn": user + ":" + pass + "@tcp(unclosed/db"}),
		"mysql, unreachable":   e.connectionBody(ch{"db_dsn": user + ":leakpass@tcp(127.0.0.1:1)/db"}),
		"key connection":       e.connectionBody(ch{"key_dsn": "postgres://" + user + ":leakpass@127.0.0.1:1/db?sslmode=disable"}),
		"bad PAT":              e.connectionBody(ch{"pat": "leakpass"}),
		"bad address":          e.connectionBody(ch{"base_url": "http://" + user + ":leakpass@host"}),
		"address unreachable":  e.connectionBody(ch{"base_url": "http://127.0.0.1:1/leakuser", "pat": "nap_leakpass"}),
	} {
		t.Run(name, func(t *testing.T) {
			body["name"] = "a"
			tested := root.do("POST", "/api/pools/test", body)
			saved := root.withProof("newapi_config").do("POST", "/api/pools", body)
			if saved.Code == 201 {
				t.Fatal("saved")
			}
			var details string
			if err := e.raw.QueryRow(`SELECT coalesce(string_agg(detail::text, ' '), '') FROM audit_logs`).Scan(&details); err != nil {
				t.Fatal(err)
			}
			for what, text := range map[string]string{"test answer": tested.Body.String(), "save answer": saved.Body.String(), "audit": details, "log": logs.String()} {
				for _, secret := range []string{user, "leakpass", pass, "leak@pa"} {
					if strings.Contains(text, secret) {
						t.Errorf("%s holds %q: %s", what, secret, text)
					}
				}
			}
		})
	}
}

func TestMovingTheAddressNeedsThePATAgain(t *testing.T) {
	e := bareEnv(t)
	e.saveConnection()
	root := e.asAdmin(e.super)
	var hits atomic.Int32
	var auths sync.Map
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		auths.Store(r.Header.Get("Authorization"), true)
		e.fakeNewAPI().ServeHTTP(w, r)
	}))
	t.Cleanup(elsewhere.Close)

	for name, body := range map[string]ch{
		"no PAT":          {"base_url": elsewhere.URL, "confirm_address_change": true},
		"blank PAT":       {"base_url": elsewhere.URL, "pat": "  ", "confirm_address_change": true},
		"no confirmation": {"base_url": elsewhere.URL, "pat": "nap_new"},
	} {
		body["id"] = 1
		root.want(400, "POST", "/api/pools/test", body)
		root.withProof("newapi_config").want(400, "PUT", "/api/pools/1", body)
		if n := hits.Load(); n != 0 {
			t.Fatalf("%s: the new address got %d requests", name, n)
		}
	}

	// A slash more is not a move.
	root.withProof("newapi_config").want(200, "PUT", "/api/pools/1", ch{"base_url": e.newAPI.URL + "/"})

	body := ch{"base_url": elsewhere.URL, "pat": "nap_new", "confirm_address_change": true}
	root.want(200, "POST", "/api/pools/test", ch{"id": 1, "base_url": elsewhere.URL, "pat": "nap_new", "confirm_address_change": true})
	root.withProof("newapi_config").want(200, "PUT", "/api/pools/1", body)
	if u, p, _ := e.storedConnection(); u != elsewhere.URL || p != "nap_new" {
		t.Errorf("%s %s", u, p)
	}
	if _, sent := auths.Load("Bearer nap_test"); sent {
		t.Error("the old PAT went to the new address")
	}
}

func TestKeyConnectionMustBeARoleOfItsOwn(t *testing.T) {
	e := bareEnv(t)
	root := e.asAdmin(e.super)

	root.want(400, "POST", "/api/pools/test", e.connectionBody(ch{"name": "a", "key_dsn": e.dsn}))
	// Not the same string, still the same role: this is what comparing DSNs misses.
	sameRole := e.connectionBody(ch{"name": "a", "key_dsn": e.dsn + "&application_name=keys"})
	if body := root.want(200, "POST", "/api/pools/test", sameRole); !strings.Contains(body, "key 隔离") || !strings.Contains(body, `"ok":false`) {
		t.Error(body)
	}
	if body := root.withProof("newapi_config").want(400, "POST", "/api/pools", sameRole); !strings.Contains(body, "key 隔离") {
		t.Error(body)
	}

	// A main role that cannot read key passes.
	role := "ro_" + strings.ToLower(rand.Text()[:10])
	for _, q := range []string{
		`CREATE ROLE ` + role + ` LOGIN PASSWORD 'ro-pass'`,
		`GRANT USAGE ON SCHEMA ` + currentSchema(t, e) + ` TO ` + role,
		`GRANT SELECT (id, name) ON channels TO ` + role,
		`GRANT SELECT ON logs TO ` + role, // the log pages read them
	} {
		if _, err := e.raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		e.raw.Exec(`DROP OWNED BY ` + role)
		e.raw.Exec(`DROP ROLE ` + role)
	})
	u, err := url.Parse(e.dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(role, "ro-pass")
	root.withProof("newapi_config").want(201, "POST", "/api/pools", e.connectionBody(ch{"name": "a", "db_dsn": u.String(), "key_dsn": e.dsn}))
}

func currentSchema(t *testing.T, e *env) string {
	t.Helper()
	var s string
	if err := e.raw.QueryRow(`SELECT current_schema()`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRestrictedAdmin(t *testing.T) {
	e := bareEnv(t) // no pool stored
	root, regular := e.asAdmin(e.super), e.asAdmin(e.a)

	if body := root.want(200, "GET", "/api/me", nil); !strings.Contains(body, `"restricted":true`) {
		t.Error(body)
	}
	root.want(200, "GET", "/api/settings", nil)
	root.want(200, "GET", "/api/status", nil)
	root.want(200, "GET", "/api/me/security", nil)
	root.want(200, "GET", "/api/pools", nil)
	for _, path := range []string{"/api/users", "/api/admins", "/api/channels", "/api/logs", "/api/audit-logs", "/api/stats", "/api/newapi/models"} {
		root.want(503, "GET", path, nil)
	}
	root.want(503, "POST", "/api/users", ch{})

	regular.want(200, "GET", "/api/me", nil)
	regular.want(403, "GET", "/api/pools", nil)
	regular.want(403, "POST", "/api/pools/test", e.connectionBody(nil))
	regular.withProof("newapi_config").want(403, "POST", "/api/pools", e.connectionBody(nil))

	e.saveConnection()
	e.serve() // the restart the new pool brings
	e.asAdmin(e.super).want(200, "GET", "/api/channels", nil)
}

func setNewAPIEnv(t *testing.T, e *env, baseURL string) {
	t.Helper()
	t.Setenv("NEWAPI_BASE_URL", baseURL)
	t.Setenv("NEWAPI_PAT", "nap_test")
	t.Setenv("NEWAPI_DB_DSN", e.dsn)
	t.Setenv("NEWAPI_LOG_DB_DSN", "")
	t.Setenv("NEWAPI_KEY_DB_DSN", "")
}

func TestImportFromTheEnvironmentHappensOnce(t *testing.T) {
	e := bareEnv(t)
	setNewAPIEnv(t, e, e.newAPI.URL)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, actor := range []string{"portal", "admin"} {
		wg.Go(func() {
			set, _, err := connection.Load(ctx, e.st, e.box, actor, nil)
			errs[i] = err
			if err == nil {
				set.Close()
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := e.raw.QueryRow(`SELECT count(*) FROM newapi_pools`).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("%d rows, %v", rows, err)
	}
	if n := e.auditCount("settings.newapi_import", true); n != 1 {
		t.Errorf("%d import records", n)
	}
	if _, _, v := e.storedConnection(); v != 1 {
		t.Errorf("version %d", v)
	}

	// From here on the environment means nothing.
	setNewAPIEnv(t, e, "http://127.0.0.1:1")
	set, _, err := connection.Load(ctx, e.st, e.box, "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	set.Close()
	if u, _, v := e.storedConnection(); u != e.newAPI.URL || v != 1 {
		t.Errorf("%s version %d", u, v)
	}
}

func TestImportRefusesABadEnvironment(t *testing.T) {
	e := bareEnv(t)
	setNewAPIEnv(t, e, e.newAPI.URL)
	t.Setenv("NEWAPI_PAT", "leakpass")
	_, _, err := connection.Load(context.Background(), e.st, e.box, "admin", nil)
	if err == nil || errors.Is(err, connection.ErrUnavailable) || strings.Contains(err.Error(), "leakpass") {
		t.Errorf("%v", err)
	}
	if v, _ := e.st.PoolsVersion(context.Background()); v != "" {
		t.Errorf("stored version %q", v)
	}
}

func TestUnusableConfigurationIsUnavailable(t *testing.T) {
	e := bareEnv(t)
	setNewAPIEnv(t, e, "")
	t.Setenv("NEWAPI_PAT", "")
	t.Setenv("NEWAPI_DB_DSN", "")
	ctx := context.Background()
	if _, _, err := connection.Load(ctx, e.st, e.box, "admin", nil); !errors.Is(err, connection.ErrUnavailable) {
		t.Errorf("nothing stored: %v", err)
	}

	e.saveConnection()
	other, err := secret.New([]byte(rand.Text() + rand.Text()))
	if err != nil {
		t.Fatal(err)
	}
	if pools, _, err := connection.Load(ctx, e.st, other, "admin", nil); err != nil || pools.Home().Reason == "" {
		t.Errorf("wrong master key: %v", err)
	}

	// After losing the master key the form must carry everything again.
	if _, err := connection.Merge(ctx, e.st, other, 1, connection.Input{}); err == nil {
		t.Error("merged onto a row it cannot read")
	}
	if _, err := connection.Merge(ctx, e.st, other, 1, connection.Input{PAT: "nap_x", DBDSN: e.dsn}); err != nil {
		t.Errorf("full form refused: %v", err)
	}

	e.newAPI.Close()
	if pools, _, err := connection.Load(ctx, e.st, e.box, "admin", nil); err != nil || pools.Home().Reason == "" {
		t.Errorf("new-api down: %v", err)
	}
}

// A save while a process opens the connection must restart it: the version it
// watches is that of the row it opened, not one read after.
func TestLoadReportsTheVersionItOpened(t *testing.T) {
	e := bareEnv(t)
	e.saveConnection()
	var once sync.Once
	e.onStatus = func() {
		once.Do(func() {
			if _, err := e.raw.Exec(`UPDATE newapi_pools SET version = version + 1`); err != nil {
				t.Error(err)
			}
		})
	}
	set, version, err := connection.Load(context.Background(), e.st, e.box, "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	set.Close()
	if _, _, stored := e.storedConnection(); version != "1:1" || stored != 2 {
		t.Errorf("opened version %q, stored %d", version, stored)
	}
}
