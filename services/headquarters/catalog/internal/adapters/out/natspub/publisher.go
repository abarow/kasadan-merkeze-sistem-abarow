// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package natspub is the outbox.Publisher that publishes the catalog to the headquarters catalog
// stream (ABAROW_CATALOG).
package natspub

import (
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/natsio"
)

// StreamName, merkezin katalog stream'idir.
const StreamName = "ABAROW_CATALOG"

// StreamConfig is the catalog stream's settings. Only the last message per subject (per product)
// is kept: the stream is the current catalog itself, it does not expire. The store servers and the
// registers take it as a source.
var StreamConfig = jetstream.StreamConfig{
	Name:              StreamName,
	Subjects:          []string{"catalog.product.>", "catalog.image.>"},
	Storage:           jetstream.FileStorage,
	MaxMsgsPerSubject: 1,
	// The outbox may publish the same message again; it is de-duplicated by the message id.
	Duplicates: 10 * time.Minute,
}

// New returns a publisher (pkg/natsio: CloudEvents headers, the time to wait for
// acknowledgement, setting up the stream at the first publish).
func New(js jetstream.JetStream) *natsio.Publisher { return natsio.NewPublisher(js, &StreamConfig) }

// Shared is a publisher whose stream is set up by someone else: on the store server report sets
// up the catalog stream (taking the source from headquarters if connected); this one does not change
// its settings, it only publishes.
func Shared(js jetstream.JetStream) *natsio.Publisher { return natsio.NewPublisher(js, nil) }
