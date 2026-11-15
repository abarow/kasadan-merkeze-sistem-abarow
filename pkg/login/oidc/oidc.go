// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package oidc is the OpenID Connect adapter plugged into the sign-in core (pkg/login):
// the authorization code flow, PKCE, state and nonce, the identity token's
// signature/aud/exp/iss check.
//
// The adapter only proves the identity: what the IdP says (sub, email) is given
// to the service's Login function; it links that identity to the person in abarow
// and reads the role and store permission from its own record. No IdP claim
// grants authority.
package oidc

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"abarow.com/pkg/login"
)

const (
	// StartPath is the endpoint that redirects the browser to the IdP; it is the address of the
	// button in the sign-in method list.
	StartPath = "/api/auth/oidc/start"
	// CallbackPath is the endpoint the IdP brings the user back to; it is the path of the
	// redirect address registered with the IdP.
	CallbackPath = "/api/auth/oidc/callback"
	// ErrorParam is the query parameter added to the screen's address when the flow
	// fails; its value is one of the codes below. It carries no message:
	// the text in the address is not shown on the screen, the screen writes its own text
	// according to the code.
	ErrorParam = "oidc_error"

	// CodeDenied: the user refused the sign-in at the IdP or the IdP returned an error.
	CodeDenied = "denied"
	// CodeExpired: the sign-in attempt took too long or was not started in this
	// browser (no cookie, the state does not match).
	CodeExpired = "expired"
	// CodeRefused: the identity is right but there is no open account linked to it in abarow.
	CodeRefused = "refused"
	// CodeUnavailable: the IdP or abarow's records could not be reached.
	CodeUnavailable = "unavailable"
	// CodeTaken: the corporate identity to be linked is linked to another person.
	CodeTaken = "taken"
	// LinkedParam is the query parameter added to the screen's address when identity
	// linking succeeds (its value is 1).
	LinkedParam = "oidc_linked"

	flowCookie = "abarow_oidc"
	flowTTL    = 10 * time.Minute
)

// Config is the OIDC provider's settings.
type Config struct {
	// Issuer is the IdP's address (discovery is under this address).
	Issuer   string
	ClientID string
	// ClientSecret is the confidential client's secret; empty for an open client with PKCE.
	ClientSecret string
	// RedirectURL is the return address registered with the IdP; its path must be
	// CallbackPath.
	RedirectURL string
	// Label is the text of the button on the sign-in screen.
	Label string
}

func (c Config) validate() error {
	if c.ClientID == "" {
		return errors.New("oidc: client id is required")
	}
	if err := secureURL(c.Issuer, "issuer"); err != nil {
		return err
	}
	if err := secureURL(c.RedirectURL, "redirect address"); err != nil {
		return err
	}
	if u, _ := url.Parse(c.RedirectURL); u.Path != CallbackPath {
		return fmt.Errorf("oidc: the path of the redirect address must be %s", CallbackPath)
	}
	return nil
}

// secureURL checks that the address is absolute and HTTPS; for local development
// HTTP is accepted only on a loopback (localhost, 127.0.0.1) address.
func secureURL(s, what string) error {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("oidc: %s is invalid: %q", what, s)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("oidc: %s must be HTTPS: %q", what, s)
}

// Claims is the identity the IdP proved in the identity token.
type Claims struct {
	// Issuer and Subject together identify the person unambiguously.
	Issuer, Subject string
	// Email is the address the IdP reported; EmailVerified means the IdP verified it.
	// An unverified address is not enough for any match.
	Email         string
	EmailVerified bool
}

// Login opens an abarow session for a person whose identity is proven. If there is no open
// account linked to the identity it must return an error in the login.Wrong or login.Refused class.
type Login func(ctx context.Context, c Claims) (token string, p login.Person, err error)

// Option changes the Flow.
type Option func(*Flow)

// WithClock changes the clock (tests).
func WithClock(now func() time.Time) Option { return func(f *Flow) { f.now = now } }

// WithHTTPClient changes the client that makes the requests to the IdP.
func WithHTTPClient(c *http.Client) Option { return func(f *Flow) { f.client = c } }

