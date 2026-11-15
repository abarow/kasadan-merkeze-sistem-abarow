// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSPA(t *testing.T) {
	dir := t.TempDir()
	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		SPA(dir, "test").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}
	if code, body := get("/raporlar"); code != http.StatusNotFound || !strings.Contains(body, "ui/apps/test") {
		t.Errorf("screen not built: %d %q", code, body)
	}
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("ana sayfa"), 0o644)
	os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("kod"), 0o644)
	if code, body := get("/assets/app.js"); code != http.StatusOK || body != "kod" {
		t.Errorf("file: %d %q", code, body)
	}
	// An unknown path (the screen's own path) falls back to index.html.
	if code, body := get("/raporlar/bugun"); code != http.StatusOK || body != "ana sayfa" {
		t.Errorf("screen path: %d %q", code, body)
	}
	// An attempt to leave the directory is rejected.
	if code, _ := get("/../../etc/passwd"); code != http.StatusBadRequest {
		t.Errorf("outside the directory: %d", code)
	}
}
