// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package pgdb opens the PostgreSQL database of a register service. A register has a
// single PostgreSQL container; every service works in its own schema, so the data
// of a service stays its own and table names do not collide.
package pgdb

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"

	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" driver
	"github.com/pressly/goose/v3"
)

var schemaName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// Open sets up schema if it does not exist, connects to it and applies the SQL files of the
// "migrations" folder in migrations (the services embed it with `//go:embed
// migrations/*.sql`). dsn must be a connection address (postgres://...).
//
// The pool is a single connection: the transactions of a service run one after another,
// so a service still has a single writer and the numbers it hands out follow the order
// of its transactions.
func Open(ctx context.Context, dsn, schema string, migrations fs.FS) (*sql.DB, error) {
	scoped, err := withSearchPath(dsn, schema)
	if err != nil {
		return nil, err
	}
	if err := ensureSchema(ctx, dsn, schema); err != nil {
		return nil, fmt.Errorf("could not set up schema %s: %w", schema, err)
	}
	db, err := sql.Open("pgx", scoped)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		db.Close()
		return nil, err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err == nil {
		_, err = p.Up(ctx)
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("could not apply the migrations of schema %s: %w", schema, err)
	}
	return db, nil
}

func ensureSchema(ctx context.Context, dsn, schema string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema)
	return err
}

// withSearchPath adds search_path to the connection address: the connections use the
// schema without the queries naming it.
func withSearchPath(dsn, schema string) (string, error) {
	if !schemaName.MatchString(schema) {
		return "", fmt.Errorf("invalid schema name: %q", schema)
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		return "", fmt.Errorf("the database address must be a postgres:// URL")
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
