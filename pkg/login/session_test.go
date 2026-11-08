// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package login

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGuardAttempt(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	g := NewGuard(Rule{MaxFailures: 3, Lock: 5 * time.Minute}, func() time.Time { return now })
	no := func() bool { return false }
	yes := func() bool { return true }

	for range 2 {
		if ok, err := g.Attempt("ali", no); ok || err != nil {
			t.Fatalf("wrong attempt: ok=%v err=%v", ok, err)
		}
	}
	// Another route is not affected; a right sign-in resets its own counter.
	if ok, err := g.Attempt("veli", yes); !ok || err != nil {
		t.Fatalf("veli: ok=%v err=%v", ok, err)
	}
	// The third wrong one locks and LockedError is returned.
	var locked LockedError
	if _, err := g.Attempt("ali", no); !errors.As(err, &locked) || !locked.Until.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("lock: %v", err)
	}
	// While locked verify is not called, and the right credential is not accepted either.
	called := false
	if ok, err := g.Attempt("ali", func() bool { called = true; return true }); ok || !errors.As(err, &locked) || called {
		t.Fatalf("kilitliyken: ok=%v err=%v called=%v", ok, err, called)
	}
	now = now.Add(6 * time.Minute)
	if ok, err := g.Attempt("ali", yes); !ok || err != nil {
		t.Fatalf("after the lock: ok=%v err=%v", ok, err)
	}
	// After a right sign-in the counter is zero: two wrong ones do not lock.
	for range 2 {
		if _, err := g.Attempt("ali", no); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGuardForgetsIdlePaths(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	g := NewGuard(Default, func() time.Time { return now })
	_, _ = g.Attempt("test", func() bool { return false })
	now = now.Add(2 * time.Hour)
	_, _ = g.Attempt("baska", func() bool { return true })
	if len(g.tries) != 0 {
		t.Errorf("the old record was not forgotten: %v", g.tries)
	}
}

// dir is a Directory that does not recognize the person whose Subject is "kapali" and rejects
// one whose fingerprint does not match.
type dir struct{ binding []byte }

func (d dir) Resolve(_ context.Context, s Session) (Person, error) {
	if s.Subject == "kapali" || string(s.Binding) != string(d.binding) {
		return Person{}, ErrNoSession
	}
	return Person{Username: s.Subject, Name: "Ad " + s.Subject, Role: "admin", AllStores: true}, nil
}

func TestSessions(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	ss := NewSessions(NewMemory(), dir{binding: []byte("a")}, time.Hour, clock)

	token, err := ss.Open(ctx, "ali", []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := ss.Person(ctx, token)
	if err != nil || p.Username != "ali" || p.Name != "Ad ali" {
		t.Fatalf("person: %+v %v", p, err)
	}

	for name, tok := range map[string]string{"boş": "", "bilinmeyen": "yok"} {
		if _, err := ss.Person(ctx, tok); !errors.Is(err, ErrNoSession) {
			t.Errorf("%s anahtar: %v", name, err)
		}
	}

	// The session of a person whose account closed is closed and deleted.
	closed, _ := ss.Open(ctx, "kapali", []byte("a"))
	if _, err := ss.Person(ctx, closed); !errors.Is(err, ErrNoSession) {
		t.Errorf("closed account: %v", err)
	}
	if _, ok, _ := ss.store.Get(ctx, TokenHash(closed), now); ok {
		t.Error("the closed session was not deleted")
	}

	// When the time is up the session becomes invalid.
	now = now.Add(time.Hour)
	if _, err := ss.Person(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("expired session: %v", err)
	}
}

func TestSessionsCloseAndRebind(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	d := &changingDir{binding: []byte("eski")}
	ss := NewSessions(NewMemory(), d, time.Hour, func() time.Time { return now })

	mine, _ := ss.Open(ctx, "ali", []byte("eski"))
	other, _ := ss.Open(ctx, "ali", []byte("eski"))
	someone, _ := ss.Open(ctx, "veli", []byte("eski"))

	// Ali changed their PIN: this session gets the new fingerprint, the other closes.
	d.binding = []byte("yeni")
	if err := ss.Rebind(ctx, mine, "ali", []byte("yeni")); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.Person(ctx, mine); err != nil {
		t.Errorf("renewed session: %v", err)
	}
	if _, err := ss.Person(ctx, other); !errors.Is(err, ErrNoSession) {
		t.Errorf("session with an old fingerprint: %v", err)
	}
	// Another person's session is not touched.
	if err := ss.Rebind(ctx, someone, "ali", []byte("yeni")); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.Person(ctx, someone); !errors.Is(err, ErrNoSession) {
		t.Errorf("someone else's session was renewed: %v", err)
	}

	// Closing all of a person's sessions.
	again, _ := ss.Open(ctx, "ali", []byte("yeni"))
	if err := ss.CloseSubject(ctx, "ali"); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{mine, again} {
		if _, err := ss.Person(ctx, tok); !errors.Is(err, ErrNoSession) {
			t.Errorf("after CloseSubject: %v", err)
		}
	}
	// Closing a single session; an empty key is not an error.
	one, _ := ss.Open(ctx, "ali", []byte("yeni"))
	if err := ss.Close(ctx, one); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.Person(ctx, one); !errors.Is(err, ErrNoSession) {
		t.Errorf("after Close: %v", err)
	}
	if err := ss.Close(ctx, ""); err != nil {
		t.Errorf("empty key: %v", err)
	}
}

type changingDir struct{ binding []byte }

func (d *changingDir) Resolve(_ context.Context, s Session) (Person, error) {
	if string(s.Binding) != string(d.binding) {
		return Person{}, ErrNoSession
	}
	return Person{Username: s.Subject}, nil
}

func TestGuardEscalationSurvivesWaitingOutTheLock(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	g := NewGuard(Rule{MaxFailures: 1, Lock: 5 * time.Minute, MaxLock: time.Hour}, func() time.Time { return now })
	no := func() bool { return false }
	for i, want := range []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute} {
		var locked LockedError
		if _, err := g.Attempt("ali", no); !errors.As(err, &locked) || locked.Until.Sub(now) != want {
			t.Fatalf("lock %d: %v, want %v", i+1, err, want)
		}
		// The lock is waited out; another route's attempt cleans up the old records
		// but the lock duration keeps growing.
		now = locked.Until.Add(time.Second)
		_, _ = g.Attempt("baska", func() bool { return true })
	}
}
