// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package channel_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"abarow.com/services/update/internal/channel"
)

func edKey(t *testing.T) (string, ed25519.PublicKey) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(pub), pub
}

func cosignPEM(t *testing.T) string {
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

func js(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Key rotation: the old key time-limited, the new key future-dated; both are valid in the transition window.
func TestManifestKeyRotationWindow(t *testing.T) {
	oldS, oldK := edKey(t)
	newS, newK := edKey(t)
	cut := now.Add(24 * time.Hour)        // the new key starts tomorrow
	retire := now.Add(7 * 24 * time.Hour) // the old key drops out a week later
	raw := js(t, map[string]any{
		"prod": []any{
			map[string]any{"key": oldS, "not_after": retire},
			map[string]any{"key": newS, "not_before": cut},
		},
	})

	has := func(tr channel.Trust, k ed25519.PublicKey) bool {
		for _, c := range tr[channel.Prod] {
			if c.Equal(k) {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		name     string
		at       time.Time
		old, new bool
	}{
		{"geçişten önce", now, true, false},
		{"geçiş penceresi", cut.Add(time.Hour), true, true},
		{"eski düştükten sonra", retire, false, true},
		{"not_after dahil değil", retire.Add(-time.Nanosecond), true, true},
	} {
		tr, err := channel.ParseTrustAt(raw, tc.at)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if has(tr, oldK) != tc.old || has(tr, newK) != tc.new {
			t.Errorf("%s: eski=%v yeni=%v, istenen %v/%v", tc.name, has(tr, oldK), has(tr, newK), tc.old, tc.new)
		}
	}
}

func TestTrustAcceptsAllForms(t *testing.T) {
	a, _ := edKey(t)
	b, _ := edKey(t)
	c, _ := edKey(t)
	raw := js(t, map[string]any{
		"build": a,
		"pilot": map[string]any{"key": b},
		"prod":  []any{c, map[string]any{"key": a}},
	})
	tr, err := channel.ParseTrustAt(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr[channel.Build]) != 1 || len(tr[channel.Pilot]) != 1 || len(tr[channel.Prod]) != 2 {
		t.Fatalf("key counts: %v", tr)
	}
}

func TestTrustRejectsMistakes(t *testing.T) {
	good, _ := edKey(t)
	for name, raw := range map[string]string{
		"bilinmeyen alan":       `{"prod":[{"key":"` + good + `","not_befor":"2026-01-01T00:00:00Z"}]}`,
		"kısa anahtar":          `{"prod":["abc"]}`,
		"pencere ters":          `{"prod":[{"key":"` + good + `","not_before":"2026-12-01T00:00:00Z","not_after":"2026-11-01T00:00:00Z"}]}`,
		"cosign bilinmeyen rol": `{"cosign":{"root":"x"}}`,
		"cosign pem değil":      `{"cosign":{"prod":"merhaba"}}`,
		"cosign özel anahtar":   `{"cosign":{"prod":"-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"}}`,
		"cosign pencere ters":   `{"cosign":{"prod":{"key":"X","not_before":"2026-12-01T00:00:00Z","not_after":"2026-11-01T00:00:00Z"}}}`,
		"bilinmeyen üst alan":   `{"prodd":["` + good + `"]}`,
	} {
		if _, err := channel.ParseTrustAt([]byte(raw), now); err == nil {
			if _, err := channel.CosignAt([]byte(raw), now); err == nil {
				t.Errorf("%s: kabul edildi", name)
			}
		}
	}
}

// A key that has expired or not started yet is also verified against typos.
func TestExpiredKeysStillValidated(t *testing.T) {
	raw := `{"prod":[{"key":"kısa","not_after":"2020-01-01T00:00:00Z"}]}`
	if _, err := channel.ParseTrustAt([]byte(raw), now); err == nil {
		t.Error("an expired but corrupt key was silently dropped")
	}
}

func TestCosignRotationWindow(t *testing.T) {
	oldP, newP := cosignPEM(t), cosignPEM(t)
	cut := now.Add(time.Hour)
	retire := now.Add(48 * time.Hour)
	raw := js(t, map[string]any{"cosign": map[string]any{
		"prod": []any{
			map[string]any{"key": oldP, "not_after": retire},
			map[string]any{"key": newP, "not_before": cut},
		},
		"pilot": cosignPEM(t),
	}})
	for _, tc := range []struct {
		at   time.Time
		want []string
	}{
		{now, []string{oldP}},
		{cut, []string{oldP, newP}},
		{retire, []string{newP}},
	} {
		got, err := channel.CosignAt(raw, tc.at)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(got[channel.Prod], "|") != strings.Join(tc.want, "|") {
			t.Errorf("%v: prod keys %d, want %d", tc.at, len(got[channel.Prod]), len(tc.want))
		}
		if len(got[channel.Pilot]) != 1 {
			t.Errorf("%v: pilot: %v", tc.at, got[channel.Pilot])
		}
	}
	// When all drop out the role is left empty (the caller treats this as a lock).
	got, err := channel.CosignAt(raw, retire.Add(1000*time.Hour))
	if err != nil || len(got[channel.Pilot]) != 1 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestInspectReportsStates(t *testing.T) {
	s1, _ := edKey(t)
	s2, _ := edKey(t)
	s3, _ := edKey(t)
	raw := js(t, map[string]any{
		"prod": []any{
			map[string]any{"key": s1, "not_after": now.Add(-time.Hour)},
			map[string]any{"key": s2},
			map[string]any{"key": s3, "not_before": now.Add(time.Hour)},
		},
		"cosign": map[string]any{"prod": cosignPEM(t)},
	})
	infos, err := channel.Inspect(raw, now)
	if err != nil {
		t.Fatal(err)
	}
	count := map[channel.KeyState]int{}
	for _, k := range infos {
		count[k.State]++
		if k.ID == "" || k.Role != channel.Prod {
			t.Errorf("bilgi: %+v", k)
		}
	}
	if len(infos) != 4 || count[channel.KeyExpired] != 1 || count[channel.KeyPending] != 1 || count[channel.KeyValid] != 2 {
		t.Fatalf("durumlar: %v (%d)", count, len(infos))
	}
	if infos[0].Kind != "cosign" {
		t.Errorf("order: %+v", infos[0])
	}
	if _, err := channel.Inspect([]byte(`{"prod":["kısa"]}`), now); err == nil {
		t.Error("bozuk anahtar kabul edildi")
	}
}
