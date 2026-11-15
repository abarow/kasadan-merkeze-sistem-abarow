// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"time"

	"abarow.com/pkg/sequence"
)

// Link is the store's (its store server's) connection state to headquarters.
type Link string

const (
	// LinkConnected: the store's stream flows to headquarters.
	LinkConnected Link = "connected"
	// LinkDisconnected: there has been no word from the store for a while (the internet is cut
	// or the store server is off). The store keeps running; events accumulate in the back
	// house.
	LinkDisconnected Link = "disconnected"
	// LinkNever: the store has never connected since it was installed.
	LinkNever Link = "never"
)

// StaleAfter: if there is no word from the store for this long the connection counts as gone.
const StaleAfter = 30 * time.Second

// Source is the state at headquarters of the store stream's source (as NATS
// reports it).
type Source struct {
	Seen  bool
	Since time.Duration
	Lag   uint64
	Error string
}

// Activity is the summary of the events that have come from the store.
type Activity struct {
	Events      int64
	LastEventAt time.Time
	Sales       int64
	// Registers are the registers that have sent events (in id order).
	Registers []RegisterActivity
	// MissingEvents are the gaps in the sequence numbers among the events the registers numbered and sent
	// (pkg/sequence): records that never reached headquarters.
	MissingEvents []sequence.Gap
}

// RegisterActivity is the summary of the events that have come from a register.
type RegisterActivity struct {
	ID          string
	Events      int64
	Sales       int64
	LastEventAt time.Time
}

// StoreStatus is the state of a store as seen from headquarters.
type StoreStatus struct {
	ID, Name string
	Link     Link
	LastSeen time.Time
	Pending  uint64
	Error    string
	Activity Activity
}

// Status works out the store's state from the source state.
func Status(id, name string, src *Source, now time.Time) StoreStatus {
	st := StoreStatus{ID: id, Name: name, Link: LinkNever}
	if src == nil {
		return st
	}
	st.Error = src.Error
	if !src.Seen {
		return st
	}
	st.LastSeen = now.Add(-src.Since)
	if src.Since > StaleAfter || src.Error != "" {
		st.Link = LinkDisconnected
		return st
	}
	st.Link = LinkConnected
	st.Pending = src.Lag
	return st
}
