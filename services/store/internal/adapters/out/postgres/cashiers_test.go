// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/services/store/internal/adapters/out/cashierproto"
	"abarow.com/services/store/internal/adapters/out/postgres/postgrestest"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

func TestCashiers(t *testing.T) {
	ctx := context.Background()
	st := postgrestest.Open(t)
	n := 0
	enc := cashierproto.Encoder{StoreID: "store-1", NewID: func() string { n++; return fmt.Sprintf("e%d", n) }}
	cashiers := app.NewCashiers(st.People(), enc)
	cashiers.HashPIN = func(pin string) ([]byte, error) { return bcrypt.GenerateFromPassword([]byte(pin), bcrypt.MinCost) }
	now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	cashiers.SetClock(func() time.Time { return now })

	if _, err := cashiers.Add(ctx, "mağaza", "zeynep", " Zeynep Demir ", domain.RoleCashier, "2468", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := cashiers.Add(ctx, "mağaza", "cem", "Cem Öz", domain.RoleManager, "13579", ""); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, name, pin string
		role          domain.Role
		want          error
	}{
		{"zeynep", "Zeynep", "1234", domain.RoleCashier, domain.ErrCashierExists},
		{"Ayşe", "Ayşe", "1234", domain.RoleCashier, domain.ErrInvalidCashierID},
		{"a.b", "A", "1234", domain.RoleCashier, domain.ErrInvalidCashierID},
		{"ayse", "  ", "1234", domain.RoleCashier, domain.ErrInvalidName},
		{"ayse", "Ayşe", "12a4", domain.RoleCashier, domain.ErrInvalidPIN},
		{"ayse", "Ayşe", "123", domain.RoleCashier, domain.ErrInvalidPIN},
		{"ayse", "Ayşe", "1234", "patron", domain.ErrInvalidRole},
	} {
		if _, err := cashiers.Add(ctx, "mağaza", tc.id, tc.name, tc.role, tc.pin, ""); !errors.Is(err, tc.want) {
			t.Errorf("%q %q %q %q: error = %v, want %v", tc.id, tc.name, tc.pin, tc.role, err, tc.want)
		}
	}

	// Name and role; PIN; closing the account. An unchanged field produces no record.
	if _, err := cashiers.Update(ctx, "mağaza", "zeynep", "Zeynep Demir", domain.RoleCashier, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := cashiers.Update(ctx, "mağaza", "zeynep", "Zeynep Kaya", domain.RoleManager, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := cashiers.SetPIN(ctx, "mağaza", "zeynep", "8642"); err != nil {
		t.Fatal(err)
	}
	c, err := cashiers.SetDisabled(ctx, "mağaza", "zeynep", true)
	if err != nil || !c.Disabled {
		t.Fatalf("hesap kapatma: %+v %v", c, err)
	}
	if _, err := cashiers.SetDisabled(ctx, "mağaza", "yok", true); !errors.Is(err, domain.ErrCashierNotFound) {
		t.Errorf("cashier that does not exist: %v", err)
	}
	c, history, err := cashiers.Get(ctx, "zeynep")
	if err != nil || c.Name != "Zeynep Kaya" || c.Role != domain.RoleManager || !c.Disabled ||
		bcrypt.CompareHashAndPassword(c.PINHash, []byte("8642")) != nil {
		t.Fatalf("cashier %+v %v", c, err)
	}
	wantHistory := []string{"hesap kapatıldı", "PIN değişti", "ad: Zeynep Demir → Zeynep Kaya; rol: kasiyer → müdür", "eklendi: Zeynep Demir (kasiyer)"}
	if len(history) != len(wantHistory) {
		t.Fatalf("history %+v", history)
	}
	for i, h := range history {
		if h.Detail != wantHistory[i] || h.By != "mağaza" || !h.At.Equal(now) {
			t.Errorf("history %d: %+v", i, h)
		}
	}
	list, err := cashiers.List(ctx)
	if err != nil || len(list) != 2 || list[0].ID != "cem" || list[1].ID != "zeynep" {
		t.Errorf("list (open accounts first): %+v %v", list, err)
	}

	// Loading from a file: an existing one is updated (a closed account stays closed), a missing one
	// is added; from a faulty file none is loaded.
	if _, err := cashiers.Import(ctx, "kurulum", []app.SeedCashier{
		{ID: "ayse", Name: "Ayşe", Role: domain.RoleCashier, PIN: "1111"},
		{ID: "ali", Name: "Ali", Role: domain.RoleManager, PIN: "99"},
	}); !errors.Is(err, domain.ErrInvalidPIN) {
		t.Errorf("faulty file: %v", err)
	}
	if _, _, err := cashiers.Get(ctx, "ayse"); !errors.Is(err, domain.ErrCashierNotFound) {
		t.Errorf("cashiers were loaded from a faulty file: %v", err)
	}
	count, err := cashiers.Import(ctx, "kurulum", []app.SeedCashier{
		{ID: "ayse", Name: "Kasiyer Ayşe", Role: domain.RoleCashier, PIN: "1111"},
		{ID: "zeynep", Name: "Zeynep Kaya", Role: domain.RoleCashier, PIN: "2222"},
	})
	if err != nil || count != 2 {
		t.Fatalf("load: %d %v", count, err)
	}
	c, history, _ = cashiers.Get(ctx, "zeynep")
	if !c.Disabled || c.Role != domain.RoleCashier || history[0].Detail != "dosyadan yüklendi: Zeynep Kaya (kasiyer)" || history[0].By != "kurulum" {
		t.Errorf("updated from the file: %+v %+v", c, history[0])
	}
	names, err := cashiers.Names(ctx)
	if err != nil || names["ayse"] != "Kasiyer Ayşe" || names["cem"] != "Cem Öz" {
		t.Errorf("adlar %v %v", names, err)
	}

	// Every change is two messages: to the registers with the PIN digest, to headquarters without the PIN.
	pending, err := st.Pending(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	// 2 additions + 1 name/role + PIN + closing + 2 loads = 7 changes.
	if len(pending) != 14 {
		t.Fatalf("%d messages in the outbox", len(pending))
	}
	cfg, ev := pending[len(pending)-2], pending[len(pending)-1]
	if cfg.Subject != "config.cashier.zeynep" || cfg.Type != cashierproto.ConfigType || ev.Subject != "store.cashier.changed.v1" || ev.Type != cashierproto.EventType {
		t.Fatalf("mesajlar %s %s / %s %s", cfg.Subject, cfg.Type, ev.Subject, ev.Type)
	}
	var cs, es staffv1.CashierState
	if err := proto.Unmarshal(cfg.Payload, &cs); err != nil {
		t.Fatal(err)
	}
	if err := proto.Unmarshal(ev.Payload, &es); err != nil {
		t.Fatal(err)
	}
	if cs.GetCashierId() != "zeynep" || cs.GetName() != "Zeynep Kaya" || cs.GetRole() != "cashier" || !cs.GetDisabled() ||
		cs.GetStoreId() != "store-1" || cs.GetUpdatedBy() != "kurulum" || bcrypt.CompareHashAndPassword(cs.GetPinHash(), []byte("2222")) != nil {
		t.Errorf("sent to the registers %+v", &cs)
	}
	if len(es.GetPinHash()) != 0 || es.GetName() != "Zeynep Kaya" || !es.GetDisabled() || es.GetEventId() == cs.GetEventId() {
		t.Errorf("sent to headquarters %+v", &es)
	}
}
