-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- The events coming from the stores, in their raw form. The event id (ce-id) de-duplicates
-- an event that comes again. If the read models change the events can be
-- reprocessed from here.
CREATE TABLE events (
    id           TEXT        PRIMARY KEY,
    subject      TEXT        NOT NULL,
    store_id     TEXT        NOT NULL,
    register_id  TEXT        NOT NULL DEFAULT '',
    occurred_at  TIMESTAMPTZ NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    content_type TEXT        NOT NULL DEFAULT '',
    payload      BYTEA       NOT NULL
);
CREATE INDEX events_store_time ON events (store_id, occurred_at);

-- The summary of the events that came, per store and register (updated with every event;
-- the status screen does not scan the events table).
CREATE TABLE store_stats (
    store_id       TEXT        PRIMARY KEY,
    events         BIGINT      NOT NULL,
    sales          BIGINT      NOT NULL,
    first_event_at TIMESTAMPTZ NOT NULL,
    last_event_at  TIMESTAMPTZ NOT NULL
);
CREATE TABLE registers (
    store_id       TEXT        NOT NULL,
    register_id    TEXT        NOT NULL,
    events         BIGINT      NOT NULL,
    sales          BIGINT      NOT NULL,
    first_event_at TIMESTAMPTZ NOT NULL,
    last_event_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (store_id, register_id)
);

-- Completed sales (sale.completed.v1). Amounts are in kuruş, quantity in thousandths,
-- the VAT rate in base points. Register ids are unique within a store.
CREATE TABLE sales (
    store_id      TEXT        NOT NULL,
    id            TEXT        NOT NULL,
    register_id   TEXT        NOT NULL,
    cashier_id    TEXT        NOT NULL,
    receipt_no    BIGINT      NOT NULL,
    fiscal_no     TEXT        NOT NULL,
    total         BIGINT      NOT NULL,
    tax_total     BIGINT      NOT NULL,
    change_amount BIGINT      NOT NULL,
    paid_at       TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (store_id, id)
);
CREATE INDEX sales_paid ON sales (paid_at);
CREATE INDEX sales_register_receipt ON sales (store_id, register_id, receipt_no);

CREATE TABLE sale_lines (
    store_id   TEXT    NOT NULL,
    sale_id    TEXT    NOT NULL,
    line_no    INTEGER NOT NULL,
    product_id TEXT    NOT NULL,
    barcode    TEXT    NOT NULL,
    name       TEXT    NOT NULL,
    qty        BIGINT  NOT NULL,
    unit_price BIGINT  NOT NULL,
    tax_rate   BIGINT  NOT NULL,
    total      BIGINT  NOT NULL,
    PRIMARY KEY (store_id, sale_id, line_no),
    FOREIGN KEY (store_id, sale_id) REFERENCES sales (store_id, id) ON DELETE CASCADE
);

CREATE TABLE sale_payments (
    store_id   TEXT    NOT NULL,
    sale_id    TEXT    NOT NULL,
    payment_no INTEGER NOT NULL,
    method     TEXT    NOT NULL,
    amount     BIGINT  NOT NULL,
    PRIMARY KEY (store_id, sale_id, payment_no),
    FOREIGN KEY (store_id, sale_id) REFERENCES sales (store_id, id) ON DELETE CASCADE
);

CREATE TABLE sale_taxes (
    store_id TEXT   NOT NULL,
    sale_id  TEXT   NOT NULL,
    rate     BIGINT NOT NULL,
    gross    BIGINT NOT NULL,
    tax      BIGINT NOT NULL,
    PRIMARY KEY (store_id, sale_id, rate),
    FOREIGN KEY (store_id, sale_id) REFERENCES sales (store_id, id) ON DELETE CASCADE
);

-- Cancelled sales (sale.voided.v1).
CREATE TABLE voids (
    store_id    TEXT        NOT NULL,
    sale_id     TEXT        NOT NULL,
    register_id TEXT        NOT NULL,
    cashier_id  TEXT        NOT NULL,
    total       BIGINT      NOT NULL,
    refunded    BOOLEAN     NOT NULL,
    voided_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (store_id, sale_id)
);
CREATE INDEX voids_time ON voids (voided_at);

-- Sales whose payment was taken but whose receipt could not be issued (sale.charged.v1). If the sale
-- is completed later it also shows in the sales table. The sales ledger
-- (pkg/ledger) uses the same tables as the store server: the first attempt (since) and
-- son deneme (last_attempt).
CREATE TABLE unfinished (
    store_id     TEXT        NOT NULL,
    sale_id      TEXT        NOT NULL,
    register_id  TEXT        NOT NULL,
    cashier_id   TEXT        NOT NULL,
    total        BIGINT      NOT NULL,
    reason       TEXT        NOT NULL,
    since        TIMESTAMPTZ NOT NULL,
    last_attempt TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (store_id, sale_id)
);

