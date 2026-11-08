// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package postgres is the adapter that reads the headquarters reports from PostgreSQL
// (app.Reports) and keeps the screen users (app.Users).
//
// The sales tables (sales, sale_lines, voids, store_links ...) are ingest's:
// ingest's migrations set up the schema, reporting only reads. The migrations of its
// own tables (users, user_stores, user_changes, sessions) are kept in a
// separate version table (reporting_goose_db_version).
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/ledger/ledgerdb"
	"abarow.com/pkg/money"
	"abarow.com/pkg/personnel/personneldb"
	"abarow.com/pkg/pgpool"
	"abarow.com/pkg/report"
	"abarow.com/pkg/sequence/sequencedb"
	"abarow.com/pkg/sqldialect"
	"abarow.com/services/headquarters/reporting/internal/app"
	"abarow.com/services/headquarters/reporting/internal/domain"
)

//go:embed migrations/*.sql
var migrations embed.FS

// VersionTable is reporting's migration version table (separate from
// ingest's).
const VersionTable = "reporting_goose_db_version"

// ErrMigration means the database schema could not be updated; it does not get better
// by waiting.
var ErrMigration = errors.New("migration uygulanamadı")

// Store implements app.Reports and app.Users with PostgreSQL.
type Store struct {
	db *sql.DB
}

var (
	_ app.Reports   = (*Store)(nil)
	_ report.Source = (*Store)(nil)
	_ app.Users     = (*personneldb.Store)(nil)
)

// ContractSearchPath is the search_path of reporting's queries: the data of ingest and
// catalog-admin is read through their contract views (ingest_v1, catalog_v1; the views
// have the tables' names, docs/REVIEW.md B1), reporting's own tables are in public. An
// older ingest without the views falls through to its tables in public.
const ContractSearchPath = "ingest_v1, catalog_v1, public"

// Open connects to the database and applies reporting's migrations. dsn is a
// connection string (postgres://... or key=value).
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	// The migrations run with the default search_path: reporting's tables are created
	// in public, never in an owner's contract schema.
	mig := stdlib.OpenDB(*cfg)
	defer mig.Close()
	if err := mig.PingContext(ctx); err != nil {
		return nil, err
	}
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, mig, fsys, goose.WithTableName(VersionTable))
	if err == nil {
		_, err = p.Up(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMigration, err)
	}
	q := cfg.Copy()
	q.RuntimeParams["search_path"] = ContractSearchPath
	db := stdlib.OpenDB(*q)
	// The connection allowance is limited (max_connections 20 on the store server).
	pgpool.Limit(db, pgpool.Report)
	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Stores returns the store states ingest writes and the sales in the range
