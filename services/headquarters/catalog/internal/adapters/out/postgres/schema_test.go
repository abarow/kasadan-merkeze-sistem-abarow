// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWithSearchPath(t *testing.T) {
	got, err := WithSearchPath("postgres://abarow:p%40ss@db:5432/abarow?sslmode=disable", "catalog")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil || u.Query().Get("search_path") != "catalog" || u.Query().Get("sslmode") != "disable" || u.Host != "db:5432" {
		t.Errorf("adres %q (%v)", got, err)
	}
	if pw, _ := u.User.Password(); pw != "p@ss" {
		t.Errorf("parola bozuldu: %q", pw)
	}
	for _, bad := range []string{"", "Katalog", "a-b", "1abc", "a;drop table x", strings.Repeat("a", 64)} {
		if _, err := WithSearchPath("postgres://x@h/db", bad); err == nil {
			t.Errorf("%q şema adı kabul edildi", bad)
		}
	}
	if _, err := WithSearchPath("host=db dbname=abarow", "catalog"); err == nil {
		t.Error("URL olmayan adres kabul edildi")
	}
}

// When a schema is given catalog-admin's tables are set up in that schema; they do not
// collide with a table of the same name of another service in the same database.
func TestSchemaIsolation(t *testing.T) {
	base := os.Getenv("ABAROW_TEST_POSTGRES")
	if base == "" {
		t.Skip("ABAROW_TEST_POSTGRES yok; PostgreSQL testleri atlandı")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("abarow_schema_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)") })

	u, _ := url.Parse(base)
	u.Path = "/" + name
	dsn := u.String()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Another service's table of the same name (the mirror of the store server's report).
	if _, err := db.ExecContext(ctx, `CREATE TABLE catalog_products (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	if err := EnsureSchema(ctx, dsn, "catalog"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(ctx, dsn, "catalog"); err != nil {
		t.Fatalf("ikinci kurulum hata verdi: %v", err)
	}
	scoped, err := WithSearchPath(dsn, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, scoped)
	if err != nil {
		t.Fatalf("şemada migration: %v", err)
	}
	defer store.Close()

	var inSchema, inPublic sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('catalog.catalog_products')::text, to_regclass('public.catalog_products')::text`).Scan(&inSchema, &inPublic); err != nil {
		t.Fatal(err)
	}
	if !inSchema.Valid {
		t.Error("catalog-admin tablosu katalog şemasında yok")
	}
	var cols int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'catalog_products'`).Scan(&cols); err != nil || cols != 1 {
		t.Errorf("public.catalog_products bozuldu: %d sütun, %v", cols, err)
	}
	_ = inPublic

	if err := EnsureSchema(ctx, dsn, "Kötü Ad"); err == nil {
		t.Error("geçersiz şema adı kabul edildi")
	}
}
