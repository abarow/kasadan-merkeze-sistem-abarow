// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package memory is the app.Baskets adapter that keeps baskets in memory;
// it is used in tests. At the register baskets are stored in SQLite.
package memory

import (
	"context"
	"sync"
	"time"

	"abarow.com/services/modules/retail/internal/app"
	"abarow.com/services/modules/retail/internal/domain"
)

// Baskets, app.Baskets'i uygular.
type Baskets struct {
	mu      sync.Mutex
	baskets map[domain.BasketID]*domain.Basket
}

var _ app.Baskets = (*Baskets)(nil)

// NewBaskets returns an empty store.
func NewBaskets() *Baskets { return &Baskets{baskets: map[domain.BasketID]*domain.Basket{}} }

func (s *Baskets) Get(_ context.Context, id domain.BasketID) (*domain.Basket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.baskets[id]
	if !ok {
		return nil, app.ErrNotFound
	}
	return b.Clone(), nil
}

func (s *Baskets) Save(_ context.Context, b *domain.Basket) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.baskets[b.ID] = b.Clone()
	return nil
}

func (s *Baskets) Latest(_ context.Context, cashierID string, since time.Time) (*domain.Basket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest *domain.Basket
	for _, b := range s.baskets {
		if b.CashierID == cashierID && !b.UpdatedAt.Before(since) && (latest == nil || b.UpdatedAt.After(latest.UpdatedAt)) {
			latest = b
		}
	}
	if latest == nil {
		return nil, app.ErrNotFound
	}
	return latest.Clone(), nil
}

func (s *Baskets) DeleteBefore(_ context.Context, before time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, b := range s.baskets {
		if b.UpdatedAt.Before(before) {
			delete(s.baskets, id)
		}
	}
	return nil
}
