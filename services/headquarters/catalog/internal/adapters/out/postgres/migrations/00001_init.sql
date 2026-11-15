-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- Headquarters' catalog (catalog-admin writes). The tables share the headquarters
-- database with ingest and reporting; their names start with catalog_.
CREATE TABLE catalog_products (
    code         TEXT        PRIMARY KEY,
    name         TEXT        NOT NULL,
    search_name  TEXT        NOT NULL, -- Türkçe küçük harf (arama)
    unit         TEXT        NOT NULL CHECK (unit IN ('piece', 'kg')),
    tax_rate     BIGINT      NOT NULL,
    quick_key    BOOLEAN     NOT NULL,
    discontinued BOOLEAN     NOT NULL DEFAULT false,
    updated_by   TEXT        NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    -- Increases with each record: of two changes made at the same time the one that comes second is
    -- rejected, it does not silently overwrite the earlier.
    version      BIGINT      NOT NULL,
    -- Restaurant menu: category (not on the menu if empty), menu order, kitchen
    -- station, option groups and the digest and type of the current image.
    category      TEXT    NOT NULL DEFAULT '',
    menu_order    INTEGER NOT NULL DEFAULT 0,
    station       TEXT    NOT NULL DEFAULT '',
    option_groups JSONB,
    image_sha256  TEXT    NOT NULL DEFAULT '',
    image_type    TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE catalog_barcodes (
    barcode TEXT PRIMARY KEY,
    code    TEXT NOT NULL REFERENCES catalog_products (code) ON DELETE CASCADE
);
CREATE INDEX catalog_barcodes_code ON catalog_barcodes (code);

-- Headquarters price schedule: the VAT-included price (kuruş) valid from the moment
-- from_ms (Unix milliseconds; 0 from the beginning).
CREATE TABLE catalog_prices (
    code    TEXT   NOT NULL REFERENCES catalog_products (code) ON DELETE CASCADE,
    from_ms BIGINT NOT NULL,
    amount  BIGINT NOT NULL,
    PRIMARY KEY (code, from_ms)
);

-- Who changed what, when.
CREATE TABLE catalog_changes (
    id      BIGSERIAL   PRIMARY KEY,
    code    TEXT        NOT NULL,
    at      TIMESTAMPTZ NOT NULL,
    by_user TEXT        NOT NULL,
    action  TEXT        NOT NULL,
    detail  TEXT        NOT NULL
);
CREATE INDEX catalog_changes_code ON catalog_changes (code, id DESC);

-- Messages that will go to the registers: written in the same transaction as the product, the relay
-- publishes to NATS in order.
CREATE TABLE catalog_outbox (
    seq          BIGSERIAL   PRIMARY KEY,
    id           TEXT        NOT NULL UNIQUE,
    type         TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    payload      BYTEA       NOT NULL,
    content_type TEXT        NOT NULL,
    occurred_at  TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ
);
CREATE INDEX catalog_outbox_pending ON catalog_outbox (seq) WHERE published_at IS NULL;

-- Product images (at most 512 KB; the screen shrinks before uploading).
CREATE TABLE catalog_images (
    code         TEXT  PRIMARY KEY REFERENCES catalog_products (code) ON DELETE CASCADE,
    sha256       TEXT  NOT NULL,
    content_type TEXT  NOT NULL,
    data         BYTEA NOT NULL
);

-- +goose Down
DROP TABLE catalog_images;
DROP TABLE catalog_outbox;
DROP TABLE catalog_changes;
DROP TABLE catalog_prices;
DROP TABLE catalog_barcodes;
DROP TABLE catalog_products;
