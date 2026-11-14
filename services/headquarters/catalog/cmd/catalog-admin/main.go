// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command catalog-admin is the composition root of headquarters' catalog management
// service: products, barcodes and the headquarters price schedule (including future-dated
// ones). Every change is recorded with who made it; the product's latest state is
// published to the headquarters catalog stream (ABAROW_CATALOG), from where it goes down to the stores
// and the registers.
//
// The registers' initial file (catalog.json) can be loaded to headquarters once:
//
//	catalog-admin import [user] < catalog.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
	_ "time/tzdata" // scratch image'da saat dilimi verisi yok

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/leader"
	"abarow.com/pkg/menu"
	"abarow.com/pkg/money"
	"abarow.com/pkg/outbox"
	"abarow.com/pkg/retention"
	"abarow.com/pkg/service"
	"abarow.com/services/headquarters/catalog/internal/adapters/in/httpapi"
	"abarow.com/services/headquarters/catalog/internal/adapters/out/eventproto"
	"abarow.com/services/headquarters/catalog/internal/adapters/out/natspub"
	"abarow.com/services/headquarters/catalog/internal/adapters/out/postgres"
	"abarow.com/services/headquarters/catalog/internal/app"
	"abarow.com/services/headquarters/catalog/internal/domain"
)

func main() {
	// The images in the file: catalog-admin image-list < catalog.json; each line is
	// "<product code> <image file>" (the install script uploads them one by one).
	if len(os.Args) > 1 && os.Args[1] == "image-list" {
		var items []seedProduct
		if err := json.NewDecoder(os.Stdin).Decode(&items); err != nil {
			fmt.Fprintln(os.Stderr, "catalog-admin: could not read the file:", err)
			os.Exit(1)
		}
		for _, it := range items {
			if it.Image != "" {
				fmt.Println(it.ID, it.Image)
			}
		}
		return
	}
	// Image: catalog-admin image <product code> [person] < image.jpg
	if len(os.Args) > 2 && os.Args[1] == "image" {
		by := "import"
		if len(os.Args) > 3 {
			by = os.Args[3]
		}
		if err := setImage(os.Stdin, os.Args[2], by); err != nil {
			fmt.Fprintln(os.Stderr, "catalog-admin:", err)
			os.Exit(1)
		}
		fmt.Println(os.Args[2] + ": image uploaded; going down to the registers")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "import" {
		by := "import"
		if len(os.Args) > 2 {
			by = os.Args[2]
		}
		n, err := importCatalog(os.Stdin, by)
		if err != nil {
			fmt.Fprintln(os.Stderr, "catalog-admin:", err)
			os.Exit(1)
		}
		fmt.Printf("%d products loaded; going down to the registers\n", n)
		return
	}
	healthAddr := service.Env("ABAROW_CATALOG_ADMIN_HEALTH_ADDR", ":8081")
	service.Main("catalog-admin", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		dsn, err := databaseURL()
		if err != nil {
			return err
		}
		svc, db, err := open(ctx, log, dsn)
		if err != nil {
			return err
		}
		defer db.Close()

		natsOpts := []nats.Option{nats.Name("abarow-catalog-admin"), nats.MaxReconnects(-1), nats.RetryOnFailedConnect(true)}
		// Identity: the service's NKey (tools/natsauth -role headquarters produces it).
		if seed := os.Getenv("ABAROW_NATS_NKEY"); seed != "" {
			opt, err := nats.NkeyOptionFromSeed(seed)
			if err != nil {
				return fmt.Errorf("could not read the NATS key: %w", err)
			}
			natsOpts = append(natsOpts, opt)
		}
		nc, err := nats.Connect(service.Env("ABAROW_NATS_URL", "nats://nats:4222"), natsOpts...)
		if err != nil {
			return err
		}
		defer nc.Close()
		js, err := jetstream.New(nc)
		if err != nil {
			return err
		}
		// Changes are recorded even while NATS is down; they wait in the outbox and are
		// published in order once NATS comes.
		// On the store server (ABAROW_CATALOG_STREAM=shared) report sets up the stream.
		pub := natspub.New(js)
		shared := service.Env("ABAROW_CATALOG_STREAM", "") == "shared"
		if shared {
			pub = natspub.Shared(js)
		}
		// Retention (docs/decisions/RETENTION.md): the published outbox messages go.
		keep, err := retention.ConfigFromEnv(os.Getenv)
		if err != nil {
			return err
		}
		work := leader.All(
			outbox.NewRelay(db, pub, time.Second, log).Run,
			retention.Runner{Interval: keep.Interval, Delay: time.Minute, Next: keep.Daily(), Log: log, Jobs: []retention.Job{
				{Name: "outbox", Keep: keep.Outbox, Clean: db.PruneOutbox},
			}}.Run,
		)
		if shared {
			// The store server is one machine.
			go work(ctx)
		} else {
			// At headquarters on three machines one copy publishes the outbox (pkg/leader);
			// the screen's requests are served by every copy.
			go leader.Leader{DB: db, Key: leader.Key("catalog-admin"), Name: "catalog-admin", Log: log}.Run(ctx, work)
		}

		// Roles with write permission: administrator and catalog at headquarters; the store manager
		// in the back house (ABAROW_CATALOG_WRITERS=manager).
		var writers []string
		for _, r := range strings.Split(os.Getenv("ABAROW_CATALOG_WRITERS"), ",") {
			if r = strings.TrimSpace(r); r != "" {
				writers = append(writers, r)
			}
		}
		return service.ServeHTTP(ctx, log, service.HTTPConfig{
			Handler:    httpapi.New(svc, writers...),
			Addr:       service.Env("ABAROW_CATALOG_ADMIN_ADDR", ":8080"),
			HealthAddr: healthAddr,
			Health:     db.Ping,
		})
	})
}