-- The registers' shifts (shift.opened.v1, shift.closed.v1). The shift
-- number increases within a register. The closing fields are empty in an open shift.
CREATE TABLE shifts (
    store_id      TEXT        NOT NULL,
    register_id   TEXT        NOT NULL,
    shift_id      BIGINT      NOT NULL,
    cashier_id    TEXT        NOT NULL,
    cashier_name  TEXT        NOT NULL,
    opened_at     TIMESTAMPTZ NOT NULL,
    opening_cash  BIGINT      NOT NULL,
    closed_at     TIMESTAMPTZ,
    closed_by     TEXT        NOT NULL DEFAULT '',
    receipts      BIGINT      NOT NULL DEFAULT 0,
    total         BIGINT      NOT NULL DEFAULT 0,
    expected_cash BIGINT      NOT NULL DEFAULT 0,
    counted_cash  BIGINT      NOT NULL DEFAULT 0,
    difference    BIGINT      NOT NULL DEFAULT 0,
    PRIMARY KEY (store_id, register_id, shift_id)
);

-- The stores' connection state to headquarters; ingest reads it from NATS and writes it at
-- intervals (checked_at). The headquarters screen reads it from here without
-- connecting to NATS. When a store is removed from the list its row is deleted.
CREATE TABLE store_links (
    store_id       TEXT        PRIMARY KEY,
    name           TEXT        NOT NULL,
    link           TEXT        NOT NULL,
    last_seen      TIMESTAMPTZ,
    pending_events BIGINT      NOT NULL,
    error          TEXT        NOT NULL DEFAULT '',
    checked_at     TIMESTAMPTZ NOT NULL
);

-- Store prices (exceptions): each store's price
-- takviminin son hali (store.price.changed.v1). prices: [{amount, from,
-- clear}] (if from is absent from the beginning; clear means from that moment the exception is lifted).
-- An empty schedule means there is no exception at the store. Headquarters' "products with different
-- prices" report comes from here.
CREATE TABLE store_prices (
    store_id     TEXT        NOT NULL,
    product_id   TEXT        NOT NULL,
    product_name TEXT        NOT NULL,
    prices       JSONB       NOT NULL,
    updated_by   TEXT        NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (store_id, product_id)
);

-- The latest state of the cashiers the stores manage on the store server
-- (store.cashier.changed.v1); the cashier name in the reports comes from here.
-- The cashier id is specific to the store: the same id in another store is a different person.
-- PIN merkeze gelmez.
CREATE TABLE cashiers (
    store_id   TEXT        NOT NULL,
    cashier_id TEXT        NOT NULL,
    name       TEXT        NOT NULL,
    role       TEXT        NOT NULL,
    disabled   BOOLEAN     NOT NULL DEFAULT false,
    updated_by TEXT        NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (store_id, cashier_id)
);

-- Delivery receipt (pkg/delivery): in the transaction in which the event is processed into the database a
-- UUIDv7 receipt id is given for the event and the receipt is written to the outbox to be
-- published back to the store that sent the event. The store does not delete an old event without seeing the receipt.
CREATE TABLE delivery_issued (
    event_id   TEXT        PRIMARY KEY,
    receipt_id TEXT        NOT NULL UNIQUE,
    owner      TEXT        NOT NULL,
    stream     TEXT        NOT NULL,
    issued_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE receipt_outbox (
    seq          BIGSERIAL   PRIMARY KEY,
    id           TEXT        NOT NULL UNIQUE,
    type         TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    payload      BYTEA       NOT NULL,
    content_type TEXT        NOT NULL,
    occurred_at  TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ
);
CREATE INDEX receipt_outbox_pending ON receipt_outbox (seq) WHERE published_at IS NULL;

-- The ranges seen in the register events' sequence numbers (pkg/sequence/sequencedb):
-- a gap between ranges shows the events that did not arrive.
CREATE TABLE event_ranges (
    store_id    TEXT   NOT NULL,
    register_id TEXT   NOT NULL,
    producer    TEXT   NOT NULL,
    from_seq    BIGINT NOT NULL,
    to_seq      BIGINT NOT NULL,
    PRIMARY KEY (store_id, register_id, producer, from_seq),
    CHECK (from_seq <= to_seq)
);
CREATE UNIQUE INDEX event_ranges_to ON event_ranges (store_id, register_id, producer, to_seq);

-- +goose Down
DROP TABLE event_ranges;
DROP TABLE receipt_outbox;
DROP TABLE delivery_issued;
DROP TABLE cashiers;
DROP TABLE store_prices;
DROP TABLE store_links;
DROP TABLE shifts;
DROP TABLE unfinished;
DROP TABLE voids;
DROP TABLE sale_taxes;
DROP TABLE sale_payments;
DROP TABLE sale_lines;
DROP TABLE sales;
DROP TABLE registers;
DROP TABLE store_stats;
DROP TABLE events;
