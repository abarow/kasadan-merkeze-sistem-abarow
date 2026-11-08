// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package personnel

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRules(t *testing.T) {
	for _, pin := range []string{"1234", "12345678"} {
		if ValidatePIN(pin) != nil {
			t.Errorf("%q reddedildi", pin)
		}
	}
	for _, pin := range []string{"", "123", "123456789", "12a4", "١٢٣٤"} {
		if !errors.Is(ValidatePIN(pin), ErrInvalidPIN) {
			t.Errorf("%q was accepted", pin)
		}
	}
	if ValidateID("ayse-1_x") != nil || !errors.Is(ValidateID("Ayşe"), ErrInvalidID) || !errors.Is(ValidateID(""), ErrInvalidID) {
		t.Error("code rule")
	}
	if n, err := CleanName("  Ayşe Yılmaz "); err != nil || n != "Ayşe Yılmaz" {
		t.Errorf("ad: %q %v", n, err)
	}
	if _, err := CleanName("   "); !errors.Is(err, ErrInvalidName) {
		t.Error("an empty name was accepted")
	}
	if ValidateRole(RoleManager) != nil || !errors.Is(ValidateRole("admin"), ErrInvalidRole) {
		t.Error("role rule")
	}
	if RoleName(RoleManager) != "müdür" || RoleName(RoleCashier) != "kasiyer" {
		t.Error("role names")
	}
}

func TestEncodeDecode(t *testing.T) {
	c := Cashier{StoreID: "store-1", ID: "ayse", Name: "Ayşe", Role: RoleManager, PINHash: []byte("özet"),
		Disabled: true, UpdatedBy: "Müdür", UpdatedAt: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)}
	data, err := Encode(c, "e1", true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode("application/protobuf", data)
	if err != nil || !reflect.DeepEqual(got, c) {
		t.Errorf("sent to the register:\n%+v %v\nwant\n%+v", got, err, c)
	}
	// There is no PIN digest in the event that goes to headquarters.
	data, _ = Encode(c, "e2", false)
	if got, _ := Decode("application/protobuf", data); got.PINHash != nil || got.Name != "Ayşe" {
		t.Errorf("sent to headquarters: %+v", got)
	}
	if _, err := Decode("application/json", data); !errors.Is(err, ErrUndecodable) {
		t.Errorf("JSON: %v", err)
	}
}
