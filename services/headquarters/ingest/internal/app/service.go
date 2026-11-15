// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package app is the application layer of headquarters' ingest service: it builds the
// headquarters stream from the stores, writes the events to PostgreSQL, and gives the stores'
// state.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"abarow.com/pkg/nodes"
	"abarow.com/services/headquarters/ingest/internal/domain"
)

// HeadquartersStream is the port of the headquarters stream: the stores' streams
// (ABAROW_STORE) flow into this stream as sources over the leaf connection.
type HeadquartersStream interface {
	// Ensure sets up or updates the stream so that it takes these stores as its
	// sources.
	Ensure(ctx context.Context, storeIDs []string) error
	// Sources is the source states by store id.
	Sources(ctx context.Context) (map[string]domain.Source, error)
}

// Store is the headquarters database (PostgreSQL).
type Store interface {
	// Record writes the event in its raw form and its body (Sale, Void, Unfinished or
	// Shift) to the read models in the same transaction. If the event came before
	// (the same id) it writes nothing and returns false: events come at least once,
	// the record is neither missing nor duplicated.
	Record(ctx context.Context, e domain.Event, body any) (bool, error)
	// RecordBatch writes the events (with their sequence numbers) in a single
	// transaction; if one cannot be written none is written.
	RecordBatch(ctx context.Context, items []Item) error
	// Activity is the summary of the events that have come, by store id.
	Activity(ctx context.Context) (map[string]domain.Activity, error)
	// SaveLinks writes the stores' connection state; it deletes the state of the
	// stores that are not in the list (their past data stays).
	SaveLinks(ctx context.Context, links []domain.StoreStatus, at time.Time) error
}

// Service holds the use cases of ingest.
type Service struct {
	stream HeadquartersStream
	store  Store
	stores []nodes.Node
	// RetryInterval is the retry interval if the headquarters stream cannot be set up.
	RetryInterval time.Duration
	// LinkInterval is the interval at which the stores' connection state is written
	// to the database.
	LinkInterval time.Duration
	now          func() time.Time
}

// NewService returns a service with the stores in stores.json.
func NewService(stream HeadquartersStream, store Store, stores []nodes.Node) *Service {
	return &Service{stream: stream, store: store, stores: stores, RetryInterval: 2 * time.Second, LinkInterval: 15 * time.Second, now: time.Now}
}

// SetupStream sets up the headquarters stream; it tries until NATS is ready.
// It does not set it up if there is no connected store: it is set up when a store is added and
// the service restarted.
func (s *Service) SetupStream(ctx context.Context, log *slog.Logger) error {
	if len(s.stores) == 0 {
		log.Warn("no store is connected to headquarters (stores.json is empty); the headquarters stream will be set up once a store is added and the service restarted")
		return nil
	}
	ids := make([]string, len(s.stores))
	for i, n := range s.stores {
		ids[i] = n.ID
	}
	for {
		err := s.stream.Ensure(ctx, ids)
		if err == nil {
			log.Info("the headquarters stream is ready", "stores", ids)
			return nil
		}
		log.Warn("could not set up the headquarters stream; will retry", "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.RetryInterval):
		}
	}
}

// Record records the event coming from a store; the second value says whether the event is new.
// It returns domain.ErrInvalidEvent if the event or its body is incomplete.
func (s *Service) Record(ctx context.Context, e domain.Event, body any) (bool, error) {
	if err := validate(e, body); err != nil {
		return false, err
	}
	return s.store.Record(ctx, e, body)
}

// Item is an event of a batch with its body and, if it carries one, the sender's
// sequence number of it (Producer is empty if not).
type Item struct {
	Event    domain.Event
	Body     any
	Producer string
	Seq      int64
}

// RecordBatch records the events in a single transaction (docs/REVIEW.md P2). An
// invalid event fails the whole batch with domain.ErrInvalidEvent: the caller then
// records them one by one (Record), so that only that one is rejected.
func (s *Service) RecordBatch(ctx context.Context, items []Item) error {
	for _, it := range items {
		if err := validate(it.Event, it.Body); err != nil {
			return err
		}
	}
	return s.store.RecordBatch(ctx, items)
}

// validate returns domain.ErrInvalidEvent if the event or its body is incomplete.
func validate(e domain.Event, body any) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("event %q: %w", e.ID, err)
	}
	switch b := body.(type) {
	case domain.Sale:
		if err := domain.InStore(b.StoreID, b.Validate()); err != nil {
			return fmt.Errorf("sale %q: %w", b.ID, err)
		}
	case domain.Void:
		if err := domain.InStore(b.StoreID, b.Validate()); err != nil {
			return fmt.Errorf("void %q: %w", b.SaleID, err)
		}
	case domain.Unfinished:
		if err := domain.InStore(b.StoreID, b.Validate()); err != nil {
			return fmt.Errorf("sale whose receipt could not be issued %q: %w", b.SaleID, err)
		}
	case domain.Shift:
		if err := domain.InStore(b.StoreID, b.Validate()); err != nil {
			return fmt.Errorf("shift %s/%s/%d: %w", b.StoreID, b.RegisterID, b.ID, err)
		}
	case domain.StorePrices:
		if err := b.Validate(); err != nil {
			return fmt.Errorf("store price %s/%s: %w", b.StoreID, b.ProductID, err)
		}
	case domain.Cashier:
		if err := b.Validate(); err != nil {
			return fmt.Errorf("cashier %s/%s: %w", b.StoreID, b.CashierID, err)
		}
	case nil:
		// Unknown event kind: it is stored only in its raw form.
	default:
		return fmt.Errorf("unknown event body %T", body)
	}
	return nil
}

// Stores is the state of the stores (in the order of stores.json).
func (s *Service) Stores(ctx context.Context) ([]domain.StoreStatus, error) {
	sources, err := s.stream.Sources(ctx)
	if err != nil {
		return nil, err
	}
	activity, err := s.store.Activity(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]domain.StoreStatus, len(s.stores))
	for i, n := range s.stores {
		var src *domain.Source
		if v, ok := sources[n.ID]; ok {
			src = &v
		}
		out[i] = domain.Status(n.ID, n.DisplayName(), src, now)
		out[i].Activity = activity[n.ID]
	}
	return out, nil
}

// PublishLinks writes the stores' connection state to the database at intervals
// until ctx ends. The headquarters screen (reporting) reads the state from here without connecting to
// NATS; if the write time is old it is understood that ingest is not running.
func (s *Service) PublishLinks(ctx context.Context, log *slog.Logger) {
	failing := false
	for {
		err := s.saveLinks(ctx)
		switch {
		case err != nil && !failing && ctx.Err() == nil:
			log.Warn("could not write the stores' connection state; will retry", "err", err)
			failing = true
		case err == nil && failing:
			log.Info("the stores' connection state is being written again")
			failing = false
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.LinkInterval):
		}
	}
}

func (s *Service) saveLinks(ctx context.Context) error {
	links, err := s.Stores(ctx)
	if err != nil {
		return err
	}
	return s.store.SaveLinks(ctx, links, s.now())
}