// WithLogger changes the log the errors are written to.
func WithLogger(l *slog.Logger) Option { return func(f *Flow) { f.log = l } }

// Flow is the two ends of the authorization code flow (start, return). The flow's state (state,
// nonce, PKCE verifier) stays in the sealed cookie in the browser; the service keeps
// no pending sign-in in memory.
type Flow struct {
	cfg     Config
	login   Login
	session login.Cookie
	now     func() time.Time
	client  *http.Client
	log     *slog.Logger
	aead    cipher.AEAD
	account Account

	mu       sync.Mutex
	provider *gooidc.Provider
}

// New checks the setup and returns the flow. It does not connect to the IdP: an IdP that cannot
// be reached does not prevent the service from starting, it is tried at the first sign-in.
func New(cfg Config, login Login, session login.Cookie, opts ...Option) (*Flow, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	f := &Flow{
		cfg: cfg, login: login, session: session, aead: aead,
		now: time.Now, client: &http.Client{Timeout: 10 * time.Second}, log: slog.Default(),
	}
	for _, o := range opts {
		o(f)
	}
	return f, nil
}

// Method is the "sign in with corporate sign-in" button on the sign-in screen.
func (f *Flow) Method() login.Method {
	label := f.cfg.Label
	if label == "" {
		label = "Kurumsal girişle gir"
	}
	return login.Method{ID: "oidc", Kind: login.KindRedirect, Label: label, URL: StartPath}
}

// Handle adds the two ends of the flow to the mux.
func (f *Flow) Handle(mux *http.ServeMux) {
	mux.HandleFunc("GET "+StartPath, f.start)
	mux.HandleFunc("GET "+CallbackPath, f.callback)
}

// pending is the flow state sealed in the cookie in the browser.
type pending struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Expires  int64  `json:"x"`
	// Link says the flow was started not for sign-in but to link an identity to the
	// account of a person whose session is open; it is sealed when the person starts it
	// (the Strict session cookie is not sent on return from the IdP).
	Link *login.Person `json:"l,omitempty"`
}

func (f *Flow) seal(p pending) (string, error) {
	plain, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, f.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(f.aead.Seal(nonce, nonce, plain, nil)), nil
}

func (f *Flow) open(s string) (pending, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) < f.aead.NonceSize() {
		return pending{}, false
	}
	plain, err := f.aead.Open(nil, raw[:f.aead.NonceSize()], raw[f.aead.NonceSize():], nil)
	if err != nil {
		return pending{}, false
	}
	var p pending
	if json.Unmarshal(plain, &p) != nil || f.now().Unix() > p.Expires {
		return pending{}, false
	}
	return p, true
}

func random() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// discover fetches the IdP's description; it stores it on success. A failed
// attempt is not stored: when the IdP comes back the next sign-in works.
func (f *Flow) discover() (*gooidc.Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.provider != nil {
		return f.provider, nil
	}
	// The provider is not tied to the request's context: the key set is used in
	// later requests too.
	p, err := gooidc.NewProvider(gooidc.ClientContext(context.Background(), f.client), f.cfg.Issuer)
	if err != nil {
		return nil, err
	}
	f.provider = p
	return p, nil
}

func (f *Flow) oauth(p *gooidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID: f.cfg.ClientID, ClientSecret: f.cfg.ClientSecret, Endpoint: p.Endpoint(),
		RedirectURL: f.cfg.RedirectURL, Scopes: []string{gooidc.ScopeOpenID, "email"},
	}
}

func (f *Flow) setCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	// SameSite=Lax: the return from the IdP is another site's top-level GET navigation,
	// the Strict cookie is not sent on it.
	http.SetCookie(w, &http.Cookie{
		Name: flowCookie, Value: value, Path: CallbackPath, MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: login.OverHTTPS(r),
	})
}

