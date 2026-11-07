-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- The units registered with the update source (register, store server, headquarters). Each device
-- is a single row; the status changes with compare-and-write (the race of two approvers does not
-- lose the update).
CREATE TABLE update_registrations (
    device       TEXT        PRIMARY KEY,
    public_key   BYTEA       NOT NULL,
    -- Headquarters only: headquarters' token signing key (its certificate is issued for this).
    signing_key  BYTEA,
    fingerprint  TEXT        NOT NULL,
    tier         TEXT        NOT NULL CHECK (tier IN ('register', 'store', 'headquarters')),
    level        TEXT        NOT NULL CHECK (level IN ('test', 'pilot', 'prod')),
    store_id     TEXT        NOT NULL DEFAULT '',
    register_id  TEXT        NOT NULL DEFAULT '',
    name         TEXT        NOT NULL DEFAULT '',
    status       TEXT        NOT NULL CHECK (status IN ('pending', 'approved', 'rejected', 'revoked')),
    attempts     INTEGER     NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL,
    decided_at   TIMESTAMPTZ,
    decided_by   TEXT        NOT NULL DEFAULT ''
);
CREATE INDEX update_registrations_status ON update_registrations (status, created_at);

-- +goose Down
DROP TABLE update_registrations;
