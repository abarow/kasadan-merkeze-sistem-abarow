// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package personneldb is the database side of the people: the SQL that writes the person,
// store scope, change history and sessions is written once; the store server
// (SQLite) and headquarters (PostgreSQL) use the same. The tables are the same
// in every tier; the services' migrations set up the schema (see
// testdata/*.sql). Dialect differences are in sqldialect.Dialect.
//
// Store is used as both personnel.Store (people) and login.Store (sessions).
package personneldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/pkg/outbox"
	"abarow.com/pkg/personnel"
	"abarow.com/pkg/sqldialect"
)

// Store is the SQL store of the people and sessions.
type Store struct {
	DB      *sql.DB
	Dialect sqldialect.Dialect
	// Outbox is the outbox table the change is propagated to; it is written in the same
	// transaction as the person. It is not used at a tier with no propagation (SaveRequest.Msgs
	// empty).
	Outbox outbox.Table
	// NoSessions says there is no session table (the store server, where the sessions are kept
	// in memory): SaveRequest.CloseSessions is ignored; the sessions close when the person is
	// resolved again on every request (Directory).
	NoSessions bool
}

var (
	_ personnel.Store = (*Store)(nil)
	_ login.Store     = (*Store)(nil)
)

const userColumns = `id, name, role, all_stores, disabled, pin_hash, password_hash, created_at, updated_by, updated_at, email, idp_issuer, idp_subject`

func scanUser(row interface{ Scan(...any) error }) (personnel.User, error) {
	var u personnel.User
	err := row.Scan(&u.ID, &u.Name, &u.Role, &u.AllStores, &u.Disabled, &u.PINHash, &u.PasswordHash,
		sqldialect.ScanTime(&u.CreatedAt), &u.UpdatedBy, sqldialect.ScanTime(&u.UpdatedAt), &u.Email, &u.Issuer, &u.Subject)
	return u, err
}

