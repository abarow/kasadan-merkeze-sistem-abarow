// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"regexp"
)

// On the store server catalog-admin shares the same database with report and inventory;
// since report's catalog mirror also uses a table called catalog_products the table names
// collide. So catalog-admin works in its own schema (ABAROW_DB_SCHEMA=catalog):
// EnsureSchema sets up the schema, WithSearchPath directs the connections to that schema.
// At headquarters no schema is given.

var schemaName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// EnsureSchema sets up the schema if it does not exist. dsn must be a connection address (postgres://...).
func EnsureSchema(ctx context.Context, dsn, schema string) error {
	if !schemaName.MatchString(schema) {
		return fmt.Errorf("ABAROW_DB_SCHEMA geçersiz: %q (küçük harf, rakam ve _ olabilir)", schema)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema)
	return err
}

// WithSearchPath adds search_path to the connection address: the connections use the schema
// without table names being given. dsn must be in URL form.
func WithSearchPath(dsn, schema string) (string, error) {
	if !schemaName.MatchString(schema) {
		return "", fmt.Errorf("ABAROW_DB_SCHEMA geçersiz: %q (küçük harf, rakam ve _ olabilir)", schema)
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		return "", fmt.Errorf("ABAROW_DB_SCHEMA için ABAROW_DATABASE_URL postgres://... biçiminde olmalı")
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
