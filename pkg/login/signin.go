// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package login holds the sign-in's shared rules: the wrong-attempt counter and lock,
// producing and digesting the session key. The register (cashier PIN), the store server
// (manager PIN, store password) and headquarters (user password) use the same code;
// only the verifier (PIN or password) and the lock rule
// differ.
package login

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"
)

// Rule is the lock rule: after MaxFailures wrong attempts the sign-in is locked for
// the Lock duration. If MaxLock is given each successive lock lasts twice
// the previous one (at most MaxLock); on a right sign-in the duration starts over.
type Rule struct {
	MaxFailures   int
	Lock, MaxLock time.Duration
}

// Default is the lock rule of all sign-ins (the register, store server and headquarters
// use the same rule): a 5-minute lock after 5 wrong attempts; each successive
// lock lasts twice the previous one (at most 1 hour), on a right sign-in the duration
// starts over. This is the only setting. At the register the lock counter keeps only the cashier's
// recorded attempts (the failed attempt count and the lock time), so
// the duration does not grow there: the first lock duration applies.
var Default = Rule{MaxFailures: 5, Lock: 5 * time.Minute, MaxLock: time.Hour}

// Tries are the wrong attempts of a sign-in route (cashier, manager, username, store
// password).
type Tries struct {
	Failures int
	// Strikes is the number of successive locks (in the rule where the duration doubles).
	Strikes     int
	LockedUntil time.Time
	// Last is the time of the last wrong attempt.
	Last time.Time
}

// Locked says the sign-in is locked at the moment now.
func (t Tries) Locked(now time.Time) bool { return now.Before(t.LockedUntil) }

// Fail counts the wrong attempt and returns the remaining attempts; when the limit is
// reached the sign-in is locked and 0 is returned.
func (t *Tries) Fail(now time.Time, r Rule) (remaining int) {
	t.Failures++
	t.Last = now
	if t.Failures < r.MaxFailures {
		return r.MaxFailures - t.Failures
	}
	d := r.Lock
	if r.MaxLock > 0 {
		for range t.Strikes {
			if d *= 2; d >= r.MaxLock {
				d = r.MaxLock
				break
			}
		}
		t.Strikes++
	}
	t.Failures, t.LockedUntil = 0, now.Add(d)
	return 0
}

// Succeed resets the counter and the lock on a right sign-in.
func (t *Tries) Succeed() { *t = Tries{} }

// Idle says the record can be forgotten: it is not locked and the longest lock
// duration has passed since the last wrong attempt (memory should not grow with every name
// tried). In the rule where the duration doubles the record is kept as long as the longest
// lock; otherwise someone waiting out the lock would reset the counter.
func (t Tries) Idle(now time.Time, r Rule) bool {
	return !t.Locked(now) && now.Sub(t.Last) > max(r.Lock, r.MaxLock)
}

// NewToken produces an unguessable 256-bit session key.
func NewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read returns no error
	return base64.RawURLEncoding.EncodeToString(b)
}

// TokenHash is the stored digest of the session key (SHA-256): someone who reads the database
// or memory cannot open a session.
func TokenHash(token string) [32]byte { return sha256.Sum256([]byte(token)) }
