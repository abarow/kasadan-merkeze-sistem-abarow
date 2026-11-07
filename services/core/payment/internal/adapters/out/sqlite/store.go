// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package sqlite is the app.Repository adapter that keeps the payment records in SQLite.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"abarow.com/pkg/sqlitedb"
	"abarow.com/services/core/payment/internal/app"
	"abarow.com/services/core/payment/internal/domain"
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
	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Get returns the sale's payment record; app.ErrNotFound if none.
func (s *Store) Get(ctx context.Context, saleID string) (*domain.Charge, error) {
	var payments, at string
	err := s.db.QueryRowContext(ctx, `SELECT payments, charged_at FROM charges WHERE sale_id = ?`, saleID).Scan(&payments, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, app.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("ödeme kaydı okunamadı: %w", err)
	}
	c := &domain.Charge{SaleID: saleID}
	if err := json.Unmarshal([]byte(payments), &c.Payments); err != nil {
		return nil, fmt.Errorf("ödeme kaydı çözülemedi: %w", err)
	}
	if c.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
		return nil, fmt.Errorf("ödeme zamanı çözülemedi: %w", err)
	}
	return c, nil
}

// Save writes the payment record. A second record for the same sale is rejected.
func (s *Store) Update(ctx context.Context, c domain.Charge) error {
	payments, err := json.Marshal(c.Payments)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE charges SET payments = ? WHERE sale_id = ?`, string(payments), c.SaleID)
	if err != nil {
		return fmt.Errorf("ödeme kaydı güncellenemedi: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return errors.Join(err, app.ErrNotFound)
	}
	return nil
}

func (s *Store) Save(ctx context.Context, c domain.Charge) error {
	payments, err := json.Marshal(c.Payments)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO charges (sale_id, payments, charged_at) VALUES (?, ?, ?)`,
		c.SaleID, string(payments), c.At.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("ödeme kaydı yazılamadı: %w", err)
	}
	return nil
}
