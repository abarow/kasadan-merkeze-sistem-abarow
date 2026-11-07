// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package sqlitedb

import (
	"context"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestOpen(t *testing.T) {
	ctx := context.Background()
	migrations := fstest.MapFS{
		"migrations/00001_init.sql": {Data: []byte(`-- +goose Up
CREATE TABLE a (id INTEGER PRIMARY KEY);
CREATE TABLE b (a_id INTEGER NOT NULL REFERENCES a (id));
-- +goose Down
DROP TABLE b;
DROP TABLE a;
`)},
	}
	path := filepath.Join(t.TempDir(), "x.db")
	for range 2 { // the migration is not applied again at the second open
		db, err := Open(ctx, path, migrations)
		if err != nil {
			t.Fatal(err)
		}
		var mode string
		if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
			t.Errorf("journal: %q %v", mode, err)
		}
		// The foreign key is enforced.
		if _, err := db.ExecContext(ctx, `INSERT INTO b VALUES (42)`); err == nil {
			t.Error("foreign key was not enforced")
		}
		db.Close()
	}
	if _, err := Open(ctx, filepath.Join(t.TempDir(), "y.db"), fstest.MapFS{"migrations/00001_x.sql": {Data: []byte("-- +goose Up\nBOZUK SQL;\n")}}); err == nil {
		t.Error("bozuk migration kabul edildi")
	}
}
