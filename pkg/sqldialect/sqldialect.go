// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package sqldialect holds the dialect differences so that the same SQL works on SQLite and
// PostgreSQL: time format, parameter marker, a few function names.
// The packages that write shared SQL (sales ledger, outbox) use it.
package sqldialect

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Dialect is the database's SQL dialect.
type Dialect struct{ postgres bool }

var (
	// SQLite: times are fixed-width UTC text (timeLayout).
	SQLite = Dialect{}
	// PostgreSQL: zamanlar TIMESTAMPTZ.
	Postgres = Dialect{postgres: true}
)

// DB is a *sql.DB or a *sql.Tx.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// timeLayout is the time format in SQLite: fixed-width UTC. Thanks to the fixed
// width text comparison is the same as time order.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// Time turns the time into the form to be written to the database.
func (d Dialect) Time(t time.Time) any {
	if d.postgres {
		return t.UTC()
	}
	return t.UTC().Format(timeLayout)
}

// ScanTime reads a time column (text in SQLite, time in PostgreSQL);
// NULL is the zero time.
func ScanTime(t *time.Time) sql.Scanner { return timeScanner{t} }

type timeScanner struct{ t *time.Time }

func (s timeScanner) Scan(src any) error {
	var err error
	switch v := src.(type) {
	case nil:
		*s.t = time.Time{}
	case time.Time:
		*s.t = v.UTC()
	case string:
		*s.t, err = time.Parse(time.RFC3339Nano, v)
	case []byte:
		*s.t, err = time.Parse(time.RFC3339Nano, string(v))
	default:
		err = fmt.Errorf("zaman okunamadı: %T", src)
	}
	return err
}

// Q turns a query with ? markers into the dialect (PostgreSQL's $1, $2, ...).
func (d Dialect) Q(query string) string {
	if !d.postgres {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Least is the name of the function that picks the smallest.
func (d Dialect) Least() string {
	if d.postgres {
		return "least"
	}
	return "min"
}

// Greatest is the name of the function that picks the largest.
func (d Dialect) Greatest() string {
	if d.postgres {
		return "greatest"
	}
	return "max"
}

// Postgres says the dialect is PostgreSQL.
func (d Dialect) Postgres() bool { return d.postgres }
