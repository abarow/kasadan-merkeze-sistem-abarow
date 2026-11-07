// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package agent_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/services/update/internal/adapters/in/httpapi"
	"abarow.com/services/update/internal/adapters/out/httpclient"
	"abarow.com/services/update/internal/agent"
	"abarow.com/services/update/internal/identity"
	"abarow.com/services/update/internal/registration"
	"abarow.com/services/update/internal/token"
)

// fakeClock is a fake clock that can be moved forward; the server and the agent use the same clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (s *fakeClock) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.t
}

func (s *fakeClock) Add(d time.Duration) {
	s.mu.Lock()
	s.t = s.t.Add(d)
	s.mu.Unlock()
}

// server runs the real registration service as two real HTTP endpoints.
type server struct {
	svc     *registration.Service
	dev     *httptest.Server
	adm     http.Handler
	rootPub ed25519.PublicKey
	clock   *fakeClock
}

func newServer(t *testing.T, allowed []token.Tier, ttl time.Duration) *server {
	t.Helper()
	rootPub, rootPriv, _ := ed25519.GenerateKey(nil)
	clock := &fakeClock{t: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)}
	svc, err := registration.New(registration.Config{
		Store: registration.NewMemory(), Issuer: registration.SigningIssuer{Key: rootPriv, ID: token.PublisherIssuer},
		Allowed: allowed, TTL: ttl, CertTTL: 90 * 24 * time.Hour, Now: clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	dev := httptest.NewTLSServer(httpapi.NewDevice(svc))
	t.Cleanup(dev.Close)
	return &server{svc: svc, dev: dev, adm: httpapi.NewAdmin(svc), rootPub: rootPub, clock: clock}
}

func (s *server) approve(t *testing.T, device string) {
	t.Helper()
	body, _ := json.Marshal(httpapi.ApproveRequest{Code: identity.Code(device)})
	req := httptest.NewRequest("POST", "/api/update/registrations/"+device+"/approve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	login.Person{Username: "yonetici", Name: "Yönetici", Role: "admin", AllStores: true}.SetHeaders(req.Header)
	rec := httptest.NewRecorder()
	s.adm.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("onay: %d %s", rec.Code, rec.Body)
	}
}

func (s *server) client(t *testing.T) *httpclient.Client {
	t.Helper()
	pemCA := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.dev.Certificate().Raw})
	c, err := httpclient.New(httpclient.Options{BaseURL: s.dev.URL, CAPEM: pemCA})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func machine(id string) fstest.MapFS {
	return fstest.MapFS{"etc/machine-id": {Data: []byte(id + "\n")}}
}

