// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package personneldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"abarow.com/pkg/login"
	"abarow.com/pkg/outbox"
	"abarow.com/pkg/personnel"
	"abarow.com/pkg/sqldialect"
)

type testDB struct {
	db *sql.DB
	d  sqldialect.Dialect
}

// databases are the databases the test runs on: always SQLite;
// PostgreSQL too if ABAROW_TEST_POSTGRES is given.
func databases(t *testing.T) map[string]testDB {
	t.Helper()
	ctx := context.Background()
	out := map[string]testDB{}
	schema := func(file string) string {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	lite, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	lite.SetMaxOpenConns(1)
	t.Cleanup(func() { lite.Close() })
	if _, err := lite.ExecContext(ctx, schema("testdata/sqlite.sql")); err != nil {
		t.Fatal(err)
	}
	out["sqlite"] = testDB{lite, sqldialect.SQLite}

	dsn := os.Getenv("ABAROW_TEST_POSTGRES")
	if dsn == "" {
		return out
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	name := fmt.Sprintf("abarow_personnel_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	pg := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		pg.Close()
		admin.ExecContext(ctx, "DROP DATABASE "+name)
	})
	if _, err := pg.ExecContext(ctx, schema("testdata/postgres.sql")); err != nil {
		t.Fatal(err)
	}
	out["postgres"] = testDB{pg, sqldialect.Postgres}
	return out
}

var t0 = time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)

func newStore(x testDB) *Store {
	return &Store{DB: x.db, Dialect: x.d, Outbox: outbox.Table{Name: "outbox", Dialect: x.d, Store: true}}
}

func save(t *testing.T, s *Store, u personnel.User, isNew bool, detail string, closeSessions bool, msgs ...outbox.Message) error {
	t.Helper()
	return s.Save(context.Background(), personnel.SaveRequest{
		User: u, New: isNew, Msgs: msgs, CloseSessions: closeSessions,
		Change: personnel.Change{ID: u.ID, At: u.UpdatedAt, By: u.UpdatedBy, Detail: detail},
	})
}

