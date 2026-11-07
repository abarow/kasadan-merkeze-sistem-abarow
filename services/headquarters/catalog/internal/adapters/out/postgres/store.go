// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package postgres is the adapter that keeps headquarters' catalog in PostgreSQL
// (app.Repository, outbox.Store).
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" driver for database/sql
	"github.com/pressly/goose/v3"

	"abarow.com/pkg/outbox"
	"abarow.com/pkg/pgpool"
	"abarow.com/pkg/sqldialect"
	"abarow.com/pkg/text"
	"abarow.com/services/headquarters/catalog/internal/app"
	"abarow.com/services/headquarters/catalog/internal/domain"
)

// outboxTable is the outbox table of the events (its SQL is in pkg/outbox).
var outboxTable = outbox.Table{Name: "catalog_outbox", Dialect: sqldialect.Postgres, StoreID: "headquarters"}

//go:embed migrations/*.sql
var migrations embed.FS

// VersionTable is catalog-admin's migration version table (separate from ingest's and
// reporting's).
const VersionTable = "catalog_goose_db_version"

// ErrMigration means the database schema could not be updated; it does not get better
// by waiting.
var ErrMigration = errors.New("migration uygulanamadı")

// Store keeps the catalog with PostgreSQL.
type Store struct {
	db *sql.DB
}

var (
	_ app.Repository = (*Store)(nil)
	_ outbox.Store   = (*Store)(nil)
)

// Open connects to the database and applies the migrations.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	// The connection allowance is limited (max_connections 20 on the store server).
	pgpool.Limit(db, pgpool.CatalogAdmin)
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
		return nil, fmt.Errorf("%w: %w", ErrMigration, err)
	}
	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

const productColumns = `code, name, unit, tax_rate, quick_key, discontinued, updated_by, updated_at, version,
	category, menu_order, station, option_groups, image_sha256, image_type`

// optionsJSON turns the option groups into the form to be stored (NULL if none).
func optionsJSON(p domain.Product) (any, error) {
	if len(p.Options) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(p.Options)
	return string(b), err
}

// Get returns the product.
func (s *Store) Get(ctx context.Context, code string) (domain.Product, error) {
	products, err := s.query(ctx, `SELECT `+productColumns+` FROM catalog_products WHERE code = $1`, code)
	if err != nil {
		return domain.Product{}, err
	}
	if len(products) == 0 {
		return domain.Product{}, domain.ErrNotFound
	}
	return products[0], nil
}

// List returns the products that have the query in their name, code or barcode in Turkish
// alphabet order.
func (s *Store) List(ctx context.Context, query string, limit int) ([]domain.Product, error) {
	q := text.Fold(query)
	return s.query(ctx, `
		SELECT `+productColumns+` FROM catalog_products p
		WHERE $1 = '' OR p.search_name LIKE '%' || $2 || '%' OR lower(p.code) = $1
		   OR EXISTS (SELECT 1 FROM catalog_barcodes b WHERE b.code = p.code AND b.barcode = $3)
		ORDER BY p.name COLLATE "tr-x-icu", p.code LIMIT $4`, q, escapeLike(q), strings.TrimSpace(query), limit)
}

// Create writes the new product with the change record and the outbox message.
func (s *Store) Create(ctx context.Context, p domain.Product, change app.Change, msg outbox.Message) error {
	options, err := optionsJSON(p)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO catalog_products (code, name, search_name, unit, tax_rate, quick_key, discontinued, updated_by, updated_at, version,
				category, menu_order, station, option_groups, image_sha256, image_type)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
			p.Code, p.Name, text.Fold(p.Name), string(p.Unit), int64(p.TaxRate), p.QuickKey, p.Discontinued, p.UpdatedBy, p.UpdatedAt.UTC(), p.Version,
			p.Category, p.MenuOrder, p.Station, options, p.ImageSHA256, p.ImageType)
		if err != nil {
			return err
		}
		return s.writeRest(ctx, tx, p, change, msg)
	})
}

// Update updates the product from version p.Version-1.
func (s *Store) Update(ctx context.Context, p domain.Product, change app.Change, msg outbox.Message) error {
	options, err := optionsJSON(p)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE catalog_products SET name = $2, search_name = $3, unit = $4, tax_rate = $5, quick_key = $6, discontinued = $7,
				updated_by = $8, updated_at = $9, version = $10,
				category = $12, menu_order = $13, station = $14, option_groups = $15, image_sha256 = $16, image_type = $17
			WHERE code = $1 AND version = $11`,
			p.Code, p.Name, text.Fold(p.Name), string(p.Unit), int64(p.TaxRate), p.QuickKey, p.Discontinued, p.UpdatedBy, p.UpdatedAt.UTC(),
			p.Version, p.Version-1, p.Category, p.MenuOrder, p.Station, options, p.ImageSHA256, p.ImageType)
		if err != nil {
			return err
		}
		if err := updated(ctx, tx, res, p.Code); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_barcodes WHERE code = $1`, p.Code); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_prices WHERE code = $1`, p.Code); err != nil {
			return err
		}
		return s.writeRest(ctx, tx, p, change, msg)
	})
}

// updated checks that the versioned update changed one row; domain.ErrNotFound if the
// product does not exist, domain.ErrConflict if someone changed it in the meantime.
func updated(ctx context.Context, tx *sql.Tx, res sql.Result, code string) error {
	n, err := res.RowsAffected()
	if err != nil || n > 0 {
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM catalog_products WHERE code = $1)`, code).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return domain.ErrNotFound
	}
	return domain.ErrConflict
}

