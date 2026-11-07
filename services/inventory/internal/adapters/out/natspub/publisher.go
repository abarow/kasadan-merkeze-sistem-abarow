// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package natspub holds the outbox publishers of the stock service. The headquarters scope
// publishes the transfer events to headquarters' transfer stream (ABAROW_TRANSFERS);
// the inventory of each store takes from this stream as its source only what is
// sent to it and the place list. The store scope publishes the movement
// events to the store stream.
package natspub

import (
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/natsio"
)

// StreamName, merkezin transfer stream'idir.
const StreamName = "ABAROW_TRANSFERS"

// StreamConfig is the settings of the transfer stream. Only the last message
// is kept per subject: each transfer has its own subject, only the last state of the place list
// is valid. It does not expire: a store whose connection to headquarters stays down for a long
// time gets its transfers too.
var StreamConfig = jetstream.StreamConfig{
	Name:              StreamName,
	Subjects:          []string{"transfer.>"},
	Storage:           jetstream.FileStorage,
	MaxMsgsPerSubject: 1,
	// The outbox can publish the same message again; it is filtered out by message id.
	Duplicates: 10 * time.Minute,
}

// New returns the publisher of the headquarters scope (pkg/natsio: CloudEvents
// headers, the time to wait for the ack, creating the stream on the first publish).
func New(js jetstream.JetStream) *natsio.Publisher { return natsio.NewPublisher(js, &StreamConfig) }

// NewShop returns the publisher of the store scope: the movement events are written to the
// store stream (store.stock.moved.v1; they go from there to headquarters). report creates the
// stream; if it does not exist the publish fails and the outbox retries later.
func NewShop(js jetstream.JetStream) *natsio.Publisher { return natsio.NewPublisher(js, nil) }