func TestUsers(t *testing.T) {
	for name, x := range databases(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(x)

			if _, err := s.User(ctx, "yok"); !errors.Is(err, personnel.ErrNotFound) {
				t.Fatalf("non-existent person: %v", err)
			}

			// Store staff: there is a PIN digest, no store scope.
			ali := personnel.User{ID: "ali", Name: "Ali Kaya", Role: personnel.RoleCashier, PINHash: []byte("pin-ozet"),
				CreatedAt: t0, UpdatedBy: "müdür", UpdatedAt: t0}
			msg := outbox.Message{ID: "m1", Type: "tip", Subject: "config.cashier.ali", StoreID: "store-1", Payload: []byte("x"),
				ContentType: "application/protobuf", OccurredAt: t0}
			if err := save(t, s, ali, true, "eklendi", false, msg); err != nil {
				t.Fatal(err)
			}
			if got, err := s.User(ctx, "ali"); err != nil || !reflect.DeepEqual(got, ali) {
				t.Errorf("person %+v %v, want %+v", got, err, ali)
			}
			var n int
			if err := x.db.QueryRowContext(ctx, "SELECT count(*) FROM outbox").Scan(&n); err != nil || n != 1 {
				t.Errorf("%d messages in the outbox (%v)", n, err)
			}
			if err := save(t, s, ali, true, "yine", false); !errors.Is(err, personnel.ErrExists) {
				t.Errorf("same code: %v", err)
			}

			// Headquarters user: password digest and stores.
			ayse := personnel.User{ID: "ayse", Name: "Ayşe Yılmaz", Role: personnel.RoleReport, Stores: []string{"m1", "m2"},
				PasswordHash: "parola-ozet", CreatedAt: t0, UpdatedBy: "yönetici", UpdatedAt: t0.Add(time.Minute)}
			if err := save(t, s, ayse, true, "eklendi", false); err != nil {
				t.Fatal(err)
			}
			if got, err := s.User(ctx, "ayse"); err != nil || !reflect.DeepEqual(got, ayse) {
				t.Errorf("headquarters user %+v %v, want %+v", got, err, ayse)
			}

			// Update: all stores → the store rows are deleted.
			ayse.Role, ayse.AllStores, ayse.Stores, ayse.Disabled = personnel.RoleCatalog, true, nil, true
			ayse.UpdatedAt = t0.Add(2 * time.Minute)
			if err := save(t, s, ayse, false, "rol", false); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.User(ctx, "ayse"); !reflect.DeepEqual(got, ayse) {
				t.Errorf("current person %+v, want %+v", got, ayse)
			}
			if err := x.db.QueryRowContext(ctx, "SELECT count(*) FROM user_stores").Scan(&n); err != nil || n != 0 {
				t.Errorf("store row %d (%v)", n, err)
			}
			ghost := personnel.User{ID: "hayalet", Name: "H", Role: personnel.RoleReport, UpdatedAt: t0}
			if err := save(t, s, ghost, false, "x", false); !errors.Is(err, personnel.ErrNotFound) {
				t.Errorf("updating a non-existent person: %v", err)
			}

			users, err := s.Users(ctx)
			if err != nil || len(users) != 2 || users[0].ID != "ali" || !reflect.DeepEqual(users[1], ayse) {
				t.Errorf("people %+v %v", users, err)
			}

			// History from newest to oldest.
			h, err := s.History(ctx, "ayse", 10)
			if err != nil || len(h) != 2 || h[0].Detail != "rol" || h[1].Detail != "eklendi" || !h[0].At.Equal(ayse.UpdatedAt) || h[0].By != "yönetici" {
				t.Errorf("history %+v %v", h, err)
			}
			if h, _ := s.History(ctx, "ayse", 1); len(h) != 1 {
				t.Errorf("limit: %d", len(h))
			}

			// Store lists do not get mixed up between people.
			bob := personnel.User{ID: "bob", Name: "Bob", Role: personnel.RoleReport, Stores: []string{"m9"}, CreatedAt: t0, UpdatedAt: t0}
			if err := save(t, s, bob, true, "eklendi", false); err != nil {
				t.Fatal(err)
			}
			users, _ = s.Users(ctx)
			if len(users) != 3 || len(users[0].Stores) != 0 || !reflect.DeepEqual(users[2].Stores, []string(nil)) && users[1].ID == "ayse" && len(users[1].Stores) != 0 {
				t.Errorf("stores got mixed up: %+v", users)
			}
			if got, _ := s.User(ctx, "bob"); !reflect.DeepEqual(got.Stores, []string{"m9"}) {
				t.Errorf("bob's stores %v", got.Stores)
			}
		})
	}
}

