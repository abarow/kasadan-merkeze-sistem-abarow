// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	kurus "abarow.com/pkg/money"
	"abarow.com/pkg/outbox"
	"abarow.com/pkg/sqldialect"
	"abarow.com/pkg/text"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

// outboxTable is the outbox table of the events (its SQL is in pkg/outbox).
var outboxTable = outbox.Table{Name: "outbox", Dialect: sqldialect.Postgres, Store: true}

var (
	_ app.Catalog     = (*Store)(nil)
	_ app.StorePrices = (*Store)(nil)
	_ outbox.Store    = (*Store)(nil)
)

type pricePoint struct {
	Amount int64 `json:"amount"`
	FromMs int64 `json:"from_ms"`
}

func fromMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func toTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// UpsertCatalogProduct writes the latest state of the product coming from headquarters.
func (s *Store) UpsertCatalogProduct(ctx context.Context, p domain.CatalogProduct) error {
	barcodes, err := json.Marshal(append([]string{}, p.Barcodes...))
	if err != nil {
		return err
	}
	points := []pricePoint{}
	for _, pt := range p.HeadquartersPrices {
		points = append(points, pricePoint{Amount: int64(pt.Amount), FromMs: fromMillis(pt.From)})
	}
	prices, err := json.Marshal(points)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, q(`
		INSERT INTO catalog_products (id, name, search_name, unit, barcodes, headquarters_prices, discontinued) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, search_name = excluded.search_name, unit = excluded.unit,
			barcodes = excluded.barcodes, headquarters_prices = excluded.headquarters_prices, discontinued = excluded.discontinued`),
		p.ID, p.Name, text.Fold(p.Name), p.Unit, string(barcodes), string(prices), p.Discontinued)
	return err
}

// CatalogProducts returns the products that have the query in their name, code or
// barcode.
func (s *Store) CatalogProducts(ctx context.Context, query string, limit int) ([]domain.CatalogProduct, error) {
	folded := text.Fold(query)
	rows, err := s.db.QueryContext(ctx, q(`
		SELECT id, name, unit, barcodes, headquarters_prices, discontinued FROM catalog_products
		WHERE ? = '' OR search_name LIKE ? ESCAPE '\' OR lower(id) = ? OR barcodes LIKE ? ESCAPE '\'
		LIMIT ?`), folded, "%"+escapeLike(folded)+"%", folded, `%"`+escapeLike(strings.TrimSpace(query))+`"%`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CatalogProduct
	for rows.Next() {
		p, err := scanCatalogProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CatalogProduct returns the product.
func (s *Store) CatalogProduct(ctx context.Context, id string) (domain.CatalogProduct, error) {
	rows, err := s.db.QueryContext(ctx, q(`SELECT id, name, unit, barcodes, headquarters_prices, discontinued FROM catalog_products WHERE id = ?`), id)
	if err != nil {
		return domain.CatalogProduct{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return domain.CatalogProduct{}, err
		}
		return domain.CatalogProduct{}, domain.ErrProductNotFound
	}
	return scanCatalogProduct(rows)
}

func scanCatalogProduct(rows *sql.Rows) (domain.CatalogProduct, error) {
	var p domain.CatalogProduct
	var barcodes, prices string
	if err := rows.Scan(&p.ID, &p.Name, &p.Unit, &barcodes, &prices, &p.Discontinued); err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(barcodes), &p.Barcodes); err != nil {
		return p, err
	}
	var points []pricePoint
	if err := json.Unmarshal([]byte(prices), &points); err != nil {
		return p, err
	}
	for _, pt := range points {
		p.HeadquartersPrices = append(p.HeadquartersPrices, domain.PricePoint{Amount: kurus.Money(pt.Amount), From: toTime(pt.FromMs)})
	}
	return p, nil
}

// StorePrices returns the product's store schedule sorted by effective moment.
func (s *Store) StorePrices(ctx context.Context, productID string) ([]domain.PricePoint, error) {
	all, err := s.storePrices(ctx, `WHERE product_id = ?`, productID)
	return all[productID], err
}

// AllStorePrices are the schedules of the products that have a store schedule.
func (s *Store) AllStorePrices(ctx context.Context) (map[string][]domain.PricePoint, error) {
	return s.storePrices(ctx, ``)
}

func (s *Store) storePrices(ctx context.Context, where string, args ...any) (map[string][]domain.PricePoint, error) {
	rows, err := s.db.QueryContext(ctx, q(`SELECT product_id, from_ms, amount, clear FROM store_prices `+where+` ORDER BY product_id, from_ms`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]domain.PricePoint{}
	for rows.Next() {
		var id string
		var from int64
		var pt domain.PricePoint
		if err := rows.Scan(&id, &from, &pt.Amount, &pt.Clear); err != nil {
			return nil, err
		}
		pt.From = toTime(from)
		out[id] = append(out[id], pt)
	}
	return out, rows.Err()
}

// SaveStorePrices writes the schedule with the change record and the outbox messages.
func (s *Store) SaveStorePrices(ctx context.Context, productID string, points []domain.PricePoint, change app.PriceChange, msgs []outbox.Message) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, q(`DELETE FROM store_prices WHERE product_id = ?`), productID); err != nil {
			return err
		}
		for _, pt := range points {
			if _, err := tx.ExecContext(ctx, q(`INSERT INTO store_prices (product_id, from_ms, amount, clear) VALUES (?, ?, ?, ?)`),
				productID, fromMillis(pt.From), pt.Amount, pt.Clear); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, q(`INSERT INTO store_price_changes (product_id, at, by_user, detail) VALUES (?, ?, ?, ?)`),
			change.ProductID, change.At.UTC(), change.By, change.Detail); err != nil {
			return err
		}
		return insertOutbox(ctx, tx, msgs)
	})
}

// insertOutbox writes the messages to go to the registers and headquarters to the outbox in the
// change's transaction.
func insertOutbox(ctx context.Context, tx *sql.Tx, msgs []outbox.Message) error {
	return outboxTable.Enqueue(ctx, tx, msgs...)
}

// PriceHistory returns the product's store price changes from newest to oldest.
func (s *Store) PriceHistory(ctx context.Context, productID string, limit int) ([]app.PriceChange, error) {
	rows, err := s.db.QueryContext(ctx, q(`
		SELECT product_id, at, by_user, detail FROM store_price_changes WHERE product_id = ? ORDER BY id DESC LIMIT ?`), productID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.PriceChange
	for rows.Next() {
		var c app.PriceChange
		if err := rows.Scan(&c.ProductID, &c.At, &c.By, &c.Detail); err != nil {
			return nil, err
		}
		c.At = c.At.UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// Pending returns the unpublished messages in the order they were written.
func (s *Store) Pending(ctx context.Context, limit int) ([]outbox.Pending, error) {
	return outboxTable.Pending(ctx, s.db, limit)
}

// MarkPublished marks the message as published.
func (s *Store) MarkPublished(ctx context.Context, seq int64, at time.Time) error {
	return outboxTable.MarkPublished(ctx, s.db, seq, at)
}

// CountPending counts the unpublished messages.
func (s *Store) CountPending(ctx context.Context) (int64, error) {
	return outboxTable.CountPending(ctx, s.db)
}

// escapeLike escapes the special characters in a LIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
