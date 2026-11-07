// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package sqlite is the app.Repository adapter that keeps the products in SQLite.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"abarow.com/pkg/sqlitedb"
	"abarow.com/pkg/text"
	"abarow.com/services/core/catalog/internal/app"
	"abarow.com/services/core/catalog/internal/domain"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store, app.Repository'yi SQLite ile uygular.
type Store struct {
	db *sql.DB
}

var _ app.Repository = (*Store)(nil)

// Open opens the database and applies the migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sqlitedb.Open(ctx, path, migrations)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.fillSortNames(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// fillSortNames fills in the sort key of products that have none (those written
// before the column was added).
func (s *Store) fillSortNames(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM products WHERE sort_name = ''`)
	if err != nil {
		return err
	}
	type row struct{ id, name string }
	var empty []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.name); err != nil {
			rows.Close()
			return err
		}
		empty = append(empty, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range empty {
		if _, err := s.db.ExecContext(ctx, `UPDATE products SET sort_name = ? WHERE id = ?`, text.SortKey(r.name), r.id); err != nil {
			return fmt.Errorf("sıralama anahtarı yazılamadı (%s): %w", r.id, err)
		}
	}
	return nil
}

// Close closes the database connection.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

const productColumns = `p.id, p.name, p.unit, p.tax_rate, p.quick_key, p.category, p.menu_order, p.station, p.option_groups, p.image_sha256, p.image_type`

// Get returns the product (it is not found if withdrawn from sale).
func (s *Store) Get(ctx context.Context, id string) (domain.Product, error) {
	products, err := s.query(ctx, `SELECT `+productColumns+` FROM products p WHERE p.id = ? AND p.discontinued = 0`, id)
	if err != nil {
		return domain.Product{}, err
	}
	if len(products) == 0 {
		return domain.Product{}, app.ErrNotFound
	}
	return products[0], nil
}

// Menu: the products that have a category (are on the menu).
func (s *Store) Menu(ctx context.Context) ([]domain.Product, error) {
	return s.query(ctx, `SELECT `+productColumns+` FROM products p WHERE p.category != '' AND p.discontinued = 0`)
}

// SaveImage writes the product's image; if Data is empty it deletes the image.
func (s *Store) SaveImage(ctx context.Context, img domain.Image) error {
	if len(img.Data) == 0 {
		_, err := s.db.ExecContext(ctx, `DELETE FROM images WHERE product_id = ?`, img.ProductID)
		return err
	}
	return saveImage(ctx, s.db, img)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func saveImage(ctx context.Context, db execer, img domain.Image) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO images (product_id, sha256, content_type, data) VALUES (?, ?, ?, ?)
		ON CONFLICT (product_id) DO UPDATE SET sha256 = excluded.sha256, content_type = excluded.content_type, data = excluded.data`,
		img.ProductID, img.SHA256, img.ContentType, img.Data)
	return err
}

// Image is the product's current image: an image whose digest does not match the product's
// (a new one has not come yet) is not given.
func (s *Store) Image(ctx context.Context, productID string) (domain.Image, error) {
	img := domain.Image{ProductID: productID}
	err := s.db.QueryRowContext(ctx, `
		SELECT i.sha256, i.content_type, i.data FROM images i JOIN products p ON p.id = i.product_id AND p.image_sha256 = i.sha256
		WHERE i.product_id = ? AND p.discontinued = 0`, productID).Scan(&img.SHA256, &img.ContentType, &img.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Image{}, app.ErrNotFound
	}
	return img, err
}

func (s *Store) ByBarcode(ctx context.Context, barcode string) (domain.Product, error) {
	products, err := s.query(ctx, `SELECT `+productColumns+` FROM barcodes b JOIN products p ON p.id = b.product_id
		WHERE b.barcode = ? AND p.discontinued = 0`, barcode)
	if err != nil {
		return domain.Product{}, err
	}
	if len(products) == 0 {
		return domain.Product{}, app.ErrNotFound
	}
	return products[0], nil
}

func (s *Store) Search(ctx context.Context, key string, limit int) ([]domain.Product, error) {
	pattern := "%" + escapeLike(key) + "%"
	return s.query(ctx, `SELECT `+productColumns+` FROM products p WHERE p.search_name LIKE ? ESCAPE '\' AND p.discontinued = 0
		ORDER BY p.sort_name LIMIT ?`, pattern, limit)
}

func (s *Store) QuickKeys(ctx context.Context, limit int) ([]domain.Product, error) {
	return s.query(ctx, `SELECT `+productColumns+` FROM products p WHERE p.quick_key = 1 AND p.discontinued = 0 ORDER BY p.sort_name LIMIT ?`, limit)
}

