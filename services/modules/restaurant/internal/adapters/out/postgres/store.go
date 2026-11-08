// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package postgres is the app.Store adapter that keeps the floor, tables and checks in
// PostgreSQL (a single-register restaurant: the register's own database).
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"abarow.com/pkg/pgdb"
	"abarow.com/services/modules/restaurant/internal/app"
	"abarow.com/services/modules/restaurant/internal/domain"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store implements app.Store with PostgreSQL.
type Store struct {
	db *sql.DB
}

var _ app.Store = (*Store)(nil)

// Open opens the database and applies the migrations.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := pgdb.Open(ctx, dsn, "restaurant", migrations)
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
		return nil, fmt.Errorf("could not read the floors: %w", err)
	}
	return areas, nil
}

// SaveFloor writes the floor and the tables.
func (s *Store) SaveFloor(ctx context.Context, areas []domain.Area) error {
	data, err := json.Marshal(areas)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO floor (id, areas) VALUES (1, $1) ON CONFLICT (id) DO UPDATE SET areas = excluded.areas`, string(data))
	return err
}

// OpenOrders are the open checks (in the order they were opened).
func (s *Store) OpenOrders(ctx context.Context) ([]*domain.Order, error) {
	return s.query(ctx, `SELECT data FROM orders WHERE NOT closed ORDER BY opened_at, id`)
}

// Get is the check with the given id.
func (s *Store) Get(ctx context.Context, id string) (*domain.Order, error) {
	return s.one(ctx, `SELECT data FROM orders WHERE id = $1`, id)
}

// AtTable is the table's open check.
func (s *Store) AtTable(ctx context.Context, tableID string) (*domain.Order, error) {
	return s.one(ctx, `SELECT data FROM orders WHERE table_id = $1 AND NOT closed`, tableID)
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
			return nil, fmt.Errorf("could not read the check: %w", err)
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
		var closedAt any
		if o.Closed {
			closedAt = o.ClosedAt.UTC()
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO orders (id, table_id, closed, opened_at, closed_at, version, data) VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (id) DO UPDATE SET table_id = excluded.table_id, closed = excluded.closed, closed_at = excluded.closed_at,
				version = excluded.version, data = excluded.data`,
			o.ID, o.TableID, o.Closed, o.OpenedAt.UTC(), closedAt, o.Version, string(data))
		if err != nil {
			if strings.Contains(err.Error(), "orders_open_table") {
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
	_, err := s.db.ExecContext(ctx, `DELETE FROM orders WHERE closed AND closed_at < $1`, before.UTC())
	return err
}
