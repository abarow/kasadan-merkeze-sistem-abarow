// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package personneltest is the in-memory person and session store for the services'
// tests (personnel.Store and login.Store).
package personneltest

import (
	"context"
	"slices"
	"strings"
	"sync"

	"abarow.com/pkg/login"
	"abarow.com/pkg/personnel"
)

// Store is the in-memory person and session store.
type Store struct {
	*login.Memory

	// Changes are the change records.
	Changes []personnel.Change

	mu    sync.Mutex
	users map[string]personnel.User
	// Saved is every Save request (tests look at the messages that are propagated).
	Saved []personnel.SaveRequest
}

var (
	_ personnel.Store = (*Store)(nil)
	_ login.Store     = (*Store)(nil)
)

// New returns an empty store.
func New() *Store { return &Store{Memory: login.NewMemory(), users: map[string]personnel.User{}} }

func (s *Store) User(_ context.Context, id string) (personnel.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return personnel.User{}, personnel.ErrNotFound
	}
	return u, nil
}

func (s *Store) Users(context.Context) ([]personnel.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]personnel.User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u)
	}
	slices.SortFunc(out, func(a, b personnel.User) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (s *Store) Save(ctx context.Context, r personnel.SaveRequest) error {
	s.mu.Lock()
	if _, ok := s.users[r.User.ID]; ok && r.New {
		s.mu.Unlock()
		return personnel.ErrExists
	}
	if _, ok := s.users[r.User.ID]; !ok && !r.New {
		s.mu.Unlock()
		return personnel.ErrNotFound
	}
	s.users[r.User.ID] = r.User
	s.Changes = append(s.Changes, r.Change)
	s.Saved = append(s.Saved, r)
	s.mu.Unlock()
	if r.CloseSessions {
		return s.DeleteSubject(ctx, r.User.ID)
	}
	return nil
}

func (s *Store) History(_ context.Context, id string, limit int) ([]personnel.Change, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []personnel.Change
	for i := len(s.Changes) - 1; i >= 0 && len(out) < limit; i-- {
		if s.Changes[i].ID == id {
			out = append(out, s.Changes[i])
		}
	}
	return out, nil
}
