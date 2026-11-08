// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" driver
)

func TestUserCommand(t *testing.T) {
	dsn := os.Getenv("ABAROW_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("ABAROW_TEST_POSTGRES is not set; PostgreSQL tests were skipped")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("abarow_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
	// The address of the test database, in the form the install writes (postgres://...).
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	t.Setenv("ABAROW_DATABASE_URL", u.String())

	run := func(stdin string, args ...string) (string, error) {
		var out bytes.Buffer
		err := userCommand(args, strings.NewReader(stdin), &out)
		return out.String(), err
	}
	if _, err := run("headquarters-test-1\n", "add", "Ayse", "Ayşe Yılmaz"); err != nil {
		t.Fatal(err)
	}
	if _, err := run("kısa\n", "add", "mehmet"); err == nil || !strings.Contains(err.Error(), "en az 10") {
		t.Errorf("short password: %v", err)
	}
	if _, err := run("", "add", "mehmet"); err == nil || !strings.Contains(err.Error(), "standart girdi") {
		t.Errorf("no password: %v", err)
	}
	if _, err := run("headquarters-test-2\r\n", "passwd", "ayse"); err != nil {
		t.Error(err)
	}
	// With the corporate sign-in a person is recognized at the first sign-in by their email; the address is also
	// given from the command line, an invalid address is rejected, a command with no address removes it.
	if out, err := run("", "email", "ayse", "Ayse@Firma.example"); err != nil || !strings.Contains(out, "saved") {
		t.Errorf("e-posta: %q %v", out, err)
	}
	if _, err := run("", "email", "ayse", "adres-degil"); err == nil {
		t.Error("an invalid email was accepted")
	}
	if out, err := run("", "email", "ayse"); err != nil || !strings.Contains(out, "removed") {
		t.Errorf("email removal: %q %v", out, err)
	}
	if _, err := run("", "email", "yok", "yok@firma.example"); err == nil {
		t.Error("an email was given to a non-existent user")
	}
	if out, err := run("", "unlink", "ayse"); err != nil || !strings.Contains(out, "removed") {
		t.Errorf("link removal: %q %v", out, err)
	}
	// The last administrator with an open account cannot be closed (no one would be left to manage the
	// users on the screen); a user added from the command line is an administrator.
	if _, err := run("", "disable", "ayse"); err == nil || !strings.Contains(err.Error(), "son yönetici") {
		t.Errorf("last administrator: %v", err)
	}
	if _, err := run("headquarters-test-3\n", "add", "mehmet", "Mehmet Er"); err != nil {
		t.Fatal(err)
	}
	if _, err := run("", "disable", "ayse"); err != nil {
		t.Error(err)
	}
	if out, err := run("", "list"); err != nil || out != "ayse\tAyşe Yılmaz\työnetici\tbütün mağazalar\tclosed\nmehmet\tMehmet Er\työnetici\tbütün mağazalar\topen\n" {
		t.Errorf("liste %q %v", out, err)
	}
	if _, err := run("", "disable", "veli"); err == nil {
		t.Error("a non-existent user was closed")
	}
	if _, err := run("", "sil", "ayse"); err == nil || !strings.Contains(err.Error(), "kullanım") {
		t.Errorf("bilinmeyen komut: %v", err)
	}
}
