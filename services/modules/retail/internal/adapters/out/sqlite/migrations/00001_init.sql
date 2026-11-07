-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- Baskets: the cashier's working copy. Even if the service or screen restarts
-- the cashier continues from their basket; the sale is permanently in sales.
-- Since the lines are read and written together with the basket they are stored as JSON.
CREATE TABLE baskets (
    id         TEXT PRIMARY KEY,
    cashier_id TEXT NOT NULL,
    status     TEXT NOT NULL CHECK (status IN ('open', 'checked_out')),
    sale_id    TEXT NOT NULL DEFAULT '',
    lines      TEXT NOT NULL, -- JSON
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX baskets_cashier ON baskets (cashier_id, updated_at);
CREATE INDEX baskets_updated ON baskets (updated_at);

-- +goose Down
DROP TABLE baskets;
