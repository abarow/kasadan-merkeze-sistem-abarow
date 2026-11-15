// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"time"

	"abarow.com/pkg/money"
	"abarow.com/pkg/sequence"
)

// Link is the store's connection state to headquarters (ingest writes it).
type Link string

const (
	// LinkConnected: the store's events flow to headquarters.
	LinkConnected Link = "connected"
	// LinkDisconnected: there has been no word from the store for a while (the internet is cut
	// or the store server is off). The store keeps running; events accumulate in the back
	// house.
	LinkDisconnected Link = "disconnected"
	// LinkNever: the store has never connected since it was installed.
	LinkNever Link = "never"
	// LinkUnknown: the state has not been written for a while (ingest is not running).
	LinkUnknown Link = "unknown"
)

// LinkStaleAfter: if ingest does not write the state for this long the connection counts as unknown
// (ingest writes every 15 seconds).
const LinkStaleAfter = time.Minute

// StoreOverview is a store's summary on the headquarters screen.
type StoreOverview struct {
	ID, Name string
	Link     Link
	LastSeen time.Time
	// Pending is the number of events waiting at the store (not yet arrived).
	Pending int64
	Error   string
	// CheckedAt is the time the state was written.
	CheckedAt time.Time
	// LastEventAt is the time of the last event that came from the store.
	LastEventAt time.Time
	// Today's sales (in store time).
	TodayReceipts int64
	TodayTotal    money.Money
	// MissingEvents are the gaps in the sequence numbers among the events the registers numbered and sent
	// (pkg/sequence): records that never reached headquarters.
	MissingEvents []sequence.Gap
}

// Freshen makes the connection unknown if the state is old.
func (s StoreOverview) Freshen(now time.Time) StoreOverview {
	if now.Sub(s.CheckedAt) > LinkStaleAfter {
		s.Link = LinkUnknown
	}
	return s
}
