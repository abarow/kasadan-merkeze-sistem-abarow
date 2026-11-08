// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command headquartersbench measures the performance of the headquarters database by the
// number of stores (docs/PERFORMANCE.md). It runs with the real code paths:
// ingest's Store.Record (event + sales ledger + counters + receipt + sequence
// number) and the sales ledger report reporting uses (pkg/ledger);
// there are no stores or NATS, the events the stores send are written directly.
//
//	headquartersbench -dsn postgres://... -stores 10 [-registers-per-store 2] [-days 30]
//	        [-sales-per-day 300] [-duration 60s] [-rate 0.2] [-screens -1]
//
// The data grows with the number of stores: in each store registers-per-store registers, in each
// register days days × sales-per-day sales (one event per sale, 4 lines, payment,
// 2 VAT lines, receipt; one percent cancelled; one shift per day).
// The database must be empty. During the load:
//
//   - every register sends rate sales per second (Poisson); the events are processed in a
//     single worker in order (like ingest's consumer); the latency is from the arrival
//     moment to the processing;
//   - screens screens (if not given a fifth of the number of stores + 1): every 10
//     seconds the store state, the daily summary, one store's daily summary and the ingest
//     activity; every 30 seconds the monthly summary and the product ranking.
//
// The output is a single JSON object.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"sort"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/ledger/ledgerdb"
	"abarow.com/services/headquarters/ingest/internal/adapters/out/postgres"
	"abarow.com/services/headquarters/ingest/internal/app"
	"abarow.com/services/headquarters/ingest/internal/domain"
)

func main() {
	dsn := flag.String("dsn", "", "boş PostgreSQL veritabanının adresi")
	stores := flag.Int("stores", 1, "mağaza sayısı")
	perStore := flag.Int("registers-per-store", 2, "mağaza başına kasa")
	days := flag.Int("days", 30, "kasa başına geçmiş gün")
	perDay := flag.Int("sales-per-day", 300, "kasa başına günlük satış")
	duration := flag.Duration("duration", 60*time.Second, "yük süresi")
	rate := flag.Float64("rate", 0.2, "kasa başına saniyedeki satış (gerçek yoğun saat: 0,03–0,1)")
	screens := flag.Int("screens", -1, "açık ekran sayısı; -1: mağaza sayısının beşte biri + 1")
	skipSeed := flag.Bool("skip-seed", false, "veriyi yükleme (zaten yüklü)")
	batch := flag.Int("batch", 1, "the most waiting sales written in one transaction (ingest's consumer: 100)")
	flag.Parse()
	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "headquartersbench: -dsn is required")
		os.Exit(2)
	}
	if *screens < 0 {
		*screens = *stores/5 + 1
	}
	cfg := config{stores: *stores, perStore: *perStore, days: *days, perDay: *perDay, duration: *duration, rate: *rate, screens: *screens, batch: max(*batch, 1)}
	res, err := run(context.Background(), *dsn, cfg, *skipSeed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "headquartersbench:", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(res)
}

type config struct {
	stores, perStore, days, perDay int
	duration                       time.Duration
	rate                           float64
	screens                        int
	// batch is the most waiting sales written in one transaction (RecordBatch).
	batch int
}

type result struct {
	Stores      int                `json:"stores"`
	Registers   int                `json:"registers"`
	Sales       int                `json:"seeded_sales"`
	SeedSeconds float64            `json:"seed_seconds"`
	DBSizeMB    float64            `json:"db_size_mb"`
	Tables      map[string]float64 `json:"table_size_mb"`
	Ingest      stat               `json:"ingest"`
	IngestRate  float64            `json:"ingest_per_second"`
	MaxQueue    int                `json:"ingest_max_queue"`
	Capacity    float64            `json:"ingest_capacity_per_second"`
	Reads       map[string]stat    `json:"reads"`
}

type stat struct {
	N      int     `json:"n"`
	Errors int     `json:"errors"`
	P50    float64 `json:"p50_ms"`
	P95    float64 `json:"p95_ms"`
	P99    float64 `json:"p99_ms"`
	Max    float64 `json:"max_ms"`
	First  string  `json:"first_error,omitempty"`
}