// Upsert adds or updates the products. The product's headquarters price schedule is
// replaced with the given one; the store prices (exceptions) are kept. A product coming from the
// initial file (OriginSeed) does not change a product that came from headquarters.
func (s *Store) Upsert(ctx context.Context, products []domain.Product, origin domain.Origin) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range products {
		if origin == domain.OriginSeed {
			var existing string
			err := tx.QueryRowContext(ctx, `SELECT origin FROM products WHERE id = ?`, p.ID).Scan(&existing)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if existing == string(domain.OriginHeadquarters) {
				continue
			}
		}
		options := ""
		if len(p.Options) > 0 {
			b, err := json.Marshal(p.Options)
			if err != nil {
				return err
			}
			options = string(b)
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO products (id, name, search_name, sort_name, unit, tax_rate, quick_key, origin, discontinued,
				category, menu_order, station, option_groups, image_sha256, image_type)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET
				name = excluded.name, search_name = excluded.search_name, sort_name = excluded.sort_name, unit = excluded.unit,
				tax_rate = excluded.tax_rate, quick_key = excluded.quick_key, origin = excluded.origin, discontinued = excluded.discontinued,
				category = excluded.category, menu_order = excluded.menu_order, station = excluded.station,
				option_groups = excluded.option_groups, image_sha256 = excluded.image_sha256, image_type = excluded.image_type`,
			p.ID, p.Name, text.Fold(p.Name), text.SortKey(p.Name), p.Unit, p.TaxRate, p.QuickKey, string(origin), p.Discontinued,
			p.Category, p.MenuOrder, p.Station, options, p.ImageSHA256, p.ImageType)
		if err != nil {
			return fmt.Errorf("ürün yazılamadı (%s): %w", p.ID, err)
		}
		// The initial file's image is written together with the product.
		if len(p.ImageData) > 0 {
			img := domain.Image{ProductID: p.ID, SHA256: p.ImageSHA256, ContentType: p.ImageType, Data: p.ImageData}
			if err := saveImage(ctx, tx, img); err != nil {
				return fmt.Errorf("görsel yazılamadı (%s): %w", p.ID, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM prices WHERE product_id = ? AND source = 'headquarters'`, p.ID); err != nil {
			return err
		}
		for _, pr := range p.Prices {
			if pr.Source != domain.HeadquartersPrice {
				return fmt.Errorf("ürün yazılamadı (%s): mağaza fiyatı ürünle birlikte yazılmaz", p.ID)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO prices (product_id, source, from_ms, amount, clear) VALUES (?, 'headquarters', ?, ?, 0)`,
				p.ID, fromMillis(pr.From), pr.Amount); err != nil {
				return fmt.Errorf("fiyat yazılamadı (%s): %w", p.ID, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM barcodes WHERE product_id = ?`, p.ID); err != nil {
			return err
		}
		for _, b := range p.Barcodes {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO barcodes (barcode, product_id) VALUES (?, ?)
				ON CONFLICT (barcode) DO UPDATE SET product_id = excluded.product_id`, b, p.ID)
			if err != nil {
				return fmt.Errorf("barkod yazılamadı (%s): %w", b, err)
			}
		}
	}
	return tx.Commit()
}

// SetStorePrices replaces the product's store price schedule with the given one.
func (s *Store) SetStorePrices(ctx context.Context, productID string, prices []domain.Price) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM products WHERE id = ?)`, productID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return app.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM prices WHERE product_id = ? AND source = 'store'`, productID); err != nil {
		return err
	}
	for _, pr := range prices {
		if _, err := tx.ExecContext(ctx, `INSERT INTO prices (product_id, source, from_ms, amount, clear) VALUES (?, 'store', ?, ?, ?)`,
			productID, fromMillis(pr.From), pr.Amount, pr.Clear); err != nil {
			return fmt.Errorf("mağaza fiyatı yazılamadı (%s): %w", productID, err)
		}
	}
	return tx.Commit()
}

// query reads the products and their barcodes.
func (s *Store) query(ctx context.Context, query string, args ...any) ([]domain.Product, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ürünler okunamadı: %w", err)
	}
	var products []domain.Product
	for rows.Next() {
		var p domain.Product
		var options string
		if err := rows.Scan(&p.ID, &p.Name, &p.Unit, &p.TaxRate, &p.QuickKey, &p.Category, &p.MenuOrder, &p.Station, &options,
			&p.ImageSHA256, &p.ImageType); err != nil {
			rows.Close()
			return nil, err
		}
		if options != "" {
			if err := json.Unmarshal([]byte(options), &p.Options); err != nil {
				rows.Close()
				return nil, fmt.Errorf("opsiyonlar okunamadı (%s): %w", p.ID, err)
			}
		}
		products = append(products, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range products {
		if products[i].Barcodes, err = s.barcodes(ctx, products[i].ID); err != nil {
			return nil, err
		}
		if products[i].Prices, err = s.prices(ctx, products[i].ID); err != nil {
			return nil, err
		}
	}
	return products, nil
}

// prices returns the product's price schedule sorted by effective moment.
func (s *Store) prices(ctx context.Context, productID string) ([]domain.Price, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source, from_ms, amount, clear FROM prices WHERE product_id = ? ORDER BY from_ms, source`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Price
	for rows.Next() {
		var p domain.Price
		var from int64
		if err := rows.Scan(&p.Source, &from, &p.Amount, &p.Clear); err != nil {
			return nil, err
		}
		if from != 0 {
			p.From = time.UnixMilli(from).UTC()
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// fromMillis turns the effective moment into the stored form; the zero moment (from the beginning) is 0.
func fromMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func (s *Store) barcodes(ctx context.Context, productID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT barcode FROM barcodes WHERE product_id = ? ORDER BY barcode`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// escapeLike escapes the special characters in a LIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
