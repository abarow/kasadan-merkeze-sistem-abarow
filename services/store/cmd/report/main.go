// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command report is the composition root of the store server's store reports service: it
// builds the store stream from the registers' sales streams, processes the sale events into the
// report database, and serves the store reports and the registers' state (the API and the store
// server screen).
//
// The report, the sign-in endpoints and the screen shell are in the core shared with headquarters
// (pkg/report, pkg/login); only what is specific to the store server stays in this
// service: the registers' state, cashiers, store prices, the message path and
// the manager PIN / store password sign-in.
//
// Managers sign in to the screen with their own code and PIN; while no manager is defined,
// with the store password. The service takes the bcrypt digest of the password from
// ABAROW_REPORT_PASSWORD_HASH. To produce the digest the password is given on
// standard input:
//
//	echo 'store password' | report hash-password
//
// Once the registers' cashier file (cashiers.json) is loaded into the store server the
// cashiers are managed on the store server screen; the file is given on standard input and
// written to the running service's database (the service publishes the changes to the
// registers and to headquarters):
//
//	report import-cashiers < cashiers.json
//
// While a manager with an open account exists the store password is off. If a manager forgets their PIN
// another manager gives them a new PIN from the screen; if there is no other manager the PIN is
// changed from the command line (the new PIN on standard input):
//
//	echo 2468 | report set-pin ayse
//
// When the database is restored from a backup, the sales between the backup and now are
// missing from the report. replay deletes report's durable consumer; the running
// report reads the store stream from the start (existing events are skipped, missing ones
// are added):
//
//	report replay
//
// With the corporate sign-in (ABAROW_LOGIN=oidc or local,oidc; the settings are in
// pkg/login/oidc) a manager is recognized at the first sign-in by the email address. The address is
// given in the Cashiers section; in an install with no local method, for the first manager
// from the command line (giving no address removes it, unlink removes the identity link):
//
//	report set-email ali ali@company.example
//	report unlink ali
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"
	_ "time/tzdata" // scratch image'da saat dilimi verisi yok

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/delivery"
	"abarow.com/pkg/login"
	"abarow.com/pkg/login/oidc"
	"abarow.com/pkg/natsio"
	"abarow.com/pkg/nodes"
	"abarow.com/pkg/outbox"
	"abarow.com/pkg/retention"
	"abarow.com/pkg/service"
	"abarow.com/services/store/internal/adapters/in/httpapi"
	"abarow.com/services/store/internal/adapters/in/natsconsumer"
	"abarow.com/services/store/internal/adapters/out/cashierproto"
	"abarow.com/services/store/internal/adapters/out/natspub"
	"abarow.com/services/store/internal/adapters/out/natsstore"
	"abarow.com/services/store/internal/adapters/out/postgres"
	"abarow.com/services/store/internal/adapters/out/priceproto"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hash-password" {
		if err := hashPassword(); err != nil {
			fmt.Fprintln(os.Stderr, "report:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "import-cashiers" {
		if err := importCashiers(); err != nil {
			fmt.Fprintln(os.Stderr, "report:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "replay" {
		if err := replay(); err != nil {
			fmt.Fprintln(os.Stderr, "report:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "set-email" || os.Args[1] == "unlink") {
		if err := identityCommand(os.Args[1], os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "report:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "pgtune" {
		if err := pgtuneCommand(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "report:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "set-pin" {
		if err := setPIN(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "report:", err)
			os.Exit(1)
		}
		return
	}
	healthAddr := service.Env("ABAROW_REPORT_HEALTH_ADDR", ":8081")
	service.Main("report", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		// Sales figures are a trade secret: the screen does not open without a password.
		hash := os.Getenv("ABAROW_REPORT_PASSWORD_HASH")
		if hash == "" {
			return errors.New("ABAROW_REPORT_PASSWORD_HASH is required: the digest of the store password (report hash-password produces it)")
		}
		// The reports are served from the database even while NATS is down. The sessions are in
		// the database too: they are not lost when the service restarts.
		dsn, err := databaseURL()
		if err != nil {
			return err
		}
		reports, err := openDB(ctx, log, dsn)
		if err != nil {
			return err
		}
		defer reports.Close()
		auth, err := app.NewAuth(hash, reports.People())
		if err != nil {
			return err
		}
		// The connected registers (written by natsauth -add-register). When the list changes the service
		// is restarted.
		regs, err := nodes.Load(service.Env("ABAROW_REGISTERS", "/config/registers.json"))
		if err != nil {
			return err
		}
		// Days are computed in the store's time zone.
		loc, err := time.LoadLocation(service.Env("ABAROW_TZ", "Europe/Istanbul"))
		if err != nil {
			return err
		}

		natsOpts := []nats.Option{nats.Name("abarow-report"), nats.MaxReconnects(-1), nats.RetryOnFailedConnect(true)}
		// Identity: the service's NKey (tools/natsauth -role store produces it).
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

		svc := app.NewService(natsstore.New(js), reports, regs, loc)
		// The service starts even while NATS is down; the store stream is
		// set up once NATS comes, and the consumer waits for the stream too.
		go func() {
			if err := svc.SetupStore(ctx, log); err != nil && ctx.Err() == nil {
				log.Error("could not set up the store stream", "err", err)
			}
		}()
		newID := func() string { return uuid.Must(uuid.NewV7()).String() }
		// Cashiers are managed on the store server screen: they go down to the registers and are
		// reported to headquarters. The messages carry the store id (ABAROW_STORE_ID).
		storeID := os.Getenv("ABAROW_STORE_ID")
		reports.StoreID = storeID
		// The delivery receipt (pkg/delivery): when each event coming from a register is processed the register
		// is given a UUIDv7 receipt; the register does not delete the old event without seeing the receipt.
		// Since the receipt is published with the store id, without an id no receipt is given
		// (no event is deleted at the registers).
		var issuer natsconsumer.Issuer
		if storeID != "" {
			issuer = reports
		}
		go natsconsumer.New(js, svc, log, issuer, natsconsumer.WithSequences(reports)).Run(ctx)
		if err := setupReceipts(ctx, js, reports, storeID, os.Getenv("ABAROW_CATALOG_SOURCE"), log); err != nil {
			return err
		}
		if err := startRetention(ctx, reports, log); err != nil {
			return err
		}
		var cashiers *app.Cashiers
		if storeID != "" {
			cashiers = app.NewCashiers(reports.People(), cashierproto.Encoder{StoreID: storeID, NewID: newID})
			// Managers sign in to the screen with their own code and PIN.
			auth.AllowManagers(app.PeopleDirectory{Store: reports.People()})
			pub := natspub.New(js)
			go func() {
				for ctx.Err() == nil && pub.Ensure(ctx) != nil {
					select {
					case <-ctx.Done():
					case <-time.After(2 * time.Second):
					}
				}
			}()
			// Changes are recorded even while NATS is down; they wait in the outbox.
			go outbox.NewRelay(reports, pub, time.Second, log).Run(ctx)
		} else {
			log.Warn("ABAROW_STORE_ID is missing: cashier management is off (it opens when the install script is run again)")
		}
		// A store server connected to headquarters takes headquarters' catalog into the store for the registers
		// (ABAROW_CATALOG_SOURCE: headquarters' JetStream domain) and manages the store
		// prices (exceptions): it sends them down to the registers and reports them to headquarters.
		var prices *app.Prices
		if source := os.Getenv("ABAROW_CATALOG_SOURCE"); source != "" {
			if storeID == "" {
				return errors.New("ABAROW_STORE_ID is required: the store id of the store server connected to headquarters")
			}
			prices = app.NewPrices(reports, reports, priceproto.Encoder{StoreID: storeID, NewID: newID}, loc)
			go natsconsumer.NewCatalog(js, prices, log).Run(ctx)
		}
		// The catalog stream follows the "headquarters connected" setting: if connected it is fed
		// from headquarters and the products are read-only at the store; otherwise it is
		// managed at the store (catalog-admin). If headquarters is not installed it is always disconnected.
		office := app.NewHeadquarters(reports, natsstore.Catalog{JS: js, Source: os.Getenv("ABAROW_CATALOG_SOURCE"), Log: log}, os.Getenv("ABAROW_CATALOG_SOURCE") != "")
		office.Bind(ctx, log)
		go office.Run(ctx, log)
		// If product and price management at the store (catalog-admin) is installed the screen's
		// catalog requests are forwarded to it.
		var catalog *url.URL
		if s := os.Getenv("ABAROW_CATALOG_ADMIN_URL"); s != "" {
			if catalog, err = url.Parse(s); err != nil {
				return fmt.Errorf("ABAROW_CATALOG_ADMIN_URL is invalid: %w", err)
			}
		}
		// If the stock module (inventory, store scope) is installed the screen's stock requests are
		// forwarded to it.
		var stock *url.URL
		if s := os.Getenv("ABAROW_INVENTORY_URL"); s != "" {
			if stock, err = url.Parse(s); err != nil {
				return fmt.Errorf("ABAROW_INVENTORY_URL is invalid: %w", err)
			}
		}
		// Update (optional): the state of this store server's update agent.
		var updateAgent *url.URL
		if s := os.Getenv("ABAROW_UPDATE_AGENT_URL"); s != "" {
			if updateAgent, err = url.Parse(s); err != nil {
				return fmt.Errorf("ABAROW_UPDATE_AGENT_URL is invalid: %w", err)
			}
		}
		// If the corporate sign-in (OIDC) is configured it is added to the screen; the managers are
		// registered beforehand, the IdP only proves the identity. Since the managers are kept in cashier
		// management the store id is needed.
		var corp *oidc.Corporate
		if cashiers != nil {
			if corp, err = oidc.Configure(os.Getenv, auth.OIDCService(cashiers.People(), httpapi.SessionCookie, log)); err != nil {
				return err
			}
		} else if mode, err := login.ParseMode(os.Getenv("ABAROW_LOGIN")); err != nil {
			return err
		} else if mode.OIDC {
			return errors.New("ABAROW_LOGIN includes oidc but the corporate sign-in needs a manager record: ABAROW_STORE_ID must be defined")
		}
		tlsCert, tlsKey, err := service.TLSFromEnv()
		if err != nil {
			return err
		}
		return service.ServeHTTP(ctx, log, service.HTTPConfig{
			Handler:     httpapi.New(svc, auth, corp, service.Env("ABAROW_REPORT_WEB_DIR", "/web"), prices, cashiers, stock, httpapi.WithCatalog(catalog, office), httpapi.WithUpdate(updateAgent, os.Getenv("ABAROW_UPDATE_APPROVE_SECRET"))),
			Addr:        service.Env("ABAROW_REPORT_ADDR", ":8080"),
			HealthAddr:  healthAddr,
			Health:      reports.Ping,
			TLSCertFile: tlsCert,
			TLSKeyFile:  tlsKey,
		})
	})
}

// setupReceipts sets up the receipt layout. The receipt stream and the publishing of the receipts
// given to the registers always work. If headquarters is defined (source: headquarters'
// JetStream domain) headquarters' receipts are taken too and the old events of the store
// stream are cleaned: only an event proven by the receipt that it was processed into headquarters'
// database is deleted. If there is no headquarters (headquarters is not installed) no event is
// deleted from the store stream.
func setupReceipts(ctx context.Context, js jetstream.JetStream, reports *postgres.Store, storeID, source string, log *slog.Logger) error {
	if storeID == "" {
		return nil
	}
	receiptAge, err := delivery.ReceiptMaxAgeFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	// The receipts given to the registers are published from the outbox to the receipt stream.
	cfg := delivery.ReceiptConfig(source, storeID, receiptAge)
	go outbox.NewRelay(reports.Receipts(), natsio.NewPublisher(js, &cfg), time.Second, log).Run(ctx)
	return delivery.Sender{
		JS: js, Owner: storeID, Source: source, Stream: natsstore.StreamName, Durable: "receipts",
		Save: reports.SaveReceived, Received: reports.Received, DefaultMaxAge: cleanMaxAge,
	}.Start(ctx, os.Getenv, log)
}

// cleanMaxAge is the least time an event stays in the store stream after headquarters'
// receipt (ABAROW_CLEAN_MAX_AGE changes it).
const cleanMaxAge = 90 * 24 * time.Hour

// startRetention removes the published outbox messages and the old receipts at intervals
// (docs/decisions/RETENTION.md). The sales ledger of the store is never removed.
func startRetention(ctx context.Context, reports *postgres.Store, log *slog.Logger) error {
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
	go retention.Runner{Interval: keep.Interval, Delay: time.Minute, Next: keep.Daily(), Log: log, Jobs: []retention.Job{
		{Name: "outbox", Keep: keep.Outbox, Clean: reports.PruneOutbox},
		{Name: "receipts", Keep: keep.Receipts, Clean: reports.PruneReceipts},
	}}.Run(ctx)
	return nil
}

// hashPassword reads the first line on standard input as the store password
// and prints its bcrypt digest.
func hashPassword() error {
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return errors.New("parola standart girdiden verilmeli")
	}
	hash, err := app.HashPassword(strings.TrimRight(line, "\r\n"))
	if err != nil {
		return err
	}
	fmt.Println(hash)
	return nil
}

// cashiersOf opens the cashiers in the running service's database for the command line
// commands; the service publishes the changes to the registers and to headquarters.
func cashiersOf(ctx context.Context) (*app.Cashiers, func(), error) {
	storeID := os.Getenv("ABAROW_STORE_ID")
	if storeID == "" {
		return nil, nil, errors.New("ABAROW_STORE_ID is required: the store server's store id")
	}
	dsn, err := databaseURL()
	if err != nil {
		return nil, nil, err
	}
	reports, err := postgres.Open(ctx, dsn)
	if err != nil {
		return nil, nil, err
	}
	cashiers := app.NewCashiers(reports.People(), cashierproto.Encoder{StoreID: storeID, NewID: func() string { return uuid.Must(uuid.NewV7()).String() }})
	return cashiers, func() { reports.Close() }, nil
}

// importCashiers loads the cashier file on standard input into the store
// server's database.
func importCashiers() error {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return err
	}
	var seeds []app.SeedCashier
	if err := json.Unmarshal(data, &seeds); err != nil {
		return fmt.Errorf("could not read the cashier file: %w", err)
	}
	ctx := context.Background()
	cashiers, done, err := cashiersOf(ctx)
	if err != nil {
		return err
	}
	defer done()
	n, err := cashiers.Import(ctx, "kurulum (dosya)", seeds)
	if err != nil {
		return err
	}
	fmt.Printf("%d cashiers loaded; going down to the registers.\n", n)
	return nil
}

// setPIN changes the cashier's PIN with the first line on standard input:
// the recovery path of the only manager who forgot their PIN (the store password is off
// while a manager exists). "kurulum (komut satırı)" is written to the record.
func setPIN(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: report set-pin <cashier-code> (the new PIN on standard input)")
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return errors.New("yeni PIN standart girdiden verilmeli")
	}
	ctx := context.Background()
	cashiers, done, err := cashiersOf(ctx)
	if err != nil {
		return err
	}
	defer done()
	c, err := cashiers.SetPIN(ctx, "kurulum (komut satırı)", args[0], strings.TrimSpace(line))
	if err != nil {
		return err
	}
	fmt.Printf("%s (%s, %s) PIN changed; going down to the registers.\n", c.Name, c.ID, domain.RoleName(c.Role))
	switch {
	case c.Disabled:
		fmt.Println("Account closed: they cannot sign in at the register or the store server; another manager can open it from the screen.")
	case c.Role != domain.RoleManager:
		fmt.Println("Not a manager: they cannot sign in to the store server screen.")
	}
	return nil
}

// identityCommand changes a person's corporate sign-in record from the command line:
// set-email gives the email address (without an address: removes it), unlink removes the
// identity link. "kurulum (komut satırı)" is written to the record.
func identityCommand(cmd string, args []string) error {
	usage := errors.New("usage: report set-email <cashier-code> [email] | report unlink <cashier-code>")
	if len(args) == 0 || len(args) > 2 || cmd == "unlink" && len(args) != 1 {
		return usage
	}
	ctx := context.Background()
	cashiers, done, err := cashiersOf(ctx)
	if err != nil {
		return err
	}
	defer done()
	const by = "kurulum (komut satırı)"
	if cmd == "unlink" {
		if _, err := cashiers.UnlinkIdentity(ctx, by, args[0]); err != nil {
			return err
		}
		fmt.Println("corporate identity link removed")
		return nil
	}
	email := ""
	if len(args) == 2 {
		email = args[1]
	}
	if _, err := cashiers.SetEmail(ctx, by, args[0], email); err != nil {
		return err
	}
	if email == "" {
		fmt.Println("email address removed")
	} else {
		fmt.Println("email address saved; the manager is recognized by this address at the first corporate sign-in")
	}
	return nil
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

// openDB tries until PostgreSQL is up: when the store server starts the database
// may be ready after the service.
func openDB(ctx context.Context, log *slog.Logger, dsn string) (*postgres.Store, error) {
	last := ""
	for {
		db, err := postgres.Open(ctx, dsn)
		if err == nil {
			return db, nil
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

// replay deletes report's durable consumer; the running report reads the store
// stream from the start.
func replay() error {
	existed, err := natsio.Replay(context.Background(), "abarow-report", natsconsumer.Stream, natsconsumer.Durable)
	switch {
	case err != nil:
		return err
	case !existed:
		fmt.Println("the consumer does not exist yet; report will read the store stream from the start when it starts")
	default:
		fmt.Println("consumer deleted; report is reading the store stream from the start (existing events are skipped, missing ones are added)")
	}
	return nil
}
