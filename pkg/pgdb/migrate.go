// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package pgdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
)

// MigrationLockTimeout is how long a statement of a migration waits for a lock, and
// MigrationRetry how long the migration waits before it tries again.
//
// A schema change (ALTER TABLE) waits behind a long reader of the table (the nightly
// pg_dump, a monthly report), and while it waits every new query of the table waits behind
// it: the services stop. With the limit the change gives up after a second, the queries go
// on, and it tries again later; decision 14.11.2026.
var (
	MigrationLockTimeout = time.Second
	MigrationRetry       = 10 * time.Second
)

// Migrate applies the migrations in fsys (goose) on a connection of its own to dsn, with
// MigrationLockTimeout. A migration that does not get its lock is rolled back (every migration
// is one transaction) and tried again after MigrationRetry until ctx ends; other errors are
// returned. dsn is what sql.Open("pgx", ...) takes; its parameters (the search_path) are kept.
func Migrate(ctx context.Context, dsn string, fsys fs.FS, opts ...goose.ProviderOption) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	// One connection that stays open: lock_timeout is a setting of its session. (If it broke,
	// the new connection would run without the limit, as before.)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.ExecContext(ctx, fmt.Sprintf("SET lock_timeout = '%dms'", MigrationLockTimeout.Milliseconds())); err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys, opts...)
	if err != nil {
		return err
	}
	for {
		_, err := p.Up(ctx)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55P03" { // lock_not_available
			return err
		}
		slog.Warn("a migration did not get its lock (a long reader of the table); it is tried again",
			"retry", MigrationRetry, "err", pgErr.Message)
		select {
		case <-ctx.Done():
			return fmt.Errorf("the migration did not get its lock: %w", err)
		case <-time.After(MigrationRetry):
		}
	}
}
