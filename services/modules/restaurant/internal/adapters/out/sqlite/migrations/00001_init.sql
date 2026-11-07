-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- Floors and tables (a single record, JSON): the manager edits it from the register.
CREATE TABLE floor (
    id    INTEGER PRIMARY KEY CHECK (id = 1),
    areas TEXT NOT NULL
);

-- Checks: the document itself is JSON (lines, payment in progress); the table, status and times
-- are in separate columns for querying. At most one open check at a table
-- adisyon olur.
CREATE TABLE orders (
    id        TEXT    PRIMARY KEY,
    table_id  TEXT    NOT NULL,
    closed    INTEGER NOT NULL DEFAULT 0,
    opened_at TEXT    NOT NULL,
    closed_at TEXT    NOT NULL DEFAULT '',
    version   INTEGER NOT NULL,
    data      TEXT    NOT NULL
);
CREATE UNIQUE INDEX orders_open_table ON orders (table_id) WHERE closed = 0;
CREATE INDEX orders_closed_at ON orders (closed_at);

-- +goose Down
DROP TABLE orders;
DROP TABLE floor;