// databaseURL is the PostgreSQL address; since it contains the password it comes from an
// environment variable (a secret at install).
func databaseURL() (string, error) {
	dsn := os.Getenv("ABAROW_DATABASE_URL")
	if dsn == "" {
		return "", errors.New("ABAROW_DATABASE_URL is required: the PostgreSQL address (postgres://user:password@server/database)")
	}
	return dsn, nil
}

// open opens the database (retries until it opens; a migration error does not get
// better by waiting) and builds the service.
func open(ctx context.Context, log *slog.Logger, dsn string) (*app.Service, *postgres.Store, error) {
	loc, err := time.LoadLocation(service.Env("ABAROW_TZ", "Europe/Istanbul"))
	if err != nil {
		return nil, nil, err
	}
	// On the store server catalog-admin works in its own schema (ABAROW_DB_SCHEMA): it does not
	// collide with report's tables of the same name.
	schema := os.Getenv("ABAROW_DB_SCHEMA")
	base := dsn
	if schema != "" {
		scoped, err := postgres.WithSearchPath(dsn, schema)
		if err != nil {
			return nil, nil, err
		}
		dsn = scoped
	}
	last := ""
	for {
		var db *postgres.Store
		var err error
		if schema != "" {
			err = postgres.EnsureSchema(ctx, base, schema)
		}
		if err == nil {
			db, err = postgres.Open(ctx, dsn)
		}
		if err == nil {
			enc := eventproto.Encoder{NewID: func() string { return uuid.Must(uuid.NewV7()).String() }}
			return app.NewService(db, enc, loc), db, nil
		}
		if errors.Is(err, postgres.ErrMigration) || log == nil {
			return nil, nil, err
		}
		if err.Error() != last {
			log.Warn("could not connect to the database; will retry", "err", err)
			last = err.Error()
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// seedProduct is a product in the registers' initial file (the same format as services/core/catalog):
// unit_price_kurus is the price valid from the beginning; prices are the future-dated
// prices.
type seedProduct struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Unit      string      `json:"unit"`
	UnitPrice money.Money `json:"unit_price_kurus"`
	TaxRate   money.Rate  `json:"tax_rate_bp"`
	Barcodes  []string    `json:"barcodes"`
	QuickKey  bool        `json:"quick_key"`
	// Restaurant menu (see pkg/menu).
	Category  string       `json:"category"`
	MenuOrder int          `json:"menu_order"`
	Station   string       `json:"station"`
	Options   []menu.Group `json:"option_groups"`
	// Image file (relative to the file's folder); import does not upload it, image-list
	// lists it.
	Image  string `json:"image"`
	Prices []struct {
		UnitPrice money.Money `json:"unit_price_kurus"`
		From      time.Time   `json:"from"`
	} `json:"prices"`
}

// importCatalog loads the initial file to headquarters.
func importCatalog(r io.Reader, by string) (int, error) {
	var items []seedProduct
	if err := json.NewDecoder(r).Decode(&items); err != nil {
		return 0, fmt.Errorf("could not read the file (it must be a list in the catalog.json format): %w", err)
	}
	products := make([]domain.Product, len(items))
	for i, it := range items {
		p := domain.Product{Code: it.ID, Name: it.Name, Unit: domain.Unit(it.Unit), TaxRate: it.TaxRate, Barcodes: it.Barcodes,
			QuickKey: it.QuickKey, Prices: []domain.Price{{Amount: it.UnitPrice}},
			Category: it.Category, MenuOrder: it.MenuOrder, Station: it.Station, Options: it.Options}
		for _, sp := range it.Prices {
			if sp.From.IsZero() {
				return 0, fmt.Errorf("%s: the effective moment (from) of the future-dated price is missing", it.ID)
			}
			p.Prices = append(p.Prices, domain.Price{Amount: sp.UnitPrice, From: sp.From})
		}
		products[i] = p
	}
	dsn, err := databaseURL()
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	svc, db, err := open(ctx, nil, dsn)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	return svc.Import(ctx, by, products)
}

// setImage uploads the product's image from standard input (JPEG, PNG or
// WebP; at most menu.MaxImageBytes). Unlike uploading from the screen the image is not
// shrunk: the file must be prepared beforehand.
func setImage(r io.Reader, code, by string) error {
	data, err := io.ReadAll(io.LimitReader(r, menu.MaxImageBytes+1))
	if err != nil {
		return err
	}
	dsn, err := databaseURL()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	svc, db, err := open(ctx, nil, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	p, err := svc.Get(ctx, code)
	if err != nil {
		return err
	}
	_, err = svc.SetImage(ctx, by, code, p.Version, data)
	return err
}
