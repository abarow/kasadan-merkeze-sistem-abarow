// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command storebench measures the performance of the store server database by the number of
// registers (docs/PERFORMANCE.md). It runs with the real Store code (SaveSale,
// IssueReceipt, Activity, Report); there are no registers or NATS, the sales the registers
// send are written straight to the database.
//
//	storebench -dsn postgres://... -registers 10 [-days 30] [-sales-per-day 300]
//	        [-duration 60s] [-rate 0.5] [-screens 0]
//
// The data grows with the number of registers: for every register days days × sales-per-day sales
// (4 lines per sale, 1-2 payments, 2 VAT lines, one receipt), one shift per day, one
// percent cancelled. The database must be empty. During the load:
//
//   - every register sends rate sales per second (Poisson); the sales are processed in a
//     single worker in order (like the store server's consumer) and the latency is measured
//     from the arrival moment to the processing moment;
//   - screens screens (if not given a tenth of the number of registers + 1) ask for the register
//     state (Activity) every 5 seconds, and the daily and monthly summary and the product
//     ranking (Report) every 10 seconds.
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
	"abarow.com/services/store/internal/adapters/out/postgres"
)

func main() {
	dsn := flag.String("dsn", "", "boş PostgreSQL veritabanının adresi")
	registers := flag.Int("registers", 1, "kasa sayısı")
	days := flag.Int("days", 30, "kasa başına geçmiş gün")
	perDay := flag.Int("sales-per-day", 300, "kasa başına günlük satış")
	duration := flag.Duration("duration", 60*time.Second, "yük süresi")
	rate := flag.Float64("rate", 0.5, "kasa başına saniyedeki satış (gerçek yoğun saat: 0,03–0,1)")
	screens := flag.Int("screens", -1, "açık ekran sayısı; -1: kasa sayısının onda biri + 1")
	skipSeed := flag.Bool("skip-seed", false, "veriyi yükleme (zaten yüklü)")
	flag.Parse()
	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "storebench: -dsn is required")
		os.Exit(2)
	}
	if *screens < 0 {
		*screens = *registers/10 + 1
	}
	res, err := run(context.Background(), *dsn, *registers, *days, *perDay, *duration, *rate, *screens, *skipSeed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "storebench:", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(res)
}

