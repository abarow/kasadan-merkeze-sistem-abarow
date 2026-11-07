// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package sqlite is the app.Store adapter that keeps the periods and the report records
// in SQLite.
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
	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

//go:embed migrations/*.sql
var migrations embed.FS

// timeLayout is the form of times in the database: fixed-width UTC. Thanks to
// the fixed width text comparison is the same as time order (session expiry and shift
// boundaries are compared in SQL).
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// Store, app.Store'u SQLite ile uygular.
type Store struct {
	db *sql.DB
}

var _ app.Store = (*Store)(nil)

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

func (s *Store) SaveCompleted(ctx context.Context, sale domain.CompletedSale) error {
	taxes, err := json.Marshal(sale.Taxes)
	if err != nil {
		return err
	}
	payments, err := json.Marshal(sale.Payments)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		p, err := openPeriod(ctx, tx, sale.At)
		if err != nil {
			return err
		}
		shift, err := shiftAt(ctx, tx, sale.CashierID, sale.At)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO completed_sales (sale_id, z_no, receipt_no, total, tax_total, change_amount, taxes, payments, occurred_at, cashier_id, shift_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (sale_id) DO NOTHING`,
			sale.SaleID, p.ZNo, sale.ReceiptNo, sale.Total, sale.TaxTotal, sale.Change, string(taxes), string(payments), formatTime(sale.At),
			sale.CashierID, shift)
		return err
	})
}

func (s *Store) SaveVoided(ctx context.Context, v domain.VoidedSale) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		p, err := openPeriod(ctx, tx, v.At)
		if err != nil {
			return err
		}
		shift, err := shiftAt(ctx, tx, v.CashierID, v.At)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO voided_sales (sale_id, z_no, total, occurred_at, cashier_id, shift_id) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (sale_id) DO NOTHING`, v.SaleID, p.ZNo, v.Total, formatTime(v.At), v.CashierID, shift)
		return err
	})
}

func (s *Store) OpenPeriod(ctx context.Context, now time.Time) (domain.Period, error) {
	var p domain.Period
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		p, err = openPeriod(ctx, tx, now)
		return err
	})
	return p, err
}

func (s *Store) ClosePeriod(ctx context.Context, now time.Time) (domain.Period, error) {
	var p domain.Period
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if p, err = openPeriod(ctx, tx, now); err != nil {
			return err
		}
		p.ClosedAt = now
		if _, err := tx.ExecContext(ctx, `UPDATE periods SET closed_at = ? WHERE z_no = ?`, formatTime(now), p.ZNo); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO periods (opened_at) VALUES (?)`, formatTime(now))
		return err
	})
	return p, err
}

func (s *Store) Facts(ctx context.Context, zNo int64) ([]domain.CompletedSale, []domain.VoidedSale, error) {
	return s.facts(ctx, "z_no", zNo)
}

// facts returns the sales whose column (z_no or shift_id) equals the given value.
func (s *Store) facts(ctx context.Context, column string, value int64) ([]domain.CompletedSale, []domain.VoidedSale, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sale_id, cashier_id, receipt_no, total, tax_total, change_amount, taxes, payments, occurred_at
		FROM completed_sales WHERE `+column+` = ? ORDER BY receipt_no`, value)
	if err != nil {
		return nil, nil, err
	}
	var completed []domain.CompletedSale
	for rows.Next() {
		var c domain.CompletedSale
		var taxes, payments, at string
		if err := rows.Scan(&c.SaleID, &c.CashierID, &c.ReceiptNo, &c.Total, &c.TaxTotal, &c.Change, &taxes, &payments, &at); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if err := errors.Join(json.Unmarshal([]byte(taxes), &c.Taxes), json.Unmarshal([]byte(payments), &c.Payments)); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("rapor kaydı çözülemedi (%s): %w", c.SaleID, err)
		}
		if c.At, err = parseTime(at); err != nil {
			rows.Close()
			return nil, nil, err
		}
		completed = append(completed, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT sale_id, cashier_id, total, occurred_at FROM voided_sales WHERE `+column+` = ?`, value)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var voided []domain.VoidedSale
	for rows.Next() {
		var v domain.VoidedSale
		var at string
		if err := rows.Scan(&v.SaleID, &v.CashierID, &v.Total, &at); err != nil {
			return nil, nil, err
		}
		if v.At, err = parseTime(at); err != nil {
			return nil, nil, err
		}
		voided = append(voided, v)
	}
	return completed, voided, rows.Err()
}

func (s *Store) Period(ctx context.Context, zNo int64) (domain.Period, error) {
	p := domain.Period{ZNo: zNo}
	var opened string
	var closed sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT opened_at, closed_at FROM periods WHERE z_no = ?`, zNo).Scan(&opened, &closed)
	if errors.Is(err, sql.ErrNoRows) {
		return p, domain.ErrUnknownPeriod
	}
	if err != nil {
		return p, err
	}
	if p.OpenedAt, err = parseTime(opened); err != nil {
		return p, err
	}
	if closed.Valid {
		p.ClosedAt, err = parseTime(closed.String)
	}
	return p, err
}

// openPeriod returns the open period; opens one with now if there is none.
func openPeriod(ctx context.Context, tx *sql.Tx, now time.Time) (domain.Period, error) {
	var p domain.Period
	var opened string
	err := tx.QueryRowContext(ctx, `SELECT z_no, opened_at FROM periods WHERE closed_at IS NULL`).Scan(&p.ZNo, &opened)
	if errors.Is(err, sql.ErrNoRows) {
		res, err := tx.ExecContext(ctx, `INSERT INTO periods (opened_at) VALUES (?)`, formatTime(now))
		if err != nil {
			return p, err
		}
		p.ZNo, err = res.LastInsertId()
		p.OpenedAt = now.UTC()
		return p, err
	}
	if err != nil {
		return p, err
	}
	p.OpenedAt, err = parseTime(opened)
	return p, err
}

func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// parseTime decodes the time in the database. The first versions wrote RFC3339Nano
// (trailing zeros dropped); those records are decoded too.
func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }
