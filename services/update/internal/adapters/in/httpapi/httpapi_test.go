// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/services/update/internal/identity"
	"abarow.com/services/update/internal/registration"
	"abarow.com/services/update/internal/token"
)

type env struct {
	dev     http.Handler
	adm     http.Handler
	rootPub ed25519.PublicKey
}

func newEnv(t *testing.T) env {
	t.Helper()
	rootPub, rootPriv, _ := ed25519.GenerateKey(nil)
	mPub, mPriv, _ := ed25519.GenerateKey(nil)
	cert, err := token.IssueCert(rootPriv, token.CertInput{HeadquartersDevice: "ABRW-HEADQUARTERS", SigningKey: mPub, NotBefore: time.Now().Add(-time.Hour), Expiry: time.Now().Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := registration.New(registration.Config{
		Store: registration.NewMemory(), Issuer: registration.SigningIssuer{Key: mPriv, ID: "ABRW-HEADQUARTERS", Chain: cert},
		Allowed: []token.Tier{token.Store, token.Register}, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return env{dev: NewDevice(svc), adm: NewAdmin(svc), rootPub: rootPub}
}

func call(h http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	switch b := body.(type) {
	case nil:
		rd = bytes.NewReader(nil)
	case string:
		rd = bytes.NewReader([]byte(b))
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func person(role string) map[string]string {
	h := http.Header{}
	login.Person{Username: "ayse", Name: "Ayşe Yılmaz", Role: role, AllStores: true}.SetHeaders(h)
	return map[string]string{login.UserHeader: h.Get(login.UserHeader), login.PersonHeader: h.Get(login.PersonHeader), login.RoleHeader: h.Get(login.RoleHeader), login.StoresHeader: h.Get(login.StoresHeader)}
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("response could not be decoded: %v: %s", err, rec.Body.String())
	}
	return v
}

func submission(i *identity.Identity, tier token.Tier) SubmitJSON {
	const level, name = token.Prod, "Kasa 1"
	return SubmitJSON{
		Device: i.ID, PublicKey: enc.EncodeToString(i.Public), Fingerprint: string(i.Fingerprint), Tier: string(tier), Level: string(level),
		Store: "store-1", Register: "register-1", Name: name, Signature: enc.EncodeToString(i.Sign(registration.SubmitMessage(i.ID, tier, level, name, nil))),
	}
}

func newIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	i, err := identity.Generate(identity.NewFingerprint(map[string]string{"machine-id": "test"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestFullFlowOverHTTP(t *testing.T) {
	e := newEnv(t)
	i := newIdentity(t)

	if rec := call(e.dev, "POST", "/v1/registrations", submission(i, token.Register), nil); rec.Code != 200 || decodeBody[StatusJSON](t, rec).Status != "pending" {
		t.Fatalf("registration: %d %s", rec.Code, rec.Body)
	}
	// A unit that is not approved cannot get a challenge.
	if rec := call(e.dev, "POST", "/v1/challenge", ChallengeRequest{Device: i.ID}, nil); rec.Code != 409 || decodeBody[ErrorJSON](t, rec).Code != CodePending {
		t.Errorf("challenge without approval: %d %s", rec.Code, rec.Body)
	}

	// The approver sees the list; the list contains no keys or approval code.
	rec := call(e.adm, "GET", "/api/update/registrations", nil, person("admin"))
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	list := decodeBody[ListJSON](t, rec)
	if len(list.Registrations) != 1 || list.Registrations[0].Device != i.ID || list.Registrations[0].Store != "store-1" {
		t.Fatalf("list: %+v", list)
	}
	if strings.Contains(rec.Body.String(), enc.EncodeToString(i.Public)) || strings.Contains(rec.Body.String(), identity.Code(i.ID)) {
		t.Error("the list leaks a key or approval code")
	}

	// Wrong code, right code.
	path := "/api/update/registrations/" + i.ID + "/approve"
	if rec := call(e.adm, "POST", path, ApproveRequest{Code: "AAAA-AAAA"}, person("admin")); rec.Code != 403 || decodeBody[ErrorJSON](t, rec).Code != CodeWrongCode {
		t.Errorf("wrong code: %d %s", rec.Code, rec.Body)
	}
	if rec := call(e.adm, "POST", path, ApproveRequest{Code: identity.Code(i.ID)}, person("admin")); rec.Code != 200 {
		t.Fatalf("approval: %d %s", rec.Code, rec.Body)
	}
	if rec := call(e.dev, "GET", "/v1/registrations/"+i.ID, nil, nil); rec.Code != 200 || decodeBody[StatusJSON](t, rec).Status != "approved" {
		t.Errorf("status: %d %s", rec.Code, rec.Body)
	}

	// Challenge and token.
	nonce := decodeBody[ChallengeResponse](t, call(e.dev, "POST", "/v1/challenge", ChallengeRequest{Device: i.ID}, nil)).Nonce
	tok := call(e.dev, "POST", "/v1/token", TokenRequest{Device: i.ID, Nonce: nonce, Signature: enc.EncodeToString(i.Sign(token.PossessionMessage(i.ID, nonce)))}, nil)
	if tok.Code != 200 {
		t.Fatalf("jeton: %d %s", tok.Code, tok.Body)
	}
	res := decodeBody[TokenResponse](t, tok)
	c, err := token.Verify(e.rootPub, res.Token, time.Now())
	if err != nil || c.Device != i.ID || c.Tier != token.Register {
		t.Fatalf("token could not be verified: %+v, %v", c, err)
	}
	if !res.ExpiresAt.After(time.Now()) || res.Cert != "" {
		t.Errorf("response: %+v", res)
	}

	// After revocation no new challenge is given.
	if rec := call(e.adm, "POST", "/api/update/registrations/"+i.ID+"/revoke", "{}", person("admin")); rec.Code != 200 {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	if rec := call(e.dev, "POST", "/v1/challenge", ChallengeRequest{Device: i.ID}, nil); rec.Code != 403 || decodeBody[ErrorJSON](t, rec).Code != CodeRevoked {
		t.Errorf("after revocation: %d %s", rec.Code, rec.Body)
	}
}

func TestAdminAuth(t *testing.T) {
	e := newEnv(t)
	i := newIdentity(t)
	call(e.dev, "POST", "/v1/registrations", submission(i, token.Register), nil)
	path := "/api/update/registrations/" + i.ID + "/approve"
	ok := ApproveRequest{Code: identity.Code(i.ID)}

	cases := []struct {
		label string
		rec   *httptest.ResponseRecorder
		want  int
	}{
		{"oturumsuz liste", call(e.adm, "GET", "/api/update/registrations", nil, nil), 401},
		{"oturumsuz onay", call(e.adm, "POST", path, ok, nil), 401},
		{"yönetici olmayan liste", call(e.adm, "GET", "/api/update/registrations", nil, person("report")), 403},
		{"yönetici olmayan onay", call(e.adm, "POST", path, ok, person("catalog")), 403},
		{"geçersiz durum", call(e.adm, "GET", "/api/update/registrations?status=all", nil, person("admin")), 400},
	}
	for _, c := range cases {
		if c.rec.Code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.label, c.rec.Code, c.want, c.rec.Body)
		}
	}
	// A request without the headers changed nothing in the registration: it is still pending.
	if rec := call(e.dev, "GET", "/v1/registrations/"+i.ID, nil, nil); decodeBody[StatusJSON](t, rec).Status != "pending" {
		t.Error("an unauthorized request changed the registration")
	}
	// A non-JSON write request (CSRF with a form) is rejected.
	req := httptest.NewRequest("POST", path, strings.NewReader("code=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range person("admin") {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.adm.ServeHTTP(rec, req)
	if rec.Code != 415 {
		t.Errorf("form request: %d, want 415", rec.Code)
	}
}

func TestDeviceInputValidation(t *testing.T) {
	e := newEnv(t)
	i := newIdentity(t)
	good := submission(i, token.Register)

	badSig := good
	badSig.Signature = enc.EncodeToString(make([]byte, 64))
	badKey := good
	badKey.PublicKey = "kisa"
	headquarters := submission(i, token.Headquarters) // this service does not accept the headquarters tier
	long := good
	long.Name = strings.Repeat("a", 20<<10)

	cases := []struct {
		label string
		body  any
		want  int
	}{
		{"bozuk imza", badSig, 400},
		{"bozuk anahtar", badKey, 400},
		{"izinsiz katman", headquarters, 400},
		{"bilinmeyen alan", `{"device":"x","fazladan":1}`, 400},
		{"çöp gövde", `not json`, 400},
		{"fazladan veri", `{"device":"x"}{"device":"y"}`, 400},
		{"çok büyük gövde", long, 400},
	}
	for _, c := range cases {
		if rec := call(e.dev, "POST", "/v1/registrations", c.body, nil); rec.Code != c.want {
			t.Errorf("%s: %d, want %d (%s)", c.label, rec.Code, c.want, rec.Body)
		}
	}
	// A non-JSON content type.
	req := httptest.NewRequest("POST", "/v1/registrations", strings.NewReader("x"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	e.dev.ServeHTTP(rec, req)
	if rec.Code != 415 {
		t.Errorf("text/plain: %d, want 415", rec.Code)
	}
	// Bilinmeyen birim.
	if rec := call(e.dev, "GET", "/v1/registrations/ABRW-YOK", nil, nil); rec.Code != 404 {
		t.Errorf("unknown unit: %d", rec.Code)
	}
	// No invalid request created a registration.
	list := decodeBody[ListJSON](t, call(e.adm, "GET", "/api/update/registrations", nil, person("admin")))
	if len(list.Registrations) != 0 {
		t.Errorf("invalid requests created registrations: %+v", list.Registrations)
	}
}

func TestTokenRequiresValidSignature(t *testing.T) {
	e := newEnv(t)
	i := newIdentity(t)
	call(e.dev, "POST", "/v1/registrations", submission(i, token.Register), nil)
	call(e.adm, "POST", "/api/update/registrations/"+i.ID+"/approve", ApproveRequest{Code: identity.Code(i.ID)}, person("admin"))

	nonce := decodeBody[ChallengeResponse](t, call(e.dev, "POST", "/v1/challenge", ChallengeRequest{Device: i.ID}, nil)).Nonce
	_, malformed, _ := ed25519.GenerateKey(nil)
	rec := call(e.dev, "POST", "/v1/token", TokenRequest{Device: i.ID, Nonce: nonce, Signature: enc.EncodeToString(ed25519.Sign(malformed, token.PossessionMessage(i.ID, nonce)))}, nil)
	if rec.Code != 400 {
		t.Errorf("signature of another key: %d %s", rec.Code, rec.Body)
	}
	// The challenge was spent: even a correct signature no longer passes.
	rec = call(e.dev, "POST", "/v1/token", TokenRequest{Device: i.ID, Nonce: nonce, Signature: enc.EncodeToString(i.Sign(token.PossessionMessage(i.ID, nonce)))}, nil)
	if rec.Code != 400 || decodeBody[ErrorJSON](t, rec).Code != CodeChallenge {
		t.Errorf("spent challenge: %d %s", rec.Code, rec.Body)
	}
}

// An approved unit sends the status report to the device API; the admin sees the running version in the list.
func TestReportOverHTTP(t *testing.T) {
	e := newEnv(t)
	i := newIdentity(t)
	call(e.dev, "POST", "/v1/registrations", submission(i, token.Register), nil)
	payload := []byte(`{"version":"0.4.0","phase":"none","units":{"abarow-sales":"sha256:` + strings.Repeat("a", 64) + `"}}`)
	send := func(ts time.Time, sig []byte) *httptest.ResponseRecorder {
		return call(e.dev, "POST", "/v1/report", ReportRequest{Device: i.ID, Timestamp: ts, Payload: enc.EncodeToString(payload), Signature: enc.EncodeToString(sig)}, nil)
	}
	now := time.Now()
	good := i.Sign(registration.ReportMessage(i.ID, now, payload))

	// A unit that is not approved cannot send a report.
	if rec := send(now, good); rec.Code != 409 || decodeBody[ErrorJSON](t, rec).Code != CodePending {
		t.Fatalf("without approval: %d %s", rec.Code, rec.Body)
	}
	call(e.adm, "POST", "/api/update/registrations/"+i.ID+"/approve", ApproveRequest{Code: identity.Code(i.ID)}, person("admin"))

	if rec := send(now, []byte("sahte-imza")); rec.Code != 400 {
		t.Errorf("invalid signature: %d %s", rec.Code, rec.Body)
	}
	if rec := send(now.Add(-time.Hour), i.Sign(registration.ReportMessage(i.ID, now.Add(-time.Hour), payload))); rec.Code != 400 {
		t.Errorf("old timestamp: %d", rec.Code)
	}
	if rec := send(now, good); rec.Code != 200 {
		t.Fatalf("bildirim: %d %s", rec.Code, rec.Body)
	}
	list := decodeBody[ListJSON](t, call(e.adm, "GET", "/api/update/registrations?status=approved", nil, person("admin")))
	if len(list.Registrations) != 1 || list.Registrations[0].Report == nil || list.Registrations[0].Report.Version != "0.4.0" ||
		list.Registrations[0].Report.Units["abarow-sales"] != "sha256:"+strings.Repeat("a", 64) || list.Registrations[0].Report.At.IsZero() {
		t.Fatalf("list: %+v", list.Registrations)
	}
}
