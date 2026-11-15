// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package natspub is the outbox.Publisher that publishes the store server's own messages:
// the store settings (config.>, going down to the registers) and the store events (store.>,
// going from the store stream to headquarters).
package natspub

import (
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/natsio"
)

// ConfigStream is the store settings stream.
const ConfigStream = "ABAROW_STORE_CONFIG"

// ConfigStreamConfig: only the latest state per subject (e.g. the store price per product) is
// kept, it does not expire; the registers take it as a source.
var ConfigStreamConfig = jetstream.StreamConfig{
	Name:              ConfigStream,
	Subjects:          []string{"config.>"},
	Storage:           jetstream.FileStorage,
	MaxMsgsPerSubject: 1,
	Duplicates:        10 * time.Minute,
}

// New returns a publisher (pkg/natsio: CloudEvents headers, the time to wait for
// acknowledgement). For the store events report sets up the store stream (with the store.>
// subjects); here only the settings stream is set up (Ensure; it is also called when the
// service starts: the registers take the stream as a source).
func New(js jetstream.JetStream) *natsio.Publisher {
	return natsio.NewPublisher(js, &ConfigStreamConfig)
}