type result struct {
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

func run(ctx context.Context, dsn string, regs, days, perDay int, duration time.Duration, rate float64, screens int, skipSeed bool) (*result, error) {
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	store.StoreID = "store-1"

	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	defer raw.Close()
	raw.SetMaxOpenConns(2)

	res := &result{Registers: regs, Reads: map[string]stat{}}
	if !skipSeed {
		t0 := time.Now()
		if err := seed(ctx, raw, regs, days, perDay); err != nil {
			return nil, fmt.Errorf("veri yüklenemedi: %w", err)
		}
		res.SeedSeconds = math.Round(time.Since(t0).Seconds()*10) / 10
	}
	res.Sales = regs * days * perDay
	if err := sizes(ctx, raw, res); err != nil {
		return nil, err
	}

	loc, _ := time.LoadLocation("Europe/Istanbul")
	loadCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	var wg sync.WaitGroup
	ingest := &recorder{}
	reads := map[string]*recorder{"activity": {}, "day": {}, "month": {}, "top_products": {}}

	// Registers that send sales: the arrival moments go into a queue, a single worker processes them.
	type arrival struct {
		at  time.Time
		reg int
	}
	queue := make(chan arrival, 1<<16)
	var maxQueue int
	var seq sync.Map
	for r := 1; r <= regs; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(r), 7))
			for {
				wait := time.Duration(rng.ExpFloat64() / rate * float64(time.Second))
				select {
				case <-loadCtx.Done():
					return
				case <-time.After(wait):
				}
				select {
				case queue <- arrival{time.Now(), r}:
				default:
				}
			}
		}(r)
	}
	var processed int
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
				n, _ := seq.LoadOrStore(a.reg, new(int64))
				no := n.(*int64)
				*no++
				sale := newSale(a.reg, days*perDay+int(*no), time.Now())
				err := store.SaveSale(ctx, sale)
				if err == nil {
					err = store.IssueReceipt(ctx, fmt.Sprintf("register-%d", a.reg), "ABAROW_SALES", "ev-"+sale.ID)
				}
				ingest.add(time.Since(a.at), err)
				processed++
			}
		}
	}()

	// Open screens.
	for s := 0; s < screens; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			t5, t10 := time.NewTicker(5*time.Second), time.NewTicker(10*time.Second)
			defer t5.Stop()
			defer t10.Stop()
			time.Sleep(time.Duration(s) * 700 * time.Millisecond)
			do := func(name string, fn func() error) {
				t := time.Now()
				err := fn()
				if loadCtx.Err() != nil {
					return
				}
				reads[name].add(time.Since(t), err)
			}
			tick := func(all bool) {
				do("activity", func() error { _, err := store.Activity(loadCtx); return err })
				if !all {
					return
				}
				now := time.Now().In(loc)
				today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
				do("day", func() error {
					_, err := store.Report(loadCtx, ledger.Filter{Start: today, End: today.AddDate(0, 0, 1), Location: loc})
					return err
				})
				do("month", func() error {
					_, err := store.Report(loadCtx, ledger.Filter{Start: today.AddDate(0, 0, -30), End: today.AddDate(0, 0, 1), Location: loc})
					return err
				})
				do("top_products", func() error {
					_, err := store.Report(loadCtx, ledger.Filter{Start: today.AddDate(0, 0, -30), End: today.AddDate(0, 0, 1), Location: loc, MaxProducts: 20})
					return err
				})
			}
			tick(true)
			for {
				select {
				case <-loadCtx.Done():
					return
				case <-t5.C:
					tick(false)
				case <-t10.C:
					tick(true)
				}
			}
		}(s)
	}
	wg.Wait()

	res.Ingest = ingest.stat()
	res.IngestRate = math.Round(float64(processed)/duration.Seconds()*10) / 10
	res.MaxQueue = maxQueue
	for k, v := range reads {
		res.Reads[k] = v.stat()
	}

	// Capacity: the single worker's maximum rate with no queue (5 s).
	capEnd := time.Now().Add(5 * time.Second)
	n := 0
	for time.Now().Before(capEnd) {
		sale := newSale(1+n%regs, days*perDay+1_000_000+n, time.Now())
		if store.SaveSale(ctx, sale) != nil || store.IssueReceipt(ctx, "register-1", "ABAROW_SALES", "cap-"+sale.ID) != nil {
			break
		}
		n++
	}
	res.Capacity = math.Round(float64(n) / 5 * 10 / 10)

	if err := sizes(ctx, raw, res); err != nil {
		return nil, err
	}
	return res, nil
}

func newSale(reg, no int, at time.Time) ledger.Sale {
	id := fmt.Sprintf("b-%d-%d-%d", reg, no, at.UnixNano())
	s := ledger.Sale{
		ID: id, StoreID: "store-1", RegisterID: fmt.Sprintf("register-%d", reg), CashierID: fmt.Sprintf("k%d", no%4),
		ReceiptNo: int64(no), FiscalNo: fmt.Sprintf("F%d", no), PaidAt: at, Total: 8000, TaxTotal: 700,
		Payments: []ledger.Payment{{Method: ledger.Cash, Amount: 8000}},
		Taxes:    []ledger.Tax{{Rate: 100, Gross: 3000, Tax: 30}, {Rate: 1000, Gross: 5000, Tax: 455}},
	}
	for i := 0; i < 4; i++ {
		s.Lines = append(s.Lines, ledger.Line{ProductID: fmt.Sprintf("p%03d", (no+i*37)%500), Barcode: "869000000", Name: fmt.Sprintf("Product %d", (no+i*37)%500), Qty: 1000, UnitPrice: 2000, TaxRate: 1000})
	}
	return s
}

