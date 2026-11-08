-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- The product ranking and the monthly summary join sale_lines to the sales of the period
-- (docs/PERFORMANCE.md, "Aylık rapor için indeks çalışması"). The key carries the columns
-- the report reads, so the join is answered from the index alone, without visiting the
-- table (index-only scan): 1.2 million sales, 30-day product ranking 1996 ms → 685 ms.
-- name is bounded by ledgerdb.MaxLineNameBytes; an index entry cannot exceed a third of a page.
ALTER TABLE sale_lines
    DROP CONSTRAINT sale_lines_pkey,
    ADD PRIMARY KEY (store_id, sale_id, line_no) INCLUDE (product_id, name, qty, total);

-- +goose Down
ALTER TABLE sale_lines
    DROP CONSTRAINT sale_lines_pkey,
    ADD PRIMARY KEY (store_id, sale_id, line_no);
