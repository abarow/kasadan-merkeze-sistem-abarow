// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package sequence is lost-event detection with event sequence numbers. The register (and every
// sender outbox) gives a sequential number (a counter row, which does not consume a number in a
// rolled-back transaction) when it makes an event ready to send; the number and the
// outbox's name travel in the event's headers (ce-sequence, ce-producer).
// The receiver (the store server, then headquarters) records the number of every event; if a
// number is missing in between that event is lost or has not come yet and the receiver can
// say so. The delivery receipt (pkg/delivery) proves that the event was processed; the sequence
// number reveals an event that never came.
//
// Limit: old events from before numbering began (without the header) are not
// tracked; what is before the first number seen does not count as a gap.
package sequence

import (
	"fmt"
	"strconv"
)

// Header names (CloudEvents extension attributes).
const (
	HeaderSequence = "ce-sequence"
	HeaderProducer = "ce-producer"
)

// Gap is a range of numbers a sender is missing (both ends included).
type Gap struct {
	Store    string `json:"store,omitempty"`
	Register string `json:"register"`
	Producer string `json:"producer"`
	From     int64  `json:"from"`
	To       int64  `json:"to"`
}

// Count is the number of missing events in the range.
func (g Gap) Count() int64 { return g.To - g.From + 1 }

func (g Gap) String() string {
	if g.From == g.To {
		return fmt.Sprintf("%s/%s #%d", g.Register, g.Producer, g.From)
	}
	return fmt.Sprintf("%s/%s #%d–%d", g.Register, g.Producer, g.From, g.To)
}

// Getter is anything that reads headers (nats.Header).
type Getter interface{ Get(key string) string }

// FromHeaders reads the event's sender and sequence number from the headers.
// If the headers are absent or invalid ok is false (an event from before numbering).
func FromHeaders(h Getter) (producer string, seq int64, ok bool) {
	producer = h.Get(HeaderProducer)
	n, err := strconv.ParseInt(h.Get(HeaderSequence), 10, 64)
	if producer == "" || err != nil || n <= 0 {
		return "", 0, false
	}
	return producer, n, true
}

// Total is the total number of missing events in the ranges.
func Total(gaps []Gap) int64 {
	var n int64
	for _, g := range gaps {
		n += g.Count()
	}
	return n
}
