// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package postgres is the registry.Store adapter that keeps the registrations in PostgreSQL.
// The update_registrations table is in the headquarters (or publisher) database.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" driver for database/sql
	"github.com/pressly/goose/v3"

	"abarow.com/pkg/pgdb"
	"abarow.com/pkg/pgpool"
	"abarow.com/services/update/internal/registration"
	"abarow.com/services/update/internal/token"
)

//go:embed migrations/*.sql
var migrations embed.FS

// VersionTable is this service's migration version table (the database is shared with
// other services).
const VersionTable = "update_goose_db_version"

// poolSize is the default limit of the connection pool: at headquarters it is taken from the share
// pgtune sets aside for new services (pkg/pgtune HeadquartersMaxConnections).
const poolSize = 2

// Store, registry.Store'u PostgreSQL ile uygular.
type Store struct{ db *sql.DB }

var _ registration.Store = (*Store)(nil)

// Open connects to the database and applies the migrations.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	pgpool.Limit(db, poolSize)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := pgdb.Migrate(ctx, dsn, fsys, goose.WithTableName(VersionTable)); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the connections.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database is reachable (health check).
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

const columns = `device, public_key, signing_key, fingerprint, tier, level, store_id, register_id, name, status, attempts, created_at, decided_at, decided_by, report`

func scan(row interface{ Scan(...any) error }) (registration.Request, error) {
	var (
		r       registration.Request
		tier    string
		level   string
		status  string
		decided sql.NullTime
		signing []byte
		report  []byte
	)
	if err := row.Scan(&r.Device, &r.Public, &signing, &r.Fingerprint, &tier, &level, &r.Store, &r.Register, &r.Name, &status, &r.Attempts, &r.CreatedAt, &decided, &r.DecidedBy, &report); err != nil {
		return registration.Request{}, err
	}
	r.SigningKey = signing
	r.Tier, r.Level, r.Status = token.Tier(tier), token.Level(level), registration.Status(status)
	r.CreatedAt = r.CreatedAt.UTC()
	if decided.Valid {
		r.DecidedAt = decided.Time.UTC()
	}
	if len(report) > 0 {
		var rep registration.Report
		if err := json.Unmarshal(report, &rep); err == nil {
			r.Report = &rep
		}
	}
	return r, nil
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// Get returns the registration.
func (s *Store) Get(ctx context.Context, device string) (registration.Request, error) {
	r, err := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM update_registrations WHERE device = $1`, device))
	if errors.Is(err, sql.ErrNoRows) {
		return registration.Request{}, registration.ErrNotFound
	}
	return r, err
}

// Create adds a new registration; if the device is already registered it returns registry.ErrExists.
func (s *Store) Create(ctx context.Context, r registration.Request) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO update_registrations (`+columns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,NULL)`,
		r.Device, []byte(r.Public), nullBytes(r.SigningKey), r.Fingerprint, string(r.Tier), string(r.Level), r.Store, r.Register, r.Name,
		string(r.Status), r.Attempts, r.CreatedAt, nullTime(r.DecidedAt), r.DecidedBy)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" { // unique_violation
		return registration.ErrExists
	}
	return err
}

// Update changes the registration only if its status and attempt count are the same as old.
func (s *Store) Update(ctx context.Context, old, next registration.Request) error {
	res, err := s.db.ExecContext(ctx, `UPDATE update_registrations SET status = $1, attempts = $2, decided_at = $3, decided_by = $4
		WHERE device = $5 AND status = $6 AND attempts = $7`,
		string(next.Status), next.Attempts, nullTime(next.DecidedAt), next.DecidedBy, old.Device, string(old.Status), old.Attempts)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		if _, gerr := s.Get(ctx, old.Device); errors.Is(gerr, registration.ErrNotFound) {
			return registration.ErrNotFound
		}
		return registration.ErrConflict
	}
	return nil
}

// SaveReport, birimin son durum bildirimini yazar.
func (s *Store) SaveReport(ctx context.Context, device string, r registration.Report) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE update_registrations SET report = $1 WHERE device = $2`, raw, device)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return registration.ErrNotFound
	}
	return nil
}

// List returns the registrations in the given status in creation order.
func (s *Store) List(ctx context.Context, status registration.Status) ([]registration.Request, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM update_registrations WHERE status = $1 ORDER BY created_at, device`, string(status))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []registration.Request
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
