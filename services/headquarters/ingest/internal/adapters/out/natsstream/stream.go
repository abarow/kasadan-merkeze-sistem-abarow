// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package natsstream is the NATS JetStream adapter of the headquarters stream (ABAROW_HEADQUARTERS)
// (app.HeadquartersStream).
package natsstream

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/natsio"
	"abarow.com/services/headquarters/ingest/internal/app"
	"abarow.com/services/headquarters/ingest/internal/domain"
)

const (
	// StreamName, merkezdeki merkez stream'idir.
	StreamName = "ABAROW_HEADQUARTERS"
	// storeStream is the store stream in each store's own JetStream domain
	// (report sets it up on the store server).
	storeStream = "ABAROW_STORE"
)

// Config is the settings of the headquarters stream. Each store's stream is added as a
// source from the store's JetStream domain (the domain name is the store id) over the
// leaf connection.
//
// The store id is added to the front of the events' subject (store-1.sale.completed.v1):
// which store an event came from is determined by headquarters' own configuration, not by
// what is written inside the event. So a misconfigured register cannot write a sale in the
// name of another store; registers with the same name in different stores are not
// mixed up either.
//
// NATS keeps where it left off at each source: the events that accumulate at the store while the internet
// is down come without loss or repetition once the connection returns.
func Config(storeIDs []string) jetstream.StreamConfig {
	cfg := jetstream.StreamConfig{
		Name:    StreamName,
		Storage: jetstream.FileStorage,
		// The events are kept for a while after they are written to PostgreSQL too:
		// even if ingest stays down for a long time no event is lost (DESIGN.md
		// section 14).
		MaxAge: 90 * 24 * time.Hour,
	}
	for _, id := range storeIDs {
		cfg.Sources = append(cfg.Sources, &jetstream.StreamSource{
			Name: storeStream, Domain: id,
			SubjectTransforms: []jetstream.SubjectTransformConfig{{Source: ">", Destination: id + ".>"}},
		})
	}
	return cfg
}

// Stream, app.MerkezStream'i JetStream ile uygular.
type Stream struct {
	js jetstream.JetStream
}

var _ app.HeadquartersStream = (*Stream)(nil)

// New returns a headquarters stream adapter.
func New(js jetstream.JetStream) *Stream { return &Stream{js: js} }

// Ensure sets up the stream or updates its sources.
func (s *Stream) Ensure(ctx context.Context, storeIDs []string) error {
	cfg, err := natsio.Replicated(Config(storeIDs))
	if err != nil {
		return err
	}
	_, err = s.js.CreateOrUpdateStream(ctx, cfg)
	return err
}

// Sources is the state of the store sources as NATS reports it.
func (s *Stream) Sources(ctx context.Context) (map[string]domain.Source, error) {
	stream, err := s.js.Stream(ctx, StreamName)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]domain.Source{}
	for _, src := range stream.CachedInfo().Sources {
		id := storeID(src)
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

// storeID extracts the store id from the source's JetStream domain ("$JS.<store>.API");
// returns empty for any other source.
func storeID(src *jetstream.StreamSourceInfo) string {
	if src == nil || src.Name != storeStream || src.External == nil {
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

// Route splits a subject in the headquarters stream into the store id and the subject
// at the store (store-1.sale.completed.v1 → store-1,
// sale.completed.v1).
func Route(subject string) (storeID, eventSubject string, ok bool) {
	storeID, eventSubject, ok = strings.Cut(subject, ".")
	return storeID, eventSubject, ok && storeID != "" && eventSubject != ""
}
