// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package personnel

import (
	"context"
	"errors"
	"testing"
)

func TestEmailPreset(t *testing.T) {
	ctx := context.Background()
	st := newMemStore()
	m := headquartersManage(st, nil)
	root := Actor{ID: "ayse", Name: "Ayşe", Manage: true}

	// The address is lower-cased; an invalid address is rejected.
	u, err := m.Add(ctx, root, "ayse", "uzun-parola-12", Input{Role: RoleAdmin, Email: "  Ayse@Firma.EXAMPLE "})
	if err != nil || u.Email != "ayse@firma.example" {
		t.Fatalf("administrator: %+v %v", u, err)
	}
	for _, bad := range []string{"ayse", "ayse@", "Ayşe <ayse@firma.example>", "a b@firma.example"} {
		if _, err := m.Add(ctx, root, "ali", "uzun-parola-12", Input{Role: RoleAdmin, Email: bad}); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	// An address cannot belong to two people; a person can keep their own address.
	if _, err := m.Add(ctx, root, "ali", "uzun-parola-12", Input{Role: RoleAdmin, Email: "AYSE@firma.example"}); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("same address: %v", err)
	}
	ali, err := m.Add(ctx, root, "ali", "uzun-parola-12", Input{Role: RoleAdmin})
	if err != nil || ali.Email != "" {
		t.Fatalf("adressiz: %+v %v", ali, err)
	}
	if _, err := m.Update(ctx, root, "ali", Input{Name: "ali", Role: RoleAdmin, Email: "ayse@firma.example"}); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("same address on update: %v", err)
	}
	if _, err := m.Update(ctx, root, "ayse", Input{Name: "ayse", Role: RoleAdmin, Email: "ayse@firma.example"}); err != nil {
		t.Errorf("kendi adresi: %v", err)
	}
	// An address change is recorded; the address can be removed.
	if _, err := m.Update(ctx, root, "ali", Input{Name: "ali", Role: RoleAdmin, Email: "ali@firma.example"}); err != nil {
		t.Fatal(err)
	}
	if got := st.changes[len(st.changes)-1].Detail; got != "e-posta: yok → ali@firma.example" {
		t.Errorf("record %q", got)
	}
	if u, err := m.Update(ctx, root, "ali", Input{Name: "ali", Role: RoleAdmin}); err != nil || u.Email != "" {
		t.Errorf("address removal: %+v %v", u, err)
	}
}

