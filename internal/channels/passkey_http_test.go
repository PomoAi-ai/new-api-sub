package channels_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

// softKey is an authenticator in software: what a browser hands the server.
type softKey struct {
	t      *testing.T
	priv   *ecdsa.PrivateKey
	id     []byte
	rpID   string
	origin string
	handle string
	count  uint32
}

func newSoftKey(t *testing.T, origin string) *softKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	rand.Read(id)
	u, _ := url.Parse(origin)
	return &softKey{t: t, priv: priv, id: id, rpID: u.Hostname(), origin: origin, count: 1}
}

var b64 = base64.RawURLEncoding

func (k *softKey) clientData(typ, challenge string) []byte {
	b, err := json.Marshal(map[string]string{"type": typ, "challenge": challenge, "origin": k.origin})
	if err != nil {
		k.t.Fatal(err)
	}
	return b
}

func (k *softKey) authData(flags byte) []byte {
	rp := sha256.Sum256([]byte(k.rpID))
	return binary.BigEndian.AppendUint32(append(rp[:], flags), k.count)
}

const flagsPresentVerified = 0x05

// create answers navigator.credentials.create.
func (k *softKey) create(challenge string) json.RawMessage {
	k.t.Helper()
	cose, err := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1,
		-2: k.priv.X.FillBytes(make([]byte, 32)), -3: k.priv.Y.FillBytes(make([]byte, 32))})
	if err != nil {
		k.t.Fatal(err)
	}
	auth := k.authData(flagsPresentVerified | 0x40)
	auth = append(auth, make([]byte, 16)...) // AAGUID
	auth = binary.BigEndian.AppendUint16(auth, uint16(len(k.id)))
	auth = append(append(auth, k.id...), cose...)
	att, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": auth})
	if err != nil {
		k.t.Fatal(err)
	}
	return k.credential(map[string]string{
		"clientDataJSON":    b64.EncodeToString(k.clientData("webauthn.create", challenge)),
		"attestationObject": b64.EncodeToString(att),
	})
}

