// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package podman_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"abarow.com/services/update/internal/adapters/out/podman"
	"abarow.com/services/update/internal/channel"
)

const (
	d1 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	d2 = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

type fakeSystemd struct {
	mu       sync.Mutex
	calls    []string
	active   bool
	restartE error
}

func (f *fakeSystemd) Reload(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "reload")
	return nil
}

func (f *fakeSystemd) Restart(_ context.Context, unit string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "restart "+unit)
	return f.restartE
}

func (f *fakeSystemd) Active(context.Context, string) (bool, error) { return f.active, nil }

// fakePodman imitates the endpoints of the Podman API that are used.
type fakePodman struct {
	running   map[string]string // container name → running digest
	pullAuth  string
	pullRef   string
	pullLines string
	pulledDig string // the digest that image inspect returns after the pull
	pullCode  int
}

func (f *fakePodman) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/libpod/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path[strings.Index(r.URL.Path, "/containers/"):], "/containers/"), "/json")
		dig, ok := f.running[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ImageDigest": dig, "State": map[string]any{"Running": true}})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/libpod/images/pull"):
		f.pullAuth, f.pullRef = r.Header.Get("X-Registry-Auth"), r.URL.Query().Get("reference")
		if f.pullCode != 0 {
			http.Error(w, "reddedildi", f.pullCode)
			return
		}
		fmt.Fprint(w, f.pullLines)
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/libpod/images/"):
		_ = json.NewEncoder(w).Encode(map[string]any{"Digest": f.pulledDig})
	default:
		http.NotFound(w, r)
	}
}

