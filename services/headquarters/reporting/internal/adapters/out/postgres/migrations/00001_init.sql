-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- The headquarters screen's users. Only the password's bcrypt digest
-- is stored. ingest writes the sales tables; reporting only reads.
--
-- The user's role and the stores they see (decision 29.10.2026): the administrator
-- manages users, changes the catalog, sees all the stores;
-- the catalog user changes products and prices; the report user sees only the
-- reports. catalog-admin reads the role together with the session.
CREATE TABLE users (
    id            TEXT        PRIMARY KEY,
    name          TEXT        NOT NULL,
    password_hash TEXT        NOT NULL DEFAULT '',
    disabled      BOOLEAN     NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL,
    role          TEXT        NOT NULL DEFAULT 'admin' CHECK (role IN ('admin', 'catalog', 'report')),
    all_stores    BOOLEAN     NOT NULL DEFAULT true,
    pin_hash      BYTEA,
    updated_by    TEXT        NOT NULL DEFAULT '',
    updated_at    TIMESTAMPTZ NOT NULL,
    email         TEXT        NOT NULL DEFAULT '',
    idp_issuer    TEXT        NOT NULL DEFAULT '',
    idp_subject   TEXT        NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX users_email ON users (email) WHERE email <> '';
CREATE UNIQUE INDEX users_idp ON users (idp_issuer, idp_subject) WHERE idp_subject <> '';

-- Open sessions. Only the SHA-256 digest of the session key is stored.
CREATE TABLE sessions (
    token_hash BYTEA       PRIMARY KEY,
    user_id    TEXT        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    binding    BYTEA
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

-- If all_stores is false the user sees only the stores here.
CREATE TABLE user_stores (
    user_id  TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    store_id TEXT NOT NULL,
    PRIMARY KEY (user_id, store_id)
);

-- The record of user changes: who, when, what (the password is not written).
CREATE TABLE user_changes (
    id      BIGSERIAL   PRIMARY KEY,
    user_id TEXT        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    at      TIMESTAMPTZ NOT NULL,
    by_user TEXT        NOT NULL,
    detail  TEXT        NOT NULL
);
CREATE INDEX user_changes_user ON user_changes (user_id, at DESC, id DESC);

-- +goose Down
DROP TABLE user_changes;
DROP TABLE user_stores;
DROP TABLE sessions;
DROP TABLE users;