// User returns the person (with their stores); personnel.ErrNotFound if none.
func (s *Store) User(ctx context.Context, id string) (personnel.User, error) {
	u, err := scanUser(s.DB.QueryRowContext(ctx, s.Dialect.Q(`SELECT `+userColumns+` FROM users WHERE id = ?`), id))
	if errors.Is(err, sql.ErrNoRows) {
		return personnel.User{}, personnel.ErrNotFound
	}
	if err != nil {
		return personnel.User{}, err
	}
	rows, err := s.DB.QueryContext(ctx, s.Dialect.Q(`SELECT store_id FROM user_stores WHERE user_id = ? ORDER BY store_id`), id)
	if err != nil {
		return personnel.User{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var store string
		if err := rows.Scan(&store); err != nil {
			return personnel.User{}, err
		}
		u.Stores = append(u.Stores, store)
	}
	return u, rows.Err()
}

// Users returns all the people (including closed accounts) by their codes.
func (s *Store) Users(ctx context.Context) ([]personnel.User, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var out []personnel.User
	byID := map[string]int{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		byID[u.ID] = len(out)
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	stores, err := s.DB.QueryContext(ctx, `SELECT user_id, store_id FROM user_stores ORDER BY user_id, store_id`)
	if err != nil {
		return nil, err
	}
	defer stores.Close()
	for stores.Next() {
		var id, store string
		if err := stores.Scan(&id, &store); err != nil {
			return nil, err
		}
		if i, ok := byID[id]; ok {
			out[i].Stores = append(out[i].Stores, store)
		}
	}
	return out, stores.Err()
}

// Save writes the person in the same transaction as the change record and the outbox
// messages.
func (s *Store) Save(ctx context.Context, r personnel.SaveRequest) error {
	d, u := s.Dialect, r.User
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // Commit'ten sonra etkisiz

	if r.New {
		var one int
		switch err := tx.QueryRowContext(ctx, d.Q(`SELECT 1 FROM users WHERE id = ?`), u.ID).Scan(&one); {
		case err == nil:
			return personnel.ErrExists
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		_, err = tx.ExecContext(ctx, d.Q(`INSERT INTO users (`+userColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
			u.ID, u.Name, u.Role, u.AllStores, u.Disabled, nilIfEmpty(u.PINHash), u.PasswordHash,
			d.Time(u.CreatedAt), u.UpdatedBy, d.Time(u.UpdatedAt), u.Email, u.Issuer, u.Subject)
		if err != nil {
			return err
		}
	} else {
		res, err := tx.ExecContext(ctx, d.Q(`
			UPDATE users SET name = ?, role = ?, all_stores = ?, disabled = ?, pin_hash = ?, password_hash = ?,
				updated_by = ?, updated_at = ?, email = ?, idp_issuer = ?, idp_subject = ? WHERE id = ?`),
			u.Name, u.Role, u.AllStores, u.Disabled, nilIfEmpty(u.PINHash), u.PasswordHash, u.UpdatedBy, d.Time(u.UpdatedAt),
			u.Email, u.Issuer, u.Subject, u.ID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return personnel.ErrNotFound
		}
	}
	if _, err := tx.ExecContext(ctx, d.Q(`DELETE FROM user_stores WHERE user_id = ?`), u.ID); err != nil {
		return err
	}
	for _, store := range u.Stores {
		if _, err := tx.ExecContext(ctx, d.Q(`INSERT INTO user_stores (user_id, store_id) VALUES (?, ?)`), u.ID, store); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, d.Q(`INSERT INTO user_changes (user_id, at, by_user, detail) VALUES (?, ?, ?, ?)`),
		r.Change.ID, d.Time(r.Change.At), r.Change.By, r.Change.Detail); err != nil {
		return err
	}
	if r.CloseSessions && !s.NoSessions {
		if _, err := tx.ExecContext(ctx, d.Q(`DELETE FROM sessions WHERE user_id = ?`), u.ID); err != nil {
			return err
		}
	}
	if len(r.Msgs) > 0 {
		if err := s.Outbox.Enqueue(ctx, tx, r.Msgs...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// nilIfEmpty writes an empty digest as NULL (a person with no digest).
func nilIfEmpty(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// History returns the person's changes from newest to oldest.
func (s *Store) History(ctx context.Context, id string, limit int) ([]personnel.Change, error) {
	rows, err := s.DB.QueryContext(ctx, s.Dialect.Q(`
		SELECT user_id, at, by_user, detail FROM user_changes WHERE user_id = ? ORDER BY id DESC LIMIT ?`), id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []personnel.Change
	for rows.Next() {
		var c personnel.Change
		if err := rows.Scan(&c.ID, sqldialect.ScanTime(&c.At), &c.By, &c.Detail); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Put saves the session (replaces it if it exists) and deletes the expired
// sessions (login.Store).
func (s *Store) Put(ctx context.Context, key [32]byte, sess login.Session, now time.Time) error {
	d := s.Dialect
	if _, err := s.DB.ExecContext(ctx, d.Q(`DELETE FROM sessions WHERE expires_at <= ?`), d.Time(now)); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, d.Q(`
		INSERT INTO sessions (token_hash, user_id, binding, created_at, expires_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (token_hash) DO UPDATE SET user_id = excluded.user_id, binding = excluded.binding, expires_at = excluded.expires_at`),
		key[:], sess.Subject, nilIfEmpty(sess.Binding), d.Time(now), d.Time(sess.Expires))
	if err != nil {
		return fmt.Errorf("oturum yazılamadı: %w", err)
	}
	return nil
}

// Get returns the session that has not expired (login.Store).
func (s *Store) Get(ctx context.Context, key [32]byte, now time.Time) (login.Session, bool, error) {
	var sess login.Session
	err := s.DB.QueryRowContext(ctx, s.Dialect.Q(`SELECT user_id, binding, expires_at FROM sessions WHERE token_hash = ? AND expires_at > ?`),
		key[:], s.Dialect.Time(now)).Scan(&sess.Subject, &sess.Binding, sqldialect.ScanTime(&sess.Expires))
	if errors.Is(err, sql.ErrNoRows) {
		return login.Session{}, false, nil
	}
	return sess, err == nil, err
}

// Delete, oturumu siler (login.Store).
func (s *Store) Delete(ctx context.Context, key [32]byte) error {
	_, err := s.DB.ExecContext(ctx, s.Dialect.Q(`DELETE FROM sessions WHERE token_hash = ?`), key[:])
	return err
}

// DeleteSubject deletes all of the person's sessions (login.Store).
func (s *Store) DeleteSubject(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, s.Dialect.Q(`DELETE FROM sessions WHERE user_id = ?`), id)
	return err
}
