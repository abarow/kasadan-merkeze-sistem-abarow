-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
CREATE TABLE products (
    id          TEXT PRIMARY KEY,
    name        TEXT    NOT NULL,
    search_name TEXT    NOT NULL, -- Turkish lower case (domain.SearchKey)
    -- Sort key by the Turkish alphabet (domain.SortKey): the byte order
    -- is the alphabetical order (Ç after C; Ş after S).
    sort_name   TEXT    COLLATE "C" NOT NULL DEFAULT '',
    unit        TEXT    NOT NULL CHECK (unit IN ('piece', 'kg')),
    tax_rate    BIGINT  NOT NULL,
    quick_key   BOOLEAN NOT NULL DEFAULT FALSE,
    -- origin: where the product came from. 'seed' from the initial file (catalog.json),
    -- 'headquarters' from headquarters' catalog stream. A product that came from headquarters is not
    -- changed by the initial file: even if the file is old headquarters' latest state is valid.
    origin       TEXT    NOT NULL DEFAULT 'seed' CHECK (origin IN ('seed', 'headquarters')),
    -- discontinued: headquarters withdrew the product from sale; it is not found at the register.
    discontinued BOOLEAN NOT NULL DEFAULT FALSE,
    -- Restaurant menu: category (not on the menu if empty), menu order, kitchen
    -- station, option groups (JSON) and the digest and type of the current image.
    category       TEXT    NOT NULL DEFAULT '',
    menu_order     BIGINT  NOT NULL DEFAULT 0,
    station        TEXT    NOT NULL DEFAULT '',
    option_groups  TEXT    NOT NULL DEFAULT '',
    image_sha256   TEXT    NOT NULL DEFAULT '',
    image_type     TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX products_sort_name ON products (sort_name);

CREATE TABLE barcodes (
    barcode    TEXT PRIMARY KEY,
    product_id TEXT NOT NULL REFERENCES products (id) ON DELETE CASCADE
);
CREATE INDEX barcodes_product ON barcodes (product_id);

-- Price schedule: headquarters ('headquarters') and store ('store') prices, with the effective moment
-- (from_ms: Unix milliseconds, UTC; 0 from the beginning). The price in effect at sale time
-- is computed (domain.PriceAt); a future-dated price takes effect on
-- time even when the internet is down. clear: the store exception is lifted at that moment.
CREATE TABLE prices (
    product_id TEXT    NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    source     TEXT    NOT NULL CHECK (source IN ('headquarters', 'store')),
    from_ms    BIGINT  NOT NULL,
    amount     BIGINT  NOT NULL,
    clear      BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (product_id, source, from_ms)
);

-- Product images. Not tied to the product: the image from headquarters may come
-- before the product's details. The one shown is the image whose digest holds the
-- product's digest.
CREATE TABLE images (
    product_id   TEXT PRIMARY KEY,
    sha256       TEXT NOT NULL,
    content_type TEXT NOT NULL,
    data         BYTEA NOT NULL
);

-- +goose Down
DROP TABLE images;
DROP TABLE prices;
DROP TABLE barcodes;
DROP TABLE products;
