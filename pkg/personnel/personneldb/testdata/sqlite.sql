-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- personneldb's SQLite schema (store server). The service's migration sets up the same
-- tables; the PostgreSQL counterpart is in postgres.sql.

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    name          TEXT    NOT NULL,
    role          TEXT    NOT NULL,
    all_stores    INTEGER NOT NULL DEFAULT 0,
    disabled      INTEGER NOT NULL DEFAULT 0,
    pin_hash      BLOB,
    password_hash TEXT    NOT NULL DEFAULT '',
    created_at    TEXT    NOT NULL,
    updated_by    TEXT    NOT NULL DEFAULT '',
    updated_at    TEXT    NOT NULL,
    email         TEXT    NOT NULL DEFAULT '',
    idp_issuer    TEXT    NOT NULL DEFAULT '',
    idp_subject   TEXT    NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX users_email ON users (email) WHERE email <> '';
CREATE UNIQUE INDEX users_idp ON users (idp_issuer, idp_subject) WHERE idp_subject <> '';

CREATE TABLE user_stores (
    user_id  TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    store_id TEXT NOT NULL,
    PRIMARY KEY (user_id, store_id)
);

CREATE TABLE user_changes (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL REFERENCES users (id),
    at      TEXT NOT NULL,
    by_user TEXT NOT NULL,
    detail  TEXT NOT NULL
);
CREATE INDEX user_changes_user ON user_changes (user_id, id);

CREATE TABLE sessions (
    token_hash BLOB PRIMARY KEY,
    user_id    TEXT NOT NULL,
    binding    BLOB,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE outbox (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    id           TEXT NOT NULL UNIQUE,
    type         TEXT NOT NULL,
    subject      TEXT NOT NULL,
    store_id     TEXT NOT NULL DEFAULT '',
    payload      BLOB NOT NULL,
    content_type TEXT NOT NULL,
    occurred_at  TEXT NOT NULL,
    published_at TEXT
);
