// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/services/update/internal/adapters/in/httpapi"
	"abarow.com/services/update/internal/agent"
	"abarow.com/services/update/internal/release"
)

type fakeUpdates struct {
	err      error
	by       string
	seq      uint64
	snooze   time.Duration
	skipped  bool
	applied  int
	snoozed  int
	snoozeBy string
}

func (f *fakeUpdates) Status() release.Status {
	return release.Status{Phase: release.PhaseAvailable, Channel: "prod", Version: "0.4.0", Seq: 7}
}

func (f *fakeUpdates) Apply(_ context.Context, by string, seq uint64) error {
	f.by, f.seq = by, seq
	f.applied++
	return f.err
}

func (f *fakeUpdates) Skip(by string) error { f.by, f.skipped = by, true; return f.err }

func (f *fakeUpdates) Snooze(by string, d time.Duration) error {
	f.snoozeBy, f.snooze = by, d
	f.snoozed++
	return f.err
}

func newAgentHandler(u httpapi.Updates, secret string) http.Handler {
	return httpapi.NewAgent(src{agent.Status{State: agent.StateApproved, Device: "ABRW-X", Tier: "register", Level: "prod"}}, httpapi.WithUpdates(u, secret))
}

type src struct{ st agent.Status }

func (s src) Status() agent.Status { return s.st }

func post(h http.Handler, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func person(secret string) map[string]string {
	h := http.Header{}
	login.Person{Username: "ayse", Name: "Ayşe", Role: "manager"}.SetHeaders(h)
	out := map[string]string{login.UserHeader: h.Get(login.UserHeader), login.RoleHeader: "manager", login.PersonHeader: h.Get(login.PersonHeader), login.StoresHeader: "*"}
	if secret != "" {
		out[login.UpdateSecretHeader] = secret
	}
	return out
}

func TestAgentStatusIncludesUpdate(t *testing.T) {
	h := newAgentHandler(&fakeUpdates{}, "gizli")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/update/status", nil))
	var out httpapi.AgentStatusJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 {
		t.Fatalf("%d %v %s", rec.Code, err, rec.Body)
	}
	if out.Device != "ABRW-X" || out.Update == nil || out.Update.Version != "0.4.0" {
		t.Fatalf("durum: %+v", out)
	}
	// If the new release flow is not installed the field is absent.
	rec = httptest.NewRecorder()
	httpapi.NewAgent(src{agent.Status{Device: "ABRW-X"}}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/update/status", nil))
	if strings.Contains(rec.Body.String(), `"update"`) {
		t.Fatalf("the update field exists while not installed: %s", rec.Body)
	}
}

func TestApplyNeedsSecretAndPerson(t *testing.T) {
	u := &fakeUpdates{}
	h := newAgentHandler(u, "gizli")
	// No secret key or a wrong one: 403, nothing is applied.
	for _, secret := range []string{"", "yanlis"} {
		hdr := person(secret)
		if secret == "" {
			delete(hdr, login.UpdateSecretHeader)
		}
		if rec := post(h, "/api/update/apply", `{"seq":7}`, hdr); rec.Code != http.StatusForbidden {
			t.Errorf("gizli %q: %d", secret, rec.Code)
		}
	}
	// No person: 401.
	if rec := post(h, "/api/update/apply", `{"seq":7}`, map[string]string{login.UpdateSecretHeader: "gizli"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("no person: %d", rec.Code)
	}
	if u.applied != 0 {
		t.Fatal("an unauthorized request was applied")
	}
	rec := post(h, "/api/update/apply", `{"seq":7}`, person("gizli"))
	if rec.Code != http.StatusAccepted || u.by != "ayse" || u.seq != 7 {
		t.Fatalf("onay: %d %q %d", rec.Code, u.by, u.seq)
	}
	// Unknown fields and non-JSON content are rejected.
	if rec := post(h, "/api/update/apply", `{"seq":7,"fazla":1}`, person("gizli")); rec.Code != http.StatusBadRequest {
		t.Errorf("bilinmeyen alan: %d", rec.Code)
	}
}

func TestApplyErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
		body string
	}{
		{release.BlockedError{Reason: "açık vardiya var"}, 409, `"code":"blocked"`},
		{release.ErrNoOffer, 404, `"code":"no_offer"`},
		{release.ErrStale, 409, `"code":"stale"`},
		{release.ErrBusy, 409, `"code":"busy"`},
		{context.DeadlineExceeded, 500, `"code":"internal"`},
	} {
		rec := post(newAgentHandler(&fakeUpdates{err: tc.err}, ""), "/api/update/apply", `{"seq":7}`, person(""))
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.body) {
			t.Errorf("%v: %d %s", tc.err, rec.Code, rec.Body)
		}
	}
	rec := post(newAgentHandler(&fakeUpdates{err: release.BlockedError{Reason: "açık vardiya var"}}, ""), "/api/update/apply", `{"seq":7}`, person(""))
	if !strings.Contains(rec.Body.String(), "açık vardiya var") {
		t.Errorf("engel nedeni iletilmedi: %s", rec.Body)
	}
}

func TestSkipAndSnooze(t *testing.T) {
	u := &fakeUpdates{}
	h := newAgentHandler(u, "")
	if rec := post(h, "/api/update/skip", `{}`, person("")); rec.Code != 200 || !u.skipped || u.by != "ayse" {
		t.Fatalf("atla: %d", rec.Code)
	}
	if rec := post(h, "/api/update/snooze", `{}`, person("")); rec.Code != 200 || u.snooze != time.Hour {
		t.Fatalf("postponement default: %d %v", rec.Code, u.snooze)
	}
	if rec := post(h, "/api/update/snooze", `{"minutes":240}`, person("")); rec.Code != 200 || u.snooze != 4*time.Hour {
		t.Fatalf("erteleme: %d %v", rec.Code, u.snooze)
	}
	for _, m := range []string{`{"minutes":1}`, `{"minutes":-5}`, `{"minutes":999999}`} {
		if rec := post(h, "/api/update/snooze", m, person("")); rec.Code != 400 {
			t.Errorf("%s: %d", m, rec.Code)
		}
	}
	if u.snoozed != 2 {
		t.Errorf("an invalid postponement was applied: %d", u.snoozed)
	}
}

func TestWriteEndpointsAbsentWithoutUpdates(t *testing.T) {
	h := httpapi.NewAgent(src{})
	if rec := post(h, "/api/update/apply", `{"seq":1}`, person("")); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("while not installed: %d", rec.Code)
	}
}

func TestChannels(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "prod.json"), []byte(`{"payload":"x","signatures":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// A file name that tries to escape the directory.
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "gizli.json"), []byte(`sır`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := httpapi.NewDevice(nil, httpapi.WithChannels(dir))
	get := func(p string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		return rec
	}
	if rec := get("/v1/channels/prod"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"payload"`) || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("prod: %d %s", rec.Code, rec.Body)
	}
	for _, p := range []string{"/v1/channels/pilot", "/v1/channels/beta", "/v1/channels/..%2Fgizli", "/v1/channels/prod.json"} {
		if rec := get(p); rec.Code != 404 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}
