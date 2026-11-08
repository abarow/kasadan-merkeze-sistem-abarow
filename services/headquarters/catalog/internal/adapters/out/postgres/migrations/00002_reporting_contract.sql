-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- The catalog data reporting reads (the store price list), as a contract
-- (docs/REVIEW.md B1; the same rule as ingest's 00004_reporting_contract.sql): reporting
-- reads only these views, a change that would break it fails here.
CREATE SCHEMA catalog_v1;
CREATE VIEW catalog_v1.catalog_products AS SELECT * FROM catalog_products;
CREATE VIEW catalog_v1.catalog_prices AS SELECT * FROM catalog_prices;

-- +goose Down
DROP SCHEMA catalog_v1 CASCADE;