// seed loads the past data by the number of registers with one SQL (the tables are empty).
func seed(ctx context.Context, db *sql.DB, regs, days, perDay int) error {
	steps := []string{
		// Sales: register r, evenly spaced through the day; the receipt number is sequential at the register.
		`INSERT INTO sales (store_id, id, register_id, cashier_id, receipt_no, fiscal_no, total, tax_total, change_amount, paid_at)
		 SELECT 'store-1', 's-'||r||'-'||n, 'register-'||r, 'k'||(n%4), n, 'F'||n, 5000 + (n*37)%20000, 400, 0,
		        now() - make_interval(secs => ($2::int * $3::int - n) * (86400.0 / $3::int))
		 FROM generate_series(1, $1::int) r, generate_series(1, $2::int * $3::int) n`,
		`INSERT INTO sale_lines (store_id, sale_id, line_no, product_id, barcode, name, qty, unit_price, tax_rate, total)
		 SELECT 'store-1', 's-'||r||'-'||n, l, 'p'||lpad(((n + l*37) % 500)::text, 3, '0'), '86900000'||l, 'Ürün '||((n + l*37) % 500), 1000, 2000, 1000, 2000
		 FROM generate_series(1, $1::int) r, generate_series(1, $2::int * $3::int) n, generate_series(1, 4) l`,
		`INSERT INTO sale_payments (store_id, sale_id, payment_no, method, amount)
		 SELECT 'store-1', 's-'||r||'-'||n, 1, CASE WHEN n%3=0 THEN 'card' ELSE 'cash' END, 5000 + (n*37)%20000
		 FROM generate_series(1, $1::int) r, generate_series(1, $2::int * $3::int) n`,
		`INSERT INTO sale_taxes (store_id, sale_id, rate, gross, tax)
		 SELECT 'store-1', 's-'||r||'-'||n, rate, 3000, 30
		 FROM generate_series(1, $1::int) r, generate_series(1, $2::int * $3::int) n, (VALUES (100), (1000)) AS t(rate)`,
		// One percent cancelled.
		`INSERT INTO voids (store_id, sale_id, register_id, cashier_id, total, refunded, voided_at)
		 SELECT 'store-1', 'v-'||r||'-'||n, 'register-'||r, 'k1', 5000, true, now() - make_interval(secs => ($2::int * $3::int - n) * (86400.0 / $3::int))
		 FROM generate_series(1, $1::int) r, generate_series(1, $2::int * $3::int, 100) n`,
		// One closed shift per day, one open shift per register.
		`INSERT INTO shifts (store_id, register_id, shift_id, cashier_id, cashier_name, opened_at, opening_cash, closed_at, closed_by, receipts, total, expected_cash, counted_cash, difference)
		 SELECT 'store-1', 'register-'||r, d, 'k'||(d%4), 'Kasiyer '||(d%4), now() - make_interval(days => $2::int - d + 1), 50000,
		        now() - make_interval(days => $2::int - d + 1) + interval '10 hours', 'k1', $3::int, 1000000, 1000000, 1000000, 0
		 FROM generate_series(1, $1::int) r, generate_series(1, $2::int) d`,
		`INSERT INTO shifts (store_id, register_id, shift_id, cashier_id, cashier_name, opened_at, opening_cash)
		 SELECT 'store-1', 'register-'||r, $2::int + 1, 'k0', 'Kasiyer 0', now() - interval '2 hours', 50000 FROM generate_series(1, $1::int) r WHERE $3::int > 0`,
		// Delivery receipt: one receipt for each sale event (published).
		`INSERT INTO delivery_issued (event_id, receipt_id, owner, stream, issued_at)
		 SELECT 'ev-s-'||r||'-'||n, 'rc-'||r||'-'||n, 'register-'||r, 'ABAROW_SALES', now()
		 FROM generate_series(1, $1::int) r, generate_series(1, $2::int * $3::int) n`,
		`INSERT INTO receipt_outbox (id, type, subject, store_id, payload, content_type, occurred_at, published_at)
		 SELECT 'ro-'||r||'-'||n, 'com.abarow.delivery.receipt.v1', 'receipt.register-'||r||'.ABAROW_SALES', 'store-1', '\x00'::bytea, 'application/x-protobuf', now(), now()
		 FROM generate_series(1, $1::int) r, generate_series(1, $2::int * $3::int) n`,
		// The store's own events (cashier, price): published history.
		`INSERT INTO outbox (id, type, subject, store_id, payload, content_type, occurred_at, published_at)
		 SELECT 'o-'||n, 'com.abarow.config.v1', 'config.price.p'||n, 'store-1', '\x00'::bytea, 'application/x-protobuf', now(), now()
		 FROM generate_series(1, $1::int * $2::int * 2) n WHERE $3::int > 0`,
		`VACUUM ANALYZE`,
	}
	for i, q := range steps {
		var err error
		if i == len(steps)-1 {
			_, err = db.ExecContext(ctx, q)
		} else {
			_, err = db.ExecContext(ctx, q, regs, days, perDay)
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
