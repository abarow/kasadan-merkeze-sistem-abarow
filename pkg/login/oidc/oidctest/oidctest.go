// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package oidctest is a fake OpenID Connect provider for the tests of the corporate
// sign-in (pkg/login/oidc): discovery, key set and token endpoint;
// it really checks PKCE. The services' end-to-end tests use it too.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// IdP stands in for a real IdP in tests: discovery, key set
// and token endpoint; it really checks PKCE. Each test asks for its own token
// corruption with a field.
type IdP struct {
	clientID string
	srv      *httptest.Server
	key      *rsa.PrivateKey

	mu    sync.Mutex
	codes map[string]grant
	down  bool

	// Sub, Email, Verified: the identity in the token (Verified can be a bool or text).
	Sub, Email string
	Verified   any
	// Corruptions: if not empty that value is used in the token / the token's signature
	// is made with another key.
	Nonce    string
	Audience string
	Expiry   time.Duration
	BadKey   bool
}

type grant struct{ challenge, nonce string }

func New(t *testing.T, clientID string) *IdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &IdP{clientID: clientID, key: key, codes: map[string]grant{}, Sub: "sub-1", Email: "ayse@firma.example", Verified: true, Expiry: time.Hour}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		if p.isDown() {
			http.Error(w, "kapalı", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/authorize",
			"token_endpoint": p.srv.URL + "/token", "jwks_uri": p.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("POST /token", p.token)
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

// URL, IdP'nin adresidir (issuer).
func (p *IdP) URL() string { return p.srv.URL }

func (p *IdP) isDown() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.down
}

// SetDown closes or opens the IdP (the discovery endpoint returns 503).
func (p *IdP) SetDown(down bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = down
}

// Authorize does the moment the user signs in at the IdP and gives approval: it produces a code
// from the request at the authorization address and returns the return address.
func (p *IdP) Authorize(t *testing.T, authURL string) (callback string, state string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil || !strings.HasPrefix(authURL, p.srv.URL+"/authorize?") {
		t.Fatalf("yetkilendirme adresi: %q (%v)", authURL, err)
	}
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != p.clientID ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" ||
		q.Get("state") == "" || q.Get("nonce") == "" || !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("yetkilendirme isteği eksik: %v", q)
	}
	code := "kod-" + q.Get("state")
	p.mu.Lock()
	p.codes[code] = grant{challenge: q.Get("code_challenge"), nonce: q.Get("nonce")}
	p.mu.Unlock()
	cb, _ := url.Parse(q.Get("redirect_uri"))
	cb.RawQuery = url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
	return cb.String(), q.Get("state")
}

func (p *IdP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	g, ok := p.codes[r.PostForm.Get("code")]
	delete(p.codes, r.PostForm.Get("code")) // a code passes once
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || r.PostForm.Get("grant_type") != "authorization_code" ||
		base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
		return
	}
	nonce := g.nonce
	if p.Nonce != "" {
		nonce = p.Nonce
	}
	aud := p.clientID
	if p.Audience != "" {
		aud = p.Audience
	}
	signer := p.key
	if p.BadKey {
		signer, _ = rsa.GenerateKey(rand.Reader, 2048)
	}
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: signer}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now()
	raw, err := jwt.Signed(sig).Claims(jwt.Claims{
		Issuer: p.srv.URL, Subject: p.Sub, Audience: jwt.Audience{aud},
		IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(p.Expiry)),
	}).Claims(map[string]any{"nonce": nonce, "email": p.Email, "email_verified": p.Verified}).Serialize()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "erisim", "token_type": "Bearer", "id_token": raw})
}
