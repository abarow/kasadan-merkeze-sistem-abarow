// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// inventory is the stock service. The same program runs in two scopes:
//
// The storage is PostgreSQL in both scopes (ABAROW_DATABASE_URL; decision
// 04.11.2026):
//
//   - store (if ABAROW_STORE_ID is given; the store server): the store's identity.
//     Sales are deducted from stock, the movements go to headquarters;
//   - headquarters (otherwise): the headquarters warehouse and the stock of all stores,
//     the stores' movements are processed from the headquarters stream, transfers
//     are forwarded to the stores.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/leader"
	"abarow.com/pkg/natsio"
	"abarow.com/pkg/nodes"
	"abarow.com/pkg/outbox"
	"abarow.com/pkg/retention"
	"abarow.com/pkg/service"
	"abarow.com/pkg/stock"
	"abarow.com/services/inventory/internal/adapters/in/httpapi"
	"abarow.com/services/inventory/internal/adapters/in/natsconsumer"
	"abarow.com/services/inventory/internal/adapters/out/eventproto"
	"abarow.com/services/inventory/internal/adapters/out/natspub"
	"abarow.com/services/inventory/internal/adapters/out/postgres"
	"abarow.com/services/inventory/internal/app"
)

// backend is the storage and connection steps of the chosen scope.
type backend struct {
	store interface {
		app.Store
		outbox.Store
		Close() error
		Ping(context.Context) error
		PruneOutbox(ctx context.Context, before time.Time) (int64, error)
		Conn(ctx context.Context) (*sql.Conn, error)
	}
	svc *app.Service
	// headquarters: at headquarters on three machines one copy reads the consumers and
	// publishes the outbox (pkg/leader).
	headquarters bool
	// start prepares the scope at start (every copy).
	start func(ctx context.Context) error
	// consumers are the consumers specific to the scope.
	consumers func(js jetstream.JetStream, log *slog.Logger) []func(ctx context.Context)
	// publisher is the outbox publisher of the scope (the stream depends on the scope).
	publisher func(js jetstream.JetStream) *natsio.Publisher
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "replay" {
		if err := replay(); err != nil {
			fmt.Fprintln(os.Stderr, "inventory:", err)
			os.Exit(1)
		}
		return
	}
	healthAddr := service.Env("ABAROW_INVENTORY_HEALTH_ADDR", ":8081")
	service.Main("inventory", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		newID := func() string { return uuid.Must(uuid.NewV7()).String() }
		dsn := os.Getenv("ABAROW_DATABASE_URL")
		if dsn == "" {
			return errors.New("ABAROW_DATABASE_URL gerekli: PostgreSQL adresi (postgres://kullanıcı:parola@server/veritabanı)")
		}
		var b backend
		var err error
		if storeID := os.Getenv("ABAROW_STORE_ID"); storeID != "" {
			b, err = shop(ctx, dsn, storeID, newID)
		} else {
			b, err = headquarters(ctx, dsn, newID)
		}
		if err != nil {
			return err
		}
		defer b.store.Close()

		// It is retried on an authorization error too: while the NATS configuration is
		// regenerated (a new user, key) the connection is not closed for good.
		natsOpts := []nats.Option{nats.Name("abarow-inventory"), nats.MaxReconnects(-1), nats.RetryOnFailedConnect(true), nats.IgnoreAuthErrorAbort()}
		// Identity: the service's NKey (tools/natsauth produces it).
		if seed := os.Getenv("ABAROW_NATS_NKEY"); seed != "" {
			opt, err := nats.NkeyOptionFromSeed(seed)
			if err != nil {
				return fmt.Errorf("NATS anahtarı okunamadı: %w", err)
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
		if err := b.start(ctx); err != nil {
			return err
		}
		// Retention (docs/decisions/RETENTION.md): the published outbox messages go.
		keep, err := retention.ConfigFromEnv(os.Getenv)
		if err != nil {
			return err
		}
		work := leader.All(append(b.consumers(js, log),
			// Movements are recorded even while NATS is down; they wait in the outbox.
			outbox.NewRelay(b.store, b.publisher(js), time.Second, log).Run,
			retention.Runner{Interval: keep.Interval, Delay: time.Minute, Next: keep.Daily(), Log: log, Jobs: []retention.Job{
				{Name: "outbox", Keep: keep.Outbox, Clean: b.store.PruneOutbox},
			}}.Run)...)
		if b.headquarters {
			go leader.Leader{DB: b.store, Key: leader.Key("inventory"), Name: "inventory", Log: log}.Run(ctx, work)
		} else {
			// The store server is one machine.
			go work(ctx)
		}

		return service.ServeHTTP(ctx, log, service.HTTPConfig{
			Handler:    httpapi.New(b.svc),
			Addr:       service.Env("ABAROW_INVENTORY_ADDR", ":8080"),
			HealthAddr: healthAddr,
			Health:     b.store.Ping,
		})
	})
}

// headquarters is the headquarters scope.
func headquarters(ctx context.Context, dsn string, newID func() string) (backend, error) {
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		return backend{}, err
	}
	// The stores connected to headquarters (natsauth manages them); these and the
	// headquarters warehouse are the places a transfer can be sent to. When a store is
	// added the install script restarts the service.
	nodeList, err := nodes.Load(service.Env("ABAROW_STORES", "/config/stores.json"))
	if err != nil {
		store.Close()
		return backend{}, err
	}
	stores := make([]stock.Location, 0, len(nodeList))
	for _, n := range nodeList {
		stores = append(stores, stock.Location{ID: n.ID, Name: n.DisplayName()})
	}
	svc := app.NewService(store, eventproto.Encoder{NewID: newID, Now: time.Now},
		app.Config{Here: app.Warehouse, Headquarters: true, Stores: stores}, newID, time.Now)
	return backend{store: store, svc: svc, headquarters: true,
		// Every copy publishes the place list at start: the stream keeps only the last one.
		start: svc.PublishLocations,
		consumers: func(js jetstream.JetStream, log *slog.Logger) []func(ctx context.Context) {
			return []func(ctx context.Context){natsconsumer.Movements(js, svc, log).Run, natsconsumer.Catalog(js, svc, log).Run}
		},
		publisher: natspub.New}, nil
}

