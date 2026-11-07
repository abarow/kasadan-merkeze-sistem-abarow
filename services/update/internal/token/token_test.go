// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package token

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func key(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// chain builds the publisher root, the headquarters certificate and headquarters' signing key.
type chain struct {
	rootPub          ed25519.PublicKey
	rootPriv         ed25519.PrivateKey
	headquartersPub  ed25519.PublicKey
	headquartersPriv ed25519.PrivateKey
	cert             string
	devPub           ed25519.PublicKey
	devPriv          ed25519.PrivateKey
}

func setup(t *testing.T) chain {
	t.Helper()
	var z chain
	z.rootPub, z.rootPriv = key(t)
	z.headquartersPub, z.headquartersPriv = key(t)
	z.devPub, z.devPriv = key(t)
	cert, err := IssueCert(z.rootPriv, CertInput{HeadquartersDevice: "ABRW-HEADQUARTERS", SigningKey: z.headquartersPub, NotBefore: now.Add(-time.Hour), Expiry: now.Add(30 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	z.cert = cert
	return z
}

func (z chain) unitToken(t *testing.T, tier Tier, exp time.Time) string {
	t.Helper()
	raw, err := IssueToken(z.headquartersPriv, "ABRW-HEADQUARTERS", Claims{
		Device: "ABRW-REGISTER", Tier: tier, Level: Pilot, Store: "store-1", Register: "register-1",
		Scopes: []string{ScopePull}, Key: Thumbprint(z.devPub), Expiry: exp,
	}, z.cert, now)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestHeadquartersTokenForUnit(t *testing.T) {
	z := setup(t)
	c, err := Verify(z.rootPub, z.unitToken(t, Register, now.Add(24*time.Hour)), now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Device != "ABRW-REGISTER" || c.Tier != Register || c.Level != Pilot || c.Store != "store-1" || c.Issuer != "ABRW-HEADQUARTERS" || !c.Allows(ScopePull) {
		t.Errorf("content: %+v", c)
	}
	if c.Allows("push") {
		t.Error("a permission that was not given appears present")
	}
}

func TestPublisherTokenForHeadquarters(t *testing.T) {
	z := setup(t)
	raw, err := IssueToken(z.rootPriv, PublisherIssuer, Claims{Device: "ABRW-HEADQUARTERS", Tier: Headquarters, Level: Prod, Scopes: []string{ScopePull}, Key: Thumbprint(z.devPub), Expiry: now.Add(time.Hour)}, "", now)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Verify(z.rootPub, raw, now)
	if err != nil || c.Tier != Headquarters || c.Issuer != PublisherIssuer {
		t.Fatalf("publisher token: %+v, %v", c, err)
	}
}

func TestVerifyRejects(t *testing.T) {
	z := setup(t)
	other, bpriv := key(t)
	valid := z.unitToken(t, Register, now.Add(24*time.Hour))

	// Headquarters cannot issue a token for the headquarters tier.
	headquartersToken, err := IssueToken(z.headquartersPriv, "ABRW-HEADQUARTERS", Claims{Device: "X", Tier: Headquarters, Scopes: []string{ScopePull}, Key: "k", Expiry: now.Add(time.Hour)}, z.cert, now)
	if err != nil {
		t.Fatal(err)
	}
	// Someone who is not the certificate owner forges a token by attaching the same certificate.
	_, fakePriv := key(t)
	fake, err := IssueToken(fakePriv, "ABRW-HEADQUARTERS", Claims{Device: "X", Tier: Register, Scopes: []string{ScopePull}, Key: "k", Expiry: now.Add(time.Hour)}, z.cert, now)
	if err != nil {
		t.Fatal(err)
	}
	// Another publisher root (a fake root) issues a certificate.
	fakeCert, err := IssueCert(bpriv, CertInput{HeadquartersDevice: "ABRW-HEADQUARTERS", SigningKey: z.headquartersPub, NotBefore: now.Add(-time.Hour), Expiry: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	fakeRoot, err := IssueToken(z.headquartersPriv, "ABRW-HEADQUARTERS", Claims{Device: "X", Tier: Register, Scopes: []string{ScopePull}, Key: "k", Expiry: now.Add(time.Hour)}, fakeCert, now)
	if err != nil {
		t.Fatal(err)
	}
	// A token cut in the name of another headquarters (the issuer does not match the certificate owner).
	otherIssuer, err := IssueToken(z.headquartersPriv, "ABRW-BASKA", Claims{Device: "X", Tier: Register, Scopes: []string{ScopePull}, Key: "k", Expiry: now.Add(time.Hour)}, z.cert, now)
	if err != nil {
		t.Fatal(err)
	}
	// The certificate has expired.
	oldCert, err := IssueCert(z.rootPriv, CertInput{HeadquartersDevice: "ABRW-HEADQUARTERS", SigningKey: z.headquartersPub, NotBefore: now.Add(-48 * time.Hour), Expiry: now.Add(-24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	oldCertToken, err := IssueToken(z.headquartersPriv, "ABRW-HEADQUARTERS", Claims{Device: "X", Tier: Register, Scopes: []string{ScopePull}, Key: "k", Expiry: now.Add(time.Hour)}, oldCert, now)
	if err != nil {
		t.Fatal(err)
	}
	// A token with its payload changed.
	p := strings.Split(valid, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(p[1])
	mutated := strings.Replace(string(payload), `"tier":"register"`, `"tier":"store"`, 1)
	if mutated == string(payload) {
		t.Fatal("the test payload did not change")
	}
	tampered := p[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(mutated)) + "." + p[2]
	// alg=none
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	none := hdr + "." + p[1] + "."

	cases := []struct {
		label string
		raw   string
		root  ed25519.PublicKey
		sim   time.Time
		want  error
	}{
		{"süresi dolmuş jeton", z.unitToken(t, Register, now.Add(time.Hour)), z.rootPub, now.Add(2 * time.Hour), ErrExpired},
		{"yanlış kök", valid, other, now, ErrInvalid},
		{"kurcalanmış içerik", tampered, z.rootPub, now, ErrInvalid},
		{"alg none", none, z.rootPub, now, ErrInvalid},
		{"çöp", "abc.def.ghi", z.rootPub, now, ErrInvalid},
		{"merkez jetonu merkezden", headquartersToken, z.rootPub, now, ErrNotAllowed},
		{"sertifikasız imzalayan", fake, z.rootPub, now, ErrInvalid},
		{"sahte kökün sertifikası", fakeRoot, z.rootPub, now, ErrInvalid},
		{"başka merkez adı", otherIssuer, z.rootPub, now, ErrInvalid},
		{"süresi dolmuş sertifika", oldCertToken, z.rootPub, now, ErrExpired},
	}
	for _, c := range cases {
		if _, err := Verify(c.root, c.raw, c.sim); !errors.Is(err, c.want) {
			t.Errorf("%s: error %v, want %v", c.label, err, c.want)
		}
	}
}

func TestCertificateDoesNotReplaceToken(t *testing.T) {
	z := setup(t)
	if _, err := Verify(z.rootPub, z.cert, now); !errors.Is(err, ErrInvalid) {
		t.Errorf("sertifika jeton gibi kabul edildi: %v", err)
	}
}

func TestPossession(t *testing.T) {
	z := setup(t)
	c, err := Verify(z.rootPub, z.unitToken(t, Register, now.Add(time.Hour)), now)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(z.devPriv, PossessionMessage(c.Device, "nonce-1"))
	if err := VerifyPossession(c, z.devPub, "nonce-1", sig); err != nil {
		t.Errorf("valid ownership was rejected: %v", err)
	}
	if err := VerifyPossession(c, z.devPub, "nonce-2", sig); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("another nonce was accepted: %v", err)
	}
	otherPub, otherPriv := key(t)
	if err := VerifyPossession(c, otherPub, "nonce-1", ed25519.Sign(otherPriv, PossessionMessage(c.Device, "nonce-1"))); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("another device key was accepted: %v", err)
	}
}

func TestIssueRejectsBadInput(t *testing.T) {
	z := setup(t)
	if _, err := IssueToken(z.rootPriv, "x", Claims{Device: "d", Key: "k", Expiry: now.Add(time.Hour)}, "", now); err == nil {
		t.Error("an unauthorized token was produced")
	}
	if _, err := IssueToken(z.rootPriv, "x", Claims{Device: "d", Key: "k", Scopes: []string{ScopePull}, Expiry: now.Add(-time.Hour)}, "", now); err == nil {
		t.Error("an expired token was produced")
	}
	if _, err := IssueCert(z.rootPriv, CertInput{HeadquartersDevice: "m", SigningKey: z.headquartersPub, NotBefore: now, Expiry: now}); err == nil {
		t.Error("a certificate with no expiry was produced")
	}
}

func TestCertSubject(t *testing.T) {
	z := setup(t)
	if sub, err := CertSubject(z.cert); err != nil || sub != "ABRW-HEADQUARTERS" {
		t.Errorf("sertifika sahibi %q, %v", sub, err)
	}
	if _, err := CertSubject(z.unitToken(t, Register, now.Add(time.Hour))); !errors.Is(err, ErrInvalid) {
		t.Errorf("jeton sertifika gibi okundu: %v", err)
	}
	if _, err := CertSubject("çöp"); !errors.Is(err, ErrInvalid) {
		t.Errorf("garbage certificate: %v", err)
	}
}
