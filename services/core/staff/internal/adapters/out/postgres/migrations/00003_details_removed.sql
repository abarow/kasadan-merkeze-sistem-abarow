-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- The register keeps the sale details of a closed day for a limited time (pkg/retention,
-- docs/decisions/RETENTION.md). details_removed_at is the moment the sales, cash
-- movements and drawer openings of the period or the shift were removed; its report is
-- then no longer made at the register (it would show zero).
ALTER TABLE periods ADD COLUMN details_removed_at TEXT COLLATE "C";
ALTER TABLE shifts ADD COLUMN details_removed_at TEXT COLLATE "C";

-- +goose Down
ALTER TABLE shifts DROP COLUMN details_removed_at;
ALTER TABLE periods DROP COLUMN details_removed_at;