// shop is the store scope (the store server).
func shop(ctx context.Context, dsn, storeID string, newID func() string) (backend, error) {
	// Stock is served and entered from the database even while NATS is down.
	db, err := postgres.Open(ctx, dsn)
	if err != nil {
		return backend{}, err
	}
	store := db.Shop(storeID)
	svc := app.NewService(store, eventproto.Encoder{StoreID: storeID, NewID: newID, Now: time.Now},
		app.Config{Here: storeID, Locations: store}, newID, time.Now)
	return backend{store: store, svc: svc,
		start: func(context.Context) error { return nil },
		consumers: func(js jetstream.JetStream, log *slog.Logger) []func(ctx context.Context) {
			jobs := []func(ctx context.Context){natsconsumer.Sales(js, svc, log).Run}
			// In a store connected to headquarters the products come from headquarters' catalog
			// (in an unconnected one they are learned by name from the sale lines);
			// transfers and the places that can be sent to come down from headquarters
			// (ABAROW_CATALOG_SOURCE: headquarters' JetStream domain).
			if source := os.Getenv("ABAROW_CATALOG_SOURCE"); source != "" {
				jobs = append(jobs, natsconsumer.Catalog(js, svc, log).Run, natsconsumer.Transfers(js, svc, source, storeID, log).Run)
			}
			return jobs
		},
		publisher: natspub.NewShop}, nil
}

// replay deletes the sales consumer of the store stock; the running inventory reads the
// store stream from the start (sales are deduplicated by id: an existing movement
// is skipped, the stock deduction of missing sales is added). It is run after the database
// has been restored from a backup; goods receipts and counts exist only in the database,
// so those after the backup do not come back. It exists only in the store scope
// (the store server).
func replay() error {
	existed, err := natsio.Replay(context.Background(), "abarow-inventory", natsconsumer.StoreStream, natsconsumer.Durable)
	switch {
	case err != nil:
		return err
	case !existed:
		fmt.Println("the consumer does not exist yet; inventory will read the store stream from the start when it starts")
	default:
		fmt.Println("consumer deleted; inventory is reading the store stream from the start (existing movements are skipped, missing sales are deducted)")
	}
	return nil
}
