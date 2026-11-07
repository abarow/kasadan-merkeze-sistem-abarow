-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- The sales ledger's SQLite schema (for tests). At the store server report's
-- migrations set up the same tables. Times are fixed-width UTC text.
CREATE TABLE sales (
    store_id      TEXT        NOT NULL,
    id            TEXT        NOT NULL,
    register_id   TEXT        NOT NULL,
    cashier_id    TEXT        NOT NULL,
    receipt_no    INTEGER     NOT NULL,
    fiscal_no     TEXT        NOT NULL,
    total         INTEGER     NOT NULL,
    tax_total     INTEGER     NOT NULL,
    change_amount INTEGER     NOT NULL,
    paid_at       TEXT        NOT NULL,
    PRIMARY KEY (store_id, id)
);
CREATE TABLE sale_lines (
    store_id   TEXT    NOT NULL,
    sale_id    TEXT    NOT NULL,
    line_no    INTEGER NOT NULL,
    product_id TEXT    NOT NULL,
    barcode    TEXT    NOT NULL,
    name       TEXT    NOT NULL,
    qty        INTEGER NOT NULL,
    unit_price INTEGER NOT NULL,
    tax_rate   INTEGER NOT NULL,
    total      INTEGER NOT NULL,
    PRIMARY KEY (store_id, sale_id, line_no),
    FOREIGN KEY (store_id, sale_id) REFERENCES sales (store_id, id) ON DELETE CASCADE
);
CREATE TABLE sale_payments (
    store_id   TEXT    NOT NULL,
    sale_id    TEXT    NOT NULL,
    payment_no INTEGER NOT NULL,
    method     TEXT    NOT NULL,
    amount     INTEGER NOT NULL,
    PRIMARY KEY (store_id, sale_id, payment_no),
    FOREIGN KEY (store_id, sale_id) REFERENCES sales (store_id, id) ON DELETE CASCADE
);
CREATE TABLE sale_taxes (
    store_id TEXT   NOT NULL,
    sale_id  TEXT   NOT NULL,
    rate     INTEGERNOT NULL,
    gross    INTEGERNOT NULL,
    tax      INTEGERNOT NULL,
    PRIMARY KEY (store_id, sale_id, rate),
    FOREIGN KEY (store_id, sale_id) REFERENCES sales (store_id, id) ON DELETE CASCADE
);
CREATE TABLE voids (
    store_id    TEXT        NOT NULL,
    sale_id     TEXT        NOT NULL,
    register_id TEXT        NOT NULL,
    cashier_id  TEXT        NOT NULL,
    total       INTEGER     NOT NULL,
    refunded    INTEGER     NOT NULL,
    voided_at   TEXT        NOT NULL,
    PRIMARY KEY (store_id, sale_id)
);
CREATE TABLE unfinished (
    store_id     TEXT        NOT NULL,
    sale_id      TEXT        NOT NULL,
    register_id  TEXT        NOT NULL,
    cashier_id   TEXT        NOT NULL,
    total        INTEGER     NOT NULL,
    reason       TEXT        NOT NULL,
    since        TEXT        NOT NULL,
    last_attempt TEXT        NOT NULL,
    PRIMARY KEY (store_id, sale_id)
);
CREATE TABLE shifts (
    store_id      TEXT        NOT NULL,
    register_id   TEXT        NOT NULL,
    shift_id      INTEGER     NOT NULL,
    cashier_id    TEXT        NOT NULL,
    cashier_name  TEXT        NOT NULL,
    opened_at     TEXT        NOT NULL,
    opening_cash  INTEGER     NOT NULL,
    closed_at     TEXT       ,
    closed_by     TEXT        NOT NULL DEFAULT '',
    receipts      INTEGER     NOT NULL DEFAULT 0,
    total         INTEGER     NOT NULL DEFAULT 0,
    expected_cash INTEGER     NOT NULL DEFAULT 0,
    counted_cash  INTEGER     NOT NULL DEFAULT 0,
    difference    INTEGER     NOT NULL DEFAULT 0,
    PRIMARY KEY (store_id, register_id, shift_id)
);
