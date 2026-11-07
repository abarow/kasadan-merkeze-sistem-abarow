// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package postgres is the app.Store adapter that keeps headquarters' stock in PostgreSQL.
// The tables are in the headquarters database with the stock_ prefix.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" driver for database/sql
	"github.com/pressly/goose/v3"

	"abarow.com/pkg/money"
	"abarow.com/pkg/outbox"
	"abarow.com/pkg/pgpool"
	"abarow.com/pkg/sqldialect"
	"abarow.com/pkg/stock"
	"abarow.com/pkg/text"
	"abarow.com/services/inventory/internal/app"
)

// outboxTable is the events' outbox table (its SQL is in pkg/outbox).
var outboxTable = outbox.Table{Name: "stock_outbox", Dialect: sqldialect.Postgres, StoreID: app.Warehouse}

//go:embed migrations/*.sql
var migrations embed.FS

// VersionTable is the inventory's migration version table (the database is shared
// with other services).
const VersionTable = "stock_goose_db_version"

// Store, app.Store'u PostgreSQL ile uygular.
type Store struct {
	db *sql.DB
	// here is the store's identity in the store scope (Shop); empty at headquarters.
	here string
}

var (
	_ app.Store    = (*Store)(nil)
	_ outbox.Store = (*Store)(nil)
)

// Open connects to the database and applies the migrations.
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
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithTableName(VersionTable))
	if err == nil {
		_, err = p.Up(ctx)
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("migration uygulanamadı: %w", err)
	}
	return &Store{db: db}, nil
}

// Shop uses the store in the store scope (the store server): the stock has a single place
// (the store itself), the outbox messages carry the store id. It returns a new view
// that shares the same connection.
func (s *Store) Shop(here string) *Store { return &Store{db: s.db, here: here} }

// outbox is the outbox table specific to the store: the store id of the messages is
// Warehouse at headquarters and the store's identity in the store scope.
func (s *Store) outbox() outbox.Table {
	t := outboxTable
	if s.here != "" {
		t.StoreID = s.here
	}
	return t
}

// Close closes the connection.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // Commit'ten sonra etkisiz
	if err := fn(tx); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "stock_movements_receipt_once" {
			return stock.ErrDuplicateReceipt
		}
		return err
	}
	return tx.Commit()
}

// Record writes the movement and the place's stock in the same transaction; it writes the events to the
// outbox.
func (s *Store) Record(ctx context.Context, location string, m stock.Movement, events []outbox.Message) (bool, error) {
	recorded := false
	err := s.inTx(ctx, func(tx *sql.Tx) (err error) {
		if recorded, err = recordTx(ctx, tx, location, m); err != nil || !recorded {
			return err
		}
		return outboxTable.Enqueue(ctx, tx, events...)
	})
	return recorded, err
}

// recordTx writes the movement and the place's stock; false if the movement already exists.
func recordTx(ctx context.Context, tx *sql.Tx, location string, m stock.Movement) (bool, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO stock_movements (location, id, kind, ref, supplier, counterpart, by_name, at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (location, id) DO NOTHING`,
		location, m.ID, string(m.Kind), m.Ref, m.Supplier, m.Counterpart, m.By, m.At.UTC())
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil // tekrar gelen hareket
	}
	for i, l := range m.Lines {
		var counted, system any
		if m.Kind == stock.Count {
			counted, system = int64(l.Counted), int64(l.System)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stock_lines (location, movement_id, line_no, product_id, name, qty, counted, system)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, location, m.ID, i+1, l.ProductID, l.Name, int64(l.Qty), counted, system); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stock_levels (location, product_id, qty) VALUES ($1, $2, $3)
			ON CONFLICT (location, product_id) DO UPDATE SET qty = stock_levels.qty + excluded.qty`,
			location, l.ProductID, int64(l.Qty)); err != nil {
			return false, err
		}
	}
	return true, nil
}

func enqueueTx(ctx context.Context, tx *sql.Tx, e outbox.Message) error {
	return outboxTable.Enqueue(ctx, tx, e)
}

// saveTransferTx writes the transfer (with its sent lines); does nothing if it
// already exists.
func saveTransferTx(ctx context.Context, tx *sql.Tx, t stock.Transfer) error {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO stock_transfers (id, from_loc, to_loc, by_name, at) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO NOTHING`, t.ID, t.From, t.To, t.By, t.At.UTC())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		for i, l := range t.Lines {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO stock_transfer_lines (transfer_id, line_no, product_id, name, qty) VALUES ($1, $2, $3, $4, $5)`,
				t.ID, i+1, l.ProductID, l.Name, int64(l.Qty)); err != nil {
				return err
			}
		}
	}
	return nil
}

