// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command sales is the composition root of the sales service: it builds the adapters
// and connects them to the ports.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	salesv1 "abarow.com/gen/com/abarow/sales/v1"

	"abarow.com/pkg/delivery"
	"abarow.com/pkg/service"
	"abarow.com/services/core/sales/internal/adapters/in/grpcserver"
	"abarow.com/services/core/sales/internal/adapters/out/eventproto"
	"abarow.com/services/core/sales/internal/adapters/out/fiscalgrpc"
	"abarow.com/services/core/sales/internal/adapters/out/natspub"
	"abarow.com/services/core/sales/internal/adapters/out/paymentgrpc"
	"abarow.com/services/core/sales/internal/adapters/out/postgres"
	"abarow.com/services/core/sales/internal/app"
)

func main() {
	healthAddr := service.Env("ABAROW_SALES_HEALTH_ADDR", ":8081")
	service.Main("sales", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		storeID, err := service.MustEnv("ABAROW_STORE_ID")
		if err != nil {
			return err
		}
		registerID, err := service.MustEnv("ABAROW_REGISTER_ID")
		if err != nil {
			return err
		}

		dsn, err := service.MustEnv("ABAROW_DATABASE_URL")
		if err != nil {
			return err
		}
		store, err := postgres.Open(ctx, dsn)
		if err != nil {
			return err
		}
		defer store.Close()

		// The services talk in the same register's container network; TLS will be added to the
		// connections between tiers (store server, headquarters).
		paymentConn, err := service.Dial(service.Env("ABAROW_PAYMENT_ADDR", "payment:9000"))
		if err != nil {
			return err
		}
		defer paymentConn.Close()
		fiscalConn, err := service.Dial(service.Env("ABAROW_FISCAL_ADDR", "fiscal:9000"))
		if err != nil {
			return err
		}
		defer fiscalConn.Close()

		svc, err := app.NewService(store, paymentgrpc.New(paymentConn), fiscalgrpc.New(fiscalConn), app.Config{
			StoreID:    storeID,
			RegisterID: registerID,
			NewID:      func() string { return uuid.Must(uuid.NewV7()).String() },
			Encoder:    eventproto.Encoder{},
			// If the fiscal device charges the card from its own POS (Beko X30TR):
			// ABAROW_FISCAL_COLLECTS_CARDS=1.
			FiscalCollects: os.Getenv("ABAROW_FISCAL_COLLECTS_CARDS") == "1",
		})
		if err != nil {
			return err
		}

		// Outbox relay: sales continue while NATS cannot be reached, the events wait in the
		// outbox; they are published in order once the connection comes.
		natsOpts := []nats.Option{nats.Name("abarow-sales"), nats.MaxReconnects(-1), nats.RetryOnFailedConnect(true)}
		// Identity: the service's NKey (tools/natsauth produces it). The NATS server enforces
		// per-service permission; if no key is given it connects without an identity.
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
		pub, err := natspub.New(nc)
		if err != nil {
			return err
		}
		// Delivery receipt (pkg/delivery): when the store server processes an event into the database it
		// gives a receipt; an old event in the register stream is deleted only if proven
		// by its receipt. If there is no store server (ABAROW_RECEIPT_SOURCE is empty)
		// no event is deleted.
		js, err := jetstream.New(nc)
		if err != nil {
			return err
		}
		if err := (delivery.Sender{
			JS: js, Owner: registerID, Source: os.Getenv("ABAROW_RECEIPT_SOURCE"), Stream: natspub.StreamName,
			Durable: "sales-receipts", Save: store.SaveReceived, Received: store.Received, DefaultMaxAge: 30 * 24 * time.Hour,
		}).Start(ctx, os.Getenv, log); err != nil {
			return err
		}
		interval, err := time.ParseDuration(service.Env("ABAROW_SALES_RELAY_INTERVAL", "500ms"))
		if err != nil {
			return err
		}
		relay := app.NewRelay(store, pub, interval, log).Sequenced("sales")
		relayDone := make(chan struct{})
		go func() {
			relay.Run(ctx)
			close(relayDone)
		}()

		srv := service.NewGRPCServer()
		salesv1.RegisterSalesServiceServer(srv, grpcserver.New(svc, relay))
		err = service.Serve(ctx, log, service.ServeConfig{
			GRPC:       srv,
			GRPCAddr:   service.Env("ABAROW_SALES_GRPC_ADDR", ":9000"),
			HealthAddr: healthAddr,
			Health:     store.Ping,
		})
		<-relayDone
		return err
	})
}
