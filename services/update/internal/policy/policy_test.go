// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package policy_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"abarow.com/services/update/internal/channel"
	"abarow.com/services/update/internal/policy"
	"abarow.com/services/update/internal/token"
)

func pemKey(t *testing.T) string {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func keys(t *testing.T) policy.Keys {
	return policy.Keys{channel.Build: {pemKey(t)}, channel.Pilot: {pemKey(t)}, channel.Prod: {pemKey(t)}}
}

type doc struct {
	Default    []map[string]any `json:"default"`
	Transports struct {
		Docker map[string][]struct {
			Type         string   `json:"type"`
			KeyDatas     []string `json:"keyDatas"`
			SignedIdenty struct {
				Type, Prefix, SignedPrefix string
			} `json:"signedIdentity"`
		} `json:"docker"`
	} `json:"transports"`
}

func TestGeneratePerLevel(t *testing.T) {
	k := keys(t)
	b64 := func(r channel.Role) string { return base64.StdEncoding.EncodeToString([]byte(k[r][0])) }
	for _, tc := range []struct {
		level token.Level
		want  []string
	}{
		{token.Test, []string{b64(channel.Build)}},
		{token.Pilot, []string{b64(channel.Pilot), b64(channel.Prod)}},
		{token.Prod, []string{b64(channel.Prod)}},
	} {
		f, err := policy.Generate(tc.level, "store.lan:5443", k)
		if err != nil {
			t.Fatal(err)
		}
		var d doc
		if err := json.Unmarshal(f.Policy, &d); err != nil {
			t.Fatalf("%s: %v\n%s", tc.level, err, f.Policy)
		}
		scope := "store.lan:5443/" + string(tc.level)
		req := d.Transports.Docker[scope]
		if len(req) != 1 || req[0].Type != "sigstoreSigned" || strings.Join(req[0].KeyDatas, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: kapsam %s: %+v", tc.level, scope, req)
		}
		if id := req[0].SignedIdenty; id.Type != "remapIdentity" || id.Prefix != scope || id.SignedPrefix != policy.SignedPrefix {
			t.Errorf("%s: kimlik: %+v", tc.level, id)
		}
		// The rest of the mirror is rejected; the other channel's path too.
		if h := d.Transports.Docker["store.lan:5443"]; len(h) != 1 || h[0].Type != "reject" {
			t.Errorf("%s: ayna reddedilmiyor: %+v", tc.level, h)
		}
		if len(d.Transports.Docker) != 2 {
			t.Errorf("%s: beklenmeyen kapsamlar: %v", tc.level, d.Transports.Docker)
		}
		// Images outside the mirror (postgres, nats...) are not affected.
		if len(d.Default) != 1 || d.Default[0]["type"] != "insecureAcceptAnything" {
			t.Errorf("%s: default: %v", tc.level, d.Default)
		}
		if !strings.Contains(string(f.RegistriesD), "use-sigstore-attachments: true") || !strings.Contains(string(f.RegistriesD), "store.lan:5443:") {
			t.Errorf("%s: registries.d: %s", tc.level, f.RegistriesD)
		}
		if !policy.Ours(f.Policy) {
			t.Errorf("%s: the generated policy does not carry its own marker", tc.level)
		}
	}
}

func TestGenerateRefusesPartialTrust(t *testing.T) {
	// The pilot level asks for the pilot and prod keys; if one is missing, no half verification is done.
	if _, err := policy.Generate(token.Pilot, "m:5443", policy.Keys{channel.Pilot: {pemKey(t)}}); err == nil {
		t.Error("a policy was generated with a missing key")
	}
	if _, err := policy.Generate(token.Prod, "m:5443", policy.Keys{}); err == nil {
		t.Error("a policy was generated with no key")
	}
	for _, bad := range []string{"", "m:5443/prod", "m 5443", "m:5443\"", "https://m:5443", "m:99999999"} {
		if _, err := policy.Generate(token.Prod, bad, keys(t)); err == nil {
			t.Errorf("the mirror address %q was accepted", bad)
		}
	}
}

func TestParseKeys(t *testing.T) {
	good, _ := json.Marshal(map[string]any{"cosign": map[string]string{"prod": pemKey(t)}})
	k, err := policy.ParseKeys(good, time.Now())
	if err != nil || len(k) != 1 {
		t.Fatalf("%v %v", k, err)
	}
	for label, raw := range map[string]string{
		"bozuk json":     `{`,
		"pem değil":      `{"cosign":{"prod":"merhaba"}}`,
		"özel anahtar":   `{"cosign":{"prod":"-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"}}`,
		"bozuk anahtar":  `{"cosign":{"prod":"-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n"}}`,
		"bilinmeyen rol": `{"cosign":{"root":"x"}}`,
	} {
		if _, err := policy.ParseKeys([]byte(raw), time.Now()); err == nil {
			t.Errorf("%s: accepted", label)
		}
	}
	// A trust file with no cosign section is valid (signature verification is not asked for).
	if k, err := policy.ParseKeys([]byte(`{}`), time.Now()); err != nil || len(k) != 0 {
		t.Errorf("file with no cosign: %v %v", k, err)
	}
}

func TestInstall(t *testing.T) {
	dir := t.TempDir()
	f, err := policy.Generate(token.Prod, "m:5443", keys(t))
	if err != nil {
		t.Fatal(err)
	}
	if ch, err := policy.Install(dir, f); err != nil || !ch {
		t.Fatalf("first write: %v %v", ch, err)
	}
	if ch, err := policy.Install(dir, f); err != nil || ch {
		t.Fatalf("the same content was written again: %v %v", ch, err)
	}
	// It is updated when a key changes (e.g. key rotation).
	f2, _ := policy.Generate(token.Prod, "m:5443", keys(t))
	if ch, err := policy.Install(dir, f2); err != nil || !ch {
		t.Fatalf("update: %v %v", ch, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "registries.d", "abarow.yaml")); err != nil {
		t.Error(err)
	}

	// Someone else's policy.json is not overwritten.
	other := t.TempDir()
	foreign := []byte(`{"default":[{"type":"reject"}]}`)
	if err := os.WriteFile(filepath.Join(other, "policy.json"), foreign, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Install(other, f); !errors.Is(err, policy.ErrForeign) {
		t.Fatalf("someone else's file: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(other, "policy.json")); string(got) != string(foreign) {
		t.Error("someone else's policy.json changed")
	}
	if _, err := os.Stat(filepath.Join(other, "registries.d", "abarow.yaml")); err == nil {
		t.Error("registries.d was written in a rejected install")
	}
}

// Key rotation: in the transition window the old and new keys are accepted together,
// when the old one drops out only the new one remains; if the level has no key left ErrNoKey is returned.
func TestRotationWindowAndLock(t *testing.T) {
	oldK, newK := pemKey(t), pemKey(t)
	cut := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	retire := cut.Add(14 * 24 * time.Hour)
	raw, _ := json.Marshal(map[string]any{"cosign": map[string]any{
		"prod": []any{
			map[string]any{"key": oldK, "not_after": retire},
			map[string]any{"key": newK, "not_before": cut},
		},
	}})
	datas := func(at time.Time) []string {
		t.Helper()
		k, err := policy.ParseKeys(raw, at)
		if err != nil {
			t.Fatal(err)
		}
		f, err := policy.Generate(token.Prod, "m:5443", k)
		if err != nil {
			t.Fatal(err)
		}
		var d doc
		if err := json.Unmarshal(f.Policy, &d); err != nil {
			t.Fatal(err)
		}
		return d.Transports.Docker["m:5443/prod"][0].KeyDatas
	}
	b64 := func(p string) string { return base64.StdEncoding.EncodeToString([]byte(p)) }
	if got := datas(cut.Add(-time.Hour)); len(got) != 1 || got[0] != b64(oldK) {
		t.Errorf("before the transition: %v", got)
	}
	if got := datas(cut.Add(time.Hour)); len(got) != 2 {
		t.Errorf("two keys are expected in the transition window: %v", got)
	}
	if got := datas(retire); len(got) != 1 || got[0] != b64(newK) {
		t.Errorf("after the old one dropped: %v", got)
	}

	// If the old one expires without a new key ever being added: lock.
	only, _ := json.Marshal(map[string]any{"cosign": map[string]any{"prod": map[string]any{"key": oldK, "not_after": retire}}})
	k, err := policy.ParseKeys(only, retire)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Generate(token.Prod, "m:5443", k); !errors.Is(err, policy.ErrNoKey) {
		t.Fatalf("no key left but %v", err)
	}
	locked, err := policy.Locked("m:5443")
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Ours(locked.Policy) {
		t.Error("the locked policy does not carry its own marker")
	}
	var d doc
	if err := json.Unmarshal(locked.Policy, &d); err != nil {
		t.Fatal(err)
	}
	if h := d.Transports.Docker["m:5443"]; len(h) != 1 || h[0].Type != "reject" {
		t.Errorf("the locked policy does not reject the mirror: %+v", h)
	}
	if _, err := policy.Locked("kötü adres"); err == nil {
		t.Error("an invalid mirror was accepted")
	}
	// The locked policy can be overwritten when a valid key comes; someone else's file is still not overwritten.
	dir := t.TempDir()
	if _, err := policy.Install(dir, locked); err != nil {
		t.Fatal(err)
	}
	good, _ := policy.Generate(token.Prod, "m:5443", policy.Keys{channel.Prod: {newK}})
	if ch, err := policy.Install(dir, good); err != nil || !ch {
		t.Fatalf("the lock could not be lifted: %v %v", ch, err)
	}
	foreign := t.TempDir()
	if err := os.WriteFile(filepath.Join(foreign, "policy.json"), []byte(`{"default":[{"type":"reject"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Install(foreign, locked); !errors.Is(err, policy.ErrForeign) {
		t.Fatalf("someone else's reject policy was overwritten: %v", err)
	}
}
