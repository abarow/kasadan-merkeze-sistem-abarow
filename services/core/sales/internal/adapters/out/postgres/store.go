// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package postgres is the app.Repository adapter that keeps the sales in PostgreSQL.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	"abarow.com/pkg/outbox"
	"abarow.com/pkg/pgdb"
	"abarow.com/pkg/sqldialect"
	"abarow.com/services/core/sales/internal/app"
	"abarow.com/services/core/sales/internal/domain"
)

// outboxTable is the outbox table of the events (its SQL is in pkg/outbox).
var outboxTable = outbox.Table{Name: "outbox", Dialect: sqldialect.Postgres, Store: true, Register: true}

//go:embed migrations/*.sql
var migrations embed.FS

// Store implements app.Repository with PostgreSQL.
type Store struct {
	db *sql.DB
}

var _ app.Repository = (*Store)(nil)

// Open opens the database and applies the migrations.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := pgdb.Open(ctx, dsn, "sales", migrations)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Get returns the sale; app.ErrNotFound if none.
func (s *Store) Get(ctx context.Context, id domain.SaleID) (*domain.Sale, error) {
	return getSale(ctx, s.db, id)
}

// InTx runs fn in a single transaction.
func (s *Store) ByStatus(ctx context.Context, status domain.Status, limit int) ([]*domain.Sale, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM sales WHERE status = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("satışlar okunamadı: %w", err)
	}
	var ids []domain.SaleID
	for rows.Next() {
		var id domain.SaleID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sales := make([]*domain.Sale, 0, len(ids))
	for _, id := range ids {
		sale, err := getSale(ctx, s.db, id)
		if err != nil {
			return nil, err
		}
		sales = append(sales, sale)
	}
	return sales, nil
}

func (s *Store) InTx(ctx context.Context, fn func(app.Tx) error) error {
	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = sqlTx.Rollback()
			panic(p)
		}
	}()
	if err := fn(&tx{tx: sqlTx}); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	return sqlTx.Commit()
}

type tx struct {
	tx *sql.Tx
}

func (t *tx) Get(ctx context.Context, id domain.SaleID) (*domain.Sale, error) {
	return getSale(ctx, t.tx, id)
}

