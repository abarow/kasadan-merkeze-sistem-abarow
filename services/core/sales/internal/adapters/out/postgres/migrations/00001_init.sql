-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
CREATE TABLE sales (
    id          TEXT PRIMARY KEY,
    register_id TEXT NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('open', 'charged', 'paid', 'voided')),
    receipt_no  BIGINT UNIQUE,
    fiscal_no   TEXT,
    created_at  TIMESTAMPTZ NOT NULL,
    paid_at     TIMESTAMPTZ,
    voided_at   TIMESTAMPTZ,
    cashier_id  TEXT NOT NULL DEFAULT '',
    -- Sales cancelled with their payment refunded because the payment was taken
    -- but the fiscal record could not be made (status 'voided').
    refunded    BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE sale_lines (
    sale_id    TEXT    NOT NULL REFERENCES sales (id) ON DELETE CASCADE,
    line_no    INTEGER NOT NULL,
    product_id TEXT    NOT NULL,
    barcode    TEXT    NOT NULL,
    name       TEXT    NOT NULL,
    qty        BIGINT  NOT NULL,
    unit_price BIGINT  NOT NULL,
    tax_rate   BIGINT  NOT NULL,
    PRIMARY KEY (sale_id, line_no)
);

CREATE TABLE sale_payments (
    sale_id    TEXT    NOT NULL REFERENCES sales (id) ON DELETE CASCADE,
    payment_no INTEGER NOT NULL,
    method     TEXT    NOT NULL,
    amount     BIGINT  NOT NULL,
    ref        TEXT    NOT NULL DEFAULT '',
    collect    BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (sale_id, payment_no)
);

-- The receipt number that increases per register; a single-row counter. Being a row, it
-- is rolled back with the sale's transaction: a receipt number is never skipped.
CREATE TABLE receipt_counter (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    last_no BIGINT  NOT NULL
);
INSERT INTO receipt_counter (id, last_no) VALUES (1, 0);

-- Events written in the same transaction as the sale that the relay will pass on to NATS.
-- seq comes from a counter row and not a sequence: a sequence consumes a number in a
-- rolled-back transaction, and the receiver reports a gap in the numbers as a lost event (pkg/sequence).
CREATE TABLE outbox_counter (
    id       INTEGER PRIMARY KEY CHECK (id = 1),
    last_seq BIGINT  NOT NULL
);
INSERT INTO outbox_counter (id, last_seq) VALUES (1, 0);
CREATE FUNCTION outbox_next_seq() RETURNS BIGINT LANGUAGE sql AS 'UPDATE outbox_counter SET last_seq = last_seq + 1 WHERE id = 1 RETURNING last_seq';

CREATE TABLE outbox (
    seq          BIGINT PRIMARY KEY DEFAULT outbox_next_seq(),
    id           TEXT NOT NULL UNIQUE,
    type         TEXT NOT NULL,
    subject      TEXT NOT NULL,
    store_id     TEXT NOT NULL,
    register_id  TEXT NOT NULL,
    payload      BYTEA NOT NULL,
    occurred_at  TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    -- The event's content type: new events are protobuf, earlier records JSON.
    content_type TEXT NOT NULL DEFAULT 'application/json'
);
CREATE INDEX outbox_unpublished ON outbox (seq) WHERE published_at IS NULL;

-- Delivery receipt (pkg/delivery): when the store server processes an event into the database it
-- gives a UUIDv7 receipt id for the event; the receipt is written here next to the event.
-- An old event in the register stream is deleted only if its receipt is recorded here.
CREATE TABLE delivery_received (
    event_id    TEXT PRIMARY KEY,
    receipt_id  TEXT NOT NULL UNIQUE,
    issued_at   TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE delivery_received;
DROP TABLE outbox;
DROP FUNCTION outbox_next_seq();
DROP TABLE outbox_counter;
DROP TABLE receipt_counter;
DROP TABLE sale_payments;
DROP TABLE sale_lines;
DROP TABLE sales;
