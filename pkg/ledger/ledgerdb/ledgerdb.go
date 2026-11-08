// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package ledgerdb is the database side of the sales ledger: the code that writes the events to the
// tables and the report's queries are written once; the store server
// (SQLite) and headquarters (PostgreSQL) use the same. The tables are the same in every tier
// (the same columns, including the scope columns); the services' migrations set up the schema
// (see testdata/*.sql). Dialect differences (time format, parameter marker, a few
// function names) are in Dialect.
package ledgerdb

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/money"
	"abarow.com/pkg/sqldialect"
)

// Dialect is the database's SQL dialect.
type Dialect struct{ sqldialect.Dialect }

var (
	// SQLite: times are fixed-width UTC text.
	SQLite = Dialect{sqldialect.SQLite}
	// PostgreSQL: zamanlar TIMESTAMPTZ.
	Postgres = Dialect{sqldialect.Postgres}
)

// DB is a *sql.DB or a *sql.Tx.
type DB = sqldialect.DB

// ScanTime reads a time column (text in SQLite, time in PostgreSQL);
// NULL is the zero time.
func ScanTime(t *time.Time) sql.Scanner { return sqldialect.ScanTime(t) }

// bucket is the time's 15-minute slice number (Unix time / 900).
func (d Dialect) bucket(col string) string {
	n := strconv.Itoa(int(ledger.Bucket / time.Second))
	if d.Postgres() {
		return "CAST(floor(extract(epoch FROM " + col + ") / " + n + ") AS BIGINT)"
	}
	return "CAST(strftime('%s', " + col + ") AS INTEGER) / " + n
}

// noSlice is the slice number of a row that is not grouped by time.
const noSlice = -1

// salesQuery is the report's sales query and its arguments. SQLite has no GROUPING SETS and
// only the tests run the report on it; there the same rows come from a UNION ALL.
func (d Dialect) salesQuery(where string, args []any) (string, []any) {
	bucket := d.bucket("s.paid_at")
	sums := `count(*), CAST(sum(s.total) AS BIGINT), CAST(sum(s.tax_total) AS BIGINT), CAST(sum(s.change_amount) AS BIGINT)`
	if d.Postgres() {
		return `SELECT COALESCE(s.store_id, ''), COALESCE(s.register_id, ''), COALESCE(s.cashier_id, ''),
		       COALESCE(` + bucket + `, ` + strconv.Itoa(noSlice) + `), ` + sums + `
		FROM sales s WHERE ` + where + `
		GROUP BY GROUPING SETS ((s.store_id, s.register_id, s.cashier_id), (` + bucket + `))`, args
	}
	return `SELECT s.store_id, s.register_id, s.cashier_id, ` + strconv.Itoa(noSlice) + `, ` + sums + `
		FROM sales s WHERE ` + where + ` GROUP BY s.store_id, s.register_id, s.cashier_id
		UNION ALL
		SELECT '', '', '', ` + bucket + `, ` + sums + `
		FROM sales s WHERE ` + where + ` GROUP BY 4`, append(append([]any{}, args...), args...)
}

// binary compares text byte by byte (the same as the ordering in Go).
func (d Dialect) binary(expr string) string {
	if d.Postgres() {
		return expr + ` COLLATE "C"`
	}
	return expr
}

// MaxLineNameBytes is the longest product name the ledger keeps in a sale line. The report
// index of sale_lines carries the name (migration 00002), and PostgreSQL does not accept an index
// entry larger than a third of a page (about 2.7 KB): a longer name would make the sale
// impossible to record. The register's receipt keeps the full name; only the ledger's copy is cut.
const MaxLineNameBytes = 512

// LineName cuts name to MaxLineNameBytes without splitting a character.
func LineName(name string) string {
	if len(name) <= MaxLineNameBytes {
		return name
	}
	cut := MaxLineNameBytes
	for cut > 0 && !utf8.RuneStart(name[cut]) {
		cut--
	}
	return name[:cut]
}