type recorder struct {
	mu    sync.Mutex
	lat   []float64
	errs  int
	first string
}

func (r *recorder) add(d time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.errs++
		if r.first == "" {
			r.first = err.Error()
		}
		return
	}
	r.lat = append(r.lat, float64(d)/float64(time.Millisecond))
}

func (r *recorder) stat() stat {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := stat{N: len(r.lat), Errors: r.errs, First: r.first}
	if len(r.lat) == 0 {
		return s
	}
	sort.Float64s(r.lat)
	at := func(q float64) float64 { return math.Round(r.lat[int(float64(len(r.lat)-1)*q)]*10) / 10 }
	s.P50, s.P95, s.P99, s.Max = at(.5), at(.95), at(.99), math.Round(r.lat[len(r.lat)-1]*10)/10
	return s
}

func storeID(s int) string { return fmt.Sprintf("store-%d", s) }
func regID(r int) string   { return fmt.Sprintf("register-%d", r) }

func run(ctx context.Context, dsn string, c config, skipSeed bool) (*result, error) {
	ingest, err := postgres.Open(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer ingest.Close()

	// 8 connections like reporting's connection pool.
	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	defer raw.Close()
	raw.SetMaxOpenConns(8)

	regs := c.stores * c.perStore
	res := &result{Stores: c.stores, Registers: regs, Reads: map[string]stat{}}
	if !skipSeed {
		t0 := time.Now()
		if err := seed(ctx, raw, c); err != nil {
			return nil, fmt.Errorf("veri yüklenemedi: %w", err)
		}
		res.SeedSeconds = math.Round(time.Since(t0).Seconds()*10) / 10
	}
	res.Sales = regs * c.days * c.perDay
	if err := sizes(ctx, raw, res); err != nil {
		return nil, err
	}

	loc, _ := time.LoadLocation("Europe/Istanbul")
	loadCtx, cancel := context.WithTimeout(ctx, c.duration)
	defer cancel()

	var wg sync.WaitGroup
	ing := &recorder{}
	names := []string{"stores", "activity", "day_all", "day_store", "month_all", "top_products"}
	reads := map[string]*recorder{}
	for _, n := range names {
		reads[n] = &recorder{}
	}

	type arrival struct {
		at       time.Time
		store, r int
	}
	queue := make(chan arrival, 1<<16)
	var maxQueue, processed int
	seqs := map[[2]int]int64{}
	var seqMu sync.Mutex
	next := func(k [2]int) int64 {
		seqMu.Lock()
		defer seqMu.Unlock()
		if seqs[k] == 0 {
			seqs[k] = int64(c.days*c.perDay) + 1
		}
		seqs[k]++
		return seqs[k]
	}
	for s := 1; s <= c.stores; s++ {
		for r := 1; r <= c.perStore; r++ {
			wg.Add(1)
			go func(s, r int) {
				defer wg.Done()
				rng := rand.New(rand.NewPCG(uint64(s), uint64(r)))
				for {
					wait := time.Duration(rng.ExpFloat64() / c.rate * float64(time.Second))
					select {
					case <-loadCtx.Done():
						return
					case <-time.After(wait):
					}
					select {
					case queue <- arrival{time.Now(), s, r}:
					default:
					}
				}
			}(s, r)
		}
	}
	item := func(a arrival) app.Item {
		n := next([2]int{a.store, a.r})
		now := time.Now()
		sale := newSale(a.store, a.r, n, now)
		e := domain.Event{ID: fmt.Sprintf("be-%s-%s-%d-%d", sale.StoreID, sale.RegisterID, n, now.UnixNano()), Subject: "sale.completed.v1",
			StoreID: sale.StoreID, RegisterID: sale.RegisterID, OccurredAt: now, ContentType: "application/protobuf", Payload: make([]byte, 300)}
		return app.Item{Event: e, Body: sale, Producer: "sales", Seq: n}
	}
	write := func(a arrival) error {
		it := item(a)
		if _, err := ingest.Record(ctx, it.Event, it.Body); err != nil {
			return err
		}
		return ingest.RecordSequence(ctx, it.Event.StoreID, it.Event.RegisterID, it.Producer, it.Seq)
	}
	// writeAll writes the arrivals one by one or, with -batch, in one transaction
	// (as ingest's consumer does with the events already waiting).
	writeAll := func(as []arrival) error {
		if len(as) == 1 {
			return write(as[0])
		}
		items := make([]app.Item, len(as))
		for i, a := range as {
			items[i] = item(a)
		}
		return ingest.RecordBatch(ctx, items)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-loadCtx.Done():
				return
			case a := <-queue:
				if q := len(queue) + 1; q > maxQueue {
					maxQueue = q
				}
				as := []arrival{a}
			waiting:
				for len(as) < c.batch {
					select {
					case b := <-queue:
						as = append(as, b)
					default:
						break waiting
					}
				}
				err := writeAll(as)
				for _, b := range as {
					ing.add(time.Since(b.at), err)
				}
				processed += len(as)
			}
		}
	}()

	for sc := 0; sc < c.screens; sc++ {
		wg.Add(1)
		go func(sc int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(sc), 99))
			t10, t30 := time.NewTicker(10*time.Second), time.NewTicker(30*time.Second)
			defer t10.Stop()
			defer t30.Stop()
			time.Sleep(time.Duration(sc) * 900 * time.Millisecond)
			do := func(name string, fn func() error) {
				t := time.Now()
				err := fn()
				if loadCtx.Err() != nil {
					return
				}
				reads[name].add(time.Since(t), err)
			}
			now := time.Now().In(loc)
			today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
			report := func(from time.Time, stores []string, products int) error {
				tx, err := raw.BeginTx(loadCtx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
				if err != nil {
					return err
				}
				defer tx.Rollback()
				_, err = ledgerdb.Postgres.Report(loadCtx, tx, ledger.Filter{Start: from, End: today.AddDate(0, 0, 1), Stores: stores, Location: loc, MaxProducts: products})
				return err
			}
			short := func() {
				do("stores", func() error { return storesOverview(loadCtx, raw, today) })
				do("activity", func() error { _, err := ingest.Activity(loadCtx); return err })
				do("day_all", func() error { return report(today, nil, 0) })
				do("day_store", func() error { return report(today, []string{storeID(1 + rng.IntN(c.stores))}, 0) })
			}
			long := func() {
				do("month_all", func() error { return report(today.AddDate(0, 0, -30), nil, 0) })
				do("top_products", func() error { return report(today.AddDate(0, 0, -30), nil, 20) })
			}
			short()
			long()
			for {
				select {
				case <-loadCtx.Done():
					return
				case <-t10.C:
					short()
				case <-t30.C:
					long()
				}
			}
		}(sc)
	}
	wg.Wait()

	res.Ingest = ing.stat()
	res.IngestRate = math.Round(float64(processed)/c.duration.Seconds()*10) / 10
	res.MaxQueue = maxQueue
	for k, v := range reads {
		res.Reads[k] = v.stat()
	}

	// Capacity: the single worker's maximum rate with no queue (5 s).
	capEnd := time.Now().Add(5 * time.Second)
	n := 0
	for time.Now().Before(capEnd) {
		as := make([]arrival, c.batch)
		for i := range as {
			as[i] = arrival{store: 1 + (n+i)%c.stores, r: 1 + (n+i)%c.perStore}
		}
		if writeAll(as) != nil {
			break
		}
		n += len(as)
	}
	res.Capacity = math.Round(float64(n)/5*10) / 10

	if err := sizes(ctx, raw, res); err != nil {
		return nil, err
	}
	return res, nil
}

