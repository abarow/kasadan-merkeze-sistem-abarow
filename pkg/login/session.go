// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package login

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrNoSession means there is no session, it has expired or been closed.
var ErrNoSession = errors.New("giriş gerekli")

// LockedError is returned while the sign-in is locked after many wrong attempts.
type LockedError struct{ Until time.Time }

func (e LockedError) Error() string {
	return fmt.Sprintf("çok sayıda yanlış deneme; giriş %s'e kadar kilitli", e.Until.Format("15:04"))
}

// Guard counts and locks the wrong attempts of the sign-in routes. The key
// is a sign-in route (username, manager code, store password). Verification
// is done under the lock: attempts sent at the same time are counted in order,
// the lock cannot be bypassed.
type Guard struct {
	rule Rule
	now  func() time.Time

	mu    sync.Mutex
	tries map[string]*Tries
}

// NewGuard returns a Guard by the rule. time.Now is used if now is empty.
func NewGuard(rule Rule, now func() time.Time) *Guard {
	if now == nil {
		now = time.Now
	}
	return &Guard{rule: rule, now: now, tries: map[string]*Tries{}}
}

// Attempt makes a sign-in attempt on the route key. If the route is locked verify
// is not called and LockedError is returned. verify returns true on a right credential:
// the counter is reset. If wrong (false) the attempt is counted and ok is false; if this
// attempt triggered the lock LockedError is returned.
func (g *Guard) Attempt(key string, verify func() bool) (ok bool, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for k, t := range g.tries { // memory should not grow with every name tried
		if t.Idle(now, g.rule) {
			delete(g.tries, k)
		}
	}
	t := g.tries[key]
	if t == nil {
		t = &Tries{}
	}
	if t.Locked(now) {
		return false, LockedError{Until: t.LockedUntil}
	}
	if verify() {
		delete(g.tries, key)
		return true, nil
	}
	g.tries[key] = t
	if t.Fail(now, g.rule) > 0 {
		return false, nil
	}
	return false, LockedError{Until: t.LockedUntil}
}

// Session is the stored form of a session. The key itself is not stored,
// its digest is.
type Session struct {
	// Subject is the id of the person who opened the session (username, cashier
	// code...); Directory knows its meaning.
	Subject string
	// Binding is the fingerprint of the credential at sign-in (e.g. the PIN digest):
	// Directory compares the person's current fingerprint with it, and if it
	// changed the session closes. Empty where not needed.
	Binding []byte
	Expires time.Time
}

// Store is where the sessions are kept (memory, SQLite, PostgreSQL).
// The key is its digest.
type Store interface {
	// Put saves the session (replaces it if it exists) and may delete the expired ones.
	Put(ctx context.Context, key [32]byte, s Session, now time.Time) error
	// Get returns the session; ok is false if it does not exist or has expired.
	Get(ctx context.Context, key [32]byte, now time.Time) (s Session, ok bool, err error)
	// Delete, oturumu siler.
	Delete(ctx context.Context, key [32]byte) error
	// DeleteSubject deletes all of the person's sessions.
	DeleteSubject(ctx context.Context, subject string) error
}

// Directory is the live record of the person in the session. It is asked on every request:
// if the account was closed, the authority removed or the fingerprint changed it returns
// ErrNoSession and the session closes; otherwise it returns the current person (their
// name or role may have changed).
type Directory interface {
	Resolve(ctx context.Context, s Session) (Person, error)
}

// Sessions manages the screen sessions; it does not know which adapter
// verified the identity.
type Sessions struct {
	store Store
	dir   Directory
	ttl   time.Duration
	now   func() time.Time
}

// NewSessions returns a Sessions that keeps the sessions in store and asks dir about the
// person. time.Now is used if now is empty.
func NewSessions(store Store, dir Directory, ttl time.Duration, now func() time.Time) *Sessions {
	if now == nil {
		now = time.Now
	}
	return &Sessions{store: store, dir: dir, ttl: ttl, now: now}
}

// TTL is the session's validity period (the cookie's lifetime).
func (s *Sessions) TTL() time.Duration { return s.ttl }

// Open opens a new session for a person whose identity is verified and returns its key.
func (s *Sessions) Open(ctx context.Context, subject string, binding []byte) (string, error) {
	token := NewToken()
	now := s.now()
	if err := s.store.Put(ctx, TokenHash(token), Session{Subject: subject, Binding: binding, Expires: now.Add(s.ttl)}, now); err != nil {
		return "", err
	}
	return token, nil
}

// Person returns the key's person; ErrNoSession if the session is invalid.
func (s *Sessions) Person(ctx context.Context, token string) (Person, error) {
	if token == "" {
		return Person{}, ErrNoSession
	}
	key := TokenHash(token)
	sess, ok, err := s.store.Get(ctx, key, s.now())
	if err != nil {
		return Person{}, err
	}
	if !ok {
		return Person{}, ErrNoSession
	}
	p, err := s.dir.Resolve(ctx, sess)
	if errors.Is(err, ErrNoSession) {
		_ = s.store.Delete(ctx, key)
		return Person{}, ErrNoSession
	}
	return p, err
}

// Rebind renews the open session's fingerprint: when the person changes their own
// PIN this session stays open, the others opened with the old PIN close.
// If the session belongs to another person or does not exist it does nothing.
func (s *Sessions) Rebind(ctx context.Context, token, subject string, binding []byte) error {
	key := TokenHash(token)
	now := s.now()
	sess, ok, err := s.store.Get(ctx, key, now)
	if err != nil || !ok || sess.Subject != subject || bytes.Equal(sess.Binding, binding) {
		return err
	}
	sess.Binding = binding
	return s.store.Put(ctx, key, sess, now)
}

// Close closes the session.
func (s *Sessions) Close(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.Delete(ctx, TokenHash(token))
}

// CloseSubject closes all of the person's sessions (the password changed, the account
// was closed).
func (s *Sessions) CloseSubject(ctx context.Context, subject string) error {
	return s.store.DeleteSubject(ctx, subject)
}

// Memory is the Store that keeps the sessions in memory; when the service restarts
// the sessions are lost.
type Memory struct {
	mu       sync.Mutex
	sessions map[[32]byte]Session
}

// NewMemory returns an empty memory Store.
func NewMemory() *Memory { return &Memory{sessions: map[[32]byte]Session{}} }

var _ Store = (*Memory)(nil)

func (m *Memory) Put(_ context.Context, key [32]byte, s Session, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range m.sessions { // clean up the expired ones
		if !now.Before(v.Expires) {
			delete(m.sessions, k)
		}
	}
	m.sessions[key] = s
	return nil
}

func (m *Memory) Get(_ context.Context, key [32]byte, now time.Time) (Session, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[key]
	if !ok || !now.Before(s.Expires) {
		return Session{}, false, nil
	}
	return s, true, nil
}

func (m *Memory) Delete(_ context.Context, key [32]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, key)
	return nil
}

func (m *Memory) DeleteSubject(_ context.Context, subject string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range m.sessions {
		if v.Subject == subject {
			delete(m.sessions, k)
		}
	}
	return nil
}
