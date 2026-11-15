// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package natsstore is the NATS JetStream adapter (app.StoreStream) of the store stream
// (ABAROW_STORE).
package natsstore

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

const (
	// StreamName is the store stream on the store server.
	StreamName = "ABAROW_STORE"
	// salesStream is the sales stream in each register's own JetStream domain
	// (sales sets it up at the register).
	salesStream = "ABAROW_SALES"
	// staffStream is the shift events stream at the register (staff sets it up at the register).
	staffStream = "ABAROW_STAFF"
)

// Config is the settings of the store stream. Each register's sales and shift
// stream is added as a source from the register's JetStream domain (the domain name is the register id)
// over the leaf connection. The shift stream is set up at the register with the first
// shift event; until then (or at a register of an old version) NATS retries
// the source at intervals, it does not affect the sales source.
//
// NATS keeps where it left off at each source: the events that accumulate while the store server or
// a register is off come without loss or repetition once the connection returns.
// The events come with their subjects at the register (sale.completed.v1, shift.opened.v1
// ...); which register they came from is in the event (register_id) and in the ce-source
// header. The store server's own events (store.>, e.g. a store price
// change) are written directly to this stream; they go to headquarters
// together with the registers' events.
func Config(registerIDs []string) jetstream.StreamConfig {
	cfg := jetstream.StreamConfig{
		Name:     StreamName,
		Subjects: []string{"store.>"},
		Storage:  jetstream.FileStorage,
		// It is not deleted automatically when the time is up: only the receipt cleaner
		// (pkg/delivery) deletes an old event, and only if the event has been processed into
		// headquarters' database and its receipt is recorded here. If headquarters is not
		// defined (headquarters is not installed) no event is deleted.
	}
	for _, id := range registerIDs {
		cfg.Sources = append(cfg.Sources,
			&jetstream.StreamSource{Name: salesStream, Domain: id},
			&jetstream.StreamSource{Name: staffStream, Domain: id})
	}
	return cfg
}

// Store, app.StoreStream'i JetStream ile uygular.
type Store struct {
	js jetstream.JetStream
}

var _ app.StoreStream = (*Store)(nil)

// New returns a store stream adapter.
func New(js jetstream.JetStream) *Store { return &Store{js: js} }

// Ensure sets up the stream or updates its sources.
func (s *Store) Ensure(ctx context.Context, registerIDs []string) error {
	_, err := s.js.CreateOrUpdateStream(ctx, Config(registerIDs))
	return err
}

// Sources is the state of the sources as NATS reports it. The register's connection
// is read from the sales source: while the register is connected the shift stream
// source gives an error if the stream does not exist at the register yet.
func (s *Store) Sources(ctx context.Context) (map[string]domain.Source, error) {
	stream, err := s.js.Stream(ctx, StreamName)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]domain.Source{}
	for _, src := range stream.CachedInfo().Sources {
		id := registerID(src)
		if id == "" {
			continue
		}
		// NATS reports Active as -1 for a source it has never heard from.
		d := domain.Source{Seen: src.Active >= 0, Since: src.Active, Lag: src.Lag}
		if src.Error != nil {
			d.Error = src.Error.Description
		}
		out[id] = d
	}
	return out, nil
}

// registerID extracts the register id from the source's JetStream domain ("$JS.<register>.API");
// returns empty for any other source.
func registerID(src *jetstream.StreamSourceInfo) string {
	if src == nil || src.Name != salesStream || src.External == nil {
		return ""
	}
	id, ok := strings.CutPrefix(src.External.APIPrefix, "$JS.")
	if !ok {
		return ""
	}
	id, ok = strings.CutSuffix(id, ".API")
	if !ok || id == "" || strings.Contains(id, ".") {
		return ""
	}
	return id
}

// CatalogStream is the store's copy of headquarters' catalog stream;
// the registers take it as a source.
const CatalogStream = "ABAROW_CATALOG"

// CatalogConfig is the catalog stream at the store: it keeps only the latest state per
// product. If source is not empty it is fed from the catalog stream in the source JetStream domain
// (headquarters at headquarters); the registers take the catalog from here even while the connection to
// headquarters is down (including a newly installed register). If source is empty the
// stream keeps only what is written at the store (product management at the store). The subjects
// are defined in both cases: while off, catalog-admin publishes the management.
func CatalogConfig(source string) jetstream.StreamConfig {
	cfg := jetstream.StreamConfig{
		Name:              CatalogStream,
		Subjects:          []string{"catalog.product.>", "catalog.image.>"},
		Storage:           jetstream.FileStorage,
		MaxMsgsPerSubject: 1,
	}
	if source != "" {
		cfg.Sources = []*jetstream.StreamSource{{Name: CatalogStream, Domain: source}}
	}
	return cfg
}

// Catalog sets up the catalog stream at the store (app.CatalogStream).
type Catalog struct {
	JS jetstream.JetStream
	// Source is headquarters' JetStream domain; if empty headquarters is not installed.
	Source string
	Log    *slog.Logger
}

// Apply sets up or updates the stream: if connected it is fed from headquarters.
func (c Catalog) Apply(ctx context.Context, connected bool) error {
	source := ""
	if connected {
		source = c.Source
	}
	if _, err := c.JS.CreateOrUpdateStream(ctx, CatalogConfig(source)); err != nil {
		return err
	}
	if c.Log != nil {
		if connected {
			c.Log.Info("headquarters' catalog is being taken into the store", "stream", CatalogStream, "source", source)
		} else {
			c.Log.Info("the catalog is managed at the store; it is not taken from headquarters", "stream", CatalogStream)
		}
	}
	return nil
}
