// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package postgres is the app.Baskets adapter that keeps baskets in PostgreSQL.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"abarow.com/pkg/money"
	"abarow.com/pkg/pgdb"
	"abarow.com/pkg/sqldialect"
	"abarow.com/services/modules/retail/internal/app"
	"abarow.com/services/modules/retail/internal/domain"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store implements app.Baskets with PostgreSQL.
type Store struct {
	db *sql.DB
}

var _ app.Baskets = (*Store)(nil)

// Open opens the database and applies the migrations.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := pgdb.Open(ctx, dsn, "retail", migrations)
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
		INSERT INTO baskets (id, cashier_id, status, sale_id, lines, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			status = excluded.status, sale_id = excluded.sale_id, lines = excluded.lines, updated_at = excluded.updated_at`,
		b.ID, b.CashierID, b.Status, b.SaleID, string(data), formatTime(b.CreatedAt), formatTime(b.UpdatedAt))
	return err
}

const basketColumns = `id, cashier_id, status, sale_id, lines, created_at, updated_at FROM baskets`

func (s *Store) Get(ctx context.Context, id domain.BasketID) (*domain.Basket, error) {
	return scanBasket(s.db.QueryRowContext(ctx, `SELECT `+basketColumns+` WHERE id = $1`, id))
}

func (s *Store) Latest(ctx context.Context, cashierID string, since time.Time) (*domain.Basket, error) {
	return scanBasket(s.db.QueryRowContext(ctx, `
		SELECT `+basketColumns+` WHERE cashier_id = $1 AND updated_at >= $2
		ORDER BY updated_at DESC LIMIT 1`, cashierID, formatTime(since)))
}

func (s *Store) DeleteBefore(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM baskets WHERE updated_at < $1`, formatTime(before))
	return err
}

func scanBasket(row *sql.Row) (*domain.Basket, error) {
	var (
		b    domain.Basket
		data string
	)
	err := row.Scan(&b.ID, &b.CashierID, &b.Status, &b.SaleID, &data, sqldialect.ScanTime(&b.CreatedAt), sqldialect.ScanTime(&b.UpdatedAt))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, app.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var lines []lineJSON
	if err := json.Unmarshal([]byte(data), &lines); err != nil {
		return nil, fmt.Errorf("could not decode basket %s: %w", b.ID, err)
	}
	for _, l := range lines {
		b.Lines = append(b.Lines, domain.Line{
			ProductID: l.ProductID, Barcode: l.Barcode, Name: l.Name, Qty: money.Qty(l.QtyMilli),
			UnitPrice: money.Money(l.UnitPrice), TaxRate: money.Rate(l.TaxRateBp), Weighed: l.Weighed,
		})
	}
	return &b, nil
}

func formatTime(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }
