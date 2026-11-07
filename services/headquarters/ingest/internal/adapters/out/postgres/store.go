// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package postgres is the app.Store adapter that keeps the events coming from the stores
// in PostgreSQL.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" driver for database/sql
	"github.com/pressly/goose/v3"

	"abarow.com/pkg/delivery"
	"abarow.com/pkg/delivery/deliverydb"
	"abarow.com/pkg/ledger/ledgerdb"
	"abarow.com/pkg/outbox"
	"abarow.com/pkg/pgpool"
	"abarow.com/pkg/sequence/sequencedb"
	"abarow.com/pkg/sqldialect"
	"abarow.com/services/headquarters/ingest/internal/app"
	"abarow.com/services/headquarters/ingest/internal/domain"
)

// receiptStream is which stream's event the receipt is given for: headquarters
// takes only the stores' store stream (ABAROW_STORE) as a source.
const receiptStream = "ABAROW_STORE"

// receiptOutbox is the outbox table of the receipts (its SQL is in pkg/outbox).
var receiptOutbox = outbox.Table{Name: "receipt_outbox", Dialect: sqldialect.Postgres, StoreID: "headquarters"}

//go:embed migrations/*.sql
var migrations embed.FS

// ErrMigration means the database schema could not be updated; it does not get better
// by waiting.
var ErrMigration = errors.New("migration uygulanamadı")

// Store, app.Store'u PostgreSQL ile uygular.
type Store struct {
	db *sql.DB
	// NewID produces a receipt id (UUIDv7); it can be fixed in a test.
	NewID func() string
}

var _ app.Store = (*Store)(nil)

// Open connects to the database and applies the migrations. dsn is the PostgreSQL
// connection address (postgres://user:password@server/database).
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	// The connection allowance is limited (max_connections 20 on the store server).
	pgpool.Limit(db, pgpool.Inventory)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
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
		return nil, fmt.Errorf("%w: %w", ErrMigration, err)
	}
	return &Store{db: db, NewID: delivery.NewID}, nil
}

// Close closes the database connection.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Record writes the event in its raw form and its body to the read models in a single
// transaction; if the event came before it writes nothing and returns
// false.
func (s *Store) Record(ctx context.Context, e domain.Event, body any) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() // Commit'ten sonra etkisiz

	payload := e.Payload
	if payload == nil {
		payload = []byte{}
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO events (id, subject, store_id, register_id, occurred_at, content_type, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (id) DO NOTHING`,
		e.ID, e.Subject, e.StoreID, e.RegisterID, e.OccurredAt.UTC(), e.ContentType, payload)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return false, err
	} else if n == 0 {
		// The event was processed before. If its receipt has not been given yet (it was processed
		// before this version, the headquarters stream is being read from the start) it is given now.
		if err := s.issue(ctx, tx, e); err != nil {
			return false, err
		}
		return false, tx.Commit()
	}

	newSale := false
	switch b := body.(type) {
	// The sale and shift events are written by the sales ledger (the same code
	// and tables as the store server).
	case domain.Sale:
		newSale, err = ledgerdb.Postgres.SaveSale(ctx, tx, b)
	case domain.Void:
		err = ledgerdb.Postgres.SaveVoid(ctx, tx, b)
	case domain.Unfinished:
		err = ledgerdb.Postgres.SaveUnfinished(ctx, tx, b)
	case domain.Shift:
		err = ledgerdb.Postgres.SaveShift(ctx, tx, b)
	case domain.StorePrices:
		err = saveStorePrices(ctx, tx, b)
	case domain.Cashier:
		err = saveCashier(ctx, tx, b)
	case nil:
	default:
		err = fmt.Errorf("bilinmeyen olay gövdesi %T", body)
	}
	if err != nil {
		return false, err
	}
	if err := count(ctx, tx, e, newSale); err != nil {
		return false, err
	}
	// The receipt is given in the same transaction as the event's record: if the event cannot be written there is no
	// receipt either, if the event was written the receipt certainly exists.
	if err := s.issue(ctx, tx, e); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// issue gives the receipt that the event has been processed into the database and writes the receipt
// to the outbox to be published to the store that sent the event (pkg/delivery). If a receipt
// has already been given for the event it writes nothing.
func (s *Store) issue(ctx context.Context, tx *sql.Tx, e domain.Event) error {
	r, err := deliverydb.Store{Dialect: sqldialect.Postgres}.Issue(ctx, tx, e.StoreID, receiptStream, e.ID, s.NewID, time.Now())
	if err != nil || !r.New {
		return err
	}
	msg, err := delivery.OutboxMessage(e.StoreID, receiptStream, r.Receipt)
	if err != nil {
		return err
	}
	return receiptOutbox.Enqueue(ctx, tx, msg)
}

// Pending returns the unpublished receipts in the order they were written (outbox.Store).
func (s *Store) Pending(ctx context.Context, limit int) ([]outbox.Pending, error) {
	return receiptOutbox.Pending(ctx, s.db, limit)
}

// MarkPublished marks the receipt as published (outbox.Store).
func (s *Store) MarkPublished(ctx context.Context, seq int64, at time.Time) error {
	return receiptOutbox.MarkPublished(ctx, s.db, seq, at)
}

// CountPending counts the unpublished receipts (outbox.Store).
func (s *Store) CountPending(ctx context.Context) (int64, error) {
	return receiptOutbox.CountPending(ctx, s.db)
}

// count updates the event summary of the store and the register.
func count(ctx context.Context, tx *sql.Tx, e domain.Event, newSale bool) error {
	sales := 0
	if newSale {
		sales = 1
	}
	at := e.OccurredAt.UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO store_stats (store_id, events, sales, first_event_at, last_event_at) VALUES ($1, 1, $2, $3, $3)
		ON CONFLICT (store_id) DO UPDATE SET
			events = store_stats.events + 1,
			sales = store_stats.sales + excluded.sales,
			first_event_at = least(store_stats.first_event_at, excluded.first_event_at),
			last_event_at = greatest(store_stats.last_event_at, excluded.last_event_at)`,
		e.StoreID, sales, at); err != nil {
		return err
	}
	if e.RegisterID == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO registers (store_id, register_id, events, sales, first_event_at, last_event_at) VALUES ($1, $2, 1, $3, $4, $4)
		ON CONFLICT (store_id, register_id) DO UPDATE SET
			events = registers.events + 1,
			sales = registers.sales + excluded.sales,
			first_event_at = least(registers.first_event_at, excluded.first_event_at),
			last_event_at = greatest(registers.last_event_at, excluded.last_event_at)`,
		e.StoreID, e.RegisterID, sales, at)
	return err
}

// saveStorePrices writes the latest state of the store's price schedule for the product;
// if the order gets mixed up and an older state comes it does not overwrite the newer.
func saveStorePrices(ctx context.Context, tx *sql.Tx, s domain.StorePrices) error {
	type point struct {
		Amount int64      `json:"amount"`
		From   *time.Time `json:"from,omitempty"`
		Clear  bool       `json:"clear,omitempty"`
	}
	points := []point{}
	for _, p := range s.Prices {
		pt := point{Amount: int64(p.Amount), Clear: p.Clear}
		if !p.From.IsZero() {
			from := p.From.UTC()
			pt.From = &from
		}
		points = append(points, pt)
	}
	data, err := json.Marshal(points)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO store_prices (store_id, product_id, product_name, prices, updated_by, updated_at) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (store_id, product_id) DO UPDATE SET
			product_name = excluded.product_name, prices = excluded.prices, updated_by = excluded.updated_by, updated_at = excluded.updated_at
		WHERE excluded.updated_at >= store_prices.updated_at`,
		s.StoreID, s.ProductID, s.ProductName, string(data), s.UpdatedBy, s.At.UTC())
	return err
}

// saveCashier writes the latest state of the store's cashier; if the order gets
// mixed up and an older state comes it does not overwrite the newer.
func saveCashier(ctx context.Context, tx *sql.Tx, c domain.Cashier) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO cashiers (store_id, cashier_id, name, role, disabled, updated_by, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (store_id, cashier_id) DO UPDATE SET
			name = excluded.name, role = excluded.role, disabled = excluded.disabled,
			updated_by = excluded.updated_by, updated_at = excluded.updated_at
		WHERE excluded.updated_at >= cashiers.updated_at`,
		c.StoreID, c.CashierID, c.Name, c.Role, c.Disabled, c.UpdatedBy, c.At.UTC())
	return err
}

// Activity is the summary of the events that have come, by store id.
func (s *Store) Activity(ctx context.Context) (map[string]domain.Activity, error) {
	out := map[string]domain.Activity{}
	rows, err := s.db.QueryContext(ctx, `SELECT store_id, events, last_event_at, sales FROM store_stats`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var a domain.Activity
		if err := rows.Scan(&id, &a.Events, &a.LastEventAt, &a.Sales); err != nil {
			return nil, err
		}
		a.LastEventAt = a.LastEventAt.UTC()
		out[id] = a
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	regs, err := s.db.QueryContext(ctx, `
		SELECT store_id, register_id, events, sales, last_event_at FROM registers ORDER BY store_id, register_id`)
	if err != nil {
		return nil, err
	}
	defer regs.Close()
	for regs.Next() {
		var id string
		var r domain.RegisterActivity
		if err := regs.Scan(&id, &r.ID, &r.Events, &r.Sales, &r.LastEventAt); err != nil {
			return nil, err
		}
		r.LastEventAt = r.LastEventAt.UTC()
		a := out[id]
		a.Registers = append(a.Registers, r)
		out[id] = a
	}
	if err := regs.Err(); err != nil {
		return nil, err
	}
	// Sequence number gaps (events that have not reached headquarters).
	gaps, err := s.Sequences().Gaps(ctx, "", 0)
	if err != nil {
		return nil, err
	}
	for _, g := range gaps {
		a := out[g.Store]
		a.MissingEvents = append(a.MissingEvents, g)
		out[g.Store] = a
	}
	return out, nil
}

// Sequences are the sequence number ranges of the register events coming from the stores
// (pkg/sequence).
func (s *Store) Sequences() sequencedb.Tracker {
	return sequencedb.Tracker{DB: s.db, Dialect: sqldialect.Postgres}
}

// RecordSequence records that the sequence number of a register event has come.
func (s *Store) RecordSequence(ctx context.Context, store, register, producer string, seq int64) error {
	return s.Sequences().Record(ctx, store, register, producer, seq)
}

// SaveLinks writes the stores' connection state; it deletes the state of the
// stores that are not in the list.
func (s *Store) SaveLinks(ctx context.Context, links []domain.StoreStatus, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // Commit'ten sonra etkisiz

	ids := make([]string, len(links))
	for i, l := range links {
		ids[i] = l.ID
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM store_links WHERE NOT (store_id = ANY($1))`, ids); err != nil {
		return err
	}
	for _, l := range links {
		var lastSeen any
		if !l.LastSeen.IsZero() {
			lastSeen = l.LastSeen.UTC()
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO store_links (store_id, name, link, last_seen, pending_events, error, checked_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (store_id) DO UPDATE SET
				name = excluded.name, link = excluded.link, last_seen = excluded.last_seen,
				pending_events = excluded.pending_events, error = excluded.error, checked_at = excluded.checked_at`,
			l.ID, l.Name, string(l.Link), lastSeen, int64(l.Pending), l.Error, at.UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
