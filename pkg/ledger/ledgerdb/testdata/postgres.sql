-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- The sales ledger's PostgreSQL schema (for tests). At headquarters ingest's
-- migrations set up the same tables.
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
