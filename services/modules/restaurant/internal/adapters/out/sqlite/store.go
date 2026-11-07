// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package sqlite is the app.Store adapter that keeps the floor, tables and checks in SQLite
// (a single-register restaurant: restaurant.db at the register).
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
	"abarow.com/services/modules/restaurant/internal/app"
	"abarow.com/services/modules/restaurant/internal/domain"
)

//go:embed migrations/*.sql
var migrations embed.FS

// timeLayout is the format of times in the database: fixed-width UTC.
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

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Floor is the floor and the tables (empty if not defined yet).
func (s *Store) Floor(ctx context.Context) ([]domain.Area, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT areas FROM floor WHERE id = 1`).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return []domain.Area{}, nil
	}
	if err != nil {
		return nil, err
	}
	var areas []domain.Area
	if err := json.Unmarshal([]byte(data), &areas); err != nil {
		return nil, fmt.Errorf("salonlar okunamadı: %w", err)
	}
	return areas, nil
}

// SaveFloor writes the floor and the tables.
func (s *Store) SaveFloor(ctx context.Context, areas []domain.Area) error {
	data, err := json.Marshal(areas)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO floor (id, areas) VALUES (1, ?) ON CONFLICT (id) DO UPDATE SET areas = excluded.areas`, string(data))
	return err
}

// OpenOrders are the open checks (in the order they were opened).
func (s *Store) OpenOrders(ctx context.Context) ([]*domain.Order, error) {
	return s.query(ctx, `SELECT data FROM orders WHERE closed = 0 ORDER BY opened_at, id`)
}

// Get, adisyondur.
func (s *Store) Get(ctx context.Context, id string) (*domain.Order, error) {
	return s.one(ctx, `SELECT data FROM orders WHERE id = ?`, id)
}

// AtTable is the table's open check.
func (s *Store) AtTable(ctx context.Context, tableID string) (*domain.Order, error) {
	return s.one(ctx, `SELECT data FROM orders WHERE table_id = ? AND closed = 0`, tableID)
}

func (s *Store) one(ctx context.Context, query string, args ...any) (*domain.Order, error) {
	orders, err := s.query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return nil, fmt.Errorf("%w: adisyon", domain.ErrNotFound)
	}
	return orders[0], nil
}

func (s *Store) query(ctx context.Context, query string, args ...any) ([]*domain.Order, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Order
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var o domain.Order
		if err := json.Unmarshal([]byte(data), &o); err != nil {
			return nil, fmt.Errorf("adisyon okunamadı: %w", err)
		}
		out = append(out, &o)
	}
	return out, rows.Err()
}

// Save writes the checks in one transaction.
func (s *Store) Save(ctx context.Context, orders ...*domain.Order) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, o := range orders {
		data, err := json.Marshal(o)
		if err != nil {
			return err
		}
		closedAt := ""
		if o.Closed {
			closedAt = o.ClosedAt.UTC().Format(timeLayout)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO orders (id, table_id, closed, opened_at, closed_at, version, data) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET table_id = excluded.table_id, closed = excluded.closed, closed_at = excluded.closed_at,
				version = excluded.version, data = excluded.data`,
			o.ID, o.TableID, o.Closed, o.OpenedAt.UTC().Format(timeLayout), closedAt, o.Version, string(data))
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed: orders.table_id") {
				return domain.ErrTableBusy
			}
			return err
		}
	}
	return tx.Commit()
}

// DeleteClosedBefore deletes the checks closed before the given time (sales are
// permanent in sales; the check is only a working record).
func (s *Store) DeleteClosedBefore(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM orders WHERE closed = 1 AND closed_at < ?`, before.UTC().Format(timeLayout))
	return err
}
