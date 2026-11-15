// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package domain holds the business rules of the store server's report service.
package domain

import (
	"abarow.com/pkg/sequence"
	"time"

	"abarow.com/pkg/money"
)

// Link is the register's connection state to the store server.
type Link string

const (
	// LinkConnected: the register's sales stream flows to the store server.
	LinkConnected Link = "connected"
	// LinkDisconnected: there has been no word from the register for a while (off or the network
	// is cut). The register keeps selling; events accumulate at the register.
	LinkDisconnected Link = "disconnected"
	// LinkNever: the register has never connected since it was installed (e.g. its key was
	// entered wrong or it cannot reach the store server's address).
	LinkNever Link = "never"
)

// StaleAfter: if there is no word from the register for this long the connection counts as gone. A connected
// source gives a signal (heartbeat) every few seconds even when there are no events.
const StaleAfter = 30 * time.Second

// Source is the state at the store server of the register's sales stream source
// (as NATS reports it).
type Source struct {
	// Seen: whether any word was ever received from the source.
	Seen bool
	// Since: the time elapsed since the last word.
	Since time.Duration
	// Lag: the number of events at the register that have not arrived yet (meaningful while connected).
	Lag uint64
	// Error: the source error NATS reports; empty if none.
	Error string
}

// RegisterStatus is the state of a register as seen from the store server.
type RegisterStatus struct {
	ID, Name string
	Link     Link
	// LastSeen: the time of the last word; zero if it never connected.
	LastSeen time.Time
	// Pending: the number of events waiting at the register while connected.
	Pending uint64
	Error   string
	// Activity is the summary of the sales that came from the register.
	Activity Activity
}

// ReceiptRange is a range of receipt numbers (From and To included).
type ReceiptRange struct{ From, To int64 }

// Activity is the summary of the sales that came from a register to the store server.
type Activity struct {
	Receipts      int64
	LastReceiptNo int64
	LastSaleAt    time.Time
	// Missing are the gaps between the smallest and the largest receipt number that came:
	// receipts that have not come yet or were lost. Since the receipt number is given
	// after the fiscal record, failed attempts leave no gap; a gap is not a false alarm.
	Missing []ReceiptRange
	// MissingEvents are the gaps in the sequence numbers among the events the register sent
	// (pkg/sequence): events that were given a number but never came.
	// Unlike the receipt gap it covers every kind of event (sale, shift, cancellation).
	MissingEvents []sequence.Gap
	// Unfinished are the sales whose payment was taken but whose receipt could not be issued, still
	// waiting at the register to be completed or refunded (oldest to newest).
	Unfinished []Unfinished
	// OpenShift is the open shift at the register (if the register's most recently opened shift
	// has not closed); nil if none.
	OpenShift *Shift
	// LastClosed is the register's most recently closed shift (with the cash difference).
	LastClosed *Shift
}

// Unfinished is a sale whose payment was taken but whose fiscal record (receipt) could not be made
// (sale.charged.v1). It stops waiting when the sale is completed (sale.completed.v1) or its payment
// is refunded and it is cancelled (sale.voided.v1).
type Unfinished struct {
	SaleID, StoreID, RegisterID, CashierID string
	Total                                  money.Money
	// Reason is why the fiscal record could not be made in the last attempt.
	Reason string
	// Since is the time of the first failed attempt, LastAttempt the time of the last.
	Since, LastAttempt time.Time
}

// Status works out the register's state from the source state. If there is no source at all
// (the store stream has not been set up yet) the register counts as never connected.
func Status(id, name string, src *Source, now time.Time) RegisterStatus {
	st := RegisterStatus{ID: id, Name: name, Link: LinkNever}
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