// SetImage writes or deletes the product's image (with its digest and type);
// the change record and the messages are in the same transaction.
func (s *Store) SetImage(ctx context.Context, p domain.Product, img domain.Image, change app.Change, msgs []outbox.Message) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE catalog_products SET image_sha256 = $2, image_type = $3, updated_by = $4, updated_at = $5, version = $6
			WHERE code = $1 AND version = $7`,
			p.Code, p.ImageSHA256, p.ImageType, p.UpdatedBy, p.UpdatedAt.UTC(), p.Version, p.Version-1)
		if err != nil {
			return err
		}
		if err := updated(ctx, tx, res, p.Code); err != nil {
			return err
		}
		if len(img.Data) == 0 {
			_, err = tx.ExecContext(ctx, `DELETE FROM catalog_images WHERE code = $1`, p.Code)
		} else {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO catalog_images (code, sha256, content_type, data) VALUES ($1, $2, $3, $4)
				ON CONFLICT (code) DO UPDATE SET sha256 = excluded.sha256, content_type = excluded.content_type, data = excluded.data`,
				p.Code, img.SHA256, img.ContentType, img.Data)
		}
		if err != nil {
			return err
		}
		if err := writeChange(ctx, tx, change); err != nil {
			return err
		}
		for _, m := range msgs {
			if err := writeMessage(ctx, tx, m); err != nil {
				return err
			}
		}
		return nil
	})
}

// Image is the product's image.
func (s *Store) Image(ctx context.Context, code string) (domain.Image, error) {
	img := domain.Image{Code: code}
	err := s.db.QueryRowContext(ctx, `SELECT sha256, content_type, data FROM catalog_images WHERE code = $1`, code).
		Scan(&img.SHA256, &img.ContentType, &img.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Image{}, domain.ErrNotFound
	}
	return img, err
}

func writeChange(ctx context.Context, tx *sql.Tx, change app.Change) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog_changes (code, at, by_user, action, detail) VALUES ($1, $2, $3, $4, $5)`,
		change.Code, change.At.UTC(), change.By, change.Action, change.Detail)
	return err
}

func writeMessage(ctx context.Context, tx *sql.Tx, msg outbox.Message) error {
	return outboxTable.Enqueue(ctx, tx, msg)
}

// writeRest writes the product's barcodes, prices, change record and
// outbox message.
func (s *Store) writeRest(ctx context.Context, tx *sql.Tx, p domain.Product, change app.Change, msg outbox.Message) error {
	for _, b := range p.Barcodes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_barcodes (barcode, code) VALUES ($1, $2)`, b, p.Code); err != nil {
			return fmt.Errorf("%s: %w", b, err)
		}
	}
	for _, pr := range p.Prices {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_prices (code, from_ms, amount) VALUES ($1, $2, $3)`,
			p.Code, fromMillis(pr.From), int64(pr.Amount)); err != nil {
			return err
		}
	}
	if err := writeChange(ctx, tx, change); err != nil {
		return err
	}
	return writeMessage(ctx, tx, msg)
}

// History returns the product's changes from newest to oldest.
func (s *Store) History(ctx context.Context, code string, limit int) ([]app.Change, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT code, at, by_user, action, detail FROM catalog_changes WHERE code = $1 ORDER BY id DESC LIMIT $2`, code, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []app.Change
	for rows.Next() {
		var c app.Change
		if err := rows.Scan(&c.Code, &c.At, &c.By, &c.Action, &c.Detail); err != nil {
			return nil, err
		}
		c.At = c.At.UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// query reads the products with their barcodes and prices.
func (s *Store) query(ctx context.Context, query string, args ...any) ([]domain.Product, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var products []domain.Product
	for rows.Next() {
		var p domain.Product
		var unit string
		var options sql.NullString
		if err := rows.Scan(&p.Code, &p.Name, &unit, &p.TaxRate, &p.QuickKey, &p.Discontinued, &p.UpdatedBy, &p.UpdatedAt, &p.Version,
			&p.Category, &p.MenuOrder, &p.Station, &options, &p.ImageSHA256, &p.ImageType); err != nil {
			rows.Close()
			return nil, err
		}
		if options.Valid {
			if err := json.Unmarshal([]byte(options.String), &p.Options); err != nil {
				rows.Close()
				return nil, fmt.Errorf("%s: opsiyonlar okunamadı: %w", p.Code, err)
			}
		}
		p.Unit, p.UpdatedAt = domain.Unit(unit), p.UpdatedAt.UTC()
		products = append(products, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range products {
		if err := s.details(ctx, &products[i]); err != nil {
			return nil, err
		}
	}
	return products, nil
}

func (s *Store) details(ctx context.Context, p *domain.Product) error {
	rows, err := s.db.QueryContext(ctx, `SELECT barcode FROM catalog_barcodes WHERE code = $1 ORDER BY barcode`, p.Code)
	if err != nil {
		return err
	}
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			rows.Close()
			return err
		}
		p.Barcodes = append(p.Barcodes, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT from_ms, amount FROM catalog_prices WHERE code = $1 ORDER BY from_ms`, p.Code)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var pr domain.Price
		var from int64
		if err := rows.Scan(&from, &pr.Amount); err != nil {
			return err
		}
		if from != 0 {
			pr.From = time.UnixMilli(from).UTC()
		}
		p.Prices = append(p.Prices, pr)
	}
	return rows.Err()
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

// inTx runs fn in a transaction; it turns unique-key violations into
// domain errors.
func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // Commit'ten sonra etkisiz
	if err := fn(tx); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			switch pgErr.ConstraintName {
			case "catalog_products_pkey":
				return domain.ErrExists
			case "catalog_barcodes_pkey":
				return fmt.Errorf("%w: %s", domain.ErrBarcodeTaken, pgErr.Detail)
			}
		}
		return err
	}
	return tx.Commit()
}

func fromMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// escapeLike escapes the special characters in a LIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
