// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httprelay_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"encoding/pem"
	"io"
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
	"abarow.com/services/update/internal/adapters/in/httprelay"
	"abarow.com/services/update/internal/adapters/out/httpclient"
	"abarow.com/services/update/internal/agent"
	"abarow.com/services/update/internal/identity"
	"abarow.com/services/update/internal/registration"
	"abarow.com/services/update/internal/token"
)

func pemOf(s *httptest.Server) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw})
}

// setup builds the headquarters registration server (TLS) and the proxy in front of it (with its own TLS
// certificate). The register sees only the proxy.
type env struct {
	svc          *registration.Service
	adm          http.Handler
	headquarters *httptest.Server
	relayServer  *httptest.Server
	rootPub      ed25519.PublicKey
	seen         chan http.Header // the request headers that reach headquarters
}

func setup(t *testing.T, upstreamWrap func(http.Handler) http.Handler, maxInFlight int) env {
	t.Helper()
	rootPub, rootPriv, _ := ed25519.GenerateKey(nil)
	svc, err := registration.New(registration.Config{
		Store: registration.NewMemory(), Issuer: registration.SigningIssuer{Key: rootPriv, ID: token.PublisherIssuer},
		Allowed: []token.Tier{token.Register}, TTL: 9 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := make(chan http.Header, 16)
	var h http.Handler = httpapi.NewDevice(svc)
	h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.Header.Clone():
		default:
		}
		httpapi.NewDevice(svc).ServeHTTP(w, r)
	})
	if upstreamWrap != nil {
		h = upstreamWrap(h)
	}
	headquarters := httptest.NewTLSServer(h)
	t.Cleanup(headquarters.Close)

	tr, base, err := httpclient.NewTransport(httpclient.Options{BaseURL: headquarters.URL, CAPEM: pemOf(headquarters)})
	if err != nil {
		t.Fatal(err)
	}
	rel, err := httprelay.New(httprelay.Options{Upstream: base, Transport: tr, MaxInFlight: maxInFlight})
	if err != nil {
		t.Fatal(err)
	}
	relayServer := httptest.NewTLSServer(rel)
	t.Cleanup(relayServer.Close)
	return env{svc: svc, adm: httpapi.NewAdmin(svc), headquarters: headquarters, relayServer: relayServer, rootPub: rootPub, seen: seen}
}

func (o env) approve(t *testing.T, device string) {
	t.Helper()
	body, _ := json.Marshal(httpapi.ApproveRequest{Code: identity.Code(device)})
	req := httptest.NewRequest("POST", "/api/update/registrations/"+device+"/approve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	login.Person{Username: "yonetici", Role: "admin", AllStores: true}.SetHeaders(req.Header)
	rec := httptest.NewRecorder()
	o.adm.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("approval: %d %s", rec.Code, rec.Body)
	}
}