// fail sends the user back to the screen with an error code.
func (f *Flow) fail(w http.ResponseWriter, r *http.Request, code string, err error) {
	if err != nil {
		f.log.Warn("corporate sign-in failed", "code", code, "err", err)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/?"+ErrorParam+"="+code, http.StatusSeeOther)
}

func (f *Flow) start(w http.ResponseWriter, r *http.Request) { f.begin(w, r, nil) }

// begin redirects the user to the IdP; if link is filled the flow is for linking
// an identity to that person's account.
func (f *Flow) begin(w http.ResponseWriter, r *http.Request, link *login.Person) {
	p, err := f.discover()
	if err != nil {
		f.fail(w, r, CodeUnavailable, err)
		return
	}
	state, err1 := random()
	nonce, err2 := random()
	if err := errors.Join(err1, err2); err != nil {
		f.fail(w, r, CodeUnavailable, err)
		return
	}
	st := pending{State: state, Nonce: nonce, Verifier: oauth2.GenerateVerifier(), Expires: f.now().Add(flowTTL).Unix(), Link: link}
	sealed, err := f.seal(st)
	if err != nil {
		f.fail(w, r, CodeUnavailable, err)
		return
	}
	f.setCookie(w, r, sealed, int(flowTTL.Seconds()))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, f.oauth(p).AuthCodeURL(st.State,
		oauth2.S256ChallengeOption(st.Verifier), oauth2.SetAuthURLParam("nonce", st.Nonce)), http.StatusSeeOther)
}

// flexBool reads email_verified as a bool or as the text "true": some
// IdPs send text.
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch v := v.(type) {
	case bool:
		*b = flexBool(v)
	case string:
		*b = flexBool(strings.EqualFold(v, "true"))
	}
	return nil
}

func (f *Flow) callback(w http.ResponseWriter, r *http.Request) {
	ck, err := r.Cookie(flowCookie)
	f.setCookie(w, r, "", -1) // a flow is used once
	if err != nil {
		f.fail(w, r, CodeExpired, nil)
		return
	}
	st, ok := f.open(ck.Value)
	q := r.URL.Query()
	if !ok || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(st.State)) != 1 {
		f.fail(w, r, CodeExpired, nil)
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		f.fail(w, r, CodeDenied, nil)
		return
	}
	p, err := f.discover()
	if err != nil {
		f.fail(w, r, CodeUnavailable, err)
		return
	}
	ctx := gooidc.ClientContext(r.Context(), f.client)
	tok, err := f.oauth(p).Exchange(ctx, q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		f.fail(w, r, CodeUnavailable, err)
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	id, err := p.Verifier(&gooidc.Config{ClientID: f.cfg.ClientID, Now: f.now}).Verify(ctx, raw)
	if err != nil {
		f.fail(w, r, CodeDenied, err)
		return
	}
	if subtle.ConstantTimeCompare([]byte(id.Nonce), []byte(st.Nonce)) != 1 || id.Subject == "" {
		f.fail(w, r, CodeDenied, errors.New("the identity token's nonce or sub value is invalid"))
		return
	}
	var extra struct {
		Email         string   `json:"email"`
		EmailVerified flexBool `json:"email_verified"`
	}
	if err := id.Claims(&extra); err != nil {
		f.fail(w, r, CodeDenied, err)
		return
	}
	claims := Claims{Issuer: id.Issuer, Subject: id.Subject, Email: extra.Email, EmailVerified: bool(extra.EmailVerified)}
	if st.Link != nil {
		f.completeLink(ctx, w, r, *st.Link, claims)
		return
	}
	token, _, err := f.login(ctx, claims)
	switch {
	case errors.Is(err, login.ErrWrong), errors.Is(err, login.ErrRefused):
		f.fail(w, r, CodeRefused, err)
		return
	case err != nil:
		f.fail(w, r, CodeUnavailable, err)
		return
	}
	f.session.Set(w, r, token)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// completeLink links the identity to the account of the person whose session is open.
func (f *Flow) completeLink(ctx context.Context, w http.ResponseWriter, r *http.Request, p login.Person, c Claims) {
	if f.account == nil {
		f.fail(w, r, CodeUnavailable, errors.New("kimlik bağlama kapalı"))
		return
	}
	switch err := f.account.Link(ctx, p, c); {
	case errors.Is(err, ErrTaken):
		f.fail(w, r, CodeTaken, err)
	case err != nil:
		f.fail(w, r, CodeUnavailable, err)
	default:
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "/?"+LinkedParam+"=1", http.StatusSeeOther)
	}
}
