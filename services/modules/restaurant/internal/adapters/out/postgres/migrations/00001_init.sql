-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- Floors and tables (a single record, JSON): the manager edits it from the register.
CREATE TABLE floor (
    id    INTEGER PRIMARY KEY CHECK (id = 1),
    areas TEXT NOT NULL
);

-- Checks: the document itself is JSON (lines, payment in progress); the table, status and times
-- are in separate columns for querying. There is at most one open check at a table.
CREATE TABLE orders (
    id        TEXT        PRIMARY KEY,
    table_id  TEXT        NOT NULL,
    closed    BOOLEAN     NOT NULL DEFAULT FALSE,
    opened_at TIMESTAMPTZ NOT NULL,
    closed_at TIMESTAMPTZ,
    version   BIGINT      NOT NULL,
    data      TEXT        NOT NULL
);
CREATE UNIQUE INDEX orders_open_table ON orders (table_id) WHERE NOT closed;
CREATE INDEX orders_closed_at ON orders (closed_at);

-- +goose Down
DROP TABLE orders;
DROP TABLE floor;
