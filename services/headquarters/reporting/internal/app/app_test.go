// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/pkg/personnel/personneltest"
	"abarow.com/services/headquarters/reporting/internal/domain"
)

var ist = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		panic(err)
	}
	return loc
}()

// admin sees all the stores; magaza2 sees only store-2.
var (
	admin  = domain.User{ID: "yonetici", Name: "Yönetici", Role: domain.RoleAdmin, AllStores: true}
	store2 = domain.User{ID: "bolge", Name: "Bölge", Role: domain.RoleReport, Stores: []string{"store-2"}}
)

type fakeReports struct {
	stores      []domain.StoreOverview
	dayStart    time.Time
	dayEnd      time.Time
	storePrices []domain.StorePriceRow
}

func (f *fakeReports) Stores(_ context.Context, start, end time.Time) ([]domain.StoreOverview, error) {
	f.dayStart, f.dayEnd = start, end
	return slices.Clone(f.stores), nil
}

func (f *fakeReports) StorePrices(context.Context, string) ([]domain.StorePriceRow, error) {
	return f.storePrices, nil
}

func TestStores(t *testing.T) {
	// 27 October 01:30 in Istanbul (still 26 October in UTC).
	now := time.Date(2026, 10, 26, 22, 30, 0, 0, time.UTC)
	reports := &fakeReports{stores: []domain.StoreOverview{
		{ID: "store-1", Link: domain.LinkConnected, CheckedAt: now.Add(-10 * time.Second)},
		{ID: "store-2", Link: domain.LinkConnected, CheckedAt: now.Add(-5 * time.Minute)},
	}}
	svc := NewService(reports, ist)
	svc.now = func() time.Time { return now }
	got, err := svc.Stores(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if svc.Today() != "2026-10-27" || !reports.dayStart.Equal(time.Date(2026, 10, 26, 21, 0, 0, 0, time.UTC)) || reports.dayEnd.Sub(reports.dayStart) != 24*time.Hour {
		t.Errorf("today %s: %v – %v", svc.Today(), reports.dayStart, reports.dayEnd)
	}
	if got[0].Link != domain.LinkConnected || got[1].Link != domain.LinkUnknown {
		t.Errorf("durumlar %+v", got)
	}
	// The user sees only the stores they are allowed.
	if got, _ := svc.Stores(context.Background(), store2); len(got) != 1 || got[0].ID != "store-2" {
		t.Errorf("user with limited stores: %+v", got)
	}
}

func TestAuth(t *testing.T) {
	ctx := context.Background()
	users := personneltest.New()
	auth, err := NewAuth(users)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	auth.now = func() time.Time { return now }

	reporter := UserInput{Name: "Ayşe Yılmaz", Role: domain.RoleReport, AllStores: true}
	if _, err := auth.AddUser(ctx, Installer, "Ayse", "kısa", reporter); !errors.Is(err, domain.ErrPasswordTooShort) {
		t.Errorf("short password: %v", err)
	}
	if _, err := auth.AddUser(ctx, Installer, "Ayse", "headquarters-test-1", reporter); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.AddUser(ctx, Installer, "ayse", "headquarters-test-1", reporter); !errors.Is(err, domain.ErrUserExists) {
		t.Errorf("same user: %v", err)
	}
	if saved, _ := users.User(ctx, "ayse"); saved.PasswordHash == "" || bytes.Contains([]byte(saved.PasswordHash), []byte("headquarters-test")) {
		t.Fatalf("password digest %q", saved.PasswordHash)
	}

	// The username can be entered in upper case too.
	token, u, err := auth.Login(ctx, "AYSE", "headquarters-test-1")
	if err != nil || u.Name != "Ayşe Yılmaz" {
		t.Fatalf("sign-in: %v %+v", err, u)
	}
	if u, err := auth.Session(ctx, token); err != nil || u.ID != "ayse" {
		t.Errorf("oturum: %v %+v", err, u)
	}
	// The session is stored by the key's digest (someone who reads the database cannot open a session).
	if _, ok, _ := users.Get(ctx, login.TokenHash(token), now); !ok {
		t.Error("the session key was not stored by its digest")
	}
	if _, err := auth.Session(ctx, "uydurma"); !errors.Is(err, ErrNoSession) {
		t.Errorf("uydurma oturum: %v", err)
	}
	// An expired session does not pass.
	now = now.Add(SessionTTL)
	if _, err := auth.Session(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("expired session: %v", err)
	}

	// The same error as with a non-existent user comes back.
	if _, _, err := auth.Login(ctx, "veli", "headquarters-test-1"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("non-existent user: %v", err)
	}

	// When the password changes the open session closes.
	token, _, _ = auth.Login(ctx, "ayse", "headquarters-test-1")
	if err := auth.SetPassword(ctx, Installer, "ayse", "headquarters-test-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Session(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("the password changed, the session stayed open: %v", err)
	}
	// A closed account cannot sign in; the error is still the same.
	if _, err := auth.SetDisabled(ctx, Installer, "ayse", true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := auth.Login(ctx, "ayse", "headquarters-test-2"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("closed account: %v", err)
	}
	if _, err := auth.SetDisabled(ctx, Installer, "ayse", false); err != nil {
		t.Fatal(err)
	}
	// A reopened account signs in; a successful sign-in resets the wrong-attempt counter.
	if _, _, err := auth.Login(ctx, "ayse", "headquarters-test-2"); err != nil {
		t.Fatalf("reopened account: %v", err)
	}

	// After five wrong attempts that username is locked; even the right password
	// does not pass. Other users are not affected.
	if _, err := auth.AddUser(ctx, Installer, "mehmet", "headquarters-test-3", UserInput{Role: domain.RoleReport, AllStores: true}); err != nil {
		t.Fatal(err)
	}
	var locked LockedError
	for i := range login.Default.MaxFailures {
		_, _, err := auth.Login(ctx, "ayse", "yanlış-parola")
		if i < login.Default.MaxFailures-1 && !errors.Is(err, ErrWrongPassword) {
			t.Fatalf("deneme %d: %v", i, err)
		}
		if i == login.Default.MaxFailures-1 && !errors.As(err, &locked) {
			t.Fatalf("kilitlenmedi: %v", err)
		}
	}
	if _, _, err := auth.Login(ctx, "ayse", "headquarters-test-2"); !errors.As(err, &locked) {
		t.Errorf("sign-in while locked: %v", err)
	}
	if _, _, err := auth.Login(ctx, "mehmet", "headquarters-test-3"); err != nil {
		t.Errorf("another user: %v", err)
	}
	now = now.Add(login.Default.Lock + time.Second)
	if _, _, err := auth.Login(ctx, "ayse", "headquarters-test-2"); err != nil {
		t.Errorf("sign-in after the lock lifted: %v", err)
	}
}

func TestStorePrices(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	monday := time.Date(2026, 11, 1, 21, 0, 0, 0, time.UTC)
	headquarters := []domain.PricePoint{{Amount: 1500}}
	reports := &fakeReports{storePrices: []domain.StorePriceRow{
		{StoreID: "m2", StoreName: "Moda", ProductID: "ekmek", ProductName: "Ekmek", HeadquartersPrices: headquarters, StorePrices: []domain.PricePoint{{Amount: 1700}}},
		{StoreID: "m1", StoreName: "Kadıköy", ProductID: "simit", ProductName: "Simit", HeadquartersPrices: headquarters,
			StorePrices: []domain.PricePoint{{Amount: 1800, From: monday}}},
		{StoreID: "m1", StoreName: "Kadıköy", ProductID: "cay", ProductName: "Çay", HeadquartersPrices: headquarters, StorePrices: []domain.PricePoint{{Amount: 1400}}},
		// The exception was lifted: it does not enter the report.
		{StoreID: "m1", StoreName: "Kadıköy", ProductID: "su", ProductName: "Su", HeadquartersPrices: headquarters,
			StorePrices: []domain.PricePoint{{Amount: 1, From: now.Add(-time.Hour)}, {From: now.Add(-time.Minute), Clear: true}}},
	}}
	svc := NewService(reports, ist)
	svc.now = func() time.Time { return now }
	got, err := svc.StorePrices(context.Background(), "", admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ProductID != "cay" || got[1].ProductID != "simit" || got[2].StoreName != "Moda" {
		t.Fatalf("order %+v", got)
	}
	if !got[0].HasStore || got[0].Difference() != -100 || got[1].HasStore || len(got[1].Upcoming) != 1 || got[2].Difference() != 200 {
		t.Errorf("fiyatlar %+v", got)
	}
	// The user sees only the prices of the stores they are allowed.
	if got, _ := svc.StorePrices(context.Background(), "", domain.User{Role: domain.RoleCatalog, Stores: []string{"m2"}}); len(got) != 1 || got[0].StoreID != "m2" {
		t.Errorf("user with limited stores: %+v", got)
	}
}