// [dayStart, dayEnd).
func (s *Store) Stores(ctx context.Context, dayStart, dayEnd time.Time) ([]domain.StoreOverview, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.store_id, l.name, l.link, l.last_seen, l.pending_events, l.error, l.checked_at,
		       st.last_event_at, coalesce(t.receipts, 0), coalesce(t.total, 0)
		FROM store_links l
		LEFT JOIN store_stats st ON st.store_id = l.store_id
		LEFT JOIN (
			SELECT store_id, count(*) AS receipts, sum(total)::bigint AS total
			FROM sales WHERE paid_at >= $1 AND paid_at < $2 GROUP BY store_id
		) t ON t.store_id = l.store_id
		ORDER BY l.store_id`, dayStart.UTC(), dayEnd.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.StoreOverview
	for rows.Next() {
		var o domain.StoreOverview
		var link string
		var lastSeen, lastEvent sql.NullTime
		if err := rows.Scan(&o.ID, &o.Name, &link, &lastSeen, &o.Pending, &o.Error, &o.CheckedAt,
			&lastEvent, &o.TodayReceipts, &o.TodayTotal); err != nil {
			return nil, err
		}
		o.Link = domain.Link(link)
		o.LastSeen, o.LastEventAt, o.CheckedAt = utc(lastSeen), utc(lastEvent), o.CheckedAt.UTC()
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// The sequence number gaps in the events the registers sent (ingest writes them).
	gaps, err := sequencedb.Tracker{DB: s.db, Dialect: sqldialect.Postgres}.Gaps(ctx, "", 0)
	if err != nil {
		return nil, err
	}
	for _, g := range gaps {
		for i := range out {
			if out[i].ID == g.Store {
				out[i].MissingEvents = append(out[i].MissingEvents, g)
			}
		}
	}
	return out, nil
}

// Report computes the period's report in a single, consistent read (the same snapshot)
// (report.Source). The figures are from the sales ledger's report (pkg/ledger: the same
// calculation as the store server's report); the store and cashier names are added from
// headquarters' tables.
func (s *Store) Report(ctx context.Context, f report.Filter) (report.Result, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return report.Result{}, err
	}
	defer tx.Rollback() // salt okuma

	r, err := ledgerdb.Postgres.Report(ctx, tx, ledger.Filter{Start: f.Start, End: f.End, Stores: f.Stores, Location: f.Location,
		MaxProducts: f.MaxProducts})
	if err != nil {
		return report.Result{}, err
	}
	// The store names are from stores.json (ingest's store_links table), the cashier
	// names from the cashiers the stores manage on the store server (the cashiers
	// table).
	stores := map[string]string{}
	cashiers := map[[2]string]string{}
	err = each(ctx, tx, `SELECT store_id, name FROM store_links`, func(rows *sql.Rows) error {
		var id, name string
		err := rows.Scan(&id, &name)
		stores[id] = name
		return err
	})
	if err == nil {
		err = each(ctx, tx, `SELECT store_id, cashier_id, name FROM cashiers`, func(rows *sql.Rows) error {
			var store, id, name string
			err := rows.Scan(&store, &id, &name)
			cashiers[[2]string{store, id}] = name
			return err
		})
	}
	if err != nil {
		return report.Result{}, err
	}

	return report.Result{Report: r, StoreNames: stores, CashierNames: cashiers}, nil
}

// StorePrices returns the price schedules the stores reported (ingest's store_prices
// table) with the products' headquarters schedules (catalog-admin's catalog_prices
// table).
func (s *Store) StorePrices(ctx context.Context, productID string) ([]domain.StorePriceRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sp.store_id, coalesce(l.name, sp.store_id), sp.product_id, coalesce(cp.name, sp.product_name), sp.prices::text,
		       sp.updated_by, sp.updated_at
		FROM store_prices sp
		LEFT JOIN store_links l ON l.store_id = sp.store_id
		LEFT JOIN catalog_products cp ON cp.code = sp.product_id
		WHERE $1 = '' OR sp.product_id = $1`, productID)
	if err != nil {
		return nil, err
	}
	var out []domain.StorePriceRow
	var codes []string
	for rows.Next() {
		var r domain.StorePriceRow
		var prices string
		if err := rows.Scan(&r.StoreID, &r.StoreName, &r.ProductID, &r.ProductName, &prices, &r.UpdatedBy, &r.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		var points []struct {
			Amount int64      `json:"amount"`
			From   *time.Time `json:"from"`
			Clear  bool       `json:"clear"`
		}
		if err := json.Unmarshal([]byte(prices), &points); err != nil {
			rows.Close()
			return nil, err
		}
		for _, p := range points {
			pt := domain.PricePoint{Amount: money.Money(p.Amount), Clear: p.Clear}
			if p.From != nil {
				pt.From = p.From.UTC()
			}
			r.StorePrices = append(r.StorePrices, pt)
		}
		r.UpdatedAt = r.UpdatedAt.UTC()
		out = append(out, r)
		codes = append(codes, r.ProductID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	headquarters := map[string][]domain.PricePoint{}
	prows, err := s.db.QueryContext(ctx, `SELECT code, from_ms, amount FROM catalog_prices WHERE code = ANY($1) ORDER BY code, from_ms`, codes)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	for prows.Next() {
		var code string
		var from, amount int64
		if err := prows.Scan(&code, &from, &amount); err != nil {
			return nil, err
		}
		pt := domain.PricePoint{Amount: money.Money(amount)}
		if from != 0 {
			pt.From = time.UnixMilli(from).UTC()
		}
		headquarters[code] = append(headquarters[code], pt)
	}
	if err := prows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].HeadquartersPrices = headquarters[out[i].ProductID]
	}
	return out, nil
}

func each(ctx context.Context, tx *sql.Tx, query string, fn func(*sql.Rows) error) error {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// People is the store of the users and sessions (pkg/personnel/personneldb); the same
// SQL as the store server's cashiers. There is no propagation at headquarters.
func (s *Store) People() *personneldb.Store {
	return &personneldb.Store{DB: s.db, Dialect: sqldialect.Postgres}
}

func utc(t sql.NullTime) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}
