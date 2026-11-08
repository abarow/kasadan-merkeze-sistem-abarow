// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"abarow.com/pkg/login"
	"abarow.com/services/store/internal/domain"
)

func newAuth(t *testing.T, password string) (*Auth, *time.Time) {
	t.Helper()
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewAuth(hash, login.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	return a, &now
}

// valid says that the session is open.
func valid(a *Auth, token string) bool {
	_, err := a.Session(context.Background(), token)
	return err == nil
}

func TestLogin(t *testing.T) {
	a, now := newAuth(t, "mağaza-parolası")
	if _, err := a.Login(context.Background(), "yanlış-parola"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("wrong password: %v", err)
	}
	token, err := a.Login(context.Background(), "mağaza-parolası")
	if err != nil || token == "" || !valid(a, token) {
		t.Fatalf("sign-in: %q %v", token, err)
	}
	if who, _ := a.Session(context.Background(), token); who != (Who{Name: StorePasswordName}) {
		t.Errorf("one who signed in with the store password: %+v", who)
	}
	other, _ := a.Login(context.Background(), "mağaza-parolası")
	if other == token || !valid(a, other) {
		t.Error("each sign-in should have a separate session")
	}
	if valid(a, "") || valid(a, "uydurma") {
		t.Error("an invalid key was accepted")
	}
	a.Logout(token)
	if valid(a, token) || !valid(a, other) {
		t.Error("sign-out should close only that session")
	}
	*now = now.Add(SessionTTL)
	if valid(a, other) {
		t.Error("an expired session is valid")
	}
}

func TestLoginLockout(t *testing.T) {
	a, now := newAuth(t, "mağaza-parolası")
	for i := 1; i < login.Default.MaxFailures; i++ {
		if _, err := a.Login(context.Background(), "yanlış"); !errors.Is(err, ErrWrongPassword) {
			t.Fatalf("deneme %d: %v", i, err)
		}
	}
	var locked LockedError
	if _, err := a.Login(context.Background(), "yanlış"); !errors.As(err, &locked) || !locked.Until.Equal(now.Add(login.Default.Lock)) {
		t.Fatalf("lock: %v", err)
	}
	// While locked even the right password is not accepted.
	if _, err := a.Login(context.Background(), "mağaza-parolası"); !errors.As(err, &locked) {
		t.Errorf("sign-in while locked: %v", err)
	}
	*now = now.Add(login.Default.Lock)
	if _, err := a.Login(context.Background(), "mağaza-parolası"); err != nil {
		t.Errorf("sign-in after the lock ends: %v", err)
	}
}

// Successive locks get longer (finding a PIN by trial and error gets slower);
// on a right sign-in the duration starts over.
func TestLockGrows(t *testing.T) {
	a, now := newAuth(t, "mağaza-parolası")
	lockOnce := func() time.Duration {
		t.Helper()
		var err error
		for range login.Default.MaxFailures {
			_, err = a.Login(context.Background(), "yanlış")
		}
		var locked LockedError
		if !errors.As(err, &locked) {
			t.Fatalf("lock: %v", err)
		}
		d := locked.Until.Sub(*now)
		*now = locked.Until
		return d
	}
	for i, want := range []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute, time.Hour, time.Hour} {
		if got := lockOnce(); got != want {
			t.Errorf("lock %d %v, want %v", i+1, got, want)
		}
	}
	if _, err := a.Login(context.Background(), "mağaza-parolası"); err != nil {
		t.Fatal(err)
	}
	if got := lockOnce(); got != login.Default.Lock {
		t.Errorf("lock after a right sign-in %v", got)
	}
}

func TestHashPassword(t *testing.T) {
	if _, err := HashPassword("kısa"); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("short password: %v", err)
	}
	// The length is counted in characters, not bytes: 7 Turkish letters are 14 bytes.
	if _, err := HashPassword("çğışçğı"); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("a 7-character Turkish password was accepted: %v", err)
	}
	if _, err := NewAuth("düz-parola", login.NewMemory()); err == nil {
		t.Error("a digest that is not bcrypt was accepted")
	}
}