// SaveTransfer writes the transfer sent here; does nothing if it already
// exists.
func (s *Store) SaveTransfer(ctx context.Context, t stock.Transfer) error {
	return s.inTx(ctx, func(tx *sql.Tx) error { return saveTransferTx(ctx, tx, t) })
}

// SendTransfer writes the outgoing movement to the place's stock, opens the transfer and
// writes the events to the outbox; all in the same transaction. If the movement already exists (a repeat)
// it does nothing and returns false.
func (s *Store) SendTransfer(ctx context.Context, location string, m stock.Movement, t stock.Transfer, events []outbox.Message) (bool, error) {
	recorded := false
	err := s.inTx(ctx, func(tx *sql.Tx) (err error) {
		if recorded, err = recordTx(ctx, tx, location, m); err != nil || !recorded {
			return err
		}
		if err := saveTransferTx(ctx, tx, t); err != nil {
			return err
		}
		for _, e := range events {
			if err := enqueueTx(ctx, tx, e); err != nil {
				return err
			}
		}
		return nil
	})
	return recorded, err
}

// ReceiveTransfer writes the incoming movement to the place's stock and, if the transfer was sent to this
// place and is on the way, marks it received (with the received
// quantities); in the same transaction. It returns false if the movement already exists.
func (s *Store) ReceiveTransfer(ctx context.Context, location string, m stock.Movement, events []outbox.Message) (bool, error) {
	recorded := false
	err := s.inTx(ctx, func(tx *sql.Tx) (err error) {
		if recorded, err = recordTx(ctx, tx, location, m); err != nil || !recorded {
			return err
		}
		if err := outboxTable.Enqueue(ctx, tx, events...); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE stock_transfers SET received_by = $1, received_at = $2
			WHERE id = $3 AND to_loc = $4 AND received_at IS NULL`, m.By, m.At.UTC(), m.Ref, location)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		for _, l := range m.Lines {
			if _, err := tx.ExecContext(ctx, `
				UPDATE stock_transfer_lines SET received = $1 WHERE transfer_id = $2 AND product_id = $3`,
				int64(l.Qty), m.Ref, l.ProductID); err != nil {
				return err
			}
		}
		return nil
	})
	return recorded, err
}

// Transfer, transferdir; yoksa stock.ErrUnknownTransfer.
func (s *Store) Transfer(ctx context.Context, id string) (stock.Transfer, error) {
	ts, err := s.transfers(ctx, `WHERE id = $1`, id)
	if err != nil {
		return stock.Transfer{}, err
	}
	if len(ts) == 0 {
		return stock.Transfer{}, stock.ErrUnknownTransfer
	}
	return ts[0], nil
}

// Transfers returns the ones on the way first, then the others from newest to oldest (at
// most limit).
func (s *Store) Transfers(ctx context.Context, limit int) ([]stock.Transfer, error) {
	return s.transfers(ctx, `ORDER BY received_at IS NOT NULL, at DESC, id LIMIT $1`, limit)
}

func (s *Store) transfers(ctx context.Context, where string, args ...any) ([]stock.Transfer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, from_loc, to_loc, by_name, at, received_by, received_at FROM stock_transfers `+where, args...)
	if err != nil {
		return nil, err
	}
	var out []stock.Transfer
	index := map[string]int{}
	for rows.Next() {
		var t stock.Transfer
		var receivedAt sql.NullTime
		if err := rows.Scan(&t.ID, &t.From, &t.To, &t.By, &t.At, &t.ReceivedBy, &receivedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if receivedAt.Valid {
			t.ReceivedAt = receivedAt.Time
		}
		index[t.ID] = len(out)
		out = append(out, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	ids := make([]string, 0, len(out))
	for _, t := range out {
		ids = append(ids, t.ID)
	}
	lines, err := s.db.QueryContext(ctx, `
		SELECT transfer_id, product_id, name, qty, received FROM stock_transfer_lines
		WHERE transfer_id = ANY($1) ORDER BY transfer_id, line_no`, ids)
	if err != nil {
		return nil, err
	}
	defer lines.Close()
	for lines.Next() {
		var id string
		var l stock.Line
		var qty int64
		var received sql.NullInt64
		if err := lines.Scan(&id, &l.ProductID, &l.Name, &qty, &received); err != nil {
			return nil, err
		}
		t := &out[index[id]]
		l.Qty = money.Qty(qty)
		t.Lines = append(t.Lines, l)
		if received.Valid {
			t.Received = append(t.Received, stock.Line{ProductID: l.ProductID, Name: l.Name, Qty: money.Qty(received.Int64)})
		}
	}
	return out, lines.Err()
}

// Enqueue writes the event to the outbox (the place list).
func (s *Store) Enqueue(ctx context.Context, e outbox.Message) error {
	return s.inTx(ctx, func(tx *sql.Tx) error { return enqueueTx(ctx, tx, e) })
}

// Pending returns the unpublished events in the order they were written.
func (s *Store) Pending(ctx context.Context, limit int) ([]outbox.Pending, error) {
	return s.outbox().Pending(ctx, s.db, limit)
}

// MarkPublished marks the event as published.
func (s *Store) MarkPublished(ctx context.Context, seq int64, at time.Time) error {
	return s.outbox().MarkPublished(ctx, s.db, seq, at)
}

// CountPending counts the unpublished events.
func (s *Store) CountPending(ctx context.Context) (int64, error) {
	return s.outbox().CountPending(ctx, s.db)
}

// SetLocations writes the places a transfer can be sent to (the last state is valid;
// store scope).
func (s *Store) SetLocations(ctx context.Context, locs []stock.Location) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM stock_locations`); err != nil {
			return err
		}
		for _, l := range locs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO stock_locations (id, name) VALUES ($1, $2)`, l.ID, l.Name); err != nil {
				return err
			}
		}
		return nil
	})
}