func TestCorporateIdentity(t *testing.T) {
	ctx := context.Background()
	st := newMemStore()
	m := headquartersManage(st, nil)
	root := Actor{ID: "ayse", Name: "Ayşe", Manage: true}
	for id, email := range map[string]string{"ayse": "ayse@firma.example", "ali": "ali@firma.example", "veli": ""} {
		if _, err := m.Add(ctx, root, id, "uzun-parola-12", Input{Role: RoleAdmin, Email: email}); err != nil {
			t.Fatal(err)
		}
	}
	const issuer = "https://idp.example"
	ayse := Identity{Issuer: issuer, Subject: "s-ayse", Email: "AYSE@firma.example", EmailVerified: true}

	// The address is not verified: no match.
	unverified := ayse
	unverified.EmailVerified = false
	if _, err := m.Identify(ctx, unverified); !errors.Is(err, ErrNoIdentity) {
		t.Errorf("unverified address: %v", err)
	}
	if st.users["ayse"].Subject != "" {
		t.Error("an unverified address linked an identity")
	}
	// Unknown address, missing identity: no match.
	for _, id := range []Identity{
		{Issuer: issuer, Subject: "s-x", Email: "kimse@firma.example", EmailVerified: true},
		{Issuer: issuer, Subject: "s-x", EmailVerified: true},
		{Issuer: issuer, Email: "ayse@firma.example", EmailVerified: true},
		{Subject: "s-x", Email: "ayse@firma.example", EmailVerified: true},
	} {
		if _, err := m.Identify(ctx, id); !errors.Is(err, ErrNoIdentity) {
			t.Errorf("%+v: %v", id, err)
		}
	}

	// First sign-in: the verified address selects the person and links the identity.
	u, err := m.Identify(ctx, ayse)
	if err != nil || u.ID != "ayse" || u.Issuer != issuer || u.Subject != "s-ayse" {
		t.Fatalf("first sign-in: %+v %v", u, err)
	}
	if got := st.changes[len(st.changes)-1]; got.By != "ayse" || got.Detail != "kurumsal kimlik bağlandı (ilk girişte e-postayla eşleşti)" {
		t.Errorf("record %+v", got)
	}
	// After that the address is irrelevant: the identity determines the person; even
	// if the IdP reports another address.
	later := Identity{Issuer: issuer, Subject: "s-ayse", Email: "baska@firma.example"}
	if u, err := m.Identify(ctx, later); err != nil || u.ID != "ayse" {
		t.Errorf("linked identity: %+v %v", u, err)
	}
	// Another identity cannot pass to a linked person by stating that person's address.
	thief := Identity{Issuer: issuer, Subject: "s-baska", Email: "ayse@firma.example", EmailVerified: true}
	if _, err := m.Identify(ctx, thief); !errors.Is(err, ErrNoIdentity) {
		t.Errorf("address of the linked person: %v", err)
	}
	// The same Subject is a different person at another IdP.
	if _, err := m.Identify(ctx, Identity{Issuer: "https://baska.example", Subject: "s-ayse"}); !errors.Is(err, ErrNoIdentity) {
		t.Errorf("another IdP: %v", err)
	}

	// A closed account is not linked by address.
	if _, err := m.SetDisabled(ctx, root, "ali", true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Identify(ctx, Identity{Issuer: issuer, Subject: "s-ali", Email: "ali@firma.example", EmailVerified: true}); !errors.Is(err, ErrNoIdentity) {
		t.Errorf("closed account: %v", err)
	}
	// An address change does not break the linked identity.
	if _, err := m.Update(ctx, root, "ayse", Input{Name: "ayse", Role: RoleAdmin, Email: "yeni@firma.example"}); err != nil {
		t.Fatal(err)
	}
	if got := st.users["ayse"]; got.Subject != "s-ayse" || got.Email != "yeni@firma.example" {
		t.Errorf("the update broke the identity: %+v", got)
	}

	// A person links an identity to their own account; they cannot take someone else's identity.
	self := Actor{ID: "veli", Name: "veli"}
	if _, err := m.LinkOwn(ctx, self, issuer, "s-ayse"); !errors.Is(err, ErrIdentityTaken) {
		t.Errorf("someone else's identity: %v", err)
	}
	if u, err := m.LinkOwn(ctx, self, issuer, "s-veli"); err != nil || u.Subject != "s-veli" {
		t.Fatalf("linking: %+v %v", u, err)
	}
	if got := st.changes[len(st.changes)-1].Detail; got != "kurumsal kimlik bağlandı (kendisi)" {
		t.Errorf("record %q", got)
	}
	n := len(st.changes)
	if _, err := m.LinkOwn(ctx, self, issuer, "s-veli"); err != nil || len(st.changes) != n {
		t.Errorf("relinking the same identity wrote a record: %v", err)
	}
	if _, err := m.LinkOwn(ctx, Actor{ID: "yok"}, issuer, "s-yok"); !errors.Is(err, ErrNotFound) {
		t.Errorf("non-existent person: %v", err)
	}
	if _, err := m.LinkOwn(ctx, self, "", "s"); !errors.Is(err, ErrNoIdentity) {
		t.Errorf("empty identity: %v", err)
	}

	// The link is removed by the person themselves or an administrator; no one else can remove it.
	if _, err := m.Unlink(ctx, Actor{ID: "ali", Name: "ali"}, "veli"); !errors.Is(err, ErrForbidden) {
		t.Errorf("someone else's link: %v", err)
	}
	if u, err := m.Unlink(ctx, self, "veli"); err != nil || u.Subject != "" || u.Issuer != "" {
		t.Errorf("own link: %+v %v", u, err)
	}
	if got := st.changes[len(st.changes)-1].Detail; got != "kurumsal kimlik bağı kaldırıldı (kendisi)" {
		t.Errorf("record %q", got)
	}
	if u, err := m.Unlink(ctx, Actor{ID: "ali", Name: "ali", Manage: true}, "ayse"); err != nil || u.Subject != "" {
		t.Errorf("administrator: %+v %v", u, err)
	}
	if got := st.changes[len(st.changes)-1].Detail; got != "kurumsal kimlik bağı kaldırıldı" {
		t.Errorf("record %q", got)
	}
	n = len(st.changes)
	if _, err := m.Unlink(ctx, root, "veli"); err != nil || len(st.changes) != n {
		t.Errorf("a record was written for a person with no link: %v", err)
	}
	if _, err := m.Unlink(ctx, root, "yok"); !errors.Is(err, ErrNotFound) {
		t.Errorf("non-existent person: %v", err)
	}
}
