-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- personneldb's PostgreSQL schema (headquarters). The service's migration sets up the same
-- tables; the SQLite counterpart is in sqlite.sql.

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    name          TEXT        NOT NULL,
    role          TEXT        NOT NULL,
    all_stores    BOOLEAN     NOT NULL DEFAULT FALSE,
    disabled      BOOLEAN     NOT NULL DEFAULT FALSE,
    pin_hash      BYTEA,
    password_hash TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL,
    updated_by    TEXT        NOT NULL DEFAULT '',
    updated_at    TIMESTAMPTZ NOT NULL,
    email         TEXT        NOT NULL DEFAULT '',
    idp_issuer    TEXT        NOT NULL DEFAULT '',
    idp_subject   TEXT        NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX users_email ON users (email) WHERE email <> '';
CREATE UNIQUE INDEX users_idp ON users (idp_issuer, idp_subject) WHERE idp_subject <> '';

CREATE TABLE user_stores (
    user_id  TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    store_id TEXT NOT NULL,
    PRIMARY KEY (user_id, store_id)
);

CREATE TABLE user_changes (
    id      BIGSERIAL PRIMARY KEY,
    user_id TEXT        NOT NULL REFERENCES users (id),
    at      TIMESTAMPTZ NOT NULL,
    by_user TEXT        NOT NULL,
    detail  TEXT        NOT NULL
);
CREATE INDEX user_changes_user ON user_changes (user_id, id);

CREATE TABLE sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id    TEXT        NOT NULL,
    binding    BYTEA,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE outbox (
    seq          BIGSERIAL PRIMARY KEY,
    id           TEXT        NOT NULL UNIQUE,
    type         TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    store_id     TEXT        NOT NULL DEFAULT '',
    payload      BYTEA       NOT NULL,
    content_type TEXT        NOT NULL,
    occurred_at  TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ
);