// storesOverview is reporting's store state query (with today's sales).
func storesOverview(ctx context.Context, db *sql.DB, today time.Time) error {
	rows, err := db.QueryContext(ctx, `
		SELECT l.store_id, l.name, l.link, l.last_seen, l.pending_events, l.error, l.checked_at,
		       st.last_event_at, coalesce(t.receipts, 0), coalesce(t.total, 0)
		FROM store_links l
		LEFT JOIN store_stats st ON st.store_id = l.store_id
		LEFT JOIN (
			SELECT store_id, count(*) AS receipts, sum(total)::bigint AS total
			FROM sales WHERE paid_at >= $1 AND paid_at < $2 GROUP BY store_id
		) t ON t.store_id = l.store_id
		ORDER BY l.store_id`, today.UTC(), today.AddDate(0, 0, 1).UTC())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a, b, c, e string
		var f, g, h sql.NullTime
		var p int64
		var i, j int64
		if err := rows.Scan(&a, &b, &c, &f, &p, &e, &g, &h, &i, &j); err != nil {
			return err
		}
		_ = g
	}
	return rows.Err()
}

func newSale(store, reg int, no int64, at time.Time) domain.Sale {
	s := domain.Sale{
		ID: fmt.Sprintf("b-%d-%d-%d-%d", store, reg, no, at.UnixNano()), StoreID: storeID(store), RegisterID: regID(reg),
		CashierID: fmt.Sprintf("k%d", no%4), ReceiptNo: no, FiscalNo: fmt.Sprintf("F%d", no), PaidAt: at, Total: 8000, TaxTotal: 700,
		Payments: []domain.Payment{{Method: domain.Cash, Amount: 8000}},
		Taxes:    []domain.Tax{{Rate: 100, Gross: 3000, Tax: 30}, {Rate: 1000, Gross: 5000, Tax: 455}},
	}
	for i := 0; i < 4; i++ {
		p := (int(no) + i*37) % 500
		s.Lines = append(s.Lines, domain.Line{ProductID: fmt.Sprintf("p%03d", p), Barcode: "869000000", Name: fmt.Sprintf("Product %d", p), Qty: 1000, UnitPrice: 2000, TaxRate: 1000})
	}
	return s
}