// The register agent knows only the proxy; registration, approval and token go to headquarters through the proxy.
func TestRegisterEnrollsThroughRelay(t *testing.T) {
	ctx := context.Background()
	o := setup(t, nil, 0)
	client, err := httpclient.New(httpclient.Options{BaseURL: o.relayServer.URL, CAPEM: pemOf(o.relayServer)})
	if err != nil {
		t.Fatal(err)
	}
	a, err := agent.New(agent.Config{
		Dir: t.TempDir(), Root: fstest.MapFS{"etc/machine-id": {Data: []byte("register-1\n")}},
		Tier: token.Register, Level: token.Prod, Store: "store-1", Register: "register-1", Name: "Kasa 1", Transport: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	a.Step(ctx)
	if a.Status().State != agent.StatePending {
		t.Fatalf("status %s: %s", a.Status().State, a.Status().Error)
	}
	o.approve(t, a.Status().Device)
	a.Step(ctx)
	raw, ok := a.Token()
	if !ok {
		t.Fatalf("could not get a token through the proxy: %s", a.Status().Error)
	}
	if c, err := token.Verify(o.rootPub, raw, time.Now()); err != nil || c.Device != a.Status().Device || c.Tier != token.Register {
		t.Errorf("jeton: %+v, %v", c, err)
	}
}

func get(t *testing.T, url, method string, body io.Reader, headers map[string]string, c *http.Client) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestRelayOnlyForwardsDeviceEndpoints(t *testing.T) {
	o := setup(t, nil, 0)
	c := o.relayServer.Client()
	for _, tc := range []struct {
		label, method, path string
		want                int
	}{
		{"yönetim listesi", "GET", "/api/update/registrations", 404},
		{"yönetim onayı", "POST", "/api/update/registrations/ABRW-X/approve", 404},
		{"bilinmeyen yol", "GET", "/v1/baska", 404},
		{"kök", "GET", "/", 404},
		{"yanlış yöntem", "DELETE", "/v1/registrations/ABRW-X", 405},
		{"izinli uç, olmayan birim", "GET", "/v1/registrations/ABRW-YOK", 404}, // headquarters' 404 is forwarded
	} {
		resp := get(t, o.relayServer.URL+tc.path, tc.method, nil, nil, c)
		if resp.StatusCode != tc.want {
			t.Errorf("%s: %d, want %d", tc.label, resp.StatusCode, tc.want)
		}
	}
}

// The person headers, cookies and authorization headers coming from the register do not reach headquarters.
func TestRelayStripsHeaders(t *testing.T) {
	o := setup(t, nil, 0)
	h := map[string]string{
		login.UserHeader: "saldirgan", login.RoleHeader: "admin", login.StoresHeader: "*",
		"Cookie": "oturum=1", "Authorization": "Bearer x", "Content-Type": "application/json",
	}
	resp := get(t, o.relayServer.URL+"/v1/registrations/ABRW-YOK", "GET", nil, h, o.relayServer.Client())
	_ = resp
	got := <-o.seen
	for _, k := range []string{login.UserHeader, login.RoleHeader, login.StoresHeader, "Cookie", "Authorization"} {
		if got.Get(k) != "" {
			t.Errorf("the %s header was forwarded to headquarters: %q", k, got.Get(k))
		}
	}
	if got.Get("Content-Type") != "application/json" {
		t.Errorf("the content type was not forwarded: %q", got.Get("Content-Type"))
	}
}

func TestRelayLimitsRequestSize(t *testing.T) {
	o := setup(t, nil, 0)
	big := strings.Repeat("a", 20<<10)
	resp := get(t, o.relayServer.URL+"/v1/registrations", "POST", strings.NewReader(big), map[string]string{"Content-Type": "application/json"}, o.relayServer.Client())
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("large body: %d, want 413", resp.StatusCode)
	}
	select {
	case h := <-o.seen:
		t.Errorf("a large request reached headquarters: %v", h)
	default:
	}
}

func TestRelayUpstreamDown(t *testing.T) {
	o := setup(t, nil, 0)
	o.headquarters.Close()
	resp := get(t, o.relayServer.URL+"/v1/registrations/ABRW-X", "GET", nil, nil, o.relayServer.Client())
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("while headquarters is down %d, want 502", resp.StatusCode)
	}
	var e httpapi.ErrorJSON
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil || e.Code != httprelay.CodeUpstream {
		t.Errorf("error body: %+v, %v", e, err)
	}
}

func TestRelayInFlightLimit(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	o := setup(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started <- struct{}{}
			<-release
			next.ServeHTTP(w, r)
		})
	}, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, err := o.relayServer.Client().Get(o.relayServer.URL + "/v1/registrations/ABRW-X")
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-started // the first request is stuck at headquarters
	resp := get(t, o.relayServer.URL+"/v1/registrations/ABRW-Y", "GET", nil, nil, o.relayServer.Client())
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("when the limit is exceeded %d, want 429", resp.StatusCode)
	}
	close(release)
	wg.Wait()
	// When the limit is free a new request passes.
	if resp := get(t, o.relayServer.URL+"/v1/registrations/ABRW-Z", "GET", nil, nil, o.relayServer.Client()); resp.StatusCode != 404 {
		t.Errorf("when the limit is free %d, want 404 (headquarters' response)", resp.StatusCode)
	}
}

// The register gets the channel manifest through the store server's proxy too.
func TestRelayForwardsChannels(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "prod.json"), []byte(`{"payload":"x","signatures":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	headquarters := httptest.NewTLSServer(httpapi.NewDevice(nil, httpapi.WithChannels(dir)))
	defer headquarters.Close()
	tr, base, err := httpclient.NewTransport(httpclient.Options{BaseURL: headquarters.URL, CAPEM: pemOf(headquarters)})
	if err != nil {
		t.Fatal(err)
	}
	rel, err := httprelay.New(httprelay.Options{Upstream: base, Transport: tr})
	if err != nil {
		t.Fatal(err)
	}
	relayServer := httptest.NewTLSServer(rel)
	defer relayServer.Close()
	c, err := httpclient.New(httpclient.Options{BaseURL: relayServer.URL, CAPEM: pemOf(relayServer)})
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Fetch(context.Background(), "prod")
	if err != nil || s.Payload != "x" {
		t.Fatalf("the manifest could not be fetched through the proxy: %+v %v", s, err)
	}
	if _, err := c.Fetch(context.Background(), "pilot"); err == nil {
		t.Fatal("a non-existent channel did not fail")
	}
}

func TestNewValidates(t *testing.T) {
	tr := http.DefaultTransport
	for label, o := range map[string]httprelay.Options{
		"adres yok":      {Transport: tr},
		"geçersiz adres": {Upstream: "::", Transport: tr},
		"taşıyıcı yok":   {Upstream: "https://headquarters"},
		"şema yok":       {Upstream: "ftp://headquarters", Transport: tr},
	} {
		if _, err := httprelay.New(o); err == nil {
			t.Errorf("%s: accepted", label)
		}
	}
}
