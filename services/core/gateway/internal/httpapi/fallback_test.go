// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The last good answer is used only while staff cannot be reached and only within the
// grace time; a definite answer of staff removes it.
func TestLastGood(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	l := newLastGood[string]("test", time.Minute)
	l.now = func() time.Time { return now }
	down := func() (string, error) { return "", status.Error(codes.Unavailable, "down") }

	if v, err := l.do("k", func() (string, error) { return "ayse", nil }); err != nil || v != "ayse" {
		t.Fatalf("up: %q %v", v, err)
	}
	now = now.Add(59 * time.Second)
	if v, err := l.do("k", down); err != nil || v != "ayse" {
		t.Errorf("within the grace time: %q %v", v, err)
	}
	if _, err := l.do("other", down); status.Code(err) != codes.Unavailable {
		t.Errorf("no stored answer: %v", err)
	}
	now = now.Add(2 * time.Second)
	if _, err := l.do("k", down); status.Code(err) != codes.Unavailable {
		t.Errorf("after the grace time: %v", err)
	}
	// A definite answer removes the stored one.
	l.do("k", func() (string, error) { return "ayse", nil })
	l.do("k", func() (string, error) { return "", status.Error(codes.Unauthenticated, "ended") })
	if _, err := l.do("k", down); status.Code(err) != codes.Unavailable {
		t.Errorf("after an invalid session: %v", err)
	}
	// Any other error than unreachable is not covered either.
	l.do("k", func() (string, error) { return "ayse", nil })
	if _, err := l.do("k", func() (string, error) { return "", errors.New("boom") }); err == nil {
		t.Error("a plain error was hidden")
	}
}