// Locations are the places a transfer can be sent to (store scope).
func (s *Store) Locations(ctx context.Context) ([]stock.Location, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM stock_locations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []stock.Location
	for rows.Next() {
		var l stock.Location
		if err := rows.Scan(&l.ID, &l.Name); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Products returns the given products.
func (s *Store) Products(ctx context.Context, ids []string) (map[string]stock.Product, error) {
	out := map[string]stock.Product{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, unit, discontinued FROM stock_products WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p stock.Product
		var unit string
		if err := rows.Scan(&p.ID, &p.Name, &unit, &p.Discontinued); err != nil {
			return nil, err
		}
		p.Unit = stock.Unit(unit)
		out[p.ID] = p
	}
	return out, rows.Err()
}

// Quantities is the stock of the given products at the place.
func (s *Store) Quantities(ctx context.Context, location string, ids []string) (map[string]money.Qty, error) {
	out := map[string]money.Qty{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT product_id, qty FROM stock_levels WHERE location = $1 AND product_id = ANY($2)`, location, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var q int64
		if err := rows.Scan(&id, &q); err != nil {
			return nil, err
		}
		out[id] = money.Qty(q)
	}
	return out, rows.Err()
}

// Overview returns the products with their stock per place.
func (s *Store) Overview(ctx context.Context, q string, limit int) ([]app.Row, error) {
	q = strings.TrimSpace(q)
	query := `SELECT p.id, p.name, p.unit, p.discontinued,
			coalesce((SELECT string_agg(barcode, ' ' ORDER BY barcode) FROM stock_barcodes b WHERE b.product_id = p.id), '')
		FROM stock_products p`
	args := []any{}
	if q != "" {
		query += ` WHERE strpos(p.search, $1) > 0 OR p.id IN (SELECT product_id FROM stock_barcodes WHERE barcode = $2)`
		args = append(args, text.Fold(q), q)
	}
	query += fmt.Sprintf(` ORDER BY p.search, p.id LIMIT %d`, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var out []app.Row
	index := map[string]int{}
	for rows.Next() {
		var r app.Row
		var unit, barcodes string
		if err := rows.Scan(&r.ID, &r.Name, &unit, &r.Discontinued, &barcodes); err != nil {
			rows.Close()
			return nil, err
		}
		r.Unit, r.Barcodes, r.Qty = stock.Unit(unit), strings.Fields(barcodes), map[string]money.Qty{}
		index[r.ID] = len(out)
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	ids := make([]string, 0, len(out))
	for _, r := range out {
		ids = append(ids, r.ID)
	}
	levels, err := s.db.QueryContext(ctx, `SELECT product_id, location, qty FROM stock_levels WHERE product_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer levels.Close()
	for levels.Next() {
		var id, loc string
		var qty int64
		if err := levels.Scan(&id, &loc, &qty); err != nil {
			return nil, err
		}
		out[index[id]].Qty[loc] = money.Qty(qty)
	}
	return out, levels.Err()
}

// Places are the places that have stock; in the store scope only the store itself.
func (s *Store) Places(ctx context.Context) ([]string, error) {
	if s.here != "" {
		return []string{s.here}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT location FROM stock_levels ORDER BY location`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ByBarcode is the barcode's product and its stock at the place.
func (s *Store) ByBarcode(ctx context.Context, location, barcode string) (stock.Item, error) {
	var it stock.Item
	var unit string
	var qty int64
	err := s.db.QueryRowContext(ctx, `
		SELECT p.id, p.name, p.unit, p.discontinued, coalesce(l.qty, 0)
		FROM stock_barcodes b JOIN stock_products p ON p.id = b.product_id
		LEFT JOIN stock_levels l ON l.product_id = p.id AND l.location = $2
		WHERE b.barcode = $1`, strings.TrimSpace(barcode), location).Scan(&it.ID, &it.Name, &unit, &it.Discontinued, &qty)
	if errors.Is(err, sql.ErrNoRows) {
		return stock.Item{}, app.ErrNotFound
	}
	it.Unit, it.Qty = stock.Unit(unit), money.Qty(qty)
	return it, err
}

// Movements are the product's movements at the place (newest to oldest).
func (s *Store) Movements(ctx context.Context, location, productID string, limit int) ([]stock.Movement, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.kind, m.ref, m.supplier, m.counterpart, m.by_name, m.at, l.name, l.qty, coalesce(l.counted, 0), coalesce(l.system, 0)
		FROM stock_lines l JOIN stock_movements m ON m.location = l.location AND m.id = l.movement_id
		WHERE l.location = $1 AND l.product_id = $2 ORDER BY m.at DESC, m.id DESC LIMIT $3`, location, productID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []stock.Movement
	for rows.Next() {
		var m stock.Movement
		var kind string
		var l stock.Line
		var qty, counted, system int64
		if err := rows.Scan(&m.ID, &kind, &m.Ref, &m.Supplier, &m.Counterpart, &m.By, &m.At, &l.Name, &qty, &counted, &system); err != nil {
			return nil, err
		}
		m.Kind = stock.Kind(kind)
		l.ProductID, l.Qty, l.Counted, l.System = productID, money.Qty(qty), money.Qty(counted), money.Qty(system)
		m.Lines = []stock.Line{l}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SaveProduct writes the product in the catalog; its barcodes are rebuilt.
func (s *Store) SaveProduct(ctx context.Context, p stock.Product) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stock_products (id, name, unit, discontinued, from_catalog, search) VALUES ($1, $2, $3, $4, true, $5)
			ON CONFLICT (id) DO UPDATE SET name = excluded.name, unit = excluded.unit,
				discontinued = excluded.discontinued, from_catalog = true, search = excluded.search`,
			p.ID, p.Name, string(p.Unit), p.Discontinued, text.Fold(p.Name)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM stock_barcodes WHERE product_id = $1`, p.ID); err != nil {
			return err
		}
		for _, b := range p.Barcodes {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO stock_barcodes (barcode, product_id) VALUES ($1, $2)
				ON CONFLICT (barcode) DO UPDATE SET product_id = excluded.product_id`, b, p.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// EnsureProducts adds a product in a movement by name if it is not in the catalog; a product with
// fractional quantities is weighed (kg).
func (s *Store) EnsureProducts(ctx context.Context, lines []stock.Line) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, l := range lines {
			unit := stock.Piece
			if l.Qty%money.QtyScale != 0 {
				unit = stock.Kg
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO stock_products (id, name, search, unit) VALUES ($1, $2, $3, $4)
				ON CONFLICT (id) DO UPDATE SET name = excluded.name, search = excluded.search,
					unit = CASE WHEN excluded.unit = 'kg' THEN 'kg' ELSE stock_products.unit END
				WHERE NOT stock_products.from_catalog`, l.ProductID, l.Name, text.Fold(l.Name), string(unit)); err != nil {
				return err
			}
		}
		return nil
	})
}
