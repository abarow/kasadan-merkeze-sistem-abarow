// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

var _ app.Cashiers = (*Store)(nil)

func (s *Store) Cashier(ctx context.Context, id string) (*domain.Cashier, error) {
	var c domain.Cashier
	var locked sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, role, pin_hash, failed_attempts, locked_until, disabled, origin FROM cashiers WHERE id = $1`, id).
		Scan(&c.ID, &c.Name, &c.Role, &c.PINHash, &c.FailedAttempts, &locked, &c.Disabled, &c.Origin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrUnknownCashier
	}
	if err != nil {
		return nil, err
	}
	if locked.Valid {
		if c.LockedUntil, err = parseTime(locked.String); err != nil {
			return nil, err
		}
	}
	return &c, nil
}

func (s *Store) Cashiers(ctx context.Context) ([]domain.Cashier, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, role FROM cashiers WHERE NOT disabled ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Cashier
	for rows.Next() {
		var c domain.Cashier
		if err := rows.Scan(&c.ID, &c.Name, &c.Role); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) SaveCashier(ctx context.Context, c domain.Cashier) error {
	origin := c.Origin
	if origin == "" {
		origin = domain.FromSeed
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cashiers (id, name, role, pin_hash, failed_attempts, locked_until, disabled, origin) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET
			name = excluded.name, role = excluded.role, pin_hash = excluded.pin_hash,
			failed_attempts = excluded.failed_attempts, locked_until = excluded.locked_until,
			disabled = excluded.disabled, origin = excluded.origin`,
		c.ID, c.Name, c.Role, c.PINHash, c.FailedAttempts, lockedUntil(c), c.Disabled, origin)
	return err
}

func (s *Store) SaveAttempts(ctx context.Context, c domain.Cashier) error {
	_, err := s.db.ExecContext(ctx, `UPDATE cashiers SET failed_attempts = $1, locked_until = $2 WHERE id = $3`,
		c.FailedAttempts, lockedUntil(c), c.ID)
	return err
}

func lockedUntil(c domain.Cashier) sql.NullTime {
	if c.LockedUntil.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: formatTime(c.LockedUntil), Valid: true}
}

func (s *Store) DeleteCashierSessions(ctx context.Context, cashierID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE cashier_id = $1`, cashierID)
	return err
}

func (s *Store) SaveSession(ctx context.Context, session domain.Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, cashier_id, expires_at) VALUES ($1, $2, $3)`,
		session.TokenHash, session.CashierID, formatTime(session.ExpiresAt))
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= $1`, formatTime(now))
	return err
}

func (s *Store) Session(ctx context.Context, tokenHash string) (*domain.Session, error) {
	session := domain.Session{TokenHash: tokenHash}
	var expires string
	err := s.db.QueryRowContext(ctx, `SELECT cashier_id, expires_at FROM sessions WHERE token_hash = $1`, tokenHash).
		Scan(&session.CashierID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrInvalidSession
	}
	if err != nil {
		return nil, err
	}
	if session.ExpiresAt, err = parseTime(expires); err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *Store) SaveApproval(ctx context.Context, a domain.Approval) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO approvals (token_hash, manager_id, action, requested_by, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		a.TokenHash, a.ManagerID, a.Action, a.RequestedBy, formatTime(a.ExpiresAt))
	return err
}

func (s *Store) UseApproval(ctx context.Context, tokenHash string, action domain.Action, now time.Time) (string, error) {
	var managerID string
	err := s.db.QueryRowContext(ctx, `
		UPDATE approvals SET used_at = $1
		WHERE token_hash = $2 AND action = $3 AND used_at IS NULL AND expires_at > $4
		RETURNING manager_id`, formatTime(now), tokenHash, action, formatTime(now)).Scan(&managerID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrInvalidApproval
	}
	return managerID, err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}
