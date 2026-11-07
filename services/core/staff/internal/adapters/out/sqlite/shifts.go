// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"abarow.com/pkg/outbox"
	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

var _ app.Shifts = (*Store)(nil)

func (s *Store) CreateShift(ctx context.Context, sh domain.Shift, announce func(domain.Shift) (outbox.Message, error)) (domain.Shift, error) {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		open, err := currentShift(ctx, tx)
		if err == nil {
			return domain.ShiftOpenError{CashierName: open.CashierName}
		}
		if !errors.Is(err, domain.ErrNoShift) {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO shifts (cashier_id, opened_at, opening_cash) VALUES (?, ?, ?)`,
			sh.CashierID, formatTime(sh.OpenedAt), sh.OpeningCash)
		if err != nil {
			return err
		}
		if sh.ID, err = res.LastInsertId(); err != nil || announce == nil {
			return err
		}
		m, err := announce(sh)
		if err != nil {
			return err
		}
		return appendOutbox(ctx, tx, m)
	})
	return sh, err
}

func (s *Store) CurrentShift(ctx context.Context) (domain.Shift, error) {
	var sh domain.Shift
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		sh, err = currentShift(ctx, tx)
		return err
	})
	return sh, err
}

func (s *Store) CloseShift(ctx context.Context, sh domain.Shift, msg *outbox.Message) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE shifts SET closed_at = ?, counted_cash = ?, closed_by = ? WHERE id = ? AND closed_at IS NULL`,
			formatTime(sh.ClosedAt), sh.CountedCash, sh.ClosedBy, sh.ID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return errors.Join(err, domain.ErrNoShift)
		}
		if msg == nil {
			return nil
		}
		return appendOutbox(ctx, tx, *msg)
	})
}

func (s *Store) ShiftFacts(ctx context.Context, shiftID int64) ([]domain.CompletedSale, []domain.VoidedSale, error) {
	return s.facts(ctx, "shift_id", shiftID)
}

func (s *Store) AddCashMovement(ctx context.Context, m domain.CashMovement) (domain.CashMovement, error) {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var open bool
		err := tx.QueryRowContext(ctx, `SELECT closed_at IS NULL FROM shifts WHERE id = ?`, m.ShiftID).Scan(&open)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !open) {
			return domain.ErrNoShift
		}
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO cash_movements (shift_id, kind, amount, note, cashier_id, at) VALUES (?, ?, ?, ?, ?, ?)`,
			m.ShiftID, m.Kind, m.Amount, m.Note, m.By, formatTime(m.At))
		if err != nil {
			return err
		}
		m.ID, err = res.LastInsertId()
		return err
	})
	return m, err
}

func (s *Store) CashMovements(ctx context.Context, shiftID int64) ([]domain.CashMovement, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, shift_id, kind, amount, note, cashier_id, at FROM cash_movements WHERE shift_id = ? ORDER BY id`, shiftID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CashMovement
	for rows.Next() {
		var m domain.CashMovement
		var at string
		if err := rows.Scan(&m.ID, &m.ShiftID, &m.Kind, &m.Amount, &m.Note, &m.By, &at); err != nil {
			return nil, err
		}
		if m.At, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) AddDrawerOpen(ctx context.Context, d domain.DrawerOpen) (domain.DrawerOpen, error) {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var open bool
		err := tx.QueryRowContext(ctx, `SELECT closed_at IS NULL FROM shifts WHERE id = ?`, d.ShiftID).Scan(&open)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !open) {
			return domain.ErrNoShift
		}
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO drawer_opens (shift_id, opened_by, requested_by, reason, at) VALUES (?, ?, ?, ?, ?)`,
			d.ShiftID, d.By, d.RequestedBy, d.Reason, formatTime(d.At))
		if err != nil {
			return err
		}
		d.ID, err = res.LastInsertId()
		return err
	})
	return d, err
}

func (s *Store) DrawerOpens(ctx context.Context, shiftID int64) ([]domain.DrawerOpen, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, shift_id, opened_by, requested_by, reason, at FROM drawer_opens WHERE shift_id = ? ORDER BY id`, shiftID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.DrawerOpen
	for rows.Next() {
		var d domain.DrawerOpen
		var at string
		if err := rows.Scan(&d.ID, &d.ShiftID, &d.By, &d.RequestedBy, &d.Reason, &at); err != nil {
			return nil, err
		}
		if d.At, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// shiftColumns are the columns of the SELECT that reads the shift with the cashier's name
// (in the same order as scanShift).
const shiftColumns = `s.id, s.cashier_id, c.name, s.opened_at, s.closed_at, s.opening_cash, s.counted_cash, s.closed_by
	FROM shifts s JOIN cashiers c ON c.id = s.cashier_id`

type scanner interface{ Scan(dest ...any) error }

func scanShift(row scanner) (domain.Shift, error) {
	var sh domain.Shift
	var opened string
	var closed sql.NullString
	if err := row.Scan(&sh.ID, &sh.CashierID, &sh.CashierName, &opened, &closed, &sh.OpeningCash, &sh.CountedCash, &sh.ClosedBy); err != nil {
		return sh, err
	}
	var err error
	if sh.OpenedAt, err = parseTime(opened); err != nil {
		return sh, err
	}
	if closed.Valid {
		sh.ClosedAt, err = parseTime(closed.String)
	}
	return sh, err
}

func (s *Store) Shift(ctx context.Context, id int64) (domain.Shift, error) {
	sh, err := scanShift(s.db.QueryRowContext(ctx, `SELECT `+shiftColumns+` WHERE s.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return sh, domain.ErrUnknownShift
	}
	return sh, err
}

func (s *Store) ClosedShifts(ctx context.Context, cashierID string, limit int) ([]domain.Shift, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+shiftColumns+`
		WHERE s.closed_at IS NOT NULL AND (? = '' OR s.cashier_id = ?) ORDER BY s.id DESC LIMIT ?`, cashierID, cashierID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Shift
	for rows.Next() {
		sh, err := scanShift(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// currentShift returns the open shift; domain.ErrNoShift if none.
func currentShift(ctx context.Context, tx *sql.Tx) (domain.Shift, error) {
	sh, err := scanShift(tx.QueryRowContext(ctx, `SELECT `+shiftColumns+` WHERE s.closed_at IS NULL`))
	if errors.Is(err, sql.ErrNoRows) {
		return sh, domain.ErrNoShift
	}
	return sh, err
}

// shiftAt returns the cashier's shift that was open at the moment at; NULL if none (or
// if the cashier is empty). Even if the event comes late the sale goes into the shift it was made in.
func shiftAt(ctx context.Context, tx *sql.Tx, cashierID string, at time.Time) (sql.NullInt64, error) {
	var id sql.NullInt64
	if cashierID == "" {
		return id, nil
	}
	t := formatTime(at)
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM shifts
		WHERE cashier_id = ? AND opened_at <= ? AND (closed_at IS NULL OR closed_at > ?)
		ORDER BY id DESC LIMIT 1`, cashierID, t, t).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return id, nil
	}
	return id, err
}
