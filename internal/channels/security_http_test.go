package channels_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"

	"newapisub/internal/auth"
	"newapisub/internal/store"
)

const testPassword = "a-good-long-password"

var testPasswordHash = sync.OnceValue(func() string {
	h, err := auth.HashPassword(testPassword)
	if err != nil {
		panic(err)
	}
	return h
})

// portalAccount is a portal user who can really log in, owned by admin a.
func (e *env) portalAccount(name string) caller {
	e.t.Helper()
	id, err := e.st.CreatePortalUser(context.Background(), name, testPasswordHash(), e.a)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.asUser(id)
}

func (e *env) adminAccount(name, role string) caller {
	e.t.Helper()
	id, err := e.st.CreateAdmin(context.Background(), name, testPasswordHash(), role)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.asAdmin(id)
}

// anon is the same system without a session.
func (c caller) anon() caller {
	c.cookie, c.proof = nil, ""
	return c
}

// follow takes the cookie the response set, the way a browser would.
func (c *caller) follow(rec interface{ Result() *http.Response }) {
	c.e.t.Helper()
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		c.e.t.Fatalf("want one cookie, got %v", cookies)
	}
	c.cookie = cookies[0]
}

func codeAt(t *testing.T, secret string, step int64) string {
	t.Helper()
	code, err := hotp.GenerateCodeCustom(secret, uint64(step), hotp.ValidateOpts{Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func nowCode(t *testing.T, secret string) string { return codeAt(t, secret, time.Now().Unix()/30) }

// rewind lets the current code work again, for tests that are not about replay.
func (e *env) rewind() {
	e.t.Helper()
	if _, err := e.raw.Exec(`UPDATE auth_factors SET last_step = 0 WHERE kind = 'totp'`); err != nil {
		e.t.Fatal(err)
	}
}

type bound struct {
	Secret string
	Backup []string
}

// bindTOTP takes an account with no factor through the real flow.
func (c *caller) bindTOTP() bound {
	c.e.t.Helper()
	t := c.e.t
	p := decode[struct {
		Proof string `json:"proof"`
	}](t, c.want(200, "POST", "/api/me/security/proof", ch{"purpose": "totp_bind", "password": testPassword}))
	withProof := *c
	withProof.proof = p.Proof
	s := decode[struct {
		FlowToken string `json:"flow_token"`
		URL       string `json:"otpauth_url"`
		Secret    string `json:"secret"`
	}](t, withProof.want(200, "POST", "/api/me/security/totp/setup", nil))
	u, err := url.Parse(s.URL)
	if err != nil || u.Query().Get("secret") != s.Secret {
		t.Fatalf("otpauth url: %v", err)
	}
	rec := c.do("POST", "/api/me/security/totp/confirm", ch{"flow_token": s.FlowToken, "code": nowCode(t, s.Secret)})
	if rec.Code != 200 {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	c.follow(rec)
	r := decode[struct {
		Backup []string `json:"backup_codes"`
	}](t, rec.Body.String())
	c.e.rewind()
	return bound{s.Secret, r.Backup}
}

type pending struct {
	Required  bool     `json:"mfa_required"`
	FlowToken string   `json:"flow_token"`
	Methods   []string `json:"methods"`
	ExpiresIn int      `json:"expires_in"`
}

// password logs in up to the second step.
func (c caller) password(username string) pending {
	c.e.t.Helper()
	rec := c.anon().do("POST", "/api/auth/login", ch{"username": username, "password": testPassword})
	if rec.Code != 200 {
		c.e.t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	if len(rec.Result().Cookies()) != 0 {
		c.e.t.Fatal("a pending login set a cookie")
	}
	p := decode[pending](c.e.t, rec.Body.String())
	if !p.Required || p.FlowToken == "" {
		c.e.t.Fatalf("not pending: %s", rec.Body)
	}
	return p
}

func (e *env) auditCount(action string, success bool) int {
	e.t.Helper()
	var n int
	if err := e.raw.QueryRow(`SELECT count(*) FROM audit_logs WHERE action = $1 AND success = $2`, action, success).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestNoFactorLoginIsUnchanged(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("nofactor")
	rec := c.anon().do("POST", "/api/auth/login", ch{"username": "nofactor", "password": testPassword})
	if rec.Code != 200 || len(rec.Result().Cookies()) != 1 || strings.Contains(rec.Body.String(), "mfa_required") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if e.auditCount("auth.login", true) != 1 {
		t.Error("no auth.login")
	}
}

func TestTOTPCodeWorksOnce(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("totp1")
	b := c.bindTOTP()
	code := nowCode(t, b.Secret)
	c.anon().want(200, "POST", "/api/auth/login/2fa", ch{"flow_token": c.password("totp1").FlowToken, "code": code})
	if body := c.anon().want(401, "POST", "/api/auth/login/2fa", ch{"flow_token": c.password("totp1").FlowToken, "code": code}); !strings.Contains(body, "验证码无效或已使用") {
		t.Errorf("replay: %s", body)
	}

	// Two logins racing with one code: only one gets in.
	e.rewind()
	code = nowCode(t, b.Secret)
	flows := []string{c.password("totp1").FlowToken, c.password("totp1").FlowToken}
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i, f := range flows {
		wg.Go(func() { codes[i] = c.anon().do("POST", "/api/auth/login/2fa", ch{"flow_token": f, "code": code}).Code })
	}
	wg.Wait()
	if !(codes[0] == 200 && codes[1] == 401 || codes[0] == 401 && codes[1] == 200) {
		t.Errorf("concurrent logins with one code: %v", codes)
	}
}

func TestTOTPIsStoredEncrypted(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("totp2")
	b := c.bindTOTP()
	var sealed []byte
	if err := e.raw.QueryRow(`SELECT secret FROM auth_factors WHERE kind = 'totp'`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(b.Secret)) {
		t.Error("plain secret in the table")
	}
}

func TestBindingNeedsTheFirstCode(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("totp3")
	s := decode[struct {
		FlowToken string `json:"flow_token"`
	}](t, c.withProof("totp_bind").want(200, "POST", "/api/me/security/totp/setup", nil))
	c.want(400, "POST", "/api/me/security/totp/confirm", ch{"flow_token": s.FlowToken, "code": "000000"})
	st, err := c.users.FactorStatus(context.Background(), c.id)
	if err != nil || st.Any() {
		t.Errorf("bound without a code: %+v, %v", st, err)
	}
	// Someone else's setup is nobody else's to confirm.
	e.asUser(e.bobby).want(400, "POST", "/api/me/security/totp/confirm", ch{"flow_token": s.FlowToken, "code": "000000"})
}

func TestProofIsOneUseForOnePurposeOfOneAccount(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	create := func(c caller, status int) {
		t.Helper()
		c.want(status, "POST", "/api/tokens", ch{"name": "t"})
	}
	used := alice.withProof("token_create")
	create(used, 201)
	create(used, 403)
	create(alice.withProof("totp_bind"), 403)

	bobby := e.asUser(e.bobby)
	bobby.proof = alice.withProof("token_create").proof
	create(bobby, 403)

	expired := alice.withProof("token_create")
	if _, err := e.raw.Exec(`UPDATE auth_flows SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	create(expired, 403)

	stale := alice.withProof("token_create")
	if err := alice.users.BumpVersion(context.Background(), alice.id); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	e.portalAuth.Issue(rec, alice.id, 2)
	stale.follow(rec)
	create(stale, 403)
	alice.cookie = stale.cookie
	create(alice.withProof("token_create"), 201)
}

func TestProofUsesFactorsOnceBound(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("proof1")
	// No factor: the password, and nothing else.
	c.want(400, "POST", "/api/me/security/proof", ch{"purpose": "token_create", "code": "123456"})
	c.want(400, "POST", "/api/me/security/proof", ch{"purpose": "token_create", "password": "wrong-password"})
	c.want(400, "POST", "/api/me/security/proof", ch{"purpose": "anything", "password": testPassword})
	c.want(200, "POST", "/api/me/security/proof", ch{"purpose": "token_create", "password": testPassword})

	b := c.bindTOTP()
	c.want(400, "POST", "/api/me/security/proof", ch{"purpose": "token_create", "password": testPassword})
	p := decode[struct {
		Proof     string `json:"proof"`
		ExpiresIn int    `json:"expires_in"`
	}](t, c.want(200, "POST", "/api/me/security/proof", ch{"purpose": "token_create", "code": nowCode(t, b.Secret)}))
	if p.ExpiresIn != 60 {
		t.Errorf("expires_in = %d", p.ExpiresIn)
	}
	// A code that opened a proof cannot open a second one.
	c.want(400, "POST", "/api/me/security/proof", ch{"purpose": "token_create", "code": nowCode(t, b.Secret)})
	// A lost device can still unbind.
	c.want(200, "POST", "/api/me/security/proof", ch{"purpose": "totp_unbind", "backup_code": b.Backup[0]})
	c.want(400, "POST", "/api/me/security/proof", ch{"purpose": "totp_unbind", "backup_code": b.Backup[0]})
}

func TestFlowOfOneSystemIsNothingInTheOther(t *testing.T) {
	e := newEnv(t)
	u := e.portalAccount("realm1")
	u.bindTOTP()
	p := u.password("realm1")
	// The two tables' ids overlap: only the realm tells these accounts apart.
	if _, err := e.raw.Exec(`UPDATE auth_flows SET account_id = $1, session_version = 1`, e.super); err != nil {
		t.Fatal(err)
	}
	body := caller{e: e, mux: e.adminMux}.want(401, "POST", "/api/auth/login/2fa", ch{"flow_token": p.FlowToken, "code": "000000"})
	if !strings.Contains(body, "登录已过期") {
		t.Errorf("the flow was found: %s", body)
	}
}

func TestPendingLoginIsNotASession(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("pend1")
	c.bindTOTP()
	p := c.password("pend1")
	if p.ExpiresIn != 300 || len(p.Methods) != 2 {
		t.Errorf("%+v", p)
	}
	fake := c.anon()
	fake.cookie = &http.Cookie{Name: e.portalAuth.Cookie, Value: p.FlowToken}
	fake.want(401, "GET", "/api/me", nil)
}

func TestSecondStepGate(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("gate1")
	b := c.bindTOTP()
	step2 := func(status int, flow, code string) {
		t.Helper()
		c.anon().want(status, "POST", "/api/auth/login/2fa", ch{"flow_token": flow, "code": code})
	}

	// A mistyped code leaves the login waiting.
	p := c.password("gate1")
	step2(401, p.FlowToken, "000000")
	step2(200, p.FlowToken, nowCode(t, b.Secret))
	e.rewind()
	step2(401, p.FlowToken, nowCode(t, b.Secret)) // and the flow was used up

	p = c.password("gate1")
	if _, err := e.raw.Exec(`UPDATE auth_flows SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	step2(401, p.FlowToken, nowCode(t, b.Secret))

	p = c.password("gate1")
	if _, err := e.raw.Exec(`UPDATE portal_users SET status = 2 WHERE id = $1`, c.id); err != nil {
		t.Fatal(err)
	}
	step2(401, p.FlowToken, nowCode(t, b.Secret))
	if _, err := e.raw.Exec(`UPDATE portal_users SET status = 1 WHERE id = $1`, c.id); err != nil {
		t.Fatal(err)
	}

	// The gate failing must not fall through to a session.
	if _, err := e.raw.Exec(`DROP TABLE auth_factors`); err != nil {
		t.Fatal(err)
	}
	rec := c.anon().do("POST", "/api/auth/login", ch{"username": "gate1", "password": testPassword})
	if rec.Code != 500 || len(rec.Result().Cookies()) != 0 {
		t.Errorf("%d %v", rec.Code, rec.Result().Cookies())
	}
}

func TestAuditOfATwoStepLogin(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("audit1")
	b := c.bindTOTP()
	p := c.password("audit1")
	if e.auditCount("auth.login", true) != 0 || e.auditCount("auth.login_pending", true) != 1 {
		t.Errorf("after the password: login %d, pending %d", e.auditCount("auth.login", true), e.auditCount("auth.login_pending", true))
	}
	wrong := "000000"
	c.anon().want(401, "POST", "/api/auth/login/2fa", ch{"flow_token": p.FlowToken, "code": wrong})
	if e.auditCount("auth.2fa_failed", false) != 1 || e.auditCount("auth.login", true) != 0 {
		t.Error("failed second step")
	}
	code := nowCode(t, b.Secret)
	c.anon().want(200, "POST", "/api/auth/login/2fa", ch{"flow_token": p.FlowToken, "code": code})
	if e.auditCount("auth.login", true) != 1 {
		t.Error("no auth.login after the session was issued")
	}
	if e.auditCount("auth.totp_bind", true) != 1 {
		t.Error("no auth.totp_bind")
	}
	var details string
	if err := e.raw.QueryRow(`SELECT string_agg(detail::text, ' ') FROM audit_logs`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{b.Secret, code, p.FlowToken, b.Backup[0], testPassword} {
		if strings.Contains(details, secret) {
			t.Errorf("audit detail holds %q: %s", secret, details)
		}
	}
}

func TestSecondStepLimit(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("lim1")
	b := c.bindTOTP()

	// Failed logins under a username made to look like the second step's key:
	// shared counts would reach the limit below early.
	for range 3 {
		c.anon().want(401, "POST", "/api/auth/login", ch{"username": "login:" + strconv.FormatInt(c.id, 10), "password": "wrong-password"})
	}
	p := c.password("lim1")
	for range 4 {
		c.anon().want(401, "POST", "/api/auth/login/2fa", ch{"flow_token": p.FlowToken, "code": "000000"})
	}
	// A correct password clears the password's count, not this one.
	p = c.password("lim1")
	c.anon().want(401, "POST", "/api/auth/login/2fa", ch{"flow_token": p.FlowToken, "code": "000000"})
	c.anon().want(429, "POST", "/api/auth/login/2fa", ch{"flow_token": p.FlowToken, "code": nowCode(t, b.Secret)})
	c.anon().want(429, "POST", "/api/auth/login/2fa", ch{"flow_token": c.password("lim1").FlowToken, "backup_code": b.Backup[0]})

	// Re-verification counts on its own.
	for range 5 {
		c.want(400, "POST", "/api/me/security/proof", ch{"purpose": "token_create", "code": "000000"})
	}
	c.want(429, "POST", "/api/me/security/proof", ch{"purpose": "token_create", "code": nowCode(t, b.Secret)})
}

func TestBackupCodeWorksOnceAndAWrongOneKeepsTheLogin(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("back1")
	b := c.bindTOTP()
	if len(b.Backup) != 10 {
		t.Fatalf("%d codes", len(b.Backup))
	}
	p := c.password("back1")
	c.anon().want(401, "POST", "/api/auth/login/2fa", ch{"flow_token": p.FlowToken, "backup_code": "AAAA-AAAA-AAAA-AAAA"})
	c.anon().want(200, "POST", "/api/auth/login/2fa", ch{"flow_token": p.FlowToken, "backup_code": strings.ToLower(b.Backup[0])})
	c.anon().want(401, "POST", "/api/auth/login/2fa", ch{"flow_token": c.password("back1").FlowToken, "backup_code": b.Backup[0]})

	// A dead flow must not eat the code.
	p = c.password("back1")
	if _, err := e.raw.Exec(`UPDATE auth_flows SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	c.anon().want(401, "POST", "/api/auth/login/2fa", ch{"flow_token": p.FlowToken, "backup_code": b.Backup[1]})

	st := decode[struct {
		Left int `json:"backup_codes_left"`
	}](t, c.want(200, "GET", "/api/me/security", nil))
	if st.Left != 9 {
		t.Errorf("left = %d", st.Left)
	}
	var plain int
	if err := e.raw.QueryRow(`SELECT count(*) FROM auth_factors WHERE position($1::bytea in lookup) > 0`, []byte(strings.ReplaceAll(b.Backup[1], "-", ""))).Scan(&plain); err != nil || plain != 0 {
		t.Errorf("plain code stored: %d, %v", plain, err)
	}

	old := b.Backup[1]
	rec := c.withProof("backup_regenerate").do("POST", "/api/me/security/backup-codes", nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	c.follow(rec)
	c.anon().want(401, "POST", "/api/auth/login/2fa", ch{"flow_token": c.password("back1").FlowToken, "backup_code": old})
}

func TestFactorChangeEndsOtherSessionsButNotTokens(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("chg1")
	token := e.asToken(c.id)
	old := c
	b := c.bindTOTP()
	old.want(401, "GET", "/api/me", nil)
	c.want(200, "GET", "/api/me", nil)
	token.want(200, "GET", "/api/channel/", nil)

	// A pending login from before the change is void too.
	p := c.password("chg1")
	old = c
	rec := c.withProof("totp_unbind").do("DELETE", "/api/me/security/totp", nil)
	if rec.Code != 204 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	c.follow(rec)
	old.want(401, "GET", "/api/me", nil)
	c.want(200, "GET", "/api/me", nil)
	token.want(200, "GET", "/api/channel/", nil)
	c.anon().want(401, "POST", "/api/auth/login/2fa", ch{"flow_token": p.FlowToken, "backup_code": b.Backup[0]})

	// The last factor takes the backup codes with it, and login is plain again.
	st, err := c.users.FactorStatus(context.Background(), c.id)
	if err != nil || st.Any() || st.BackupLeft != 0 {
		t.Errorf("%+v %v", st, err)
	}
	c.anon().want(200, "POST", "/api/auth/login", ch{"username": "chg1", "password": testPassword})
	if e.auditCount("auth.totp_unbind", true) != 1 {
		t.Error("no auth.totp_unbind")
	}
}

func TestReset(t *testing.T) {
	e := newEnv(t)
	u := e.portalAccount("reset1") // owned by admin a
	token := e.asToken(u.id)
	u.bindTOTP()
	e.asAdmin(e.b).want(404, "POST", "/api/users/"+strconv.FormatInt(u.id, 10)+"/2fa/reset", nil)

	mfa := func(c caller, path string, id int64) bool {
		t.Helper()
		for _, x := range decode[[]struct {
			ID  int64 `json:"id"`
			MFA bool  `json:"mfa_enabled"`
		}](t, c.want(200, "GET", path, nil)) {
			if x.ID == id {
				return x.MFA
			}
		}
		t.Fatalf("%d not listed", id)
		return false
	}
	if !mfa(e.asAdmin(e.a), "/api/users", u.id) {
		t.Error("list does not show the factor")
	}
	e.asAdmin(e.a).want(204, "POST", "/api/users/"+strconv.FormatInt(u.id, 10)+"/2fa/reset", nil)
	u.want(401, "GET", "/api/me", nil)
	token.want(200, "GET", "/api/channel/", nil)
	if mfa(e.asAdmin(e.a), "/api/users", u.id) {
		t.Error("list still shows the factor")
	}
	u.anon().want(200, "POST", "/api/auth/login", ch{"username": "reset1", "password": testPassword})
	if e.auditCount("user.2fa_reset", true) != 1 {
		t.Error("no user.2fa_reset")
	}

	a := e.adminAccount("reset-admin", store.RoleAdmin)
	a.bindTOTP()
	path := "/api/admins/" + strconv.FormatInt(a.id, 10) + "/2fa/reset"
	e.asAdmin(e.a).want(403, "POST", path, nil)
	if !mfa(e.asAdmin(e.super), "/api/admins", a.id) {
		t.Error("admin list does not show the factor")
	}
	e.asAdmin(e.super).want(204, "POST", path, nil)
	a.want(401, "GET", "/api/me", nil)
	e.asAdmin(e.super).want(400, "POST", "/api/admins/"+strconv.FormatInt(e.super, 10)+"/2fa/reset", nil)
	e.asAdmin(e.super).want(404, "POST", "/api/admins/99999/2fa/reset", nil)
}

func TestDeletingAnAccountTakesItsFactors(t *testing.T) {
	e := newEnv(t)
	u := e.portalAccount("del1")
	u.bindTOTP()
	a := e.adminAccount("del-admin", store.RoleAdmin)
	a.bindTOTP()
	// What an older release runs, knowing nothing of these tables.
	if _, err := e.raw.Exec(`DELETE FROM portal_users WHERE id = $1`, u.id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.raw.Exec(`DELETE FROM admin_users WHERE id = $1`, a.id); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.raw.QueryRow(`SELECT count(*) FROM auth_factors`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d factors left, %v", n, err)
	}
}

func TestCreatingATokenNeedsAProof(t *testing.T) {
	e := newEnv(t)
	alice := e.asUser(e.alice)
	alice.want(403, "POST", "/api/tokens", ch{"name": "t"})
	alice.withProof("totp_bind").want(403, "POST", "/api/tokens", ch{"name": "t"})
	alice.withProof("token_create").want(201, "POST", "/api/tokens", ch{"name": "t"})
	// A bad request is refused without spending the proof.
	p := alice.withProof("token_create")
	p.want(400, "POST", "/api/tokens", ch{"name": ""})
	p.want(201, "POST", "/api/tokens", ch{"name": "t2"})
}

func TestAdminSideOfSecurity(t *testing.T) {
	e := newEnv(t)
	a := e.adminAccount("sec-admin", store.RoleSuper)
	me := decode[struct {
		MFA        bool `json:"mfa_enabled"`
		Restricted bool `json:"restricted"`
	}]
	if m := me(t, a.want(200, "GET", "/api/me", nil)); m.MFA || m.Restricted {
		t.Errorf("%+v", m)
	}
	b := a.bindTOTP()
	if m := me(t, a.want(200, "GET", "/api/me", nil)); !m.MFA {
		t.Errorf("%+v", m)
	}
	var target string
	if err := e.raw.QueryRow(`SELECT target_type FROM audit_logs WHERE action = 'auth.totp_bind'`).Scan(&target); err != nil || target != "admin" {
		t.Errorf("%q %v", target, err)
	}
	a.anon().want(200, "POST", "/api/auth/login/2fa", ch{"flow_token": a.password("sec-admin").FlowToken, "code": nowCode(t, b.Secret)})
}
