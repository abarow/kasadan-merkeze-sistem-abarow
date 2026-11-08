// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package registration

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"abarow.com/services/update/internal/identity"
	"abarow.com/services/update/internal/token"
)

var t0 = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

type env struct {
	svc       *Service
	store     *Memory
	rootPub   ed25519.PublicKey
	fakeClock *time.Time
}

// setup builds the headquarters service (approves store servers and registers) with the headquarters certificate.
func setup(t *testing.T) env {
	t.Helper()
	rootPub, rootPriv, _ := ed25519.GenerateKey(nil)
	headquartersPub, headquartersPriv, _ := ed25519.GenerateKey(nil)
	cert, err := token.IssueCert(rootPriv, token.CertInput{HeadquartersDevice: "ABRW-HEADQUARTERS", SigningKey: headquartersPub, NotBefore: t0.Add(-time.Hour), Expiry: t0.Add(90 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	fakeClock := t0
	st := NewMemory()
	svc, err := New(Config{
		Store: st, Issuer: SigningIssuer{Key: headquartersPriv, ID: "ABRW-HEADQUARTERS", Chain: cert},
		Allowed: []token.Tier{token.Store, token.Register}, TTL: 7 * 24 * time.Hour, Now: func() time.Time { return fakeClock },
	})
	if err != nil {
		t.Fatal(err)
	}
	return env{svc: svc, store: st, rootPub: rootPub, fakeClock: &fakeClock}
}

func unitDevice(t *testing.T, label string) *identity.Identity {
	t.Helper()
	i, err := identity.Generate(identity.NewFingerprint(map[string]string{"machine-id": label}), nil)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func request(i *identity.Identity, tier token.Tier, level token.Level) Submission {
	return Submission{
		Device: i.ID, Public: i.Public, Fingerprint: string(i.Fingerprint), Tier: tier, Level: level,
		Store: "store-1", Register: "register-1", Name: "Kasa 1", Signature: i.Sign(SubmitMessage(i.ID, tier, level, "Kasa 1", nil)),
	}
}

// fullFlow runs the flow from registration to token.
func fullFlow(t *testing.T, o env, i *identity.Identity) string {
	t.Helper()
	ctx := context.Background()
	if _, err := o.svc.Submit(ctx, request(i, token.Register, token.Pilot)); err != nil {
		t.Fatal(err)
	}
	if err := o.svc.Approve(ctx, "yonetici", i.ID, identity.Code(i.ID)); err != nil {
		t.Fatal(err)
	}
	nonce, err := o.svc.Challenge(ctx, i.ID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := o.svc.Token(ctx, i.ID, nonce, i.Sign(token.PossessionMessage(i.ID, nonce)))
	if err != nil {
		t.Fatal(err)
	}
	return res.Token
}

func TestFullFlow(t *testing.T) {
	o := setup(t)
	i := unitDevice(t, "register-1-makinesi")
	raw := fullFlow(t, o, i)

	c, err := token.Verify(o.rootPub, raw, *o.fakeClock)
	if err != nil {
		t.Fatalf("the token produced could not be verified with the publisher root: %v", err)
	}
	if c.Device != i.ID || c.Tier != token.Register || c.Level != token.Pilot || c.Store != "store-1" || !c.Allows(token.ScopePull) {
		t.Errorf("token content: %+v", c)
	}
	if c.Key != token.Thumbprint(i.Public) {
		t.Error("the token is not bound to the device key")
	}
	if want := o.fakeClock.Add(7 * 24 * time.Hour); !c.Expiry.Equal(want) {
		t.Errorf("expiry %v, want %v", c.Expiry, want)
	}
}

func TestPendingAndIdempotent(t *testing.T) {
	o := setup(t)
	ctx := context.Background()
	i := unitDevice(t, "m")
	r, err := o.svc.Submit(ctx, request(i, token.Store, token.Prod))
	if err != nil || r.Status != Pending || r.Level != token.Prod {
		t.Fatalf("registration: %+v, %v", r, err)
	}
	if list, _ := o.svc.List(ctx, Pending); len(list) != 1 || list[0].Device != i.ID {
		t.Errorf("bekleyenler: %+v", list)
	}
	// No challenge or token is given without approval.
	if _, err := o.svc.Challenge(ctx, i.ID); !errors.Is(err, ErrPending) {
		t.Errorf("challenge without approval: %v", err)
	}
	// If the same request comes again the same registration is returned, no new registration is opened.
	if r2, err := o.svc.Submit(ctx, request(i, token.Store, token.Prod)); err != nil || r2.CreatedAt != r.CreatedAt {
		t.Errorf("yinelenen istek: %+v, %v", r2, err)
	}
}

func TestSubmitRejects(t *testing.T) {
	o := setup(t)
	ctx := context.Background()
	i := unitDevice(t, "m")
	other := unitDevice(t, "baska")

	wrongIdentity := request(i, token.Register, token.Prod)
	wrongIdentity.Device = other.ID // the id is not derived from the key and fingerprint
	wrongIdentity.Signature = i.Sign(SubmitMessage(other.ID, token.Register, token.Prod, "Kasa 1", nil))

	unsigned := request(i, token.Register, token.Prod)
	unsigned.Signature = nil

	otherSender := request(i, token.Register, token.Prod)
	otherSender.Signature = other.Sign(SubmitMessage(i.ID, token.Register, token.Prod, "Kasa 1", nil))

	mutationName := request(i, token.Register, token.Prod)
	mutationName.Name = "Başka ad"

	headquartersTier := request(i, token.Headquarters, token.Prod) // this service approves only store servers and registers
	badLevel := request(i, token.Register, token.Level("canli"))
	noLevel := request(i, token.Register, "")
	noFingerprint := request(i, token.Register, token.Prod)
	noFingerprint.Fingerprint = ""

	cases := map[string]Submission{
		"türetilmeyen kimlik": wrongIdentity, "imzasız": unsigned, "başka anahtarın imzası": otherSender,
		"imzadan sonra değişen ad": mutationName, "izinsiz katman": headquartersTier, "bilinmeyen seviye": badLevel, "seviye yok": noLevel, "parmak izi yok": noFingerprint,
	}
	for label, in := range cases {
		if _, err := o.svc.Submit(ctx, in); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: error %v, want ErrBadRequest", label, err)
		}
	}
	if list, _ := o.svc.List(ctx, Pending); len(list) != 0 {
		t.Errorf("reddedilen istekler kayda girdi: %+v", list)
	}
}

func TestSameDeviceDifferentKey(t *testing.T) {
	o := setup(t)
	ctx := context.Background()
	i := unitDevice(t, "m")
	if _, err := o.svc.Submit(ctx, request(i, token.Register, token.Prod)); err != nil {
		t.Fatal(err)
	}
	// An attempt to overwrite the registration of a registered identity with another key: the registration stays intact.
	attacker := request(i, token.Register, token.Prod)
	pub, priv, _ := ed25519.GenerateKey(nil)
	attacker.Public = pub
	attacker.Signature = ed25519.Sign(priv, SubmitMessage(i.ID, token.Register, token.Prod, "Kasa 1", nil))
	if _, err := o.svc.Submit(ctx, attacker); !errors.Is(err, ErrBadRequest) {
		t.Errorf("an id that is not derived: %v", err)
	}
	r, _ := o.store.Get(ctx, i.ID)
	if !r.Public.Equal(i.Public) {
		t.Error("the registered key changed")
	}
}

func TestApproveCode(t *testing.T) {
	o := setup(t)
	ctx := context.Background()
	i := unitDevice(t, "m")
	if _, err := o.svc.Submit(ctx, request(i, token.Register, token.Prod)); err != nil {
		t.Fatal(err)
	}
	if err := o.svc.Approve(ctx, "", i.ID, identity.Code(i.ID)); !errors.Is(err, ErrBadRequest) {
		t.Errorf("approval without an approver: %v", err)
	}
	if err := o.svc.Approve(ctx, "yonetici", i.ID, "AAAA-AAAA"); !errors.Is(err, ErrWrongCode) {
		t.Errorf("wrong code: %v", err)
	}
	// Another device's code does not approve this registration.
	if err := o.svc.Approve(ctx, "yonetici", i.ID, identity.Code(unitDevice(t, "x").ID)); !errors.Is(err, ErrWrongCode) {
		t.Errorf("another device's code: %v", err)
	}
	r, _ := o.store.Get(ctx, i.ID)
	if r.Status != Pending || r.Attempts != 2 {
		t.Errorf("status %s, attempt %d", r.Status, r.Attempts)
	}
	// When the wrong codes exceed the limit the registration is rejected; the right code is of no use any more.
	for n := 0; n < MaxCodeAttempts; n++ {
		_ = o.svc.Approve(ctx, "yonetici", i.ID, "AAAA-AAAA")
	}
	if err := o.svc.Approve(ctx, "yonetici", i.ID, identity.Code(i.ID)); !errors.Is(err, ErrBadRequest) {
		t.Errorf("a rejected registration was approved: %v", err)
	}
	if _, err := o.svc.Challenge(ctx, i.ID); !errors.Is(err, ErrRejected) {
		t.Errorf("reddedilen kayda meydan okuma: %v", err)
	}
}

func TestRejectAndRevoke(t *testing.T) {
	o := setup(t)
	ctx := context.Background()

	a := unitDevice(t, "a")
	if _, err := o.svc.Submit(ctx, request(a, token.Register, token.Prod)); err != nil {
		t.Fatal(err)
	}
	if err := o.svc.Reject(ctx, "yonetici", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := o.svc.Challenge(ctx, a.ID); !errors.Is(err, ErrRejected) {
		t.Errorf("reddedilen: %v", err)
	}

	b := unitDevice(t, "b")
	raw := fullFlow(t, o, b)
	if _, err := token.Verify(o.rootPub, raw, *o.fakeClock); err != nil {
		t.Fatal(err)
	}
	if err := o.svc.Revoke(ctx, "yonetici", b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := o.svc.Challenge(ctx, b.ID); !errors.Is(err, ErrRevoked) {
		t.Errorf("a revoked unit received a challenge: %v", err)
	}
	// A revoked unit cannot get itself approved with a new registration request.
	if r, err := o.svc.Submit(ctx, request(b, token.Register, token.Pilot)); err != nil || r.Status != Revoked {
		t.Errorf("re-request of a revoked unit: %+v, %v", r, err)
	}
	if err := o.svc.Revoke(ctx, "yonetici", a.ID); !errors.Is(err, ErrBadRequest) {
		t.Errorf("revoking an unapproved registration: %v", err)
	}
}

func TestTokenChallengeRules(t *testing.T) {
	o := setup(t)
	ctx := context.Background()
	i := unitDevice(t, "m")
	if _, err := o.svc.Submit(ctx, request(i, token.Register, token.Prod)); err != nil {
		t.Fatal(err)
	}
	if err := o.svc.Approve(ctx, "yonetici", i.ID, identity.Code(i.ID)); err != nil {
		t.Fatal(err)
	}
	signWith := func(nonce string) []byte { return i.Sign(token.PossessionMessage(i.ID, nonce)) }

	// Meydan okuma almadan jeton istenemez.
	if _, err := o.svc.Token(ctx, i.ID, "uydurma", signWith("uydurma")); !errors.Is(err, ErrNoChallenge) {
		t.Errorf("token without a challenge: %v", err)
	}

	// If another key signs the correct nonce no token is given.
	nonce, _ := o.svc.Challenge(ctx, i.ID)
	_, weirdPriv, _ := ed25519.GenerateKey(nil)
	if _, err := o.svc.Token(ctx, i.ID, nonce, ed25519.Sign(weirdPriv, token.PossessionMessage(i.ID, nonce))); !errors.Is(err, ErrBadRequest) {
		t.Errorf("token with another key: %v", err)
	}

	// A challenge is single-use: the same signature cannot be replayed.
	nonce, _ = o.svc.Challenge(ctx, i.ID)
	sig := signWith(nonce)
	if _, err := o.svc.Token(ctx, i.ID, nonce, sig); err != nil {
		t.Fatal(err)
	}
	if _, err := o.svc.Token(ctx, i.ID, nonce, sig); !errors.Is(err, ErrNoChallenge) {
		t.Errorf("replayed request: %v", err)
	}

	// An expired challenge is not accepted.
	nonce, _ = o.svc.Challenge(ctx, i.ID)
	*o.fakeClock = o.fakeClock.Add(ChallengeTTL + time.Second)
	if _, err := o.svc.Token(ctx, i.ID, nonce, signWith(nonce)); !errors.Is(err, ErrNoChallenge) {
		t.Errorf("expired challenge: %v", err)
	}

	// After revocation the old challenge is of no use.
	nonce, _ = o.svc.Challenge(ctx, i.ID)
	if err := o.svc.Revoke(ctx, "yonetici", i.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := o.svc.Token(ctx, i.ID, nonce, signWith(nonce)); !errors.Is(err, ErrRevoked) {
		t.Errorf("token after revocation: %v", err)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("an empty setting was accepted")
	}
}
