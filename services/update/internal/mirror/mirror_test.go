// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package mirror

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"abarow.com/services/update/internal/token"
)

var now = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func TestPullAllowedByLevel(t *testing.T) {
	cases := []struct {
		level token.Level
		repo  string
		want  bool
	}{
		{token.Prod, "prod/abarow-sales", true},
		{token.Prod, "pilot/abarow-sales", false},
		{token.Prod, "test/abarow-sales", false},
		{token.Pilot, "pilot/abarow-sales", true},
		{token.Pilot, "prod/abarow-sales", true},
		{token.Pilot, "test/abarow-sales", false},
		{token.Test, "test/abarow-sales", true},
		{token.Test, "prod/abarow-sales", false},
		{token.Prod, "prod/", false},
		{token.Prod, "prod", false},
		{token.Prod, "prodx/abarow-sales", false},
		{token.Prod, "prod/../pilot/abarow-sales", false},
		{token.Prod, "/prod/abarow-sales", false},
		{token.Prod, "prod//abarow-sales", false},
		{token.Level("canli"), "prod/abarow-sales", false},
		{token.Level(""), "prod/abarow-sales", false},
	}
	for _, c := range cases {
		if got := PullAllowed(c.level, c.repo); got != c.want {
			t.Errorf("PullAllowed(%q, %q) = %v, istenen %v", c.level, c.repo, got, c.want)
		}
	}
}

type env struct {
	srv      *httptest.Server
	key      *Key
	rootPriv ed25519.PrivateKey
}

