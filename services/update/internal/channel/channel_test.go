// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package channel_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"abarow.com/services/update/internal/channel"
	"abarow.com/services/update/internal/token"
)

var now = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

const dig = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func manifest(name string, seq uint64) channel.Manifest {
	return channel.Manifest{
		Channel: name, Version: "0.4.0", Seq: seq, Issued: now.Add(-time.Hour), Expires: now.Add(24 * time.Hour),
		Urgency: channel.UrgencyNormal, Notes: "Hata düzeltmeleri",
		Images: []channel.Image{{Unit: "abarow-sales", Repo: "ghcr.io/abarow/abarow-sales", Digest: dig}},
	}
}

type keys struct {
	build, pilot, prod ed25519.PrivateKey
	trust              channel.Trust
}

func newKeys(t *testing.T) keys {
	t.Helper()
	gen := func() (ed25519.PublicKey, ed25519.PrivateKey) {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return pub, priv
	}
	bp, bk := gen()
	pp, pk := gen()
	rp, rk := gen()
	return keys{build: bk, pilot: pk, prod: rk, trust: channel.Trust{channel.Build: {bp}, channel.Pilot: {pp}, channel.Prod: {rp}}}
}

func TestLevelsAcceptOnlyTheirKeys(t *testing.T) {
	k := newKeys(t)
	signedBy := func(name string, role channel.Role, key ed25519.PrivateKey) channel.Signed {
		s, err := channel.Sign(manifest(name, 5), role, key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, tc := range []struct {
		label   string
		level   token.Level
		channel string
		role    channel.Role
		key     ed25519.PrivateKey
		want    error
	}{
		{"test, derleme anahtarı", token.Test, "test", channel.Build, k.build, nil},
		{"test, pilot anahtarını kabul etmez", token.Test, "test", channel.Pilot, k.pilot, channel.ErrSignature},
		{"pilot, pilot anahtarı", token.Pilot, "pilot", channel.Pilot, k.pilot, nil},
		{"pilot, prod anahtarını da kabul eder", token.Pilot, "pilot", channel.Prod, k.prod, nil},
		{"pilot, derleme anahtarını kabul etmez", token.Pilot, "pilot", channel.Build, k.build, channel.ErrSignature},
		{"prod, prod anahtarı", token.Prod, "prod", channel.Prod, k.prod, nil},
		{"prod, pilot anahtarını kabul etmez", token.Prod, "prod", channel.Pilot, k.pilot, channel.ErrSignature},
		{"prod, derleme anahtarını kabul etmez", token.Prod, "prod", channel.Build, k.build, channel.ErrSignature},
		{"başka kanalın bildirimi", token.Prod, "pilot", channel.Prod, k.prod, channel.ErrChannel},
		{"rolü yalan söyleyen imza", token.Prod, "prod", channel.Prod, k.pilot, channel.ErrSignature},
	} {
		t.Run(tc.label, func(t *testing.T) {
			_, err := channel.Verify(signedBy(tc.channel, tc.role, tc.key), tc.level, k.trust, now, 0)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error %v, want %v", err, tc.want)
			}
		})
	}
}

func TestTamperedPayloadFails(t *testing.T) {
	k := newKeys(t)
	s, _ := channel.Sign(manifest("prod", 5), channel.Prod, k.prod)
	other := manifest("prod", 5)
	other.Images[0].Digest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	raw, _ := json.Marshal(other)
	s.Payload = base64.RawURLEncoding.EncodeToString(raw)
	if _, err := channel.Verify(s, token.Prod, k.trust, now, 0); !errors.Is(err, channel.ErrSignature) {
		t.Fatalf("a modified payload was accepted: %v", err)
	}
}

func TestSecondSignatureAddsPromotion(t *testing.T) {
	k := newKeys(t)
	s, _ := channel.Sign(manifest("pilot", 5), channel.Pilot, k.pilot)
	if _, err := channel.Verify(s, token.Prod, k.trust, now, 0); err == nil {
		t.Fatal("a prod machine should not accept a manifest signed only by pilot (channel and signature)")
	}
	// Promotion: the prod signature is added to the same manifest; a pilot machine still accepts it.
	s = s.Add(channel.Prod, k.prod)
	if len(s.Signatures) != 2 {
		t.Fatalf("signature count %d", len(s.Signatures))
	}
	if _, err := channel.Verify(s, token.Pilot, k.trust, now, 0); err != nil {
		t.Fatal(err)
	}
	// Signing again with the same key replaces the old one.
	if got := len(s.Add(channel.Prod, k.prod).Signatures); got != 2 {
		t.Fatalf("the same key was added a second time: %d", got)
	}
}

func TestRollbackAndExpiry(t *testing.T) {
	k := newKeys(t)
	s, _ := channel.Sign(manifest("prod", 5), channel.Prod, k.prod)
	if _, err := channel.Verify(s, token.Prod, k.trust, now, 6); !errors.Is(err, channel.ErrRollback) {
		t.Fatalf("an old sequence number was accepted: %v", err)
	}
	if _, err := channel.Verify(s, token.Prod, k.trust, now, 5); err != nil {
		t.Fatalf("the same sequence number was rejected: %v", err)
	}
	if _, err := channel.Verify(s, token.Prod, k.trust, now.Add(48*time.Hour), 0); !errors.Is(err, channel.ErrExpired) {
		t.Fatalf("an expired manifest was accepted: %v", err)
	}
}

func TestValidate(t *testing.T) {
	for label, mut := range map[string]func(*channel.Manifest){
		"channel":          func(m *channel.Manifest) { m.Channel = "beta" },
		"sürüm boş":        func(m *channel.Manifest) { m.Version = "" },
		"sürümde boşluk":   func(m *channel.Manifest) { m.Version = "0.4 0" },
		"sıra sıfır":       func(m *channel.Manifest) { m.Seq = 0 },
		"süre geçmişte":    func(m *channel.Manifest) { m.Expires = m.Issued },
		"aciliyet":         func(m *channel.Manifest) { m.Urgency = "acil" },
		"imaj yok":         func(m *channel.Manifest) { m.Images = nil },
		"birim öneki":      func(m *channel.Manifest) { m.Images[0].Unit = "sshd" },
		"birim yol":        func(m *channel.Manifest) { m.Images[0].Unit = "abarow-../x" },
		"birim iki kez":    func(m *channel.Manifest) { m.Images = append(m.Images, m.Images[0]) },
		"depoda etiket":    func(m *channel.Manifest) { m.Images[0].Repo = "ghcr.io/abarow/abarow-sales:latest" },
		"depoda digest":    func(m *channel.Manifest) { m.Images[0].Repo = "ghcr.io/abarow/abarow-sales@" + dig },
		"digest biçimi":    func(m *channel.Manifest) { m.Images[0].Digest = "sha256:abc" },
		"digest algoritma": func(m *channel.Manifest) { m.Images[0].Digest = "md5:" + strings.Repeat("a", 64) },
		"notlar uzun":      func(m *channel.Manifest) { m.Notes = strings.Repeat("a", 5000) },
	} {
		m := manifest("prod", 1)
		m.Images = append([]channel.Image(nil), m.Images...)
		mut(&m)
		if err := m.Validate(); !errors.Is(err, channel.ErrInvalid) {
			t.Errorf("%s: an invalid manifest was accepted: %v", label, err)
		}
	}
	if err := manifest("prod", 1).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestParseTrust(t *testing.T) {
	k := newKeys(t)
	enc := func(pub ed25519.PublicKey) string { return base64.RawURLEncoding.EncodeToString(pub) }
	raw, _ := json.Marshal(channel.TrustFile{Prod: channel.Entries{{Key: enc(k.trust[channel.Prod][0])}}})
	tr, err := channel.ParseTrust(raw)
	if err != nil || len(tr[channel.Prod]) != 1 {
		t.Fatalf("%v %v", tr, err)
	}
	if _, err := channel.ParseTrust([]byte(`{"prod":["kısa"]}`)); err == nil {
		t.Error("an invalid key was accepted")
	}
	if _, err := channel.ParseTrust([]byte(`bozuk`)); err == nil {
		t.Error("a broken file was accepted")
	}
}

func TestUnknownFieldsRejected(t *testing.T) {
	k := newKeys(t)
	raw := []byte(`{"channel":"prod","version":"1","seq":1,"issued":"2026-11-06T11:00:00Z","expires":"2026-11-07T11:00:00Z","urgency":"normal","notes":"","images":[],"fazla":1}`)
	s := channel.Signed{Payload: base64.RawURLEncoding.EncodeToString(raw)}.Add(channel.Prod, k.prod)
	if _, err := channel.Verify(s, token.Prod, k.trust, now, 0); !errors.Is(err, channel.ErrInvalid) {
		t.Fatalf("an unknown field was accepted: %v", err)
	}
}