type env struct {
	rt    *podman.Runtime
	dir   string
	sd    *fakeSystemd
	pod   *fakePodman
	token string
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	pod := &fakePodman{running: map[string]string{}, pullLines: `{"id":"x"}` + "\n", pulledDig: d2}
	srv := httptest.NewServer(pod)
	t.Cleanup(srv.Close)
	sd := &fakeSystemd{active: true}
	e := &env{dir: dir, sd: sd, pod: pod, token: "token-123"}
	rt, err := podman.New(podman.Options{
		BaseURL: srv.URL, UnitsDir: dir, Systemd: sd, Client: srv.Client(),
		Token: func() (string, bool) { return e.token, e.token != "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	e.rt = rt
	return e
}

func (e *env) unit(t *testing.T, name, image string) {
	t.Helper()
	body := "[Container]\nContainerName=" + name + "\nImage=" + image + "\n"
	if err := os.WriteFile(filepath.Join(e.dir, name+".container"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentReadsRunningDigest(t *testing.T) {
	e := setup(t)
	e.unit(t, "abarow-sales", "ghcr.io/abarow/abarow-sales:0.1")
	e.unit(t, "abarow-gateway", "ghcr.io/abarow/abarow-gateway:0.1")
	e.unit(t, "abarow-pinned", "ghcr.io/abarow/abarow-pinned:0.1")
	// Out of scope: a file whose prefix does not match.
	if err := os.WriteFile(filepath.Join(e.dir, "baska.container"), []byte("[Container]\nImage=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.pod.running["abarow-sales"] = d1
	// gateway is not running, a pinned drop-in exists: the pinned digest is used.
	if err := os.MkdirAll(filepath.Join(e.dir, "abarow-gateway.container.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dir, "abarow-gateway.container.d", podman.DropIn), []byte("[Container]\nImage=ghcr.io/abarow/abarow-gateway@"+d2+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cur, err := e.rt.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) != 3 {
		t.Fatalf("birimler: %+v", cur)
	}
	if got := cur["abarow-sales"]; got.Digest != d1 || got.Repo != "ghcr.io/abarow/abarow-sales" {
		t.Errorf("sales: %+v", got)
	}
	if got := cur["abarow-gateway"]; got.Digest != d2 || got.Repo != "ghcr.io/abarow/abarow-gateway" {
		t.Errorf("gateway: %+v", got)
	}
	if got := cur["abarow-pinned"]; got.Digest != "" {
		t.Errorf("çalışmayan ve sabitlenmemiş birimin digest'i bilinmiyor olmalı: %+v", got)
	}
}

func TestPullSendsTokenAndVerifiesDigest(t *testing.T) {
	e := setup(t)
	e.unit(t, "abarow-sales", "ghcr.io/abarow/abarow-sales:0.1")
	img := channel.Image{Unit: "abarow-sales", Repo: "ghcr.io/abarow/abarow-sales", Digest: d2}
	if err := e.rt.Pull(context.Background(), img); err != nil {
		t.Fatal(err)
	}
	if e.pod.pullRef != img.Ref() {
		t.Errorf("çekilen: %s", e.pod.pullRef)
	}
	raw, _ := base64.URLEncoding.DecodeString(e.pod.pullAuth)
	var auth map[string]string
	if err := json.Unmarshal(raw, &auth); err != nil || auth["password"] != "token-123" || auth["username"] != "abarow" || auth["serveraddress"] != "ghcr.io" {
		t.Errorf("kayıt defteri kimliği: %s %v", raw, err)
	}

	// An error if the digest of the pulled image does not match.
	e.pod.pulledDig = d1
	if err := e.rt.Pull(context.Background(), img); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Errorf("digest uyuşmazlığı kabul edildi: %v", err)
	}
	e.pod.pulledDig = d2

	// An error line in the stream.
	e.pod.pullLines = `{"error":"manifest unknown"}` + "\n"
	if err := e.rt.Pull(context.Background(), img); err == nil || !strings.Contains(err.Error(), "manifest unknown") {
		t.Errorf("akış hatası yutuldu: %v", err)
	}
	e.pod.pullLines = "{}\n"

	// HTTP error.
	e.pod.pullCode = http.StatusForbidden
	if err := e.rt.Pull(context.Background(), img); err == nil {
		t.Error("HTTP hatası yutuldu")
	}
	e.pod.pullCode = 0

	// Jeton yok.
	e.token = ""
	if err := e.rt.Pull(context.Background(), img); err == nil {
		t.Error("jetonsuz çekme denendi")
	}
}

func TestSwitchWritesDropInAndRestarts(t *testing.T) {
	e := setup(t)
	e.unit(t, "abarow-sales", "ghcr.io/abarow/abarow-sales:0.1")
	img := channel.Image{Unit: "abarow-sales", Repo: "ghcr.io/abarow/abarow-sales", Digest: d2}
	if err := e.rt.Switch(context.Background(), img); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(e.dir, "abarow-sales.container.d", podman.DropIn))
	if err != nil || !strings.Contains(string(raw), "Image=ghcr.io/abarow/abarow-sales@"+d2) {
		t.Fatalf("drop-in: %q %v", raw, err)
	}
	if got := strings.Join(e.sd.calls, ","); got != "reload,restart abarow-sales.service" {
		t.Fatalf("systemd çağrıları: %s", got)
	}
	// The actual unit file was not touched.
	body, _ := os.ReadFile(filepath.Join(e.dir, "abarow-sales.container"))
	if !strings.Contains(string(body), "abarow-sales:0.1") {
		t.Errorf("birim dosyası değişti: %s", body)
	}
	// If the restart fails it returns an error.
	e.sd.restartE = fmt.Errorf("iş başarısız")
	if err := e.rt.Switch(context.Background(), img); err == nil {
		t.Error("yeniden başlatma hatası yutuldu")
	}
}

func TestAllowListBlocksOtherUnits(t *testing.T) {
	e := setup(t)
	e.unit(t, "abarow-sales", "ghcr.io/abarow/abarow-sales:0.1")
	for _, unit := range []string{"sshd", "abarow-yok", "abarow-../x", "abarow-sales/../../x", ""} {
		img := channel.Image{Unit: unit, Repo: "ghcr.io/abarow/x", Digest: d2}
		if err := e.rt.Pull(context.Background(), img); err == nil {
			t.Errorf("%q çekilebildi", unit)
		}
		if err := e.rt.Switch(context.Background(), img); err == nil {
			t.Errorf("%q değiştirilebildi", unit)
		}
		if _, err := e.rt.Healthy(context.Background(), unit); err == nil {
			t.Errorf("%q için sağlık sorulabildi", unit)
		}
	}
	if len(e.sd.calls) != 0 {
		t.Fatalf("izinsiz birim için systemd çağrıldı: %v", e.sd.calls)
	}
}

func TestHealthyFollowsSystemd(t *testing.T) {
	e := setup(t)
	e.unit(t, "abarow-sales", "ghcr.io/abarow/abarow-sales:0.1")
	if ok, err := e.rt.Healthy(context.Background(), "abarow-sales"); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	e.sd.active = false
	if ok, _ := e.rt.Healthy(context.Background(), "abarow-sales"); ok {
		t.Fatal("etkin olmayan birim sağlıklı sayıldı")
	}
}
