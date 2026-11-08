// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command ingest is the composition root of headquarters' event intake service: it builds
// the headquarters stream from the stores' streams, writes the events to PostgreSQL,
// and serves the stores' connection state.
//
// When the database is restored from a backup, the events that came after the backup
// stay in the headquarters stream (90 days) but ingest has passed them. replay deletes ingest's
// durable consumer: the running ingest rebuilds the consumer and reads the
// stream from the start; existing events are skipped by event id, missing ones are
// added.
//
//	podman exec abarow-ingest /service replay
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/delivery"
	"abarow.com/pkg/leader"
	"abarow.com/pkg/natsio"
	"abarow.com/pkg/nodes"
	"abarow.com/pkg/outbox"
	"abarow.com/pkg/retention"
	"abarow.com/pkg/service"
	"abarow.com/services/headquarters/ingest/internal/adapters/in/httpapi"
	"abarow.com/services/headquarters/ingest/internal/adapters/in/natsconsumer"
	"abarow.com/services/headquarters/ingest/internal/adapters/out/natsstream"
	"abarow.com/services/headquarters/ingest/internal/adapters/out/postgres"
	"abarow.com/services/headquarters/ingest/internal/app"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "replay" {
		if err := replay(); err != nil {
			fmt.Fprintln(os.Stderr, "ingest:", err)
			os.Exit(1)
		}
		return
	}
	healthAddr := service.Env("ABAROW_INGEST_HEALTH_ADDR", ":8081")
	service.Main("ingest", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		// Since it contains the password the address comes from an environment variable
		// (a secret at install).
		dsn := os.Getenv("ABAROW_DATABASE_URL")
		if dsn == "" {
			return errors.New("ABAROW_DATABASE_URL gerekli: PostgreSQL adresi (postgres://kullanıcı:parola@server/veritabanı)")
		}
		// The connected stores (written by natsauth -add-store). When the list changes the service
		// is restarted.
		stores, err := nodes.Load(service.Env("ABAROW_STORES", "/config/stores.json"))
		if err != nil {
			return err
		}
		keep, err := retention.ConfigFromEnv(os.Getenv)
		if err != nil {
			return err
		}
		db, err := openDB(ctx, log, dsn)
		if err != nil {
			return err
		}
		defer db.Close()

		nc, err := connectNATS(nats.MaxReconnects(-1), nats.RetryOnFailedConnect(true))
		if err != nil {
			return err
		}
		defer nc.Close()
		js, err := jetstream.New(nc)
		if err != nil {
			return err
		}

		svc := app.NewService(natsstream.New(js), db, stores)
		// Receipts: they are written to the outbox in the same transaction the event is processed into the database; the store
		// does not delete an old event without seeing the receipt (pkg/delivery). They need not be instant;
		// while NATS is down the receipts wait in the outbox.
		receiptAge, err := delivery.ReceiptMaxAgeFromEnv(os.Getenv)
		if err != nil {
			return err
		}
		receiptCfg := delivery.ReceiptConfig("", "", receiptAge)
		// The events are read in order by one copy: at headquarters on three machines every
		// machine runs ingest, the copy holding the lock works (pkg/leader). The connection
		// state (HTTP) is served by every copy.
		work := leader.All(
			// The service starts even while NATS is down; the headquarters stream is
			// set up once NATS comes, and the consumer waits for the stream too.
			func(ctx context.Context) {
				if err := svc.SetupStream(ctx, log); err != nil && ctx.Err() == nil {
					log.Error("could not set up the headquarters stream", "err", err)
				}
			},
			natsconsumer.New(js, svc, log, natsconsumer.WithSequences(db)).Run,
			outbox.NewRelay(db, natsio.NewPublisher(js, &receiptCfg), time.Second, log).Run,
			func(ctx context.Context) { svc.PublishLinks(ctx, log) },
			// Retention (docs/decisions/RETENTION.md): the sales ledger is never removed; the
			// published receipts, the old given receipts and the raw event content are.
			retention.Runner{Interval: keep.Interval, Delay: time.Minute, Log: log, Jobs: []retention.Job{
				{Name: "receipt outbox", Keep: keep.Outbox, Clean: db.PruneOutbox},
				{Name: "given receipts", Keep: keep.Receipts, Clean: db.PruneReceipts},
				{Name: "event content", Keep: keep.EventPayload, Clean: db.PruneEventPayloads},
			}}.Run,
		)
		go leader.Leader{DB: db, Key: leader.Key("ingest"), Name: "ingest", Log: log}.Run(ctx, work)
		return service.ServeHTTP(ctx, log, service.HTTPConfig{
			Handler:    httpapi.New(svc),
			Addr:       service.Env("ABAROW_INGEST_ADDR", ":8080"),
			HealthAddr: healthAddr,
			Health:     db.Ping,
		})
	})
}

// openDB tries until PostgreSQL opens: when headquarters starts the database may be ready
// after the service. A migration error does not get better by waiting.
func openDB(ctx context.Context, log *slog.Logger, dsn string) (*postgres.Store, error) {
	last := ""
	for {
		db, err := postgres.Open(ctx, dsn)
		if err == nil {
			return db, nil
		}
		if errors.Is(err, postgres.ErrMigration) {
			return nil, err
		}
		if err.Error() != last {
			log.Warn("could not connect to the database; will retry", "err", err)
			last = err.Error()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// connectNATS connects to NATS with the service's identity (tools/natsauth -role headquarters
// produces it).
func connectNATS(opts ...nats.Option) (*nats.Conn, error) {
	opts = append([]nats.Option{nats.Name("abarow-ingest")}, opts...)
	if seed := os.Getenv("ABAROW_NATS_NKEY"); seed != "" {
		opt, err := nats.NkeyOptionFromSeed(seed)
		if err != nil {
			return nil, fmt.Errorf("NATS anahtarı okunamadı: %w", err)
		}
		opts = append(opts, opt)
	}
	return nats.Connect(service.Env("ABAROW_NATS_URL", "nats://nats:4222"), opts...)
}

// replay deletes ingest's durable consumer; the running ingest reads the headquarters
// stream from the start.
func replay() error {
	existed, err := natsio.Replay(context.Background(), "abarow-ingest", natsstream.StreamName, natsconsumer.Durable)
	switch {
	case err != nil:
		return err
	case !existed:
		fmt.Println("the consumer does not exist yet; ingest will read the headquarters stream from the start when it starts")
	default:
		fmt.Println("consumer deleted; ingest is reading the headquarters stream from the start (existing events are skipped, missing ones are added)")
	}
	return nil
}
