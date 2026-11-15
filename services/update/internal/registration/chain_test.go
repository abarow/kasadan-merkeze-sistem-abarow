// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package registration

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"testing"
	"time"

	"abarow.com/services/update/internal/identity"
	"abarow.com/services/update/internal/token"
)

// The publisher approves headquarters and gives it a certificate; headquarters gives its own units tokens.
// A unit token must be verifiable only with the publisher root.
func TestPublisherToHeadquartersToUnitChain(t *testing.T) {
	ctx := context.Background()
	rootPub, rootPriv, _ := ed25519.GenerateKey(nil)
	fakeClock := t0

	publisher, err := New(Config{
		Store: NewMemory(), Issuer: SigningIssuer{Key: rootPriv, ID: token.PublisherIssuer},
		Allowed: []token.Tier{token.Headquarters}, TTL: 24 * time.Hour, CertTTL: 90 * 24 * time.Hour, Now: func() time.Time { return fakeClock },
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1) Headquarters registers with the publisher with its own device identity and a separate token signing key.
	headquarters := unitDevice(t, "headquarters-makinesi")
	signPub, signPriv, _ := ed25519.GenerateKey(nil)
	in := Submission{
		Device: headquarters.ID, Public: headquarters.Public, SigningKey: signPub, Fingerprint: string(headquarters.Fingerprint),
		Tier: token.Headquarters, Level: token.Prod, Name: "Merkez",
	}
	in.Signature = headquarters.Sign(SubmitMessage(in.Device, in.Tier, in.Level, in.Name, in.SigningKey))
	if _, err := publisher.Submit(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := publisher.Approve(ctx, "publisher-operatoru", headquarters.ID, identity.Code(headquarters.ID)); err != nil {
		t.Fatal(err)
	}
	nonce, err := publisher.Challenge(ctx, headquarters.ID)
	if err != nil {
		t.Fatal(err)
	}
	mres, err := publisher.Token(ctx, headquarters.ID, nonce, headquarters.Sign(token.PossessionMessage(headquarters.ID, nonce)))
	if err != nil {
		t.Fatal(err)
	}
	if mres.Cert == "" {
		t.Fatal("no certificate was given to the approved headquarters")
	}
	if c, err := token.Verify(rootPub, mres.Token, fakeClock); err != nil || c.Tier != token.Headquarters || c.Issuer != token.PublisherIssuer {
		t.Fatalf("headquarters token: %+v, %v", c, err)
	}

	// 2) The headquarters service approves its own units with the certificate and gives tokens.
	mst := NewMemory()
	headquartersSvc, err := New(Config{
		Store: mst, Issuer: SigningIssuer{Key: signPriv, ID: headquarters.ID, Chain: mres.Cert},
		Allowed: []token.Tier{token.Store, token.Register}, TTL: 7 * 24 * time.Hour, Now: func() time.Time { return fakeClock },
	})
	if err != nil {
		t.Fatal(err)
	}
	o := env{svc: headquartersSvc, store: mst, rootPub: rootPub, fakeClock: &fakeClock}
	register := unitDevice(t, "register-makinesi")
	raw := fullFlow(t, o, register)
	c, err := token.Verify(rootPub, raw, fakeClock)
	if err != nil {
		t.Fatalf("the register token could not be verified with the publisher root: %v", err)
	}
	if c.Issuer != headquarters.ID || c.Device != register.ID || c.Tier != token.Register {
		t.Errorf("register token: %+v", c)
	}

	// 3) Headquarters' own key signs tokens, but without the certificate or under another root they do not pass.
	otherRoot, _, _ := ed25519.GenerateKey(nil)
	if _, err := token.Verify(otherRoot, raw, fakeClock); !errors.Is(err, token.ErrInvalid) {
		t.Errorf("verified with another root: %v", err)
	}
}

func TestHeadquartersTierRequiresSigningKey(t *testing.T) {
	ctx := context.Background()
	_, rootPriv, _ := ed25519.GenerateKey(nil)
	publisher, _ := New(Config{Store: NewMemory(), Issuer: SigningIssuer{Key: rootPriv, ID: token.PublisherIssuer}, Allowed: []token.Tier{token.Headquarters}, TTL: time.Hour, CertTTL: time.Hour})
	m := unitDevice(t, "m")
	in := Submission{Device: m.ID, Public: m.Public, Fingerprint: string(m.Fingerprint), Tier: token.Headquarters, Level: token.Prod, Name: "M"}
	in.Signature = m.Sign(SubmitMessage(in.Device, in.Tier, in.Level, in.Name, nil))
	if _, err := publisher.Submit(ctx, in); !errors.Is(err, ErrBadRequest) {
		t.Errorf("headquarters registration with no signing key: %v", err)
	}
	// A signing key cannot be given at a tier other than headquarters.
	_, err := New(Config{Store: NewMemory(), Issuer: SigningIssuer{Key: rootPriv}, Allowed: []token.Tier{token.Headquarters}, TTL: time.Hour})
	if err == nil {
		t.Error("a headquarters service without CertTTL was set up")
	}
}

func TestOnlyRootCanCertify(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	pub, _, _ := ed25519.GenerateKey(nil)
	if _, err := (SigningIssuer{Key: priv, ID: "m", Chain: "x.y.z"}).Certify(token.CertInput{HeadquartersDevice: "m", SigningKey: pub, NotBefore: t0, Expiry: t0.Add(time.Hour)}); err == nil {
		t.Error("a signer with a chain (headquarters) gave a certificate")
	}
}

func TestPendingLimit(t *testing.T) {
	o := setup(t)
	o.svc.maxPending = 3
	ctx := context.Background()
	var first *identity.Identity
	for n, label := range []string{"a", "b", "c", "d"} {
		i := unitDevice(t, label)
		if first == nil {
			first = i
		}
		_, err := o.svc.Submit(ctx, request(i, token.Register, token.Prod))
		if n < 3 && err != nil {
			t.Fatalf("%d. istek: %v", n+1, err)
		}
		if n == 3 && !errors.Is(err, ErrTooMany) {
			t.Errorf("when the limit is exceeded error %v, want ErrTooMany", err)
		}
	}
	// A repeated request for an existing pending registration is not affected by the limit.
	if _, err := o.svc.Submit(ctx, request(first, token.Register, token.Prod)); err != nil {
		t.Errorf("a repeated request was rejected at the limit: %v", err)
	}
}

// If two approvers approve the same registration at the same time, or try a wrong code, the update is not lost.
func TestConcurrentDecisionsNoLostUpdate(t *testing.T) {
	o := setup(t)
	ctx := context.Background()
	i := unitDevice(t, "m")
	if _, err := o.svc.Submit(ctx, request(i, token.Register, token.Prod)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := 0; n < 20; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = o.svc.Approve(ctx, "yonetici", i.ID, "AAAA-AAAA") // wrong code
		}()
	}
	wg.Wait()
	r, _ := o.store.Get(ctx, i.ID)
	// Every successful write counts one attempt; those that conflict get ErrConflict, the counter does not go back.
	if r.Attempts < 1 || r.Attempts > 20 {
		t.Errorf("attempt count %d", r.Attempts)
	}
	if r.Attempts >= MaxCodeAttempts && r.Status != Rejected {
		t.Errorf("the limit was exceeded but the status is %s", r.Status)
	}
}

func TestMemoryUpdateConflict(t *testing.T) {
	b := NewMemory()
	ctx := context.Background()
	r := Request{Device: "d", Status: Pending}
	if err := b.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := b.Create(ctx, r); !errors.Is(err, ErrExists) {
		t.Errorf("repeated registration: %v", err)
	}
	approval := r
	approval.Status = Approved
	if err := b.Update(ctx, r, approval); err != nil {
		t.Fatal(err)
	}
	// The second update with the old view conflicts.
	revoked := r
	revoked.Status = Revoked
	if err := b.Update(ctx, r, revoked); !errors.Is(err, ErrConflict) {
		t.Errorf("update with an old view: %v", err)
	}
	if err := b.Update(ctx, Request{Device: "yok"}, approval); !errors.Is(err, ErrNotFound) {
		t.Errorf("updating a non-existent registration: %v", err)
	}
}