func newAgent(t *testing.T, s *server, dir string, tier token.Tier, root fstest.MapFS) *agent.Agent {
	t.Helper()
	a, err := agent.New(agent.Config{
		Dir: dir, Root: root, Tier: tier, Level: token.Pilot, Store: "store-1", Register: "register-1", Name: "Kasa 1",
		Transport: s.client(t), Now: s.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestEnrollmentLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newServer(t, []token.Tier{token.Register}, 9*time.Hour)
	dir := t.TempDir()
	a := newAgent(t, s, dir, token.Register, machine("register-1"))

	st := a.Status()
	if st.Device == "" || st.Code != identity.Code(st.Device) || st.Tier != "register" || st.Level != "pilot" {
		t.Fatalf("ilk durum: %+v", st)
	}

	// Onay beklerken: kod ekranda, jeton yok.
	if wait := a.Step(ctx); wait != 15*time.Second {
		t.Errorf("bekleme %v", wait)
	}
	if a.Status().State != agent.StatePending {
		t.Fatalf("durum %s", a.Status().State)
	}
	if _, ok := a.Token(); ok {
		t.Error("an unapproved agent has a token")
	}

	// It is approved; the agent gets a token.
	s.approve(t, st.Device)
	wait := a.Step(ctx)
	if a.Status().State != agent.StateApproved {
		t.Fatalf("durum %s: %s", a.Status().State, a.Status().Error)
	}
	raw, ok := a.Token()
	if !ok {
		t.Fatal("no token")
	}
	c, err := token.Verify(s.rootPub, raw, s.clock.Now())
	if err != nil || c.Device != st.Device || c.Level != token.Pilot || c.Store != "store-1" || c.Register != "register-1" {
		t.Fatalf("jeton: %+v, %v", c, err)
	}
	// Renewal is in the last third of the lifetime: at the 6th hour of 9; a step does not exceed an hour.
	if wait != time.Hour {
		t.Errorf("approved wait %v, want 1 hour", wait)
	}
	if info, err := os.Stat(filepath.Join(dir, "token.json")); err != nil || info.Size() == 0 {
		t.Error("the token file was not written")
	}

	// Restart: the same directory loads the same identity and token; it does not make the server issue a new token.
	b := newAgent(t, s, dir, token.Register, machine("register-1"))
	if b.Status().Device != st.Device {
		t.Error("the identity changed on restart")
	}
	if tok, ok := b.Token(); !ok || tok != raw {
		t.Error("the saved token was not loaded")
	}
	b.Step(ctx)
	if tok, _ := b.Token(); tok != raw {
		t.Error("the token was renewed before it expired")
	}

	// It is renewed when the last third of the lifetime is entered.
	s.clock.Add(7 * time.Hour)
	b.Step(ctx)
	fresh, ok := b.Token()
	if !ok || fresh == raw {
		t.Error("jeton yenilenmedi")
	}
	if c, err := token.Verify(s.rootPub, fresh, s.clock.Now()); err != nil || !c.Expiry.After(s.clock.Now().Add(8*time.Hour)) {
		t.Errorf("yeni jeton: %+v, %v", c, err)
	}

	// Revocation: the agent shows the revoked state and does not try to get a token.
	if err := s.svc.Revoke(ctx, "yonetici", st.Device); err != nil {
		t.Fatal(err)
	}
	s.clock.Add(7 * time.Hour)
	b.Step(ctx)
	if b.Status().State != agent.StateRevoked {
		t.Errorf("state after revocation %s", b.Status().State)
	}
}

func TestRejectedAgent(t *testing.T) {
	ctx := context.Background()
	s := newServer(t, []token.Tier{token.Register}, time.Hour)
	a := newAgent(t, s, t.TempDir(), token.Register, machine("k"))
	a.Step(ctx)
	if err := s.svc.Reject(ctx, "yonetici", a.Status().Device); err != nil {
		t.Fatal(err)
	}
	a.Step(ctx)
	if a.Status().State != agent.StateRejected {
		t.Errorf("durum %s", a.Status().State)
	}
	if _, ok := a.Token(); ok {
		t.Error("a rejected agent has a token")
	}
}

func TestClonedDiskDoesNotRegister(t *testing.T) {
	ctx := context.Background()
	s := newServer(t, []token.Tier{token.Register}, time.Hour)
	dir := t.TempDir()
	a := newAgent(t, s, dir, token.Register, machine("makine-a"))
	a.Step(ctx)

	// The same directory (a copied disk) is opened on another machine.
	b := newAgent(t, s, dir, token.Register, machine("makine-b"))
	if b.Status().State != agent.StateIdentityMismatch || b.Status().Device != "" {
		t.Fatalf("durum: %+v", b.Status())
	}
	if wait := b.Step(ctx); wait != 10*time.Minute {
		t.Errorf("bekleme %v", wait)
	}
	if _, ok := b.Token(); ok {
		t.Error("an agent with a mismatched identity has a token")
	}
	// At the server only the original machine's registration exists.
	if list, _ := s.svc.List(ctx, registration.Pending); len(list) != 1 {
		t.Errorf("registration count %d", len(list))
	}
}

func TestServerDownKeepsStateAndBacksOff(t *testing.T) {
	ctx := context.Background()
	s := newServer(t, []token.Tier{token.Register}, 9*time.Hour)
	a := newAgent(t, s, t.TempDir(), token.Register, machine("k"))
	a.Step(ctx)
	s.approve(t, a.Status().Device)
	a.Step(ctx)
	tok, ok := a.Token()
	if !ok {
		t.Fatal("no token")
	}

	s.dev.Close() // the server closed
	if wait := a.Step(ctx); wait != 30*time.Second {
		t.Errorf("first back-off %v", wait)
	}
	if wait := a.Step(ctx); wait != time.Minute {
		t.Errorf("second back-off %v", wait)
	}
	st := a.Status()
	if st.State != agent.StateApproved || st.Error == "" {
		t.Errorf("durum: %+v", st)
	}
	// The token in hand can be used until it expires; being unable to reach the server does not stop the running system.
	if got, ok := a.Token(); !ok || got != tok {
		t.Error("mevcut jeton kayboldu")
	}
}

// Headquarters tier: the agent generates its own signing key, gets a certificate from the publisher and
// writes both to the identity directory; headquarters' registration server gives unit tokens with them.
func TestHeadquartersGetsCertificateForItsSigningKey(t *testing.T) {
	ctx := context.Background()
	s := newServer(t, []token.Tier{token.Headquarters}, 24*time.Hour)
	dir := t.TempDir()
	a := newAgent(t, s, dir, token.Headquarters, machine("headquarters"))
	if a.SigningKey() == nil {
		t.Fatal("the headquarters agent has no signing key")
	}
	if _, err := os.Stat(filepath.Join(dir, "signing.key")); err != nil {
		t.Fatalf("no signing key file: %v", err)
	}
	a.Step(ctx)
	s.approve(t, a.Status().Device)
	a.Step(ctx)
	if a.Status().State != agent.StateApproved {
		t.Fatalf("durum %s: %s", a.Status().State, a.Status().Error)
	}
	cert, err := os.ReadFile(filepath.Join(dir, "certificate.jwt"))
	if err != nil || len(cert) == 0 {
		t.Fatalf("the certificate was not written: %v", err)
	}

	// On restart the same signing key is loaded.
	b := newAgent(t, s, dir, token.Headquarters, machine("headquarters"))
	if !bytes.Equal(a.SigningKey(), b.SigningKey()) {
		t.Error("the signing key changed on restart")
	}

	// Headquarters' server gives a store server token with this key and certificate; the root verifies.
	mst := registration.NewMemory()
	headquartersSvc, err := registration.New(registration.Config{
		Store: mst, Issuer: registration.SigningIssuer{Key: a.SigningKey(), ID: a.Status().Device, Chain: string(cert)},
		Allowed: []token.Tier{token.Store}, TTL: time.Hour, Now: s.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	op, _ := identity.Generate(identity.NewFingerprint(map[string]string{"machine-id": "op"}), nil)
	in := registration.Submission{Device: op.ID, Public: op.Public, Fingerprint: string(op.Fingerprint), Tier: token.Store, Level: token.Prod, Name: "Operasyon"}
	in.Signature = op.Sign(registration.SubmitMessage(in.Device, in.Tier, in.Level, in.Name, nil))
	if _, err := headquartersSvc.Submit(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := headquartersSvc.Approve(ctx, "yonetici", op.ID, identity.Code(op.ID)); err != nil {
		t.Fatal(err)
	}
	nonce, _ := headquartersSvc.Challenge(ctx, op.ID)
	res, err := headquartersSvc.Token(ctx, op.ID, nonce, op.Sign(token.PossessionMessage(op.ID, nonce)))
	if err != nil {
		t.Fatal(err)
	}
	c, err := token.Verify(s.rootPub, res.Token, s.clock.Now())
	if err != nil || c.Issuer != a.Status().Device || c.Tier != token.Store {
		t.Fatalf("the store server token could not be verified with the publisher root: %+v, %v", c, err)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	if _, err := agent.New(agent.Config{}); err == nil {
		t.Error("an empty setting was accepted")
	}
	if _, err := agent.New(agent.Config{Dir: t.TempDir(), Root: fstest.MapFS{}, Tier: token.Register, Transport: nil}); err == nil {
		t.Error("an agent was built with no transport")
	}
	s := newServer(t, []token.Tier{token.Register}, time.Hour)
	if _, err := agent.New(agent.Config{Dir: t.TempDir(), Root: fstest.MapFS{}, Tier: token.Register, Transport: s.client(t)}); err == nil {
		t.Error("an agent was built while the hardware fingerprint could not be read")
	}
}

func TestClientRefusesPlainHTTPAndUntrustedTLS(t *testing.T) {
	s := newServer(t, []token.Tier{token.Register}, time.Hour)
	if _, err := httpclient.New(httpclient.Options{BaseURL: "http://server"}); err == nil {
		t.Error("plain HTTP was accepted without being explicitly allowed")
	}
	if _, err := httpclient.New(httpclient.Options{BaseURL: "https://server", CAPEM: []byte("pem değil")}); err == nil {
		t.Error("bozuk CA kabul edildi")
	}
	// If no CA is given the server's (test) certificate cannot be verified with the system roots.
	c, err := httpclient.New(httpclient.Options{BaseURL: s.dev.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Status(context.Background(), "ABRW-YOK"); err == nil {
		t.Error("connected to a server with an unrecognized CA")
	}
}

// OnToken is called once when the token is first received and at every renewal; if it
// fails it is retried at the next step.
func TestOnTokenAnnouncesEachTokenOnce(t *testing.T) {
	ctx := context.Background()
	s := newServer(t, []token.Tier{token.Register}, 9*time.Hour)
	var got []string
	failNext := true
	a, err := agent.New(agent.Config{
		Dir: t.TempDir(), Root: machine("register-1"), Tier: token.Register, Level: token.Prod, Store: "store-1", Register: "register-1",
		Transport: s.client(t), Now: s.clock.Now,
		OnToken: func(token string, expires time.Time) error {
			if failNext {
				failNext = false
				return errors.New("dizin yazılamadı")
			}
			got = append(got, token)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.Step(ctx)
	s.approve(t, a.Status().Device)
	a.Step(ctx) // the token is received, the notification fails
	if len(got) != 0 {
		t.Fatalf("a failed notification was counted: %v", got)
	}
	a.Step(ctx) // yeniden denenir
	first, _ := a.Token()
	if len(got) != 1 || got[0] != first {
		t.Fatalf("jeton bildirilmedi: %v", got)
	}
	a.Step(ctx) // the same token is not notified again
	if len(got) != 1 {
		t.Fatalf("the same token was notified again: %d", len(got))
	}
	s.clock.Add(7 * time.Hour)
	a.Step(ctx) // yenilenir ve bildirilir
	if second, _ := a.Token(); len(got) != 2 || got[1] != second || second == first {
		t.Fatalf("yenilenen jeton bildirilmedi: %v", got)
	}
}

// An approved agent reports the running version state to the tier above: immediately when it changes,
// when it does not change it does not resend until ReportEvery is up; Nudge cuts the wait short.
func TestAgentReportsRunningVersions(t *testing.T) {
	ctx := context.Background()
	s := newServer(t, []token.Tier{token.Register}, 9*time.Hour)
	units := map[string]string{"abarow-sales": "sha256:" + strings.Repeat("1", 64)}
	version := ""
	calls := 0
	a := newAgent(t, s, t.TempDir(), token.Register, machine("register-1"))
	a.SetReporter(func(context.Context) (registration.Report, error) {
		calls++
		return registration.Report{Version: version, Phase: "none", Units: units}, nil
	}, 10*time.Minute)

	a.Step(ctx)
	s.approve(t, a.Status().Device)
	if wait := a.Step(ctx); wait > 10*time.Minute {
		t.Errorf("while a report is open the wait %v should not exceed ReportEvery", wait)
	}
	got := func() *registration.Report {
		r, err := s.svc.Get(ctx, a.Status().Device)
		if err != nil {
			t.Fatal(err)
		}
		return r.Report
	}
	r1 := got()
	if r1 == nil || r1.Units["abarow-sales"] != units["abarow-sales"] || r1.Version != "" {
		t.Fatalf("ilk bildirim: %+v", r1)
	}

	// Not changed and the time is not up: it is not resent (the report moment stays the same even as the clock advances).
	s.clock.Add(time.Minute)
	a.Step(ctx)
	if r := got(); !r.At.Equal(r1.At) {
		t.Errorf("an unchanged state was sent again: %v → %v", r1.At, r.At)
	}
	// The state changed (a version was applied): it goes right away.
	version, units = "0.4.0", map[string]string{"abarow-sales": "sha256:" + strings.Repeat("2", 64)}
	a.Step(ctx)
	if r := got(); r.Version != "0.4.0" || r.Units["abarow-sales"] != units["abarow-sales"] {
		t.Errorf("a changed state did not go: %+v", r)
	}
	// When the time is up it goes again even if unchanged.
	before := got().At
	s.clock.Add(11 * time.Minute)
	a.Step(ctx)
	if !got().At.After(before) {
		t.Error("ReportEvery dolunca bildirim yenilenmedi")
	}
	// Nudge beklemeyi keser.
	done := make(chan struct{})
	go func() {
		a.Nudge()
		close(done)
	}()
	<-done
	select {
	case <-time.After(time.Second):
		t.Error("Nudge engelledi")
	default:
	}
}

// Even if the report fails the agent's registration and token step is not broken.
func TestAgentReportFailureDoesNotBreakStep(t *testing.T) {
	ctx := context.Background()
	s := newServer(t, []token.Tier{token.Register}, 9*time.Hour)
	a := newAgent(t, s, t.TempDir(), token.Register, machine("register-1"))
	a.SetReporter(func(context.Context) (registration.Report, error) {
		return registration.Report{}, errors.New("hazırlanamadı")
	}, time.Minute)
	a.Step(ctx)
	s.approve(t, a.Status().Device)
	a.Step(ctx)
	if _, ok := a.Token(); !ok || a.Status().State != agent.StateApproved {
		t.Fatalf("a report error broke getting the token: %+v", a.Status())
	}
}