// SaveSale writes the sale with its lines, payments and VAT; if the sale already
// exists it does nothing and returns false. The lines must be written in a single
// transaction (db is a *sql.Tx).
func (d Dialect) SaveSale(ctx context.Context, db DB, s ledger.Sale) (bool, error) {
	res, err := db.ExecContext(ctx, d.Q(`
		INSERT INTO sales (store_id, id, register_id, cashier_id, receipt_no, fiscal_no, total, tax_total, change_amount, paid_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (store_id, id) DO NOTHING`),
		s.StoreID, s.ID, s.RegisterID, s.CashierID, s.ReceiptNo, s.FiscalNo,
		int64(s.Total), int64(s.TaxTotal), int64(s.Change), d.Time(s.PaidAt))
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err // repeated sale
	}
	for i, l := range s.Lines {
		if _, err := db.ExecContext(ctx, d.Q(`
			INSERT INTO sale_lines (store_id, sale_id, line_no, product_id, barcode, name, qty, unit_price, tax_rate, total)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
			s.StoreID, s.ID, i+1, l.ProductID, l.Barcode, LineName(l.Name),
			int64(l.Qty), int64(l.UnitPrice), int64(l.TaxRate), int64(l.Total())); err != nil {
			return false, err
		}
	}
	for i, p := range s.Payments {
		if _, err := db.ExecContext(ctx, d.Q(`
			INSERT INTO sale_payments (store_id, sale_id, payment_no, method, amount) VALUES (?, ?, ?, ?, ?)`),
			s.StoreID, s.ID, i+1, p.Method, int64(p.Amount)); err != nil {
			return false, err
		}
	}
	for _, t := range s.Taxes {
		if _, err := db.ExecContext(ctx, d.Q(`
			INSERT INTO sale_taxes (store_id, sale_id, rate, gross, tax) VALUES (?, ?, ?, ?, ?)`),
			s.StoreID, s.ID, int64(t.Rate), int64(t.Gross), int64(t.Tax)); err != nil {
			return false, err
		}
	}
	return true, nil
}

// SaveVoid writes the cancellation; if it already exists it does nothing.
func (d Dialect) SaveVoid(ctx context.Context, db DB, v ledger.Void) error {
	_, err := db.ExecContext(ctx, d.Q(`
		INSERT INTO voids (store_id, sale_id, register_id, cashier_id, total, refunded, voided_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (store_id, sale_id) DO NOTHING`),
		v.StoreID, v.SaleID, v.RegisterID, v.CashierID, int64(v.Total), v.Refunded, d.Time(v.At))
	return err
}

// SaveUnfinished writes the sale whose receipt could not be issued. If the register retries the receipt
// and still cannot issue it the event comes again: the first attempt time (since) stays, the last
// attempt time and the reason are updated if newer. The result is the same
// whatever order the events come in.
func (d Dialect) SaveUnfinished(ctx context.Context, db DB, u ledger.Unfinished) error {
	_, err := db.ExecContext(ctx, d.Q(`
		INSERT INTO unfinished (store_id, sale_id, register_id, cashier_id, total, reason, since, last_attempt)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (store_id, sale_id) DO UPDATE SET
			since = `+d.Least()+`(unfinished.since, excluded.since),
			reason = CASE WHEN excluded.last_attempt > unfinished.last_attempt THEN excluded.reason ELSE unfinished.reason END,
			last_attempt = `+d.Greatest()+`(unfinished.last_attempt, excluded.last_attempt)`),
		u.StoreID, u.SaleID, u.RegisterID, u.CashierID, int64(u.Total), u.Reason, d.Time(u.At), d.Time(u.At))
	return err
}

// SaveShift writes the shift. An opening event adds the shift; a closing event
// (ClosedAt filled) writes the closing details. The result is the same whatever
// order the events come in.
func (d Dialect) SaveShift(ctx context.Context, db DB, sh ledger.Shift) error {
	var closedAt any
	if !sh.Open() {
		closedAt = d.Time(sh.ClosedAt)
	}
	_, err := db.ExecContext(ctx, d.Q(`
		INSERT INTO shifts (store_id, register_id, shift_id, cashier_id, cashier_name, opened_at, opening_cash,
			closed_at, closed_by, receipts, total, expected_cash, counted_cash, difference)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (store_id, register_id, shift_id) DO UPDATE SET
			closed_at = coalesce(excluded.closed_at, shifts.closed_at),
			closed_by = CASE WHEN excluded.closed_at IS NULL THEN shifts.closed_by ELSE excluded.closed_by END,
			receipts = CASE WHEN excluded.closed_at IS NULL THEN shifts.receipts ELSE excluded.receipts END,
			total = CASE WHEN excluded.closed_at IS NULL THEN shifts.total ELSE excluded.total END,
			expected_cash = CASE WHEN excluded.closed_at IS NULL THEN shifts.expected_cash ELSE excluded.expected_cash END,
			counted_cash = CASE WHEN excluded.closed_at IS NULL THEN shifts.counted_cash ELSE excluded.counted_cash END,
			difference = CASE WHEN excluded.closed_at IS NULL THEN shifts.difference ELSE excluded.difference END`),
		sh.StoreID, sh.RegisterID, sh.ID, sh.CashierID, sh.CashierName, d.Time(sh.OpenedAt), int64(sh.OpeningCash),
		closedAt, sh.ClosedBy, sh.Receipts, int64(sh.Total), int64(sh.ExpectedCash), int64(sh.CountedCash), int64(sh.Difference))
	return err
}

// Report produces the period's report by grouping in the database; the rules are in
// ledger.Builder. For a consistent read db can be a read-only
// transaction.
func (d Dialect) Report(ctx context.Context, db DB, f ledger.Filter) (ledger.Report, error) {
	if d.Postgres() {
		// The daily and the monthly report share their SQL text; after a few runs PostgreSQL
		// would move the prepared statement to a generic plan that does not know the dates
		// and assumes a short range (the monthly product ranking took 4-6 s instead of 0.7 s).
		// Only for this transaction: db is a *sql.Tx (outside a transaction it has no effect).
		if _, err := db.ExecContext(ctx, "SET LOCAL plan_cache_mode = force_custom_plan"); err != nil {
			return ledger.Report{}, err
		}
	}
	b := ledger.NewBuilder(f)
	args := []any{d.Time(f.Start), d.Time(f.End)}
	stores := ""
	switch {
	case f.Stores == nil:
	case len(f.Stores) == 0:
		stores = " AND 1 = 0"
	default:
		stores = " AND %s IN (?" + strings.Repeat(", ?", len(f.Stores)-1) + ")"
		for _, s := range f.Stores {
			args = append(args, s)
		}
	}
	inSales := `s.paid_at >= ? AND s.paid_at < ?` + strings.ReplaceAll(stores, "%s", "s.store_id")
	query := func(q string, qargs []any, scan func(*sql.Rows) error) error {
		rows, err := db.QueryContext(ctx, d.Q(q), qargs...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := scan(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	each := func(q string, scan func(*sql.Rows) error) error { return query(q, args, scan) }

	// The totals per store, register and cashier and the totals per time slice come from one
	// statement, so both are read from the same snapshot. A row with no slice (noSlice) is a
	// total. Grouping by all four at once returned about one row per sale.
	salesQuery, salesArgs := d.salesQuery(inSales, args)
	err := query(salesQuery, salesArgs, func(rows *sql.Rows) error {
		var store, register, cashier string
		var bucket, n int64
		var total, tax, change money.Money
		if err := rows.Scan(&store, &register, &cashier, &bucket, &n, &total, &tax, &change); err != nil {
			return err
		}
		if bucket == noSlice {
			b.AddSalesTotals(store, register, cashier, n, total, tax, change)
		} else {
			b.AddSalesSlice(time.Unix(bucket*int64(ledger.Bucket/time.Second), 0), n, total)
		}
		return nil
	})
	if err != nil {
		return ledger.Report{}, err
	}
	err = each(`
		SELECT v.store_id, v.register_id, v.cashier_id, count(*), CAST(sum(v.total) AS BIGINT)
		FROM voids v WHERE v.voided_at >= ? AND v.voided_at < ?`+strings.ReplaceAll(stores, "%s", "v.store_id")+`
		GROUP BY v.store_id, v.register_id, v.cashier_id`, func(rows *sql.Rows) error {
		var store, register, cashier string
		var n int64
		var total money.Money
		if err := rows.Scan(&store, &register, &cashier, &n, &total); err != nil {
			return err
		}
		b.AddVoids(store, register, cashier, n, total)
		return nil
	})
	if err != nil {
		return ledger.Report{}, err
	}
	err = each(`
		SELECT p.method, CAST(sum(p.amount) AS BIGINT)
		FROM sale_payments p JOIN sales s ON s.store_id = p.store_id AND s.id = p.sale_id
		WHERE `+inSales+` GROUP BY p.method`, func(rows *sql.Rows) error {
		var method string
		var amount money.Money
		if err := rows.Scan(&method, &amount); err != nil {
			return err
		}
		b.AddPayment(method, amount)
		return nil
	})
	if err != nil {
		return ledger.Report{}, err
	}
	err = each(`
		SELECT t.rate, CAST(sum(t.gross) AS BIGINT), CAST(sum(t.tax) AS BIGINT)
		FROM sale_taxes t JOIN sales s ON s.store_id = t.store_id AND s.id = t.sale_id
		WHERE `+inSales+` GROUP BY t.rate`, func(rows *sql.Rows) error {
		var t ledger.Tax
		if err := rows.Scan(&t.Rate, &t.Gross, &t.Tax); err != nil {
			return err
		}
		b.AddTax(t)
		return nil
	})
	if err != nil {
		return ledger.Report{}, err
	}
	// Products: a line with no id (e.g. entered by hand) is grouped by its name;
	// the order and name are the same as Builder's (amount descending, name and id by
	// byte order).
	limit := ""
	if f.MaxProducts > 0 {
		limit = " LIMIT " + strconv.Itoa(f.MaxProducts)
	}
	name := "max(" + d.binary("l.name") + ")"
	err = each(`
		SELECT max(l.product_id), `+name+`, CAST(sum(l.qty) AS BIGINT), CAST(sum(l.total) AS BIGINT) AS total
		FROM sale_lines l JOIN sales s ON s.store_id = l.store_id AND s.id = l.sale_id
		WHERE `+inSales+`
		GROUP BY CASE WHEN l.product_id = '' THEN 'ad:' || l.name ELSE l.product_id END
		ORDER BY total DESC, `+name+`, max(`+d.binary("l.product_id")+`)`+limit, func(rows *sql.Rows) error {
		var p ledger.ProductTotal
		if err := rows.Scan(&p.ProductID, &p.Name, &p.Qty, &p.Total); err != nil {
			return err
		}
		b.AddProduct(p)
		return nil
	})
	if err != nil {
		return ledger.Report{}, err
	}
	return b.Report(), nil
}
