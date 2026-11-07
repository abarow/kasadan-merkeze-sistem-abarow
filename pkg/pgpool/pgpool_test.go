// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package pgpool_test

import (
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"abarow.com/pkg/pgpool"
	"abarow.com/pkg/pgtune"
)

func open(t *testing.T) *sql.DB {
	t.Helper()
	// No connection is made (the pool is lazy); only the limit is tried.
	db, err := sql.Open("pgx", "postgres://x:x@127.0.0.1:1/x?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestLimitDefaultAndEnv(t *testing.T) {
	t.Setenv(pgpool.EnvMax, "")
	db := open(t)
	if n := pgpool.Limit(db, pgpool.Report); n != 8 || db.Stats().MaxOpenConnections != 8 {
		t.Errorf("default: %d, pool %d", n, db.Stats().MaxOpenConnections)
	}
	for env, want := range map[string]int{"3": 3, "0": 8, "-2": 8, "abc": 8} {
		t.Setenv(pgpool.EnvMax, env)
		db := open(t)
		if n := pgpool.Limit(db, 8); n != want || db.Stats().MaxOpenConnections != want {
			t.Errorf("%s: %d, havuz %d, istenen %d", env, n, db.Stats().MaxOpenConnections, want)
		}
	}
}

// The services' default pools fit pgtune's connection budget.
func TestDefaultsFitBudget(t *testing.T) {
	if got := pgpool.Report + pgpool.Inventory + pgpool.CatalogAdmin; got != pgtune.PoolConnections {
		t.Errorf("pools total %d, pgtune expects %d", got, pgtune.PoolConnections)
	}
}
