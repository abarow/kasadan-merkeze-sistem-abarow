// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package postgres is the app.Repository adapter that keeps the payment records in PostgreSQL.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"abarow.com/pkg/pgdb"
	"abarow.com/pkg/sqldialect"
	"abarow.com/services/core/payment/internal/app"
	"abarow.com/services/core/payment/internal/domain"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store implements app.Repository with PostgreSQL.
type Store struct {
	db *sql.DB
}

var _ app.Repository = (*Store)(nil)

// Open connects to the register database, in the service's own schema, and applies the migrations.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := pgdb.Open(ctx, dsn, "payment", migrations)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database can be reached.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Get returns the payment record of a sale.
func (s *Store) Get(ctx context.Context, saleID string) (*domain.Charge, error) {
	var payments string
	c := &domain.Charge{SaleID: saleID}
	err := s.db.QueryRowContext(ctx, `SELECT payments, charged_at FROM charges WHERE sale_id = $1`, saleID).
		Scan(&payments, sqldialect.ScanTime(&c.At))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, app.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the payment record: %w", err)
	}
	if err := json.Unmarshal([]byte(payments), &c.Payments); err != nil {
		return nil, fmt.Errorf("could not decode the payment record: %w", err)
	}
	return c, nil
}

// Update replaces the payments of a recorded sale.
func (s *Store) Update(ctx context.Context, c domain.Charge) error {
	payments, err := json.Marshal(c.Payments)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE charges SET payments = $1 WHERE sale_id = $2`, string(payments), c.SaleID)
	if err != nil {
		return fmt.Errorf("could not update the payment record: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return errors.Join(err, app.ErrNotFound)
	}
	return nil
}

// Save writes the payment record of a sale; a second record for the same sale is an error.
func (s *Store) Save(ctx context.Context, c domain.Charge) error {
	payments, err := json.Marshal(c.Payments)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO charges (sale_id, payments, charged_at) VALUES ($1, $2, $3)`,
		c.SaleID, string(payments), c.At.UTC().Truncate(time.Microsecond))
	if err != nil {
		return fmt.Errorf("could not write the payment record: %w", err)
	}
	return nil
}
