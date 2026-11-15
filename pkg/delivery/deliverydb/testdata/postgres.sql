-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- Receipt tables (PostgreSQL). The same as the schema in the services' migrations.
CREATE TABLE delivery_issued (
    event_id   TEXT PRIMARY KEY,
    receipt_id TEXT NOT NULL UNIQUE,
    owner      TEXT NOT NULL,
    stream     TEXT NOT NULL,
    issued_at  TIMESTAMPTZ NOT NULL
);
CREATE TABLE delivery_received (
    event_id    TEXT PRIMARY KEY,
    receipt_id  TEXT NOT NULL UNIQUE,
    issued_at   TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL
);
