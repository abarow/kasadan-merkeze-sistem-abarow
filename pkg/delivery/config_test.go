// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package delivery

import (
	"strings"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	const day = 24 * time.Hour
	for _, tt := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"90d", 90 * day, true},
		{"1d", day, true},
		{"2d12h", 2*day + 12*time.Hour, true},
		{"36h", 36 * time.Hour, true},
		{"1h30m", 90 * time.Minute, true},
		{" 30d ", 30 * day, true},
		{"", 0, false},
		{"d", 0, false},
		{"xd", 0, false},
		{"-1h", 0, false},
		{"2d-1h", 0, false},
		{"90", 0, false},
		{"1.5d", 0, false},
		{"abc", 0, false},
	} {
		got, err := ParseDuration(tt.in)
		if (err == nil) != tt.ok || (tt.ok && got != tt.want) {
			t.Errorf("ParseDuration(%q) = %v, %v; %v (ok=%v) beklenir", tt.in, got, err, tt.want, tt.ok)
		}
	}
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestCleanConfigFromEnv(t *testing.T) {
	const def = 90 * 24 * time.Hour
	// The defaults if nothing is given.
	c, err := CleanConfigFromEnv(env(nil), def)
	if err != nil || c.MaxAge != def || c.Interval != DefaultInterval || c.Budget != 0 {
		t.Fatalf("default = %+v, %v", c, err)
	}
	// Hepsi verilirse.
	c, err = CleanConfigFromEnv(env(map[string]string{
		EnvCleanMaxAge: "45d", EnvCleanInterval: "10m", EnvCleanBudget: "500",
	}), def)
	if err != nil || c.MaxAge != 45*24*time.Hour || c.Interval != 10*time.Minute || c.Budget != 500 {
		t.Fatalf("ortamdan = %+v, %v", c, err)
	}
	// An invalid value gives an error and names the variable.
	for name, v := range map[string]string{
		EnvCleanMaxAge:   "çok",
		EnvCleanInterval: "0s",
		EnvCleanBudget:   "0",
	} {
		_, err := CleanConfigFromEnv(env(map[string]string{name: v}), def)
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s=%q: error %v; it should contain the variable's name", name, v, err)
		}
	}
	if _, err := CleanConfigFromEnv(env(map[string]string{EnvCleanBudget: "x"}), def); err == nil {
		t.Error("a non-numeric budget was accepted")
	}
}

func TestReceiptMaxAgeFromEnv(t *testing.T) {
	if d, err := ReceiptMaxAgeFromEnv(env(nil)); err != nil || d != ReceiptMaxAge {
		t.Errorf("default = %v, %v", d, err)
	}
	if d, err := ReceiptMaxAgeFromEnv(env(map[string]string{EnvReceiptMaxAge: "7d"})); err != nil || d != 7*24*time.Hour {
		t.Errorf("7d = %v, %v", d, err)
	}
	if _, err := ReceiptMaxAgeFromEnv(env(map[string]string{EnvReceiptMaxAge: "5s"})); err == nil {
		t.Error("a receipt period of 5 seconds was accepted")
	}
}

func TestReceiptConfigMaxAge(t *testing.T) {
	if got := ReceiptConfig("", "", 0).MaxAge; got != ReceiptMaxAge {
		t.Errorf("default MaxAge = %v", got)
	}
	if got := ReceiptConfig("", "", 48*time.Hour).MaxAge; got != 48*time.Hour {
		t.Errorf("verilen MaxAge = %v", got)
	}
	cfg := ReceiptConfig("store-1", "register-1", 0)
	if len(cfg.Sources) != 1 || cfg.Sources[0].FilterSubject != "receipt.register-1.>" || cfg.Sources[0].Domain != "store-1" {
		t.Errorf("kaynak = %+v", cfg.Sources)
	}
}
