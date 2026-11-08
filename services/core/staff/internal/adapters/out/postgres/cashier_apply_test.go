// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

func pinHash(t *testing.T, pin string) []byte {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// A cashier managed on the store server: the file does not change them; when the PIN changes
// the lock is released; when the account is closed they cannot sign in, the open session closes.
func TestApplyCashier(t *testing.T) {
	auth, _, _ := newAuth(t)
	ctx := context.Background()

	// The store server takes over a cashier from the file (the same id). The store server
	// keeps the PIN's digest; it sends the same digest as long as the PIN does not change.
	hash := pinHash(t, "2468")
	if err := auth.ApplyCashier(ctx, domain.Cashier{ID: "ayse", Name: "Ayşe Yılmaz", Role: domain.RoleCashier, PINHash: hash}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := auth.Login(ctx, "ayse", "1111"); !errors.Is(err, domain.ErrWrongPIN) {
		t.Errorf("old PIN: error = %v", err)
	}
	_, c, _, err := auth.Login(ctx, "ayse", "2468")
	if err != nil || c.Name != "Ayşe Yılmaz" || c.Origin != domain.FromStore {
		t.Fatalf("sign-in with the store server's PIN: %+v, %v", c, err)
	}
	skipped, err := auth.Seed(ctx, []app.SeedCashier{
		{ID: "ayse", Name: "Kasiyer Ayşe", Role: domain.RoleCashier, PIN: "1111"},
		{ID: "ali", Name: "Müdür Ali", Role: domain.RoleManager, PIN: "9999"},
	})
	if err != nil || len(skipped) != 1 || skipped[0] != "ayse" {
		t.Fatalf("skipped when the file was loaded again: %v, %v", skipped, err)
	}
	if _, _, _, err := auth.Login(ctx, "ayse", "2468"); err != nil {
		t.Errorf("the file changed the store server's PIN: %v", err)
	}

	// Wrong attempts continue on an update that comes with the same PIN, they are
	// reset with a new PIN.
	for i := 0; i < domain.MaxFailedAttempts; i++ {
		_, _, _, _ = auth.Login(ctx, "ayse", "0000")
	}
	if _, _, _, err := auth.Login(ctx, "ayse", "2468"); !errors.Is(err, domain.ErrLocked) {
		t.Fatalf("kilitlenmedi: %v", err)
	}
	if err := auth.ApplyCashier(ctx, domain.Cashier{ID: "ayse", Name: "Ayşe Y.", Role: domain.RoleCashier, PINHash: hash}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := auth.Login(ctx, "ayse", "2468"); !errors.Is(err, domain.ErrLocked) {
		t.Errorf("a name change with the same PIN must not unlock: %v", err)
	}
	if err := auth.ApplyCashier(ctx, domain.Cashier{ID: "ayse", Name: "Ayşe Y.", Role: domain.RoleCashier, PINHash: pinHash(t, "1357")}); err != nil {
		t.Fatal(err)
	}
	token, _, _, err := auth.Login(ctx, "ayse", "1357")
	if err != nil {
		t.Fatalf("sign-in with the new PIN: %v", err)
	}

	// The account is closed: the session closes, they cannot sign in, they do not show in the list.
	if err := auth.ApplyCashier(ctx, domain.Cashier{ID: "ayse", Name: "Ayşe Y.", Role: domain.RoleCashier, PINHash: pinHash(t, "1357"), Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Validate(ctx, token); !errors.Is(err, domain.ErrInvalidSession) {
		t.Errorf("session of a closed account: %v", err)
	}
	if _, _, _, err := auth.Login(ctx, "ayse", "1357"); !errors.Is(err, domain.ErrDisabled) {
		t.Errorf("sign-in with a closed account: %v", err)
	}
	if list, err := auth.List(ctx); err != nil || len(list) != 1 || list[0].ID != "ali" {
		t.Errorf("list: %+v, %v", list, err)
	}

	// A closed manager cannot give approval.
	if err := auth.ApplyCashier(ctx, domain.Cashier{ID: "veli", Name: "Müdür Veli", Role: domain.RoleManager, PINHash: pinHash(t, "8642")}); err != nil {
		t.Fatal(err)
	}
	approval, _, err := auth.Approve(ctx, "ali", "veli", "8642", domain.ActionRefundCharged)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.ApplyCashier(ctx, domain.Cashier{ID: "veli", Name: "Müdür Veli", Role: domain.RoleManager, PINHash: pinHash(t, "8642"), Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Authorize(ctx, "ali", approval, domain.ActionRefundCharged); !errors.Is(err, domain.ErrDisabled) {
		t.Errorf("approval of a closed manager: %v", err)
	}
	if _, _, err := auth.Approve(ctx, "ali", "veli", "8642", domain.ActionRefundCharged); !errors.Is(err, domain.ErrDisabled) {
		t.Errorf("a closed manager gave an approval: %v", err)
	}

	// A missing or invalid cashier is not applied.
	if err := auth.ApplyCashier(ctx, domain.Cashier{ID: "x", Name: "X", Role: domain.RoleCashier}); !errors.Is(err, domain.ErrInvalidCashier) {
		t.Errorf("cashier without a PIN: %v", err)
	}
	if err := auth.ApplyCashier(ctx, domain.Cashier{ID: "x", Name: "X", Role: "patron", PINHash: pinHash(t, "1234")}); !errors.Is(err, domain.ErrInvalidRole) {
		t.Errorf("unknown role: %v", err)
	}
}
