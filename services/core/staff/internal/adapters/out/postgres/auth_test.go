// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"abarow.com/pkg/pgdb/pgdbtest"
	"abarow.com/services/core/staff/internal/adapters/out/bcrypthasher"
	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

func newAuth(t *testing.T) (*app.Auth, *Store, *time.Time) {
	t.Helper()
	ctx := context.Background()
	st, err := Open(ctx, pgdbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	auth := app.NewAuth(st, bcrypthasher.Hasher{Cost: bcrypt.MinCost}, 12*time.Hour)
	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	auth.Now = func() time.Time { return now }
	_, err = auth.Seed(ctx, []app.SeedCashier{
		{ID: "ayse", Name: "Kasiyer Ayşe", Role: domain.RoleCashier, PIN: "1111"},
		{ID: "ali", Name: "Müdür Ali", Role: domain.RoleManager, PIN: "9999"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return auth, st, &now
}

func TestLoginValidateLogout(t *testing.T) {
	auth, st, _ := newAuth(t)
	ctx := context.Background()

	token, c, expires, err := auth.Login(ctx, "ali", "9999")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if c.Name != "Müdür Ali" || !c.CanCloseDay() || len(token) != 43 || expires.IsZero() {
		t.Errorf("sign-in is wrong: %+v, token %d characters", c, len(token))
	}
	var stored string
	if err := st.db.QueryRow(`SELECT token_hash FROM sessions`).Scan(&stored); err != nil || stored == token {
		t.Errorf("the session key must not be stored in plain text: %q, %v", stored, err)
	}

	got, err := auth.Validate(ctx, token)
	if err != nil || got.ID != "ali" {
		t.Errorf("Validate: %+v, %v", got, err)
	}
	if _, err := auth.Validate(ctx, "uydurma"); !errors.Is(err, domain.ErrInvalidSession) {
		t.Errorf("made-up key: error = %v", err)
	}
	if err := auth.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Validate(ctx, token); !errors.Is(err, domain.ErrInvalidSession) {
		t.Errorf("the session stayed valid after sign-out: %v", err)
	}
	if list, err := auth.List(ctx); err != nil || len(list) != 2 {
		t.Errorf("cashier list: %v, %v", list, err)
	}
}

func TestSessionExpires(t *testing.T) {
	auth, _, now := newAuth(t)
	ctx := context.Background()
	token, _, _, err := auth.Login(ctx, "ayse", "1111")
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(12 * time.Hour)
	if _, err := auth.Validate(ctx, token); !errors.Is(err, domain.ErrInvalidSession) {
		t.Errorf("an expired session is valid: %v", err)
	}
}

func TestLockAfterFailedAttempts(t *testing.T) {
	auth, _, now := newAuth(t)
	ctx := context.Background()

	for i := 1; i < domain.MaxFailedAttempts; i++ {
		_, _, _, err := auth.Login(ctx, "ayse", "0000")
		var wrong domain.WrongPINError
		if !errors.As(err, &wrong) || wrong.Remaining != domain.MaxFailedAttempts-i {
			t.Fatalf("attempt %d: error = %v", i, err)
		}
	}
	if _, _, _, err := auth.Login(ctx, "ayse", "0000"); !errors.Is(err, domain.ErrWrongPIN) {
		t.Fatalf("last attempt: error = %v", err)
	}
	// While locked even the right PIN is not accepted.
	if _, _, _, err := auth.Login(ctx, "ayse", "1111"); !errors.Is(err, domain.ErrLocked) {
		t.Fatalf("while locked: error = %v, want %v", err, domain.ErrLocked)
	}
	*now = now.Add(domain.LockDuration)
	if _, _, _, err := auth.Login(ctx, "ayse", "1111"); err != nil {
		t.Errorf("sign-in after the lock ended: %v", err)
	}
	if _, _, _, err := auth.Login(ctx, "kimse", "1111"); !errors.Is(err, domain.ErrUnknownCashier) {
		t.Errorf("unknown cashier: error = %v", err)
	}
}

func TestSeedValidates(t *testing.T) {
	auth, _, _ := newAuth(t)
	ctx := context.Background()
	if _, err := auth.Seed(ctx, []app.SeedCashier{{ID: "x", Name: "X", Role: domain.RoleCashier, PIN: "12a4"}}); !errors.Is(err, domain.ErrInvalidPIN) {
		t.Errorf("PIN with letters: error = %v", err)
	}
	if _, err := auth.Seed(ctx, []app.SeedCashier{{ID: "x", Name: "X", Role: "patron", PIN: "1234"}}); !errors.Is(err, domain.ErrInvalidRole) {
		t.Errorf("unknown role: error = %v", err)
	}
}

func TestApproval(t *testing.T) {
	ctx := context.Background()
	auth, st, now := newAuth(t)

	if _, _, err := auth.Approve(ctx, "ayse", "ali", "9999", "her_sey"); !errors.Is(err, domain.ErrUnknownAction) {
		t.Errorf("unknown action: %v", err)
	}
	if _, _, err := auth.Approve(ctx, "ayse", "ayse", "1111", domain.ActionCloseDay); !errors.Is(err, domain.ErrNotManager) {
		t.Errorf("a cashier could give an approval: %v", err)
	}
	// A wrong PIN counts toward the manager's attempt counter.
	var wrong domain.WrongPINError
	if _, _, err := auth.Approve(ctx, "ayse", "ali", "0000", domain.ActionCloseDay); !errors.As(err, &wrong) || wrong.Remaining != domain.MaxFailedAttempts-1 {
		t.Errorf("wrong PIN: %v", err)
	}

	token, expires, err := auth.Approve(ctx, "ayse", "ali", "9999", domain.ActionCashOut)
	if err != nil || len(token) != 43 || !expires.Equal(now.Add(domain.ApprovalTTL)) {
		t.Fatalf("approval: %q %v %v", token, expires, err)
	}
	// The approval is used only in the operation it was given for and only once.
	if _, err := auth.Actor(ctx, "ayse", token, domain.ActionCloseDay); !errors.Is(err, domain.ErrInvalidApproval) {
		t.Errorf("used for another action: %v", err)
	}
	if actor, err := auth.Actor(ctx, "ayse", token, domain.ActionCashOut); err != nil || actor != "ali" {
		t.Errorf("the approved action was done by %q, %v; want ali", actor, err)
	}
	if _, err := auth.Actor(ctx, "ayse", token, domain.ActionCashOut); !errors.Is(err, domain.ErrInvalidApproval) {
		t.Errorf("the approval was used a second time: %v", err)
	}
	if actor, err := auth.Actor(ctx, "ayse", "", domain.ActionCashOut); err != nil || actor != "ayse" {
		t.Errorf("the action without approval was done by %q, %v; want ayse", actor, err)
	}

	// An expired approval is not used.
	token, _, err = auth.Approve(ctx, "ayse", "ali", "9999", domain.ActionCloseDay)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(domain.ApprovalTTL)
	if _, err := auth.Actor(ctx, "ayse", token, domain.ActionCloseDay); !errors.Is(err, domain.ErrInvalidApproval) {
		t.Errorf("an expired approval was used: %v", err)
	}

	// Z only by a manager (or by a cashier with manager approval, in the manager's name).
	svc := app.NewService(st, st, st, app.Events{})
	if _, err := svc.CloseDay(ctx, "ayse"); !errors.Is(err, domain.ErrCloseDayNotManager) {
		t.Errorf("a cashier could take Z: %v", err)
	}
}

func TestAuthorize(t *testing.T) {
	ctx := context.Background()
	auth, _, _ := newAuth(t)

	if id, err := auth.Authorize(ctx, "ali", "", domain.ActionRefundCharged); err != nil || id != "ali" {
		t.Errorf("manager session: %q, %v", id, err)
	}
	if _, err := auth.Authorize(ctx, "ayse", "", domain.ActionRefundCharged); !errors.Is(err, domain.ErrManagerRequired) {
		t.Errorf("cashier without approval: %v", err)
	}
	token, _, err := auth.Approve(ctx, "ayse", "ali", "9999", domain.ActionRefundCharged)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := auth.Authorize(ctx, "ayse", token, domain.ActionRefundCharged); err != nil || id != "ali" {
		t.Errorf("cashier with approval: %q, %v", id, err)
	}
	if _, err := auth.Authorize(ctx, "ayse", token, domain.ActionRefundCharged); !errors.Is(err, domain.ErrInvalidApproval) {
		t.Errorf("approval a second time: %v", err)
	}
	if _, err := auth.Authorize(ctx, "ali", "", "her_sey"); !errors.Is(err, domain.ErrUnknownAction) {
		t.Errorf("unknown action: %v", err)
	}
}
