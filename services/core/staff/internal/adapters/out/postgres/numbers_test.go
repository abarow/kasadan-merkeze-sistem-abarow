// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"testing"

	"abarow.com/pkg/pgdb/pgdbtest"
)

// A rolled-back transaction does not use up a Z number or a shift number.
func TestNumbersHaveNoGapAfterRollback(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, pgdbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	insert := func(tx *sql.Tx) (z int64) {
		t.Helper()
		if err := tx.QueryRowContext(ctx, `INSERT INTO periods (opened_at) VALUES ('2026-11-08T10:00:00.000000000Z') RETURNING z_no`).Scan(&z); err != nil {
			t.Fatal(err)
		}
		return z
	}
	begin := func() *sql.Tx {
		t.Helper()
		tx, err := st.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		return tx
	}

	tx := begin()
	first := insert(tx)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	tx = begin()
	again := insert(tx)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Errorf("Z number after a rollback is %d, want %d", again, first)
	}
}
