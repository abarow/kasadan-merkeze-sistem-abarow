// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package feed_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"abarow.com/services/update/internal/feed"
)

func TestOnTokenWritesCredentialsAndNudgesConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "zot.json")
	orig := []byte(`{"distSpecVersion":"1.1.1"}` + "\n")
	if err := os.WriteFile(cfg, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	w := feed.Writer{Dir: dir, Upstream: "headquarters:5443"}

	if err := w.OnToken("token-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	var cred map[string]map[string]string
	raw, _ := os.ReadFile(filepath.Join(dir, "identity.json"))
	if err := json.Unmarshal(raw, &cred); err != nil || cred["headquarters:5443"]["password"] != "token-1" || cred["headquarters:5443"]["username"] != "abarow" {
		t.Fatalf("kimlik: %s %v", raw, err)
	}
	after1, _ := os.ReadFile(cfg)
	if bytes.Equal(after1, orig) {
		t.Fatal("the configuration file did not change: Zot will not reload")
	}
	if !json.Valid(after1) {
		t.Fatalf("the configuration is corrupt: %q", after1)
	}

	// On every renewal the content changes again and stays valid.
	if err := w.OnToken("token-2", time.Now()); err != nil {
		t.Fatal(err)
	}
	after2, _ := os.ReadFile(cfg)
	if bytes.Equal(after2, after1) || !json.Valid(after2) {
		t.Fatalf("ikinci yenileme: %q → %q", after1, after2)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "identity.json"))
	if !bytes.Contains(raw, []byte("token-2")) {
		t.Fatalf("kimlik yenilenmedi: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "identity.json.tmp")); err == nil {
		t.Error("a temporary file remained")
	}
}

func TestMissingConfigIsNotAnError(t *testing.T) {
	w := feed.Writer{Dir: t.TempDir(), Upstream: "headquarters:5443"}
	if err := w.OnToken("j", time.Now()); err != nil {
		t.Fatalf("error while the mirror is not installed: %v", err)
	}
	if err := (feed.Writer{}).OnToken("j", time.Now()); err == nil {
		t.Fatal("a missing setting was accepted")
	}
}