func setup(t *testing.T) env {
	t.Helper()
	rootPub, rootPriv, _ := ed25519.GenerateKey(nil)
	key, _, err := GenerateKey(now)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Config{Root: rootPub, Key: key, Service: "zot", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(svc.Handler())
	t.Cleanup(srv.Close)
	return env{srv: srv, key: key, rootPriv: rootPriv}
}

func (o env) token(t *testing.T, level token.Level, exp time.Time, scopes ...string) string {
	t.Helper()
	if len(scopes) == 0 {
		scopes = []string{token.ScopePull}
	}
	raw, err := token.IssueToken(o.rootPriv, token.PublisherIssuer, token.Claims{Device: "ABRW-REGISTER", Tier: token.Register, Level: level, Scopes: scopes, Key: "k", Expiry: exp}, "", now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (o env) get(t *testing.T, password string, params url.Values) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", o.srv.URL+"/token?"+params.Encode(), nil)
	if password != "" {
		req.SetBasicAuth("token", password)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// issued verifies the token in the response with the certificate's public key and returns its content.
func (o env) issued(t *testing.T, resp *http.Response) (claims, map[string]any) {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	raw, _ := body["token"].(string)
	tok, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil {
		t.Fatalf("token could not be decoded: %v", err)
	}
	cb, _ := pem.Decode(o.key.CertPEM)
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	var c claims
	if err := tok.Claims(cert.PublicKey.(*ecdsa.PublicKey), &c); err != nil {
		t.Fatalf("token could not be verified with the certificate: %v", err)
	}
	return c, body
}

func TestTokenGrantsByLevel(t *testing.T) {
	o := setup(t)
	pilot := o.token(t, token.Pilot, now.Add(time.Hour))
	prod := o.token(t, token.Prod, now.Add(time.Hour))

	scopes := url.Values{"service": {"zot"}, "scope": {"repository:prod/abarow-sales:pull", "repository:pilot/abarow-sales:pull", "repository:test/abarow-sales:pull", "repository:prod/abarow-sales:pull,push"}}
	names := func(c claims) []string {
		var out []string
		for _, a := range c.Access {
			out = append(out, a.String())
		}
		return out
	}

	c, body := o.issued(t, o.get(t, pilot, scopes))
	got := names(c)
	want := []string{"repository:prod/abarow-sales:pull", "repository:pilot/abarow-sales:pull", "repository:prod/abarow-sales:pull"}
	if len(got) != len(want) {
		t.Fatalf("pilot access %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pilot access[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if c.Subject != "ABRW-REGISTER" || len(c.Audience) != 1 || c.Audience[0] != "zot" || c.Issuer != "abarow-mirror" {
		t.Errorf("token identity: %+v", c.Claims)
	}
	if exp := c.Expiry.Time(); !exp.Equal(now.Add(5 * time.Minute)) {
		t.Errorf("son kullanma %v", exp)
	}
	if body["expires_in"].(float64) != 300 {
		t.Errorf("expires_in %v", body["expires_in"])
	}

	// The prod level pulls only from the prod repository; push is given to no level.
	c, _ = o.issued(t, o.get(t, prod, scopes))
	if got := names(c); len(got) != 2 || got[0] != "repository:prod/abarow-sales:pull" {
		t.Errorf("prod access %v", got)
	}
	for _, a := range c.Access {
		for _, act := range a.Actions {
			if act != "pull" {
				t.Errorf("permission other than pull was given: %v", a)
			}
		}
	}
}

func TestTokenRejects(t *testing.T) {
	o := setup(t)
	good := url.Values{"service": {"zot"}, "scope": {"repository:prod/abarow-sales:pull"}}
	_, otherPriv, _ := ed25519.GenerateKey(nil)
	otherRoot, _ := token.IssueToken(otherPriv, token.PublisherIssuer, token.Claims{Device: "d", Tier: token.Register, Level: token.Prod, Scopes: []string{token.ScopePull}, Key: "k", Expiry: now.Add(time.Hour)}, "", now.Add(-time.Minute))

	for label, tc := range map[string]struct {
		pass   string
		params url.Values
		want   int
	}{
		"kimliksiz":           {"", good, 401},
		"çöp jeton":           {"abc.def.ghi", good, 401},
		"başka kök":           {otherRoot, good, 401},
		"süresi dolmuş":       {o.token(t, token.Prod, now.Add(-time.Second)), good, 401},
		"pull yetkisiz jeton": {o.token(t, token.Prod, now.Add(time.Hour), "baska"), good, 401},
		"yanlış service":      {o.token(t, token.Prod, now.Add(time.Hour)), url.Values{"service": {"baska"}, "scope": good["scope"]}, 400},
		"service yok":         {o.token(t, token.Prod, now.Add(time.Hour)), url.Values{"scope": good["scope"]}, 400},
	} {
		if resp := o.get(t, tc.pass, tc.params); resp.StatusCode != tc.want {
			t.Errorf("%s: %d, istenen %d", label, resp.StatusCode, tc.want)
		} else if tc.want == 401 && resp.Header.Get("WWW-Authenticate") == "" {
			t.Errorf("%s: no WWW-Authenticate", label)
		}
	}
}

// A request with no scope (the login check) gets a token with empty access using a valid token.
func TestLoginCheckWithoutScope(t *testing.T) {
	o := setup(t)
	resp := o.get(t, o.token(t, token.Prod, now.Add(time.Hour)), url.Values{"service": {"zot"}})
	if resp.StatusCode != 200 {
		t.Fatalf("sign-in check %d", resp.StatusCode)
	}
	c, _ := o.issued(t, resp)
	if len(c.Access) != 0 {
		t.Errorf("access was given on a request with no scope: %v", c.Access)
	}
}

func TestKeyParsing(t *testing.T) {
	k, keyPEM, err := GenerateKey(now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseKey(keyPEM, k.CertPEM); err != nil {
		t.Errorf("the generated key could not be loaded: %v", err)
	}
	other, otherKey, _ := GenerateKey(now)
	_ = other
	if _, err := ParseKey(otherKey, k.CertPEM); err == nil {
		t.Error("another key's certificate was accepted")
	}
	if _, err := ParseKey([]byte("pem değil"), k.CertPEM); err == nil {
		t.Error("bozuk anahtar kabul edildi")
	}
	if _, err := New(Config{}); err == nil {
		t.Error("an empty setting was accepted")
	}
}

// ciSetup builds a token service with a write identity (CI) defined.
func ciSetup(t *testing.T, secret string) env {
	t.Helper()
	rootPub, rootPriv, _ := ed25519.GenerateKey(nil)
	key, _, err := GenerateKey(now)
	if err != nil {
		t.Fatal(err)
	}
	w := &Writer{User: "ci", SHA256: sha256.Sum256([]byte(secret))}
	svc, err := New(Config{Root: rootPub, Key: key, Service: "zot", Now: func() time.Time { return now }, Writer: w})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(svc.Handler())
	t.Cleanup(srv.Close)
	return env{srv: srv, key: key, rootPriv: rootPriv}
}

func (o env) getAs(t *testing.T, user, password string, params url.Values) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", o.srv.URL+"/token?"+params.Encode(), nil)
	req.SetBasicAuth(user, password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// The publisher's CI gets a read and write token for the test/, pilot/ and prod/ paths with its own token.
func TestWriterGetsPushOnChannelPathsOnly(t *testing.T) {
	const secret = "ci-gizli-0123456789abcdef0123456789abcdef"
	o := ciSetup(t, secret)
	scopes := url.Values{"service": {"zot"}, "scope": {
		"repository:test/abarow-x:pull,push", "repository:prod/abarow-x:push", "repository:baska/abarow-x:push",
		"repository:prod/../ok/abarow-x:push", "repository:prod/abarow-x:delete",
	}}
	c, _ := o.issued(t, o.getAs(t, "ci", secret, scopes))
	var got []string
	for _, a := range c.Access {
		got = append(got, a.String())
	}
	want := []string{"repository:test/abarow-x:pull,push", "repository:prod/abarow-x:push"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("CI access %v, want %v", got, want)
	}
	if c.Subject != "ci:ci" {
		t.Errorf("subject %q", c.Subject)
	}
}

func TestWriterRejects(t *testing.T) {
	const secret = "ci-gizli-0123456789abcdef0123456789abcdef"
	o := ciSetup(t, secret)
	good := url.Values{"service": {"zot"}, "scope": {"repository:prod/abarow-x:push"}}
	if resp := o.getAs(t, "ci", "yanlis", good); resp.StatusCode != 401 {
		t.Errorf("wrong CI token: %d", resp.StatusCode)
	}
	// Another username cannot write with the CI secret; it goes through the abarow token route and is rejected.
	if resp := o.getAs(t, "abarow", secret, good); resp.StatusCode != 401 {
		t.Errorf("another user with the CI secret: %d", resp.StatusCode)
	}
	// Even if a unit token is presented with the CI username it is not verified by the write route.
	unit := o.token(t, token.Prod, now.Add(time.Hour))
	if resp := o.getAs(t, "ci", unit, good); resp.StatusCode != 401 {
		t.Errorf("unit token with the CI username: %d", resp.StatusCode)
	}
	// A push is still not given with a unit token (with its own username).
	c, _ := o.issued(t, o.getAs(t, "abarow", unit, url.Values{"service": {"zot"}, "scope": {"repository:prod/abarow-x:pull,push"}}))
	for _, a := range c.Access {
		for _, act := range a.Actions {
			if act != "pull" {
				t.Errorf("permission other than pull for the unit token: %v", a)
			}
		}
	}
	// If no write identity is defined the "ci" user is not special.
	plain := setup(t)
	if resp := plain.getAs(t, "ci", secret, good); resp.StatusCode != 401 {
		t.Errorf("while no write identity is defined: %d", resp.StatusCode)
	}
}

func TestWriteAllowed(t *testing.T) {
	for repo, want := range map[string]bool{
		"test/abarow-x": true, "pilot/abarow-x": true, "prod/abarow-x": true, "prod/a/b": true,
		"baska/abarow-x": false, "prod": false, "prod/": false, "prod/../x": false, "/prod/x": false, "prod//x": false,
	} {
		if got := WriteAllowed(repo); got != want {
			t.Errorf("WriteAllowed(%q) = %v", repo, got)
		}
	}
}