// get answers navigator.credentials.get.
func (k *softKey) get(challenge string, flags byte) json.RawMessage {
	k.t.Helper()
	client := k.clientData("webauthn.get", challenge)
	auth := k.authData(flags)
	sum := sha256.Sum256(client)
	digest := sha256.Sum256(append(append([]byte{}, auth...), sum[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	if err != nil {
		k.t.Fatal(err)
	}
	return k.credential(map[string]string{
		"clientDataJSON":    b64.EncodeToString(client),
		"authenticatorData": b64.EncodeToString(auth),
		"signature":         b64.EncodeToString(sig),
		"userHandle":        b64.EncodeToString([]byte(k.handle)),
	})
}

func (k *softKey) credential(response map[string]string) json.RawMessage {
	b, err := json.Marshal(map[string]any{"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id),
		"type": "public-key", "response": response})
	if err != nil {
		k.t.Fatal(err)
	}
	return b
}

type ceremony struct {
	FlowToken string `json:"flow_token"`
	Options   struct {
		PublicKey struct {
			Challenge        string `json:"challenge"`
			UserVerification string `json:"userVerification"`
			User             struct {
				ID string `json:"id"`
			} `json:"user"` // a registration's only
		} `json:"publicKey"`
	} `json:"options"`
}

// bindPasskey registers k through the real flow.
func (c *caller) bindPasskey(k *softKey) (backup []string) {
	c.e.t.Helper()
	t := c.e.t
	begin := decode[ceremony](t, c.withProof("passkey_bind").want(200, "POST", "/api/me/security/passkey/begin", nil))
	handle, err := b64.DecodeString(begin.Options.PublicKey.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	k.handle = string(handle)
	rec := c.do("POST", "/api/me/security/passkey/finish", ch{"flow_token": begin.FlowToken, "credential": k.create(begin.Options.PublicKey.Challenge)})
	if rec.Code != 200 {
		t.Fatalf("passkey finish: %d %s", rec.Code, rec.Body)
	}
	c.follow(rec)
	return decode[struct {
		Backup []string `json:"backup_codes"`
	}](t, rec.Body.String()).Backup
}

func (c caller) passkeyLogin(k *softKey, flags byte) (status int, cookies int) {
	c.e.t.Helper()
	begin := decode[ceremony](c.e.t, c.anon().want(200, "POST", "/api/auth/passkey/begin", nil))
	if begin.Options.PublicKey.UserVerification != "required" {
		c.e.t.Errorf("user verification %q", begin.Options.PublicKey.UserVerification)
	}
	k.count++
	rec := c.anon().do("POST", "/api/auth/passkey/finish", ch{"flow_token": begin.FlowToken, "credential": k.get(begin.Options.PublicKey.Challenge, flags)})
	return rec.Code, len(rec.Result().Cookies())
}

func TestPasskeyLogsInWithoutAPassword(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("pk1")
	k := newSoftKey(t, testPortalOrigin)
	if backup := c.bindPasskey(k); len(backup) != 10 {
		t.Fatalf("first factor gave %d backup codes", len(backup))
	}
	if status, cookies := c.passkeyLogin(k, flagsPresentVerified); status != 200 || cookies != 1 {
		t.Fatalf("%d %d", status, cookies)
	}
	if e.auditCount("auth.login", true) != 1 || e.auditCount("auth.passkey_bind", true) != 1 {
		t.Error("audit")
	}

	// A password now stops at the second step, and the passkey finishes it.
	if p := c.password("pk1"); len(p.Methods) != 2 || p.Methods[0] != "passkey" {
		t.Errorf("%+v", p)
	}
	if status, _ := c.passkeyLogin(k, flagsPresentVerified); status != 200 {
		t.Error(status)
	}

	// No user verification, no login: a passkey stands in for two factors.
	if status, cookies := c.passkeyLogin(k, 0x01); status != 401 || cookies != 0 {
		t.Errorf("without UV: %d %d", status, cookies)
	}
	// The wrong site's assertion.
	other := *k
	other.origin = "https://evil.example.test"
	if status, _ := c.passkeyLogin(&other, flagsPresentVerified); status != 401 {
		t.Errorf("other origin: %d", status)
	}

	begin := decode[ceremony](t, c.anon().want(200, "POST", "/api/auth/passkey/begin", nil))
	k.count++
	used := ch{"flow_token": begin.FlowToken, "credential": k.get(begin.Options.PublicKey.Challenge, flagsPresentVerified)}
	c.anon().want(200, "POST", "/api/auth/passkey/finish", used)
	c.anon().want(401, "POST", "/api/auth/passkey/finish", used)

	if _, err := e.raw.Exec(`UPDATE portal_users SET status = 2 WHERE id = $1`, c.id); err != nil {
		t.Fatal(err)
	}
	if status, cookies := c.passkeyLogin(k, flagsPresentVerified); status != 401 || cookies != 0 {
		t.Errorf("disabled: %d %d", status, cookies)
	}
}

// On one host name an authenticator keeps one passkey per user handle, so equal
// ids of the two systems must not give equal handles.
func TestPasskeyHandlesDifferBetweenTheSystems(t *testing.T) {
	e := newEnv(t)
	user, admin := e.asUser(e.alice), e.asAdmin(e.super)
	if user.id != admin.id {
		t.Fatalf("ids %d %d", user.id, admin.id)
	}
	handle := func(c caller) string {
		return decode[ceremony](t, c.withProof("passkey_bind").want(200, "POST", "/api/me/security/passkey/begin", nil)).Options.PublicKey.User.ID
	}
	if a, b := handle(user), handle(admin); a == b || a == "" {
		t.Errorf("handles %q %q", a, b)
	}
}

func TestPasskeyCounterGoingBackIsAudited(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("pk2")
	k := newSoftKey(t, testPortalOrigin)
	c.bindPasskey(k)
	k.count = 10
	if status, _ := c.passkeyLogin(k, flagsPresentVerified); status != 200 {
		t.Fatal(status)
	}
	k.count = 3
	if status, _ := c.passkeyLogin(k, flagsPresentVerified); status != 200 {
		t.Fatal(status)
	}
	if e.auditCount("auth.passkey_clone_warning", false) != 1 {
		t.Error("no clone warning")
	}
	k.count = 3
	c.passkeyLogin(k, flagsPresentVerified)
	if e.auditCount("auth.passkey_clone_warning", false) != 1 {
		t.Error("the warning is only for the moment it appears")
	}
}

func TestPasskeyProof(t *testing.T) {
	e := newEnv(t)
	c := e.portalAccount("pk3")
	c.want(400, "POST", "/api/me/security/proof/passkey/begin", ch{"purpose": "token_create"})
	k := newSoftKey(t, testPortalOrigin)
	c.bindPasskey(k)
	c.want(400, "POST", "/api/me/security/proof/passkey/begin", ch{"purpose": "anything"})

	begin := decode[ceremony](t, c.want(200, "POST", "/api/me/security/proof/passkey/begin", ch{"purpose": "token_create"}))
	k.count++
	body := ch{"flow_token": begin.FlowToken, "credential": k.get(begin.Options.PublicKey.Challenge, flagsPresentVerified)}
	e.asUser(e.bobby).want(400, "POST", "/api/me/security/proof/passkey/finish", body)
	p := decode[struct {
		Proof string `json:"proof"`
	}](t, c.want(200, "POST", "/api/me/security/proof/passkey/finish", body))
	c.want(400, "POST", "/api/me/security/proof/passkey/finish", body)
	withProof := c
	withProof.proof = p.Proof
	withProof.want(201, "POST", "/api/tokens", ch{"name": "t"})

	// The second factor gets no backup codes: the first one's are still good.
	setup := decode[struct {
		FlowToken string `json:"flow_token"`
		Secret    string `json:"secret"`
	}](t, c.withProof("totp_bind").want(200, "POST", "/api/me/security/totp/setup", nil))
	if body := c.want(200, "POST", "/api/me/security/totp/confirm", ch{"flow_token": setup.FlowToken, "code": nowCode(t, setup.Secret)}); strings.Contains(body, "backup_codes") {
		t.Errorf("second factor gave codes: %s", body)
	}
}
