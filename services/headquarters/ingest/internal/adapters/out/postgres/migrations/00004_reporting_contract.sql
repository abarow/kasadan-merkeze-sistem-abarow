-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- The data reporting reads, as a contract (docs/REVIEW.md B1). reporting reads ingest's
-- tables only through these views: its connection puts ingest_v1 first in the
-- search_path, and the views have the tables' names, so its SQL names no schema.
-- PostgreSQL does not let a column a view uses be dropped or change its type: a change
-- of ingest that would break reporting fails here, in ingest's migration. Adding a
-- column is free; anything else needs a new version of the views (ingest_v2) that
-- reporting moves to first.
CREATE SCHEMA ingest_v1;
CREATE VIEW ingest_v1.sales AS SELECT * FROM sales;
CREATE VIEW ingest_v1.sale_lines AS SELECT * FROM sale_lines;
CREATE VIEW ingest_v1.sale_payments AS SELECT * FROM sale_payments;
CREATE VIEW ingest_v1.sale_taxes AS SELECT * FROM sale_taxes;
CREATE VIEW ingest_v1.voids AS SELECT * FROM voids;
CREATE VIEW ingest_v1.store_links AS SELECT * FROM store_links;
CREATE VIEW ingest_v1.store_stats AS SELECT * FROM store_stats;
CREATE VIEW ingest_v1.store_prices AS SELECT * FROM store_prices;
CREATE VIEW ingest_v1.cashiers AS SELECT * FROM cashiers;
CREATE VIEW ingest_v1.event_ranges AS SELECT * FROM event_ranges;

-- +goose Down
DROP SCHEMA ingest_v1 CASCADE;
