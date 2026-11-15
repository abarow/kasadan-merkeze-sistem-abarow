-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- Sales ledger.
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

CREATE INDEX sales_paid ON sales (paid_at);
-- The register state screen (Activity) asks for the number of receipts per register, the last receipt number,
-- the last sale time and the gaps in the receipt numbers. The queries do not filter
-- by store and partition by register_id and sort by receipt_no; so
-- the index's first column is register_id (with store_id it could not be used and the gap
-- query would be processed with a sort spilling 450 thousand rows to disk: 335 ms
-- at 50 registers; with this index only an index scan, 209 ms). paid_at is carried
-- in the index: without going down to the table.
CREATE INDEX sales_register_receipt ON sales (register_id, receipt_no) INCLUDE (paid_at);
CREATE INDEX voids_time ON voids (voided_at);
CREATE INDEX unfinished_register ON unfinished (register_id);
-- The register's most recently opened and most recently closed shift (Activity): the greatest value
-- from the index instead of a scan per register.
CREATE INDEX shifts_register_opened ON shifts (register_id, opened_at);
CREATE INDEX shifts_register_closed ON shifts (register_id, closed_at) WHERE closed_at IS NOT NULL;

-- Headquarters' catalog (taken into the store for the registers) and the store prices.
CREATE TABLE catalog_products (
    id           TEXT    PRIMARY KEY,
    name         TEXT    NOT NULL,
    search_name  TEXT    NOT NULL, -- Türkçe küçük harf
    unit         TEXT    NOT NULL,
    barcodes     TEXT    NOT NULL, -- JSON dizi
    headquarters_prices    TEXT    NOT NULL, -- JSON: [{amount, from_ms}]
    discontinued BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE store_prices (
    product_id TEXT    NOT NULL,
    from_ms    BIGINT  NOT NULL,
    amount     BIGINT  NOT NULL,
    clear      BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (product_id, from_ms)
);

CREATE TABLE store_price_changes (
    id         BIGSERIAL   PRIMARY KEY,
    product_id TEXT        NOT NULL,
    at         TIMESTAMPTZ NOT NULL,
    by_user    TEXT        NOT NULL,
    detail     TEXT        NOT NULL
);
CREATE INDEX store_price_changes_product ON store_price_changes (product_id, id);

-- Messages that will go to the registers and headquarters (price, cashier); written in the same
-- transaction as the change.
CREATE TABLE outbox (
    seq          BIGSERIAL   PRIMARY KEY,
    id           TEXT        NOT NULL UNIQUE,
    type         TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    store_id     TEXT        NOT NULL,
    payload      BYTEA       NOT NULL,
    content_type TEXT        NOT NULL,
    occurred_at  TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ
);
-- Those waiting to be published (polled every second): without scanning
-- hundreds of thousands of published rows.
CREATE INDEX outbox_pending ON outbox (seq) WHERE published_at IS NULL;

-- People (cashier, manager) and sessions: pkg/personnel/personneldb.
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

-- Delivery receipt (pkg/delivery). Two directions:
--   delivery_issued:   the UUIDv7 receipt id given to the register when an event from the register
--                      is processed into the database (the register does not delete an
--                      old event without seeing the receipt); the receipt goes down to the register with receipt_outbox.
--   delivery_received: the receipt headquarters gave to the store server; an old event in the store stream
--                      is deleted only if its receipt is recorded here.
CREATE TABLE delivery_issued (
    event_id   TEXT        PRIMARY KEY,
    receipt_id TEXT        NOT NULL UNIQUE,
    owner      TEXT        NOT NULL,
    stream     TEXT        NOT NULL,
    issued_at  TIMESTAMPTZ NOT NULL
);

CREATE TABLE delivery_received (
    event_id    TEXT        PRIMARY KEY,
    receipt_id  TEXT        NOT NULL UNIQUE,
    issued_at   TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE receipt_outbox (
    seq          BIGSERIAL   PRIMARY KEY,
    id           TEXT        NOT NULL UNIQUE,
    type         TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    store_id     TEXT        NOT NULL,
    payload      BYTEA       NOT NULL,
    content_type TEXT        NOT NULL,
    occurred_at  TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ
);
CREATE INDEX receipt_outbox_pending ON receipt_outbox (seq) WHERE published_at IS NULL;

-- Settings changed from the administrator screen (e.g. connected to headquarters or not).
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);


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
DROP TABLE settings;
DROP TABLE receipt_outbox;
DROP TABLE delivery_received;
DROP TABLE delivery_issued;
DROP TABLE sessions;
DROP TABLE user_changes;
DROP TABLE user_stores;
DROP TABLE users;
DROP TABLE outbox;
DROP TABLE store_price_changes;
DROP TABLE store_prices;
DROP TABLE catalog_products;
DROP TABLE shifts;
DROP TABLE unfinished;
DROP TABLE voids;
DROP TABLE sale_taxes;
DROP TABLE sale_payments;
DROP TABLE sale_lines;
DROP TABLE sales;
