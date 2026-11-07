// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package natspub is the app.Publisher adapter that publishes the outbox messages to
// NATS JetStream.
package natspub

import (
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/natsio"
)

// StreamName is the JetStream stream of the sale events at the register.
const StreamName = "ABAROW_SALES"

// streamConfig: the events are written to disk. It is not deleted automatically when the time
// is up: only the receipt cleaner (pkg/delivery) deletes an old event, and only if the event
// has been processed into the tier above's database and its receipt is recorded here. The
// Duplicates window filters out the relay publishing the same message again.
var streamConfig = jetstream.StreamConfig{
	Name:       StreamName,
	Subjects:   []string{"sale.>"},
	Storage:    jetstream.FileStorage,
	Duplicates: 10 * time.Minute,
}

// New returns a publisher over the connection (pkg/natsio). It sets up the stream at the
// first publish; so the service can start even without NATS being up.
func New(nc *nats.Conn) (*natsio.Publisher, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	return natsio.NewPublisher(js, &streamConfig), nil
}
