// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StaffGrace is how long the gateway answers with staff's last good answer while staff
// cannot be reached (docs/REVIEW.md B2). Every request on the screen asks staff for the
// session and the open shift; while staff restarts (an update, a crash) the whole
// screen would stop. While staff answers its answer is always used: a sign-out or a
// closed cashier account takes effect at once. During an outage nothing can change
// in staff either, so its last answer is still true; after StaffGrace the request fails.
const StaffGrace = 5 * time.Minute

// lastGood keeps staff's last good answer for each key (the session's digest, the shift).
type lastGood[T any] struct {
	mu     sync.Mutex
	grace  time.Duration
	now    func() time.Time
	what   string
	m      map[string]stamped[T]
	warned time.Time
}

type stamped[T any] struct {
	v  T
	at time.Time
}

func newLastGood[T any](what string, grace time.Duration) *lastGood[T] {
	return &lastGood[T]{grace: grace, now: time.Now, what: what, m: map[string]stamped[T]{}}
}

// do asks staff; if staff cannot be reached it returns the last good answer for key
// that is not older than the grace time.
func (l *lastGood[T]) do(key string, call func() (T, error)) (T, error) {
	v, err := call()
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	switch code := status.Code(err); {
	case err == nil:
		l.m[key] = stamped[T]{v, now}
		l.prune(now)
		return v, nil
	case code == codes.Unavailable || code == codes.DeadlineExceeded:
		if e, ok := l.m[key]; ok && now.Sub(e.at) <= l.grace {
			if now.Sub(l.warned) >= time.Minute {
				slog.Warn("staff cannot be reached; its last answer is used", "what", l.what, "age", now.Sub(e.at).Round(time.Second), "err", err)
				l.warned = now
			}
			return e.v, nil
		}
	default:
		// A definite answer (the session is invalid and the like) ends the stored one.
		delete(l.m, key)
	}
	var zero T
	return zero, err
}

// forget removes the stored answer (sign-out).
func (l *lastGood[T]) forget(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, key)
}

// prune removes the answers older than the grace time once there are many (sessions
// of cashiers who left without signing out).
func (l *lastGood[T]) prune(now time.Time) {
	if len(l.m) < 256 {
		return
	}
	for k, e := range l.m {
		if now.Sub(e.at) > l.grace {
			delete(l.m, k)
		}
	}
}

// tokenKey is the key of a session: the digest, not the session key itself.
func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
