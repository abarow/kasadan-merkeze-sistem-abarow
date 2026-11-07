-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- Products whose stock is kept: from headquarters' catalog (from_catalog) or
-- by the name in the stores' movements. search is the name lower-cased with Turkish rules
-- (for search).
CREATE TABLE stock_products (
    id           TEXT    PRIMARY KEY,
    name         TEXT    NOT NULL,
    unit         TEXT    NOT NULL DEFAULT 'piece',
    discontinued BOOLEAN NOT NULL DEFAULT false,
    from_catalog BOOLEAN NOT NULL DEFAULT false,
    search       TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE stock_barcodes (
    barcode    TEXT PRIMARY KEY,
    product_id TEXT NOT NULL REFERENCES stock_products (id) ON DELETE CASCADE
);

-- Movements are per place (the headquarters warehouse "headquarters" or the store id) and
-- do not change. The same supplier's same delivery note is entered once at the same place.
CREATE TABLE stock_movements (
    location TEXT        NOT NULL,
    id       TEXT        NOT NULL,
    kind     TEXT        NOT NULL,
    ref      TEXT        NOT NULL,
    supplier TEXT        NOT NULL DEFAULT '',
    by_name  TEXT        NOT NULL,
    at       TIMESTAMPTZ NOT NULL,
    -- The other place in a transfer: the one sent to on an outflow, the sending place on an inflow.
    counterpart TEXT     NOT NULL DEFAULT '',
    PRIMARY KEY (location, id)
);
CREATE UNIQUE INDEX stock_movements_receipt_once ON stock_movements (location, lower(supplier), ref) WHERE kind = 'receipt';

-- Quantities are in thousandths of a unit (1000 = 1 piece or 1 kg), signed.
CREATE TABLE stock_lines (
    location    TEXT   NOT NULL,
    movement_id TEXT   NOT NULL,
    line_no     INT    NOT NULL,
    product_id  TEXT   NOT NULL,
    name        TEXT   NOT NULL,
    qty         BIGINT NOT NULL,
    counted     BIGINT,
    system      BIGINT,
    PRIMARY KEY (location, movement_id, line_no),
    FOREIGN KEY (location, movement_id) REFERENCES stock_movements (location, id)
);
CREATE INDEX stock_lines_product ON stock_lines (product_id, location);

-- Stock per place and product (the total of the movements).
CREATE TABLE stock_levels (
    location   TEXT   NOT NULL,
    product_id TEXT   NOT NULL,
    qty        BIGINT NOT NULL,
    PRIMARY KEY (location, product_id)
);

-- All the transfers: opened with the sending place's outflow, received_* is filled when
-- the receiving place takes delivery. In between the transfer is in transit.
CREATE TABLE stock_transfers (
    id          TEXT        PRIMARY KEY,
    from_loc    TEXT        NOT NULL,
    to_loc      TEXT        NOT NULL,
    by_name     TEXT        NOT NULL,
    at          TIMESTAMPTZ NOT NULL,
    received_by TEXT        NOT NULL DEFAULT '',
    received_at TIMESTAMPTZ
);
CREATE INDEX stock_transfers_at ON stock_transfers (at);

-- qty is the quantity sent, received is the quantity taken delivery of (thousandths of a unit).
CREATE TABLE stock_transfer_lines (
    transfer_id TEXT   NOT NULL REFERENCES stock_transfers (id),
    line_no     INT    NOT NULL,
    product_id  TEXT   NOT NULL,
    name        TEXT   NOT NULL,
    qty         BIGINT NOT NULL,
    received    BIGINT,
    PRIMARY KEY (transfer_id, line_no)
);

-- Events that will go to the stores (the transfers sent, the list of places): written in the same
-- transaction as the record, published to the transfer stream later.
CREATE TABLE stock_outbox (
    seq          BIGSERIAL   PRIMARY KEY,
    id           TEXT        NOT NULL UNIQUE,
    type         TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    payload      BYTEA       NOT NULL,
    content_type TEXT        NOT NULL,
    occurred_at  TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ
);
CREATE INDEX stock_outbox_pending ON stock_outbox (seq) WHERE published_at IS NULL;

-- The places transfers can be sent to in store scope (decision 04.11.2026: the store server is
-- PostgreSQL too): headquarters publishes, the latest state is valid.
CREATE TABLE stock_locations (
    id   TEXT PRIMARY KEY,
    name TEXT NOT NULL
);

-- +goose Down
DROP TABLE stock_locations;
DROP TABLE stock_outbox;
DROP TABLE stock_transfer_lines;
DROP TABLE stock_transfers;
DROP TABLE stock_levels;
DROP TABLE stock_lines;
DROP TABLE stock_movements;
DROP TABLE stock_barcodes;
DROP TABLE stock_products;