func (t *tx) Save(ctx context.Context, sale *domain.Sale) error {
	snap := sale.Snapshot()
	_, err := t.tx.ExecContext(ctx, `
		INSERT INTO sales (id, register_id, cashier_id, status, receipt_no, fiscal_no, created_at, paid_at, voided_at, refunded)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			status = excluded.status,
			receipt_no = excluded.receipt_no,
			fiscal_no = excluded.fiscal_no,
			paid_at = excluded.paid_at,
			voided_at = excluded.voided_at,
			refunded = excluded.refunded`,
		snap.ID, snap.RegisterID, snap.CashierID, snap.Status, nullInt(snap.ReceiptNo), nullString(snap.FiscalNo),
		formatTime(snap.CreatedAt), nullTime(snap.PaidAt), nullTime(snap.VoidedAt), snap.Refunded)
	if err != nil {
		return fmt.Errorf("satış yazılamadı: %w", err)
	}

	// The lines and payments are rewritten at every save; since there are few of them in
	// a sale this is fast enough.
	if _, err := t.tx.ExecContext(ctx, `DELETE FROM sale_lines WHERE sale_id = $1`, snap.ID); err != nil {
		return err
	}
	for i, l := range snap.Lines {
		_, err := t.tx.ExecContext(ctx, `
			INSERT INTO sale_lines (sale_id, line_no, product_id, barcode, name, qty, unit_price, tax_rate)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			snap.ID, i+1, l.ProductID, l.Barcode, l.Name, l.Qty, l.UnitPrice, l.TaxRate)
		if err != nil {
			return fmt.Errorf("satış satırı yazılamadı: %w", err)
		}
	}
	if _, err := t.tx.ExecContext(ctx, `DELETE FROM sale_payments WHERE sale_id = $1`, snap.ID); err != nil {
		return err
	}
	for i, p := range snap.Payments {
		_, err := t.tx.ExecContext(ctx, `
			INSERT INTO sale_payments (sale_id, payment_no, method, amount, ref, collect) VALUES ($1, $2, $3, $4, $5, $6)`,
			snap.ID, i+1, p.Method, p.Amount, p.Ref, p.Collect)
		if err != nil {
			return fmt.Errorf("ödeme yazılamadı: %w", err)
		}
	}
	return nil
}

func (t *tx) NextReceiptNo(ctx context.Context) (int64, error) {
	var no int64
	err := t.tx.QueryRowContext(ctx,
		`UPDATE receipt_counter SET last_no = last_no + 1 WHERE id = 1 RETURNING last_no`).Scan(&no)
	if err != nil {
		return 0, fmt.Errorf("fiş numarası ayrılamadı: %w", err)
	}
	return no, nil
}

func (t *tx) AppendOutbox(ctx context.Context, msgs []app.OutboxMessage) error {
	return outboxTable.Enqueue(ctx, t.tx, msgs...)
}

var _ app.Outbox = (*Store)(nil)

// Pending returns the unpublished outbox messages in the order they were written.
func (s *Store) Pending(ctx context.Context, limit int) ([]app.PendingMessage, error) {
	return outboxTable.Pending(ctx, s.db, limit)
}

// MarkPublished marks the outbox message as published.
func (s *Store) CountPending(ctx context.Context) (int64, error) {
	return outboxTable.CountPending(ctx, s.db)
}

func (s *Store) MarkPublished(ctx context.Context, seq int64, at time.Time) error {
	return outboxTable.MarkPublished(ctx, s.db, seq, at)
}

// querier is the read methods common to *sql.DB and *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getSale(ctx context.Context, q querier, id domain.SaleID) (*domain.Sale, error) {
	var (
		snap             domain.Snapshot
		receiptNo        sql.NullInt64
		fiscalNo         sql.NullString
		paidAt, voidedAt sql.NullTime
	)
	err := q.QueryRowContext(ctx, `
		SELECT id, register_id, cashier_id, status, receipt_no, fiscal_no, created_at, paid_at, voided_at, refunded
		FROM sales WHERE id = $1`, id).
		Scan(&snap.ID, &snap.RegisterID, &snap.CashierID, &snap.Status, &receiptNo, &fiscalNo, &snap.CreatedAt, &paidAt, &voidedAt, &snap.Refunded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, app.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("satış okunamadı: %w", err)
	}
	snap.ReceiptNo = receiptNo.Int64
	snap.FiscalNo = fiscalNo.String
	snap.CreatedAt = snap.CreatedAt.UTC()
	if paidAt.Valid {
		snap.PaidAt = paidAt.Time.UTC()
	}
	if voidedAt.Valid {
		snap.VoidedAt = voidedAt.Time.UTC()
	}

	if snap.Lines, err = queryLines(ctx, q, id); err != nil {
		return nil, err
	}
	if snap.Payments, err = queryPayments(ctx, q, id); err != nil {
		return nil, err
	}
	return domain.Restore(snap), nil
}

func queryLines(ctx context.Context, q querier, id domain.SaleID) ([]domain.Line, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT product_id, barcode, name, qty, unit_price, tax_rate
		FROM sale_lines WHERE sale_id = $1 ORDER BY line_no`, id)
	if err != nil {
		return nil, fmt.Errorf("satış satırları okunamadı: %w", err)
	}
	defer rows.Close()
	var lines []domain.Line
	for rows.Next() {
		var l domain.Line
		if err := rows.Scan(&l.ProductID, &l.Barcode, &l.Name, &l.Qty, &l.UnitPrice, &l.TaxRate); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

func queryPayments(ctx context.Context, q querier, id domain.SaleID) ([]domain.Payment, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT method, amount, ref, collect FROM sale_payments WHERE sale_id = $1 ORDER BY payment_no`, id)
	if err != nil {
		return nil, fmt.Errorf("ödemeler okunamadı: %w", err)
	}
	defer rows.Close()
	var payments []domain.Payment
	for rows.Next() {
		var p domain.Payment
		if err := rows.Scan(&p.Method, &p.Amount, &p.Ref, &p.Collect); err != nil {
			return nil, err
		}
		payments = append(payments, p)
	}
	return payments, rows.Err()
}

func formatTime(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: formatTime(t), Valid: true}
}

func nullInt(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: n != 0} }

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
