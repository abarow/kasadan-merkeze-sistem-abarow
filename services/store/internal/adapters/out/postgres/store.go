// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package postgres is the app.Reports adapter that keeps the sales coming from the registers in
// PostgreSQL (store server, decision 04.11.2026). The sales ledger's SQL is shared with
// headquarters (pkg/ledger/ledgerdb); people and sessions are in
// pkg/personnel/personneldb. The service sets up its own schema; its version table
// is separate (report_goose_db_version), other services can use the same database
// too (e.g. the store's stock).
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"abarow.com/pkg/delivery"
	"abarow.com/pkg/ledger"
	"abarow.com/pkg/ledger/ledgerdb"
	"abarow.com/pkg/personnel/personneldb"
	"abarow.com/pkg/pgdb"
	"abarow.com/pkg/pgpool"
	"abarow.com/pkg/sequence/sequencedb"
	"abarow.com/pkg/sqldialect"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

//go:embed migrations/*.sql
var migrations embed.FS

// VersionTable is report's migration version table.
const VersionTable = "report_goose_db_version"

// q turns a query with ? markers into PostgreSQL's form.
var q = sqldialect.Postgres.Q

// maxMissing is the largest number of missing receipt ranges returned for a register.
const maxMissing = 100

// Store, app.Reports'u PostgreSQL ile uygular.
type Store struct {
	db *sql.DB
	// StoreID is the store id that is the source of the receipt messages.
	StoreID string
	// NewID produces a receipt id (UUIDv7); it can be fixed in a test.
	NewID func() string
}

var _ app.Reports = (*Store)(nil)

// Open connects to the database and applies the migrations.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	// The connection allowance is limited (max_connections 20 on the store server).
	pgpool.Limit(db, pgpool.Report)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := pgdb.Migrate(ctx, dsn, fsys, goose.WithTableName(VersionTable)); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration uygulanamadı: %w", err)
	}
	return &Store{db: db, NewID: delivery.NewID}, nil
}

// People is the store of the people (cashier, manager) (pkg/personnel/personneldb);
// the same SQL as headquarters' users. The messages written to the outbox with the change go
// to the registers and headquarters.
func (s *Store) People() *personneldb.Store {
	return &personneldb.Store{DB: s.db, Dialect: sqldialect.Postgres, Outbox: outboxTable}
}

// Close closes the database connection.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// The sales ledger's tables are the same as headquarters'; the writing code and the report
// queries are shared (pkg/ledger/ledgerdb).

// SaveSale records the sale with its lines, payments and VAT; if the sale already
// exists it does nothing.
func (s *Store) SaveSale(ctx context.Context, sale ledger.Sale) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := ledgerdb.Postgres.SaveSale(ctx, tx, sale)
		return err
	})
}

// SaveVoid records the cancellation; if it already exists it does nothing.
func (s *Store) SaveVoid(ctx context.Context, v ledger.Void) error {
	return ledgerdb.Postgres.SaveVoid(ctx, s.db, v)
}

// SaveUnfinished records a sale whose receipt could not be issued. If the same sale
// comes again (a new failed attempt) the first time is kept; the last attempt and the reason
// are updated if newer.
func (s *Store) SaveUnfinished(ctx context.Context, u ledger.Unfinished) error {
	return ledgerdb.Postgres.SaveUnfinished(ctx, s.db, u)
}

// SaveShift records the shift. An opening event adds the shift; a closing
// event (ClosedAt filled) writes the closing details. If the event comes again the
// result does not change.
func (s *Store) SaveShift(ctx context.Context, sh ledger.Shift) error {
	return ledgerdb.Postgres.SaveShift(ctx, s.db, sh)
}

// Report is the sales ledger's report. Its queries read one snapshot: a sale arriving
// meanwhile cannot make the sales total and the payment total differ. A read-only
// transaction does not hold up the sales being recorded.
func (s *Store) Report(ctx context.Context, f ledger.Filter) (ledger.Report, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return ledger.Report{}, err
	}
	defer tx.Rollback() // read only
	return ledgerdb.Postgres.Report(ctx, tx, f)
}

