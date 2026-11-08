// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"abarow.com/pkg/pgdb/pgdbtest"
	"abarow.com/services/core/sales/internal/app"
)

var errRollback = errors.New("rollback")

func openNumbers(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), pgdbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// A rolled-back sale does not use up a receipt number.
func TestReceiptNumberHasNoGapAfterRollback(t *testing.T) {
	ctx := context.Background()
	st := openNumbers(t)
	next := func(fail bool) (no int64) {
		t.Helper()
		err := st.InTx(ctx, func(tx app.Tx) (err error) {
			if no, err = tx.NextReceiptNo(ctx); err != nil {
				return err
			}
			if fail {
				return errRollback
			}
			return nil
		})
		if err != nil && !errors.Is(err, errRollback) {
			t.Fatal(err)
		}
		return no
	}
	if got := next(false); got != 1 {
		t.Fatalf("first receipt %d", got)
	}
	if got := next(true); got != 2 {
		t.Fatalf("rolled-back receipt %d", got)
	}
	if got := next(false); got != 2 {
		t.Errorf("the number after a rollback is %d, want 2", got)
	}
}

// Sales finishing at the same time get consecutive, distinct receipt numbers.
func TestReceiptNumbersAreConsecutiveUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	st := openNumbers(t)
	const n = 50
	got := make([]int64, n)
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- st.InTx(ctx, func(tx app.Tx) (err error) {
				got[i], err = tx.NextReceiptNo(ctx)
				return err
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(got)
	for i, no := range got {
		if no != int64(i+1) {
			t.Fatalf("receipt numbers %v", got)
		}
	}
}

// A rolled-back event does not use up an outbox number: the receiver would report a gap
// in the numbers as a lost event.
func TestOutboxNumberHasNoGapAfterRollback(t *testing.T) {
	ctx := context.Background()
	st := openNumbers(t)
	add := func(id string, fail bool) {
		t.Helper()
		err := st.InTx(ctx, func(tx app.Tx) error {
			err := tx.AppendOutbox(ctx, []app.OutboxMessage{{
				ID: id, Type: "t", Subject: "s", StoreID: "store-1", RegisterID: "register-1",
				Payload: []byte("x"), ContentType: "application/protobuf", OccurredAt: time.Now(),
			}})
			if err != nil {
				return err
			}
			if fail {
				return errRollback
			}
			return nil
		})
		if err != nil && !errors.Is(err, errRollback) {
			t.Fatal(err)
		}
	}
	add("a", false)
	add("b", true)
	add("c", false)
	pending, err := st.Pending(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].Seq != 1 || pending[1].Seq != 2 || pending[1].ID != "c" {
		t.Errorf("outbox numbers: %+v", pending)
	}
}
