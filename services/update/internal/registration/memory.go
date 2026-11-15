// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package registration

import (
	"context"
	"crypto/ed25519"
	"errors"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"

	"abarow.com/services/update/internal/token"
)

// Memory is the Store that keeps the registrations in memory (for tests and trials).
type Memory struct {
	mu sync.Mutex
	m  map[string]Request
}

// NewMemory returns an empty memory store.
func NewMemory() *Memory { return &Memory{m: map[string]Request{}} }

func clone(r Request) Request {
	r.Public = slices.Clone(r.Public)
	r.SigningKey = slices.Clone(r.SigningKey)
	if r.Report != nil {
		c := *r.Report
		c.Units = maps.Clone(c.Units)
		r.Report = &c
	}
	return r
}

// Get returns the registration.
func (b *Memory) Get(_ context.Context, device string) (Request, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.m[device]
	if !ok {
		return Request{}, ErrNotFound
	}
	return clone(r), nil
}

// Create adds a new registration.
func (b *Memory) Create(_ context.Context, r Request) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.m[r.Device]; ok {
		return ErrExists
	}
	b.m[r.Device] = clone(r)
	return nil
}

// Update changes the registration only if the status and attempt count are the same as the old one.
func (b *Memory) Update(_ context.Context, old, next Request) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cur, ok := b.m[old.Device]
	if !ok {
		return ErrNotFound
	}
	if cur.Status != old.Status || cur.Attempts != old.Attempts {
		return ErrConflict
	}
	next.Report = cur.Report // a decision update does not delete the report
	b.m[old.Device] = clone(next)
	return nil
}

// SaveReport, durum bildirimini yazar.
func (b *Memory) SaveReport(_ context.Context, device string, r Report) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cur, ok := b.m[device]
	if !ok {
		return ErrNotFound
	}
	cur.Report = &r
	b.m[device] = clone(cur)
	return nil
}

// List returns the registrations in the given status in creation order.
func (b *Memory) List(_ context.Context, status Status) ([]Request, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []Request
	for _, r := range b.m {
		if r.Status == status {
			out = append(out, clone(r))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// SigningIssuer is the Issuer that signs the token with the given key. At headquarters Key is
// headquarters' token signing key and Chain is the headquarters certificate; at the publisher Key
// is the root key and Chain is empty (the root can issue certificates too).
type SigningIssuer struct {
	Key   ed25519.PrivateKey
	ID    string
	Chain string
}

// Issue produces the token.
func (i SigningIssuer) Issue(c token.Claims, now time.Time) (string, error) {
	return token.IssueToken(i.Key, i.ID, c, i.Chain, now)
}

// Certify gives a headquarters certificate; only a signer with no chain (the root) can give it.
func (i SigningIssuer) Certify(in token.CertInput) (string, error) {
	if i.Chain != "" {
		return "", errors.New("sertifikayı yalnızca yayıncı kökü verebilir")
	}
	return token.IssueCert(i.Key, in)
}
