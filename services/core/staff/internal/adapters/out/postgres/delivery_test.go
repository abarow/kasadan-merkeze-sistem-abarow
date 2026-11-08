// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"testing"
	"time"

	"abarow.com/pkg/delivery"
	"abarow.com/pkg/pgdb/pgdbtest"
)

// The store server's receipt is written next to the event; the cleaner queries it.
func TestReceived(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, pgdbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	if err := s.SaveReceived(ctx, delivery.Receipt{EventID: "e1", ReceiptID: "r1", At: at}); err != nil {
		t.Fatal(err)
	}
	// If the receipt comes again it does not fail, the first stays valid.
	if err := s.SaveReceived(ctx, delivery.Receipt{EventID: "e1", ReceiptID: "r1b", At: at}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Received(ctx, []string{"e1", "e2"})
	if err != nil || !got["e1"] || got["e2"] || len(got) != 1 {
		t.Fatalf("Received = %v, %v", got, err)
	}
}
