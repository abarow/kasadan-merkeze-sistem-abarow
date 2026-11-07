// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package sqlite is the app.Baskets adapter that keeps baskets in SQLite.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"abarow.com/pkg/money"
	"abarow.com/pkg/sqlitedb"
	"abarow.com/services/modules/retail/internal/app"
	"abarow.com/services/modules/retail/internal/domain"
)

//go:embed migrations/*.sql
var migrations embed.FS

// timeLayout is the format of times in the database: fixed-width UTC.
// Thanks to the fixed width, text comparison is the same as time order.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// Store, app.Baskets'i SQLite ile uygular.
type Store struct {
	db *sql.DB
}

var _ app.Baskets = (*Store)(nil)

// Open opens the database and applies the migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sqlitedb.Open(ctx, path, migrations)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// lineJSON is the stored form of a basket line. The field names are the storage
// contract; old records must stay readable even if the domain changes.
type lineJSON struct {
	ProductID string `json:"product_id"`
	Barcode   string `json:"barcode"`
	Name      string `json:"name"`
	QtyMilli  int64  `json:"qty_milli"`
	UnitPrice int64  `json:"unit_price_kurus"`
	TaxRateBp int64  `json:"tax_rate_bp"`
	Weighed   bool   `json:"weighed"`
}

func (s *Store) Save(ctx context.Context, b *domain.Basket) error {
	lines := make([]lineJSON, 0, len(b.Lines))
	for _, l := range b.Lines {
		lines = append(lines, lineJSON{
			ProductID: l.ProductID, Barcode: l.Barcode, Name: l.Name, QtyMilli: int64(l.Qty),
			UnitPrice: int64(l.UnitPrice), TaxRateBp: int64(l.TaxRate), Weighed: l.Weighed,
		})
	}
	data, err := json.Marshal(lines)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO baskets (id, cashier_id, status, sale_id, lines, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			status = excluded.status, sale_id = excluded.sale_id, lines = excluded.lines, updated_at = excluded.updated_at`,
		b.ID, b.CashierID, b.Status, b.SaleID, string(data), formatTime(b.CreatedAt), formatTime(b.UpdatedAt))
	return err
}

const basketColumns = `id, cashier_id, status, sale_id, lines, created_at, updated_at FROM baskets`

func (s *Store) Get(ctx context.Context, id domain.BasketID) (*domain.Basket, error) {
	return scanBasket(s.db.QueryRowContext(ctx, `SELECT `+basketColumns+` WHERE id = ?`, id))
}

func (s *Store) Latest(ctx context.Context, cashierID string, since time.Time) (*domain.Basket, error) {
	return scanBasket(s.db.QueryRowContext(ctx, `
		SELECT `+basketColumns+` WHERE cashier_id = ? AND updated_at >= ?
		ORDER BY updated_at DESC LIMIT 1`, cashierID, formatTime(since)))
}

func (s *Store) DeleteBefore(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM baskets WHERE updated_at < ?`, formatTime(before))
	return err
}

func scanBasket(row *sql.Row) (*domain.Basket, error) {
	var (
		b                      domain.Basket
		data, created, updated string
	)
	err := row.Scan(&b.ID, &b.CashierID, &b.Status, &b.SaleID, &data, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, app.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var lines []lineJSON
	if err := json.Unmarshal([]byte(data), &lines); err != nil {
		return nil, fmt.Errorf("sepet %s çözülemedi: %w", b.ID, err)
	}
	for _, l := range lines {
		b.Lines = append(b.Lines, domain.Line{
			ProductID: l.ProductID, Barcode: l.Barcode, Name: l.Name, Qty: money.Qty(l.QtyMilli),
			UnitPrice: money.Money(l.UnitPrice), TaxRate: money.Rate(l.TaxRateBp), Weighed: l.Weighed,
		})
	}
	if b.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return nil, err
	}
	if b.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return nil, err
	}
	return &b, nil
}

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }
