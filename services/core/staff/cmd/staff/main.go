// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command staff is the composition root of the cashier and end-of-day service.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"
	_ "time/tzdata" // scratch image'da saat dilimi verisi yok

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	devicev1 "abarow.com/gen/com/abarow/device/v1"
	salesv1 "abarow.com/gen/com/abarow/sales/v1"
	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/pkg/delivery"
	"abarow.com/pkg/outbox"
	"abarow.com/pkg/retention"
	"abarow.com/pkg/service"
	"abarow.com/services/core/staff/internal/adapters/in/grpcserver"
	"abarow.com/services/core/staff/internal/adapters/in/natscashiers"
	"abarow.com/services/core/staff/internal/adapters/in/natsconsumer"
	"abarow.com/services/core/staff/internal/adapters/out/bcrypthasher"
	"abarow.com/services/core/staff/internal/adapters/out/device"
	"abarow.com/services/core/staff/internal/adapters/out/eventproto"
	"abarow.com/services/core/staff/internal/adapters/out/natsbacklog"
	"abarow.com/services/core/staff/internal/adapters/out/natspub"
	"abarow.com/services/core/staff/internal/adapters/out/postgres"
	"abarow.com/services/core/staff/internal/adapters/out/salesclient"
	"abarow.com/services/core/staff/internal/app"
)

// cleanMaxAge is the least time an event stays in the shift stream after its receipt
// (ABAROW_CLEAN_MAX_AGE changes it).
const cleanMaxAge = 30 * 24 * time.Hour

func main() {
	healthAddr := service.Env("ABAROW_STAFF_HEALTH_ADDR", ":8081")
	service.Main("staff", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		dsn, err := service.MustEnv("ABAROW_DATABASE_URL")
		if err != nil {
			return err
		}
		receiptSource := os.Getenv("ABAROW_RECEIPT_SOURCE")
		keep, err := retention.ConfigFromEnv(os.Getenv)
		if err != nil {
			return err
		}
		clean, err := delivery.CleanConfigFromEnv(os.Getenv, cleanMaxAge)
		if err != nil {
			return err
		}
		if err := keep.CheckReceipts(clean.MaxAge); err != nil {
			return err
		}
		store, err := postgres.Open(ctx, dsn)
		if err != nil {
			return err
		}
		defer store.Close()

		ttl, err := time.ParseDuration(service.Env("ABAROW_STAFF_SESSION_TTL", "12h"))
		if err != nil {
			return err
		}
		auth := app.NewAuth(store, bcrypthasher.Hasher{}, ttl)
		// The cashiers are loaded from the JSON file in the install (first install and
		// simulation); at a register connected to a store server the cashiers managed on the
		// store server screen come from there and the file does not change them.
		if path := os.Getenv("ABAROW_STAFF_SEED"); path != "" {
			n, skipped, err := seedCashiers(ctx, auth, path)
			if err != nil {
				return err
			}
			log.Info("cashiers loaded", "file", path, "cashiers", n-len(skipped))
			if len(skipped) > 0 {
				log.Info("cashiers managed on the store server were not changed from the file", "cashiers", skipped)
			}
		}

		// The sale events come from NATS; while NATS is down the reports work with the
		// existing data, the events are processed once the connection comes.
		natsOpts := []nats.Option{nats.Name("abarow-staff"), nats.MaxReconnects(-1), nats.RetryOnFailedConnect(true)}
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
		js, err := jetstream.New(nc)
		if err != nil {
			return err
		}
		// At a register connected to a store server the cashiers are managed on the store server
		// screen and come down from there (ABAROW_CASHIERS_SOURCE: the store server's JetStream
		// domain, that is, the store id).
		if source := os.Getenv("ABAROW_CASHIERS_SOURCE"); source != "" {
			go natscashiers.New(js, auth, source, log).Run(ctx)
		}

		// The end of day and closing a shift are not done while there is a sale whose receipt
		// could not be printed; they wait until the pending sale events (first sales' outbox,
		// then the staff consumer) are processed.
		salesConn, err := service.Dial(service.Env("ABAROW_SALES_ADDR", "sales:9000"))
		if err != nil {
			return err
		}
		defer salesConn.Close()
		wait, err := time.ParseDuration(service.Env("ABAROW_STAFF_EVENT_WAIT", "5s"))
		if err != nil {
			return err
		}
		salesClient := salesv1.NewSalesServiceClient(salesConn)
		events := app.Events{
			Charged: salesclient.Charged{Client: salesClient},
			Sources: []app.EventBacklog{
				salesclient.Outbox{Client: salesClient},
				natsbacklog.Backlog{JS: js},
			},
			Timeout:  wait,
			Interval: 200 * time.Millisecond,
		}
		svc := app.NewService(store, store, store, events)

		consumerDone := make(chan struct{})
		go func() {
			natsconsumer.New(js, svc, log).Run(ctx)
			close(consumerDone)
		}()

		// The reports are printed on device's receipt printer (times in the register's time
		// zone); opening the drawer without a sale opens device's drawer.
		loc, err := time.LoadLocation(service.Env("ABAROW_TZ", "Europe/Istanbul"))
		if err != nil {
			return err
		}
		var printer app.Printer = device.None{}
		shifts := app.NewShiftService(store, store, events)
		// The shift events (opening, closing) are written to the outbox in the same
		// transaction as the shift record, the relay passes them on to the message path; the back
		// house sees the cashier on the shift and the cash difference from here. At an installed
		// register the image updates itself but the unit file is not updated: if the
		// identities are missing staff works but publishes no events.
		if storeID, registerID := os.Getenv("ABAROW_STORE_ID"), os.Getenv("ABAROW_REGISTER_ID"); storeID != "" && registerID != "" {
			pub, err := natspub.New(nc)
			if err != nil {
				return err
			}
			shifts.Announcer = eventproto.Encoder{StoreID: storeID, RegisterID: registerID,
				NewID: func() string { return uuid.Must(uuid.NewV7()).String() }}
			go outbox.NewRelay(store, pub, 500*time.Millisecond, log).Sequenced("staff").Run(ctx)
			// Delivery receipt (pkg/delivery): when the store server processes a shift event into the
			// database it gives a receipt; an old event in the shift stream is deleted only if
			// proven by its receipt. If there is no store server
			// (ABAROW_RECEIPT_SOURCE is empty) no event is deleted.
			js, err := jetstream.New(nc)
			if err != nil {
				return err
			}
			if err := (delivery.Sender{
				JS: js, Owner: registerID, Source: receiptSource, Stream: natspub.StreamName,
				Durable: "staff-receipts", Save: store.SaveReceived, Received: store.Received, DefaultMaxAge: cleanMaxAge,
			}).Start(ctx, os.Getenv, log); err != nil {
				return err
			}
			// Retention (docs/decisions/RETENTION.md): the details of old days go once the
			// store server confirmed the shift events; without a store server nothing goes.
			if receiptSource != "" {
				go retention.Runner{Interval: keep.Interval, Delay: time.Minute, Next: keep.Daily(), Log: log, Jobs: []retention.Job{
					{Name: "outbox", Keep: keep.Outbox, Clean: store.PruneOutbox},
					{Name: "day details", Keep: keep.Register, Clean: store.PruneDays},
					{Name: "received receipts", Keep: keep.Receipts, Clean: store.PruneReceipts},
				}}.Run(ctx)
			}
		} else {
			log.Warn("ABAROW_STORE_ID and ABAROW_REGISTER_ID are missing: shift events are not published (it opens when the install script is run again)")
		}
		if addr := os.Getenv("ABAROW_DEVICE_ADDR"); addr != "" {
			deviceConn, err := service.Dial(addr)
			if err != nil {
				return err
			}
			defer deviceConn.Close()
			client := devicev1.NewDeviceServiceClient(deviceConn)
			printer = device.Printer{Client: client}
			shifts.Drawer = device.Drawer{Client: client}
		}

		srv := service.NewGRPCServer()
		staffv1.RegisterStaffServiceServer(srv, grpcserver.New(svc, auth, shifts, app.NewPrintService(svc, shifts, printer, loc)))
		err = service.Serve(ctx, log, service.ServeConfig{
			GRPC:       srv,
			GRPCAddr:   service.Env("ABAROW_STAFF_GRPC_ADDR", ":9000"),
			HealthAddr: healthAddr,
			Health:     store.Ping,
		})
		<-consumerDone
		return err
	})
}

func seedCashiers(ctx context.Context, auth *app.Auth, path string) (int, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, err
	}
	var seeds []app.SeedCashier
	if err := json.Unmarshal(data, &seeds); err != nil {
		return 0, nil, fmt.Errorf("%s okunamadı: %w", path, err)
	}
	skipped, err := auth.Seed(ctx, seeds)
	return len(seeds), skipped, err
}
