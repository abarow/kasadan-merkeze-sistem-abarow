// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"testing"
	"time"
)

func TestUsername(t *testing.T) {
	for in, want := range map[string]string{"ayse": "ayse", " Mehmet.E ": "mehmet.e", "mudur_1": "mudur_1", "İrem": "irem", "IREM": "irem"} {
		if got, err := Username(in); err != nil || got != want {
			t.Errorf("Username(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "a", "ayşe", "ırem", "-ayse", "ali veli", "a234567890123456789012345678901234"} {
		if _, err := Username(bad); !errors.Is(err, ErrInvalidUsername) {
			t.Errorf("Username(%q) kabul edildi", bad)
		}
	}
	if ValidatePassword("kısa-parola") != nil || ValidatePassword("kısaparol") == nil {
		t.Error("the password length should be counted in characters")
	}
}

func TestFreshen(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	s := StoreOverview{Link: LinkConnected, CheckedAt: now.Add(-20 * time.Second)}
	if s.Freshen(now).Link != LinkConnected {
		t.Error("the new state counted as unknown")
	}
	s.CheckedAt = now.Add(-2 * time.Minute)
	if s.Freshen(now).Link != LinkUnknown {
		t.Error("the old state counted as valid")
	}
}
