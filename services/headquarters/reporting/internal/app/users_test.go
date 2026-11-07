// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/pkg/personnel/personneltest"
	"abarow.com/services/headquarters/reporting/internal/domain"
)

func TestUsers(t *testing.T) {
	ctx := context.Background()
	users := personneltest.New()
	auth, err := NewAuth(users)
	if err != nil {
		t.Fatal(err)
	}
	auth.now = func() time.Time { return time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC) }
	const pw = "headquarters-test-1"

	// The first administrator is added from the command line.
	ayse, err := auth.AddUser(ctx, Installer, "ayse", pw, UserInput{Name: "Ayşe Yılmaz", Role: domain.RoleAdmin, Stores: []string{"store-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !ayse.AllStores || ayse.Stores != nil {
		t.Errorf("the administrator should see all the stores: %+v", ayse)
	}

	// The administrator adds a report user; the stores are sorted and without repeats.
	ali, err := auth.AddUser(ctx, ayse, "ali", pw, UserInput{Name: " Ali  Kaya ", Role: domain.RoleReport, Stores: []string{"store-2", "store-1", "store-2"}})
	if err != nil {
		t.Fatal(err)
	}
	if ali.Name != "Ali Kaya" || !slices.Equal(ali.Stores, []string{"store-1", "store-2"}) || ali.SeesStore("store-3") || !ali.SeesStore("store-2") {
		t.Errorf("report user %+v", ali)
	}
	for name, in := range map[string]struct {
		in   UserInput
		want error
	}{
		"mağazasız":       {UserInput{Name: "Veli", Role: domain.RoleReport}, domain.ErrNoStores},
		"geçersiz mağaza": {UserInput{Name: "Veli", Role: domain.RoleReport, Stores: []string{"mağaza 1"}}, domain.ErrInvalidStore},
		"geçersiz rol":    {UserInput{Name: "Veli", Role: "patron", AllStores: true}, domain.ErrInvalidRole},
		"uzun ad":         {UserInput{Name: string(make([]rune, 65)), Role: domain.RoleReport, AllStores: true}, domain.ErrInvalidName},
	} {
		if _, err := auth.AddUser(ctx, ayse, "veli", pw, in.in); !errors.Is(err, in.want) {
			t.Errorf("%s: %v, want %v", name, err, in.want)
		}
	}

	// A user who is not an administrator cannot manage users.
	if _, err := auth.Users(ctx, ali); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("a report user saw the list: %v", err)
	}
	if _, err := auth.AddUser(ctx, ali, "veli", pw, UserInput{Name: "Veli", Role: domain.RoleAdmin}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("a report user added an administrator: %v", err)
	}
	if err := auth.SetPassword(ctx, ali, "ayse", "baska-parola-1"); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("a report user changed a password: %v", err)
	}
	if _, err := auth.SetDisabled(ctx, ali, "ayse", true); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("a report user closed an account: %v", err)
	}

	// The change is recorded with a readable summary; if there is no change there is no record.
	ali, err = auth.UpdateUser(ctx, ayse, "ali", UserInput{Name: "Ali Demir", Role: domain.RoleCatalog, AllStores: true})
	if err != nil || ali.Role != domain.RoleCatalog || !ali.AllStores || ali.Stores != nil {
		t.Fatalf("update: %+v %v", ali, err)
	}
	if _, err := auth.UpdateUser(ctx, ayse, "ali", UserInput{Name: "Ali Demir", Role: domain.RoleCatalog, AllStores: true}); err != nil {
		t.Fatal(err)
	}
	_, history, err := auth.User(ctx, ayse, "ali")
	if err != nil || len(history) != 2 ||
		history[0].Detail != "ad: Ali Kaya → Ali Demir; rol: rapor → katalog; mağazalar: store-1, store-2 → bütün mağazalar" || history[0].By != "Ayşe Yılmaz" ||
		history[1].Detail != "eklendi: Ali Kaya (rapor; store-1, store-2)" {
		t.Errorf("changes %+v %v", history, err)
	}
	if _, _, err := auth.User(ctx, ayse, "yok"); !errors.Is(err, domain.ErrUserNotFound) {
		t.Errorf("non-existent user: %v", err)
	}

	// The administrator cannot lock themselves out; the last administrator with an open account cannot
	// be closed and their role cannot be changed (from the command line either).
	if _, err := auth.SetDisabled(ctx, ayse, "ayse", true); !errors.Is(err, domain.ErrSelf) {
		t.Errorf("closed their own account: %v", err)
	}
	if _, err := auth.UpdateUser(ctx, ayse, "ayse", UserInput{Name: "Ayşe Yılmaz", Role: domain.RoleReport, AllStores: true}); !errors.Is(err, domain.ErrSelf) {
		t.Errorf("removed their own administratorship: %v", err)
	}
	if _, err := auth.SetDisabled(ctx, Installer, "ayse", true); !errors.Is(err, domain.ErrLastAdmin) {
		t.Errorf("the last administrator was closed: %v", err)
	}
	// The administrator can change their own name.
	if _, err := auth.UpdateUser(ctx, ayse, "ayse", UserInput{Name: "Ayşe Demir", Role: domain.RoleAdmin}); err != nil {
		t.Errorf("own name: %v", err)
	}
	// While there is a second administrator the first can be closed; the role of a closed
	// administrator can change too.
	mehmet, err := auth.AddUser(ctx, ayse, "mehmet", pw, UserInput{Name: "Mehmet Er", Role: domain.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.SetDisabled(ctx, mehmet, "ayse", true); err != nil {
		t.Fatalf("while there is a second administrator: %v", err)
	}
	if _, err := auth.UpdateUser(ctx, mehmet, "ayse", UserInput{Name: "Ayşe Demir", Role: domain.RoleReport, AllStores: true}); err != nil {
		t.Errorf("role of the closed administrator: %v", err)
	}
	if _, err := auth.UpdateUser(ctx, Installer, "mehmet", UserInput{Name: "Mehmet Er", Role: domain.RoleCatalog, AllStores: true}); !errors.Is(err, domain.ErrLastAdmin) {
		t.Errorf("the last administrator's role changed: %v", err)
	}
	// A password change is recorded; the password itself is not written.
	if err := auth.SetPassword(ctx, mehmet, "ali", "yeni-parola-123"); err != nil {
		t.Fatal(err)
	}
	if _, history, _ := auth.User(ctx, mehmet, "ali"); history[0].Detail != "parola değişti" || history[0].By != "Mehmet Er" {
		t.Errorf("password record %+v", history[0])
	}
}

// The person changes their own password with their current password; their other sessions
// close, a new session is opened. A wrong current password is counted together with the sign-in.
func TestChangeOwnPassword(t *testing.T) {
	ctx := context.Background()
	users := personneltest.New()
	auth, err := NewAuth(users)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	auth.now = func() time.Time { return now }
	ali, err := auth.AddUser(ctx, Installer, "ali", "headquarters-test-1", UserInput{Name: "Ali Kaya", Role: domain.RoleReport, AllStores: true})
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := auth.Login(ctx, "ali", "headquarters-test-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.ChangeOwnPassword(ctx, ali, "headquarters-test-1", "kısa"); !errors.Is(err, domain.ErrPasswordTooShort) {
		t.Errorf("short password: %v", err)
	}
	if _, err := auth.ChangeOwnPassword(ctx, ali, "yanlış-parola", "yeni-parola-12"); !errors.Is(err, ErrWrongCurrentPassword) {
		t.Errorf("wrong current password: %v", err)
	}
	token, err := auth.ChangeOwnPassword(ctx, ali, "headquarters-test-1", "yeni-parola-12")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Session(ctx, token); err != nil {
		t.Errorf("yeni oturum: %v", err)
	}
	if _, err := auth.Session(ctx, other); !errors.Is(err, ErrNoSession) {
		t.Errorf("the other session stayed open: %v", err)
	}
	if _, _, err := auth.Login(ctx, "ali", "yeni-parola-12"); err != nil {
		t.Errorf("sign-in with the new password: %v", err)
	}
	if c := users.Changes[len(users.Changes)-1]; c.Detail != "parola değişti (kendisi)" || c.By != "Ali Kaya" {
		t.Errorf("record %+v", c)
	}
	// Wrong current password attempts lock: the password cannot be found by trial and error
	// by this route either.
	var locked LockedError
	for range login.Default.MaxFailures {
		_, err = auth.ChangeOwnPassword(ctx, ali, "yanlış-parola", "baska-parola-1")
	}
	if !errors.As(err, &locked) {
		t.Fatalf("kilit: %v", err)
	}
	if _, _, err := auth.Login(ctx, "ali", "yeni-parola-12"); !errors.As(err, &locked) {
		t.Errorf("sign-in while locked: %v", err)
	}
}
