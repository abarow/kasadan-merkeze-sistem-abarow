// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package personneldb

import (
	"context"
	"reflect"
	"testing"

	"abarow.com/pkg/personnel"
)

func TestIdentityColumns(t *testing.T) {
	for name, x := range databases(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(x)
			ayse := personnel.User{ID: "ayse", Name: "Ayşe", Role: personnel.RoleReport, Stores: []string{"m1"},
				Email: "ayse@firma.example", CreatedAt: t0, UpdatedAt: t0}
			if err := save(t, s, ayse, true, "eklendi", false); err != nil {
				t.Fatal(err)
			}
			ali := personnel.User{ID: "ali", Name: "Ali", Role: personnel.RoleReport, Stores: []string{"m1"}, CreatedAt: t0, UpdatedAt: t0}
			if err := save(t, s, ali, true, "eklendi", false); err != nil {
				t.Fatal(err)
			}

			// The identity link is written and read.
			ayse.Issuer, ayse.Subject = "https://idp.example", "s-1"
			if err := save(t, s, ayse, false, "bağlandı", false); err != nil {
				t.Fatal(err)
			}
			if got, err := s.User(ctx, "ayse"); err != nil || !reflect.DeepEqual(got, ayse) {
				t.Errorf("person %+v %v, want %+v", got, err, ayse)
			}
			list, err := s.Users(ctx)
			if err != nil || len(list) != 2 || list[0].Subject != "" || list[1].Subject != "s-1" {
				t.Errorf("list %+v %v", list, err)
			}

			// The database also prevents a double link and a double address (the Manage check is
			// not enough in a race).
			ali.Issuer, ali.Subject = "https://idp.example", "s-1"
			if err := save(t, s, ali, false, "bağlandı", false); err == nil {
				t.Error("the same identity was linked to two people")
			}
			ali.Issuer, ali.Subject, ali.Email = "", "", "ayse@firma.example"
			if err := save(t, s, ali, false, "adres", false); err == nil {
				t.Error("the same address is registered to two people")
			}
			// The same Subject is free at another IdP; empty addresses do not collide.
			ali.Email, ali.Issuer, ali.Subject = "", "https://baska.example", "s-1"
			if err := save(t, s, ali, false, "başka IdP", false); err != nil {
				t.Errorf("another IdP: %v", err)
			}
		})
	}
}
