// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package login

import (
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	var tr Tries
	for want := 4; want > 0; want-- {
		if got := tr.Fail(now, Default); got != want {
			t.Fatalf("kalan %d, istenen %d", got, want)
		}
	}
	if tr.Fail(now, Default) != 0 || !tr.Locked(now) || !tr.LockedUntil.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("kilit: %+v", tr)
	}
	// The record is not forgotten until the longest lock duration (1 hour) has passed: someone waiting out the
	// lock cannot reset the counter.
	if tr.Locked(now.Add(5*time.Minute)) || tr.Idle(now.Add(6*time.Minute), Default) || tr.Idle(now.Add(time.Hour), Default) || !tr.Idle(now.Add(61*time.Minute), Default) {
		t.Errorf("lock time: %+v", tr)
	}
	tr.Succeed()
	if tr != (Tries{}) {
		t.Errorf("not reset: %+v", tr)
	}
}

func TestDoubling(t *testing.T) {
	r := Rule{MaxFailures: 5, Lock: 5 * time.Minute, MaxLock: time.Hour}
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	var tr Tries
	for _, want := range []time.Duration{5, 10, 20, 40, 60, 60} {
		for range r.MaxFailures {
			tr.Fail(now, r)
		}
		if got := tr.LockedUntil.Sub(now); got != want*time.Minute {
			t.Errorf("kilit %v, istenen %v dk", got, want)
		}
	}
}

func TestToken(t *testing.T) {
	a, b := NewToken(), NewToken()
	if a == b || len(a) != 43 || TokenHash(a) == TokenHash(b) || TokenHash(a) != TokenHash(string([]byte(a))) {
		t.Errorf("anahtarlar %q %q", a, b)
	}
}