// Activity returns, per register, the number of sales that have come, the last receipt and the missing receipt
// ranges. Missing receipt: a gap among the receipt numbers that have come from the register (if the difference
// of each receipt number from the next is greater than one).
func (s *Store) Activity(ctx context.Context) (map[string]domain.Activity, error) {
	out := map[string]domain.Activity{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT register_id, COUNT(*), MAX(receipt_no), MAX(paid_at) FROM sales GROUP BY register_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var a domain.Activity
		if err := rows.Scan(&id, &a.Receipts, &a.LastReceiptNo, &a.LastSaleAt); err != nil {
			rows.Close()
			return nil, err
		}
		a.LastSaleAt = a.LastSaleAt.UTC()
		out[id] = a
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	if rows, err = s.db.QueryContext(ctx, `
		SELECT register_id, receipt_no + 1, next_no - 1 FROM (
			SELECT register_id, receipt_no, LEAD(receipt_no) OVER (PARTITION BY register_id ORDER BY receipt_no) AS next_no
			FROM sales
		) AS t WHERE next_no > receipt_no + 1 ORDER BY register_id, receipt_no`); err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var r domain.ReceiptRange
		if err := rows.Scan(&id, &r.From, &r.To); err != nil {
			rows.Close()
			return nil, err
		}
		a := out[id]
		if len(a.Missing) < maxMissing {
			a.Missing = append(a.Missing, r)
			out[id] = a
		}
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	// The sales whose receipt could not be issued and are still waiting: those that are not completed and
	// not cancelled.
	if rows, err = s.db.QueryContext(ctx, `
		SELECT sale_id, store_id, register_id, cashier_id, total, reason, since, last_attempt FROM unfinished u
		WHERE NOT EXISTS (SELECT 1 FROM sales s WHERE s.store_id = u.store_id AND s.id = u.sale_id)
		  AND NOT EXISTS (SELECT 1 FROM voids v WHERE v.store_id = u.store_id AND v.sale_id = u.sale_id)
		ORDER BY register_id, since`); err != nil {
		return nil, err
	}
	for rows.Next() {
		var u domain.Unfinished
		if err := rows.Scan(&u.SaleID, &u.StoreID, &u.RegisterID, &u.CashierID, &u.Total, &u.Reason, &u.Since, &u.LastAttempt); err != nil {
			rows.Close()
			return nil, err
		}
		u.Since, u.LastAttempt = u.Since.UTC(), u.LastAttempt.UTC()
		a := out[u.RegisterID]
		a.Unfinished = append(a.Unfinished, u)
		out[u.RegisterID] = a
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	// Shifts: if the register's most recently opened shift has not closed it is the open
	// shift (one drawer at the register, one shift at a time); the most recently closed shift is
	// shown with the cash difference.
	if rows, err = s.db.QueryContext(ctx, `
		SELECT `+shiftColumns+` FROM shifts s
		WHERE opened_at = (SELECT max(opened_at) FROM shifts WHERE register_id = s.register_id)
		   OR closed_at = (SELECT max(closed_at) FROM shifts WHERE register_id = s.register_id)`); err != nil {
		return nil, err
	}
	for rows.Next() {
		sh, err := scanShift(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		a := out[sh.RegisterID]
		if sh.Open() {
			if a.OpenShift == nil || sh.OpenedAt.After(a.OpenShift.OpenedAt) {
				a.OpenShift = &sh
			}
		} else if a.LastClosed == nil || sh.ClosedAt.After(a.LastClosed.ClosedAt) {
			a.LastClosed = &sh
		}
		out[sh.RegisterID] = a
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	// A shift that looks open is old if a shift closed after it
	// (its closing event never came): it is not shown.
	for id, a := range out {
		if a.OpenShift != nil && a.LastClosed != nil && !a.OpenShift.OpenedAt.After(a.LastClosed.OpenedAt) {
			a.OpenShift = nil
			out[id] = a
		}
	}
	// Sequence number gaps (events that have not come from the register).
	gaps, err := s.Sequences().Gaps(ctx, "", 0)
	if err != nil {
		return nil, err
	}
	for _, g := range gaps {
		a := out[g.Register]
		a.MissingEvents = append(a.MissingEvents, g)
		out[g.Register] = a
	}
	return out, nil
}

// Sequences are the sequence number ranges of the events coming from the registers (pkg/sequence).
func (s *Store) Sequences() sequencedb.Tracker {
	return sequencedb.Tracker{DB: s.db, Dialect: sqldialect.Postgres}
}

// RecordSequence records that the sequence number of one of the register's events has come.
func (s *Store) RecordSequence(ctx context.Context, store, register, producer string, seq int64) error {
	return s.Sequences().Record(ctx, store, register, producer, seq)
}

const shiftColumns = `store_id, register_id, shift_id, cashier_id, cashier_name, opened_at, opening_cash, closed_at, closed_by,
	receipts, total, expected_cash, counted_cash, difference`

func scanShift(rows *sql.Rows) (domain.Shift, error) {
	var sh domain.Shift
	var closed sql.NullTime
	if err := rows.Scan(&sh.StoreID, &sh.RegisterID, &sh.ID, &sh.CashierID, &sh.CashierName, &sh.OpenedAt, &sh.OpeningCash, &closed, &sh.ClosedBy,
		&sh.Receipts, &sh.Total, &sh.ExpectedCash, &sh.CountedCash, &sh.Difference); err != nil {
		return sh, err
	}
	sh.OpenedAt = sh.OpenedAt.UTC()
	if closed.Valid {
		sh.ClosedAt = closed.Time.UTC()
	}
	return sh, nil
}

func closeRows(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	return rows.Close()
}

func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
