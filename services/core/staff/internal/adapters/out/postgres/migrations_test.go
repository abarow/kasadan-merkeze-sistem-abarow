// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"abarow.com/pkg/pgdb/pgdbtest"
)

// Migration 00004 turns the text times into TIMESTAMPTZ: the fixed-width form and the
// RFC 3339 form of the first versions (trailing zeros dropped) keep their moment.
func TestTimesBecomeTimestamptz(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("pgx", pgdbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO periods (opened_at, closed_at) VALUES ('2026-10-25T06:00:00.000000000Z', '2026-10-25T21:30:15.123456789Z')`,
		`INSERT INTO cashiers (id, name, role, pin_hash, origin) VALUES ('ayse', 'Ayşe', 'cashier', '\x00', 'seed')`,
		`INSERT INTO shifts (cashier_id, opened_at, closed_at, opening_cash, counted_cash, closed_by)
			VALUES ('ayse', '2026-10-25T06:05:00Z', '2026-10-25T14:00:00.5Z', 0, 0, 'ayse')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var opened, closed, shiftClosed time.Time
	if err := db.QueryRowContext(ctx, `SELECT p.opened_at, p.closed_at, s.closed_at FROM periods p, shifts s`).Scan(&opened, &closed, &shiftClosed); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		got, want time.Time
	}{
		{opened, time.Date(2026, 10, 25, 6, 0, 0, 0, time.UTC)},
		{closed, time.Date(2026, 10, 25, 21, 30, 15, 123457000, time.UTC)}, // microseconds
		{shiftClosed, time.Date(2026, 10, 25, 14, 0, 0, 500000000, time.UTC)},
	} {
		if !c.got.Equal(c.want) {
			t.Errorf("got %v, want %v", c.got, c.want)
		}
	}
	// The way back writes the fixed-width text again.
	if _, err := p.DownTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	var text string
	if err := db.QueryRowContext(ctx, `SELECT opened_at FROM periods`).Scan(&text); err != nil || text != "2026-10-25T06:00:00.000000000Z" {
		t.Errorf("down: %q, %v", text, err)
	}
}