// seed loads the past data by the number of stores with one SQL (the tables are empty).
// $1 stores, $2 registers per store, $3 days, $4 sales per day.
func seed(ctx context.Context, db *sql.DB, c config) error {
	const each = `generate_series(1, $1::int) s, generate_series(1, $2::int) r, generate_series(1, $3::int * $4::int) n`
	const when = `now() - make_interval(secs => ($3::int * $4::int - n) * (86400.0 / $4::int))`
	steps := []string{
		`INSERT INTO store_links (store_id, name, link, last_seen, pending_events, error, checked_at)
		 SELECT 'store-'||s, 'Mağaza '||s, 'connected', now(), 0, '', now() FROM generate_series(1, $1::int) s WHERE $2::int > 0 AND $3::int > 0 AND $4::int > 0`,
		`INSERT INTO sales (store_id, id, register_id, cashier_id, receipt_no, fiscal_no, total, tax_total, change_amount, paid_at)
		 SELECT 'store-'||s, 's-'||s||'-'||r||'-'||n, 'register-'||r, 'k'||(n%4), n, 'F'||n, 5000 + (n*37)%20000, 400, 0, ` + when + ` FROM ` + each,
		`INSERT INTO events (id, subject, store_id, register_id, occurred_at, content_type, payload)
		 SELECT 'e-'||s||'-'||r||'-'||n, 'sale.completed.v1', 'store-'||s, 'register-'||r, ` + when + `, 'application/protobuf', ('\x'||repeat('ab', 150))::bytea FROM ` + each,
		`INSERT INTO sale_lines (store_id, sale_id, line_no, product_id, barcode, name, qty, unit_price, tax_rate, total)
		 SELECT 'store-'||s, 's-'||s||'-'||r||'-'||n, l, 'p'||lpad(((n + l*37) % 500)::text, 3, '0'), '86900000'||l, 'Ürün '||((n + l*37) % 500), 1000, 2000, 1000, 2000
		 FROM ` + each + `, generate_series(1, 4) l`,
		`INSERT INTO sale_payments (store_id, sale_id, payment_no, method, amount)
		 SELECT 'store-'||s, 's-'||s||'-'||r||'-'||n, 1, CASE WHEN n%3=0 THEN 'card' ELSE 'cash' END, 5000 + (n*37)%20000 FROM ` + each,
		`INSERT INTO sale_taxes (store_id, sale_id, rate, gross, tax)
		 SELECT 'store-'||s, 's-'||s||'-'||r||'-'||n, rate, 3000, 30 FROM ` + each + `, (VALUES (100), (1000)) AS t(rate)`,
		`INSERT INTO voids (store_id, sale_id, register_id, cashier_id, total, refunded, voided_at)
		 SELECT 'store-'||s, 'v-'||s||'-'||r||'-'||n, 'register-'||r, 'k1', 5000, true, ` + when + `
		 FROM generate_series(1, $1::int) s, generate_series(1, $2::int) r, generate_series(1, $3::int * $4::int, 100) n`,
		`INSERT INTO shifts (store_id, register_id, shift_id, cashier_id, cashier_name, opened_at, opening_cash, closed_at, closed_by, receipts, total, expected_cash, counted_cash, difference)
		 SELECT 'store-'||s, 'register-'||r, d, 'k'||(d%4), 'Kasiyer '||(d%4), now() - make_interval(days => $3::int - d + 1), 50000,
		        now() - make_interval(days => $3::int - d + 1) + interval '10 hours', 'k1', $4::int, 1000000, 1000000, 1000000, 0
		 FROM generate_series(1, $1::int) s, generate_series(1, $2::int) r, generate_series(1, $3::int) d WHERE $4::int > 0`,
		`INSERT INTO registers (store_id, register_id, events, sales, first_event_at, last_event_at)
		 SELECT 'store-'||s, 'register-'||r, $3::int * $4::int, $3::int * $4::int, now() - make_interval(days => $3::int), now()
		 FROM generate_series(1, $1::int) s, generate_series(1, $2::int) r`,
		`INSERT INTO store_stats (store_id, events, sales, first_event_at, last_event_at)
		 SELECT 'store-'||s, $2::int * $3::int * $4::int, $2::int * $3::int * $4::int, now() - make_interval(days => $3::int), now()
		 FROM generate_series(1, $1::int) s`,
		`INSERT INTO delivery_issued (event_id, receipt_id, owner, stream, issued_at)
		 SELECT 'e-'||s||'-'||r||'-'||n, 'rc-'||s||'-'||r||'-'||n, 'store-'||s, 'ABAROW_HEADQUARTERS', now() FROM ` + each,
		`INSERT INTO receipt_outbox (id, type, subject, payload, content_type, occurred_at, published_at)
		 SELECT 'ro-'||s||'-'||r||'-'||n, 'com.abarow.delivery.receipt.v1', 'receipt.store-'||s||'.ABAROW_HEADQUARTERS', '\x00'::bytea, 'application/x-protobuf', now(), now() FROM ` + each,
		`VACUUM ANALYZE`,
	}
	for i, q := range steps {
		var err error
		if i == len(steps)-1 {
			_, err = db.ExecContext(ctx, q)
		} else {
			_, err = db.ExecContext(ctx, q, c.stores, c.perStore, c.days, c.perDay)
		}
		if err != nil {
			return fmt.Errorf("adım %d: %w", i+1, err)
		}
	}
	return nil
}

func sizes(ctx context.Context, db *sql.DB, res *result) error {
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT pg_database_size(current_database())`).Scan(&total); err != nil {
		return err
	}
	res.DBSizeMB = math.Round(float64(total)/1048576*10) / 10
	res.Tables = map[string]float64{}
	rows, err := db.QueryContext(ctx, `SELECT relname, pg_total_relation_size(oid) FROM pg_class
		WHERE relkind = 'r' AND relnamespace = 'public'::regnamespace AND pg_total_relation_size(oid) > 1048576 ORDER BY 2 DESC LIMIT 8`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var n int64
		if err := rows.Scan(&name, &n); err != nil {
			return err
		}
		res.Tables[name] = math.Round(float64(n)/1048576*10) / 10
	}
	return rows.Err()
}