// managerDir, bellekteki kasiyerlerdir.
type managerDir map[string]domain.Cashier

func (d managerDir) Cashier(_ context.Context, id string) (domain.Cashier, error) {
	c, ok := d[id]
	if !ok {
		return domain.Cashier{}, domain.ErrCashierNotFound
	}
	return c, nil
}

func (d managerDir) Cashiers(context.Context) ([]domain.Cashier, error) {
	var list []domain.Cashier
	for _, c := range d {
		list = append(list, c)
	}
	return list, nil
}

func pinHash(t *testing.T, pin string) []byte {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestLoginManager(t *testing.T) {
	ctx := context.Background()
	a, now := newAuth(t, "mağaza-parolası")
	if _, _, err := a.LoginManager(ctx, "ayse", "1234"); !errors.Is(err, ErrNoManagerLogin) {
		t.Errorf("when the manager sign-in is off: %v", err)
	}
	if ok, err := a.ManagerLogin(ctx); ok || err != nil {
		t.Errorf("the manager sign-in should look off: %v %v", ok, err)
	}
	dir := managerDir{
		"ali":   {ID: "ali", Name: "Ali Kaya", Role: domain.RoleCashier, PINHash: pinHash(t, "2468")},
		"emine": {ID: "emine", Name: "Emine Ak", Role: domain.RoleManager, PINHash: pinHash(t, "1357"), Disabled: true},
	}
	a.AllowManagers(dir)
	if ok, _ := a.ManagerLogin(ctx); ok {
		t.Error("the manager sign-in was offered while there was no manager with an open account")
	}
	dir["ayse"] = domain.Cashier{ID: "ayse", Name: "Ayşe Yılmaz", Role: domain.RoleManager, PINHash: pinHash(t, "1234")}
	if ok, _ := a.ManagerLogin(ctx); !ok {
		t.Error("the manager sign-in was not offered while there is a manager")
	}

	for _, tc := range []struct {
		id, pin string
		want    error
	}{
		{"ayse", "9999", ErrWrongPIN},
		{"yok", "1234", ErrWrongPIN},
		{"Ayşe", "1234", ErrWrongPIN},
		{"ali", "2468", ErrNotManager},
		{"emine", "1357", ErrAccountClosed},
	} {
		if _, _, err := a.LoginManager(ctx, tc.id, tc.pin); !errors.Is(err, tc.want) {
			t.Errorf("%s/%s: %v, want %v", tc.id, tc.pin, err, tc.want)
		}
	}
	token, who, err := a.LoginManager(ctx, "ayse", "1234")
	if err != nil || who != (Who{CashierID: "ayse", Name: "Ayşe Yılmaz"}) {
		t.Fatalf("sign-in: %+v %v", who, err)
	}
	// When the name changes the session returns the new name.
	c := dir["ayse"]
	c.Name = "Ayşe Demir"
	dir["ayse"] = c
	if who, err := a.Session(ctx, token); err != nil || who.Name != "Ayşe Demir" {
		t.Errorf("when the name changes: %+v %v", who, err)
	}

	// When the managership is removed, the account is closed or the cashier is deleted the session closes.
	for name, change := range map[string]func(){
		"rol kasiyer":  func() { c := dir["ayse"]; c.Role = domain.RoleCashier; dir["ayse"] = c },
		"hesap kapalı": func() { c := dir["ayse"]; c.Disabled = true; dir["ayse"] = c },
		"silindi":      func() { delete(dir, "ayse") },
	} {
		dir["ayse"] = c
		token, _, err := a.LoginManager(ctx, "ayse", "1234")
		if err != nil {
			t.Fatal(err)
		}
		change()
		if _, err := a.Session(ctx, token); !errors.Is(err, ErrNoSession) {
			t.Errorf("%s: the session stayed open: %v", name, err)
		}
		// Even if the state comes back, a session that closed stays closed.
		dir["ayse"] = c
		if valid(a, token) {
			t.Errorf("%s: the closed session was reopened", name)
		}
	}

	// When the PIN changes the sessions opened with that PIN close; the session of the manager
	// who changed the PIN stays open.
	dir["ayse"] = c
	mine, _, _ := a.LoginManager(ctx, "ayse", "1234")
	stolen, _, _ := a.LoginManager(ctx, "ayse", "1234")
	c.PINHash = pinHash(t, "8642")
	dir["ayse"] = c
	a.KeepAfterPINChange(mine, c)
	if !valid(a, mine) || valid(a, stolen) {
		t.Errorf("when the PIN changes: own %v, other %v", valid(a, mine), valid(a, stolen))
	}
	// Another manager's session does not change with someone else's PIN change.
	a.KeepAfterPINChange(mine, domain.Cashier{ID: "ali", PINHash: c.PINHash})
	if !valid(a, mine) {
		t.Error("someone else's PIN closed the session")
	}

	// The lock is per manager; it does not affect the other managers.
	dir["mehmet"] = domain.Cashier{ID: "mehmet", Name: "Mehmet Er", Role: domain.RoleManager, PINHash: pinHash(t, "5555")}
	var locked LockedError
	for range login.Default.MaxFailures {
		_, _, err = a.LoginManager(ctx, "ayse", "0000")
	}
	if !errors.As(err, &locked) || !locked.Until.Equal(now.Add(login.Default.Lock)) {
		t.Fatalf("lock: %v", err)
	}
	if _, _, err := a.LoginManager(ctx, "ayse", "8642"); !errors.As(err, &locked) {
		t.Errorf("right PIN while locked: %v", err)
	}
	if _, _, err := a.LoginManager(ctx, "mehmet", "5555"); err != nil {
		t.Errorf("the other manager was locked: %v", err)
	}
	*now = now.Add(login.Default.Lock)
	if _, _, err := a.LoginManager(ctx, "ayse", "8642"); err != nil {
		t.Errorf("after the lock: %v", err)
	}
}

// While a manager with an open account exists the store password is off: the password is
// not compared, a wrong attempt is not counted; a session opened with the password
// closes. When no manager with an open account is left the password opens again.
func TestPasswordClosed(t *testing.T) {
	ctx := context.Background()
	a, _ := newAuth(t, "mağaza-parolası")
	dir := managerDir{"ali": {ID: "ali", Name: "Ali Kaya", Role: domain.RoleCashier, PINHash: pinHash(t, "2468")}}
	a.AllowManagers(dir)
	token, err := a.Login(ctx, "mağaza-parolası")
	if err != nil {
		t.Fatalf("while there is no manager: %v", err)
	}
	// The first manager is added.
	dir["ayse"] = domain.Cashier{ID: "ayse", Name: "Ayşe Yılmaz", Role: domain.RoleManager, PINHash: pinHash(t, "1234")}
	if _, err := a.Session(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("the session opened with the password stayed open when a manager was added: %v", err)
	}
	for range login.Default.MaxFailures + 1 {
		if _, err := a.Login(ctx, "yanlış"); !errors.Is(err, ErrPasswordClosed) {
			t.Fatalf("wrong password while there is a manager: %v", err)
		}
	}
	if _, err := a.Login(ctx, "mağaza-parolası"); !errors.Is(err, ErrPasswordClosed) {
		t.Errorf("right password while there is a manager: %v", err)
	}
	// The attempts made while it was off did not count toward the lock.
	c := dir["ayse"]
	c.Disabled = true
	dir["ayse"] = c
	if _, err := a.Login(ctx, "mağaza-parolası"); err != nil {
		t.Errorf("when no manager with an open account is left: %v", err)
	}
}