func TestSessions(t *testing.T) {
	for name, x := range databases(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(x)
			key := func(n string) [32]byte { return login.TokenHash(n) }
			put := func(n, user string, at time.Time, ttl time.Duration, binding []byte) {
				t.Helper()
				if err := s.Put(ctx, key(n), login.Session{Subject: user, Binding: binding, Expires: at.Add(ttl)}, at); err != nil {
					t.Fatal(err)
				}
			}

			put("a", "ali", t0, time.Hour, []byte("parmak"))
			sess, ok, err := s.Get(ctx, key("a"), t0)
			if err != nil || !ok || sess.Subject != "ali" || string(sess.Binding) != "parmak" || !sess.Expires.Equal(t0.Add(time.Hour)) {
				t.Errorf("session %+v %v %v", sess, ok, err)
			}
			if _, ok, _ := s.Get(ctx, key("a"), t0.Add(time.Hour)); ok {
				t.Error("an expired session is valid")
			}
			if _, ok, _ := s.Get(ctx, key("yok"), t0); ok {
				t.Error("a non-existent session is valid")
			}

			// Put with the same key renews the session (including the fingerprint); a session with no fingerprint.
			put("a", "ali", t0, 2*time.Hour, []byte("yeni"))
			put("b", "ali", t0, time.Hour, nil)
			if sess, ok, _ := s.Get(ctx, key("a"), t0.Add(time.Hour)); !ok || string(sess.Binding) != "yeni" {
				t.Errorf("renewed session %+v %v", sess, ok)
			}
			if sess, ok, _ := s.Get(ctx, key("b"), t0); !ok || len(sess.Binding) != 0 {
				t.Errorf("session without a fingerprint %+v %v", sess, ok)
			}

			// When a new session is opened the expired ones are deleted.
			put("c", "veli", t0.Add(3*time.Hour), time.Hour, nil)
			var n int
			if err := x.db.QueryRowContext(ctx, "SELECT count(*) FROM sessions").Scan(&n); err != nil || n != 1 {
				t.Errorf("%d sessions; should be 1 (%v)", n, err)
			}

			put("d", "ali", t0.Add(3*time.Hour), time.Hour, nil)
			if err := s.DeleteSubject(ctx, "ali"); err != nil {
				t.Fatal(err)
			}
			if _, ok, _ := s.Get(ctx, key("d"), t0.Add(3*time.Hour)); ok {
				t.Error("the person's session was not deleted")
			}
			if _, ok, _ := s.Get(ctx, key("c"), t0.Add(3*time.Hour)); !ok {
				t.Error("someone else's session was deleted")
			}
			if err := s.Delete(ctx, key("c")); err != nil {
				t.Fatal(err)
			}
			if err := s.Delete(ctx, key("yok")); err != nil {
				t.Error(err)
			}
			if _, ok, _ := s.Get(ctx, key("c"), t0.Add(3*time.Hour)); ok {
				t.Error("a deleted session is valid")
			}
		})
	}
}

// When the credential changes or the account closes the sessions are deleted in the same
// transaction as the person.
func TestSaveClosesSessions(t *testing.T) {
	for name, x := range databases(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(x)
			u := personnel.User{ID: "ali", Name: "Ali", Role: personnel.RoleReport, AllStores: true, PasswordHash: "a", CreatedAt: t0, UpdatedAt: t0}
			if err := save(t, s, u, true, "eklendi", false); err != nil {
				t.Fatal(err)
			}
			for _, n := range []string{"1", "2"} {
				if err := s.Put(ctx, login.TokenHash(n), login.Session{Subject: "ali", Expires: t0.Add(time.Hour)}, t0); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Put(ctx, login.TokenHash("3"), login.Session{Subject: "veli", Expires: t0.Add(time.Hour)}, t0); err != nil {
				t.Fatal(err)
			}
			u.PasswordHash, u.UpdatedAt = "b", t0.Add(time.Minute)
			if err := save(t, s, u, false, "parola", false); err != nil {
				t.Fatal(err)
			}
			if _, ok, _ := s.Get(ctx, login.TokenHash("1"), t0); !ok {
				t.Error("session closing was not requested, it closed")
			}
			if err := save(t, s, u, false, "parola", true); err != nil {
				t.Fatal(err)
			}
			for n, want := range map[string]bool{"1": false, "2": false, "3": true} {
				if _, ok, _ := s.Get(ctx, login.TokenHash(n), t0); ok != want {
					t.Errorf("session %s: %v, want %v", n, ok, want)
				}
			}
		})
	}
}

// At a tier with no session table (NoSessions) the request to close the session is
// ignored; the person is still written.
func TestSaveWithoutSessionTable(t *testing.T) {
	x := databases(t)["sqlite"]
	if _, err := x.db.Exec("DROP TABLE sessions"); err != nil {
		t.Fatal(err)
	}
	s := newStore(x)
	s.NoSessions = true
	u := personnel.User{ID: "ali", Name: "Ali", Role: personnel.RoleCashier, PINHash: []byte("p"), CreatedAt: t0, UpdatedAt: t0}
	if err := save(t, s, u, true, "eklendi", true); err != nil {
		t.Fatal(err)
	}
	if got, err := s.User(context.Background(), "ali"); err != nil || got.Name != "Ali" {
		t.Errorf("person %+v %v", got, err)
	}
}
