// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package sqlitedb opens the services' SQLite database with the same settings and
// applies its migrations.
package sqlitedb

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // the SQLite driver without CGO
)

// Options are the connection settings: foreign keys are enforced, WAL
// journal (a reader does not wait for a writer), 5 seconds are waited if locked,
// transactions open with a write lock from the start (BEGIN IMMEDIATE).
const Options = "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"

// Open opens the database at path and applies the SQL files of the "migrations"
// folder in migrations (the services embed it with `//go:embed
// migrations/*.sql`). There is a single writer in the service; a single connection
// prevents SQLITE_BUSY errors.
func Open(ctx context.Context, path string, migrations fs.FS) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+Options)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		db.Close()
		return nil, err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, fsys)
	if err == nil {
		_, err = p.Up(ctx)
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("migration uygulanamadı: %w", err)
	}
	return db, nil
}
