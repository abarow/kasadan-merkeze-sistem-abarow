// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package pgpool limits the services' PostgreSQL connection pool. An unlimited pool
// can use up the database's connection allowance (max_connections) under load; on the
// store server this allowance is 20 (pkg/pgtune). The total of the services'
// defaults is pgtune.PoolConnections.
package pgpool

import (
	"database/sql"
	"os"
	"strconv"
	"time"
)

// The services' default limits (the three services that connect to the same database
// on the store server).
const (
	Report       = 8
	Inventory    = 4
	CatalogAdmin = 4
)

// EnvMax is the environment variable that changes the limit.
const EnvMax = "ABAROW_DB_MAX_CONNS"

// Limit limits the pool: ABAROW_DB_MAX_CONNS if it is a positive number, otherwise
// def. The idle connection count does not exceed the same limit; idle connections close in 5
// minutes. It returns the limit used.
func Limit(db *sql.DB, def int) int {
	n := def
	if v, err := strconv.Atoi(os.Getenv(EnvMax)); err == nil && v > 0 {
		n = v
	}
	db.SetMaxOpenConns(n)
	db.SetMaxIdleConns(n)
	db.SetConnMaxIdleTime(5 * time.Minute)
	return n
}
