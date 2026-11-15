// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command reporting is the composition root of the headquarters reports service:
// it serves the stores' state and the period summaries (the API and the headquarters
// screen). Ingest writes the events to the database; reporting only reads.
//
// The report, the sign-in endpoints and the screen shell are in the core shared with the store server
// (pkg/report, pkg/login); only what is specific to headquarters stays in this service:
// the store states, the store price report, the users and the user
// password sign-in.
//
// The screen is signed in to with personal accounts. The administrator manages the users
// from the screen (role and stores). For the first administrator and for recovery there is the command line
// (a user added from the command line is an administrator); the password is given on
// standard input:
//
//	echo 'password' | reporting user add ayse "Ayşe Yılmaz"
//	echo 'new password' | reporting user passwd ayse
//	reporting user disable ayse
//	reporting user enable ayse
//	reporting user list
//
// With the corporate sign-in (ABAROW_LOGIN=oidc or local,oidc; the settings are in
// pkg/login/oidc) a person is recognized at the first sign-in by the email address. The address is
// given from the screen; in an install with no local method, for the first administrator from the
// command line (giving no address removes it, unlink removes the identity link):
//
//	reporting user email ayse ayse@company.example
//	reporting user unlink ayse
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"
	_ "time/tzdata" // scratch image'da saat dilimi verisi yok

	"abarow.com/pkg/login/oidc"
	"abarow.com/pkg/report"
	"abarow.com/pkg/service"
	"abarow.com/services/headquarters/reporting/internal/adapters/in/httpapi"
	"abarow.com/services/headquarters/reporting/internal/adapters/out/postgres"
	"abarow.com/services/headquarters/reporting/internal/app"
	"abarow.com/services/headquarters/reporting/internal/domain"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "pgtune" {
		if err := pgtuneCommand(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "reporting:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "user" {
		if err := userCommand(os.Args[2:], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "reporting:", err)
			os.Exit(1)
		}
		return
	}
	healthAddr := service.Env("ABAROW_REPORTING_HEALTH_ADDR", ":8081")
	service.Main("reporting", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		dsn, err := databaseURL()
		if err != nil {
			return err
		}
		// Days are computed in the store's time zone.
		loc, err := time.LoadLocation(service.Env("ABAROW_TZ", "Europe/Istanbul"))
		if err != nil {
			return err
		}
		db, err := openDB(ctx, log, dsn)
		if err != nil {
			return err
		}
		defer db.Close()
		auth, err := app.NewAuth(db.People())
		if err != nil {
			return err
		}
		if users, err := auth.Users(ctx, app.Installer); err == nil && len(users) == 0 {
			log.Warn("there is no user at headquarters; to sign in to the screen: echo 'password' | reporting user add <user> \"Full Name\"")
		}
		// If catalog management (catalog-admin) is installed the screen's catalog
		// requests are forwarded to it.
		var catalog *url.URL
		if s := os.Getenv("ABAROW_CATALOG_ADMIN_URL"); s != "" {
			if catalog, err = url.Parse(s); err != nil {
				return fmt.Errorf("ABAROW_CATALOG_ADMIN_URL is invalid: %w", err)
			}
		}
		// If the stock module (inventory, headquarters scope) is installed the screen's stock requests are
		// forwarded to it.
		// Update (optional): the registration server's admin API and the state of the headquarters agent.
		var updateServer, updateAgent *url.URL
		if s := os.Getenv("ABAROW_UPDATE_ADMIN_URL"); s != "" {
			if updateServer, err = url.Parse(s); err != nil {
				return fmt.Errorf("ABAROW_UPDATE_ADMIN_URL is invalid: %w", err)
			}
		}
		if s := os.Getenv("ABAROW_UPDATE_AGENT_URL"); s != "" {
			if updateAgent, err = url.Parse(s); err != nil {
				return fmt.Errorf("ABAROW_UPDATE_AGENT_URL is invalid: %w", err)
			}
		}
		var stock *url.URL
		if s := os.Getenv("ABAROW_INVENTORY_URL"); s != "" {
			if stock, err = url.Parse(s); err != nil {
				return fmt.Errorf("ABAROW_INVENTORY_URL is invalid: %w", err)
			}
		}
		// If the corporate sign-in (OIDC) is configured it is added to the screen; the users are
		// registered beforehand, the IdP only proves the identity.
		corp, err := oidc.Configure(os.Getenv, auth.OIDCService(httpapi.SessionCookie, log))
		if err != nil {
			return err
		}
		return service.ServeHTTP(ctx, log, service.HTTPConfig{
			Handler:    httpapi.New(app.NewService(db, loc), report.New(db, loc, report.MaxProducts(domain.MaxProducts)), auth, corp, service.Env("ABAROW_REPORTING_WEB_DIR", "/web"), catalog, stock, httpapi.WithUpdate(updateServer, updateAgent), httpapi.WithUpdateSecret(os.Getenv("ABAROW_UPDATE_APPROVE_SECRET"))),
			Addr:       service.Env("ABAROW_REPORTING_ADDR", ":8080"),
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

// userCommand runs the user management commands.
func userCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	usage := errors.New(`kullanım:
  echo 'parola' | reporting user add <kullanıcı> ["Ad Soyad"]
  echo 'yeni parola' | reporting user passwd <kullanıcı>
  reporting user disable <kullanıcı>
  reporting user enable <kullanıcı>
  reporting user email <kullanıcı> [e-posta]
  reporting user unlink <kullanıcı>
  reporting user list`)
	if len(args) == 0 {
		return usage
	}
	dsn, err := databaseURL()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	auth, err := app.NewAuth(db.People())
	if err != nil {
		return err
	}

	switch cmd, rest := args[0], args[1:]; {
	case cmd == "list" && len(rest) == 0:
		users, err := auth.Users(ctx, app.Installer)
		if err != nil {
			return err
		}
		for _, u := range users {
			state := "open"
			if u.Disabled {
				state = "closed"
			}
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", u.ID, u.Name, domain.RoleName(u.Role), u.ScopeName(), state)
		}
		return nil
	case cmd == "add" && (len(rest) == 1 || len(rest) == 2):
		password, err := readPassword(stdin)
		if err != nil {
			return err
		}
		name := ""
		if len(rest) == 2 {
			name = rest[1]
		}
		// A user added from the command line is an administrator (first install, recovery); the administrator
		// gives the roles and stores from the screen.
		u, err := auth.AddUser(ctx, app.Installer, rest[0], password, app.UserInput{Name: name, Role: domain.RoleAdmin, AllStores: true})
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, "user added (administrator):", u.ID)
		return nil
	case cmd == "passwd" && len(rest) == 1:
		password, err := readPassword(stdin)
		if err != nil {
			return err
		}
		if err := auth.SetPassword(ctx, app.Installer, rest[0], password); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "password changed; open sessions closed")
		return nil
	case cmd == "email" && (len(rest) == 1 || len(rest) == 2):
		email := ""
		if len(rest) == 2 {
			email = rest[1]
		}
		if _, err := auth.SetEmail(ctx, app.Installer, rest[0], email); err != nil {
			return err
		}
		if email == "" {
			fmt.Fprintln(stdout, "email address removed")
		} else {
			fmt.Fprintln(stdout, "email address saved; the user is recognized by this address at the first corporate sign-in")
		}
		return nil
	case cmd == "unlink" && len(rest) == 1:
		if _, err := auth.UnlinkIdentity(ctx, app.Installer, rest[0]); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "corporate identity link removed")
		return nil
	case (cmd == "disable" || cmd == "enable") && len(rest) == 1:
		if _, err := auth.SetDisabled(ctx, app.Installer, rest[0], cmd == "disable"); err != nil {
			return err
		}
		if cmd == "disable" {
			fmt.Fprintln(stdout, "account closed; open sessions closed")
		} else {
			fmt.Fprintln(stdout, "account opened")
		}
		return nil
	}
	return usage
}

// readPassword reads the first line on standard input as the password.
func readPassword(stdin io.Reader) (string, error) {
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("parola standart girdiden verilmeli")
	}
	return strings.TrimRight(line, "\r\n"), nil
}
