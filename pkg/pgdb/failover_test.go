// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package pgdb

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestFailover writes to the headquarters database on three machines like a service does,
// through HAProxy's single address, while scripts/ha-db-test.sh shuts the primary down.
// Every write that was acknowledged must be on the new primary; the writes must resume.
// It runs only with ABAROW_TEST_HA_DSN (the script gives it).
func TestFailover(t *testing.T) {
	dsn := os.Getenv("ABAROW_TEST_HA_DSN")
	if dsn == "" {
		t.Skip("ABAROW_TEST_HA_DSN is not set (scripts/ha-db-test.sh)")
	}
	duration := 90 * time.Second
	if s := os.Getenv("ABAROW_TEST_HA_SECONDS"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatal(err)
		}
		duration = time.Duration(n) * time.Second
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	if _, err := db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS failover (id bigint PRIMARY KEY, at timestamptz NOT NULL DEFAULT now())"); err != nil {
		t.Fatal(err)
	}

	var acked []int64
	failed := 0
	start := time.Now()
	lastOK := start
	var maxGap time.Duration
	for id := int64(1); time.Since(start) < duration; id++ {
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := db.ExecContext(wctx, "INSERT INTO failover (id) VALUES ($1)", id)
		cancel()
		if err != nil {
			// The outcome of a failed write is unknown (it may have been committed); only the
			// acknowledged ones are checked.
			if failed == 0 || failed%20 == 0 {
				t.Logf("%s: write %d failed: %v", time.Since(start).Round(time.Second), id, err)
			}
			failed++
			time.Sleep(200 * time.Millisecond)
			continue
		}
		now := time.Now()
		maxGap = max(maxGap, now.Sub(lastOK))
		lastOK = now
		acked = append(acked, id)
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("acknowledged %d, failed %d, longest gap %s", len(acked), failed, maxGap.Round(100*time.Millisecond))

	present := map[int64]bool{}
	var rows *sql.Rows
	for i := 0; ; i++ {
		rows, err = db.QueryContext(ctx, "SELECT id FROM failover")
		if err == nil || i == 30 {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		present[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	lost := 0
	for _, id := range acked {
		if !present[id] {
			lost++
		}
	}
	if lost > 0 {
		t.Errorf("%d acknowledged writes are not in the database", lost)
	}
	if failed == 0 {
		t.Error("no write failed: the primary was not shut down during the test")
	}
	if time.Since(lastOK) > 15*time.Second {
		t.Errorf("the writes did not resume (last acknowledged %s ago)", time.Since(lastOK).Round(time.Second))
	}
	if maxGap > 90*time.Second {
		t.Errorf("the writes stopped for %s", maxGap.Round(time.Second))
	}
}
