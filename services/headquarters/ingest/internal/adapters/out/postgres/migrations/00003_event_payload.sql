-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- The raw content of an event is kept for a limited time (pkg/retention,
-- docs/decisions/RETENTION.md); after that payload is NULL. The row stays: its id still
-- de-duplicates an event that comes again. An event that had no content has an empty
-- payload, not NULL. The index holds only the rows that still have their content, so the
-- cleaning finds the old ones without scanning the table.
ALTER TABLE events ALTER COLUMN payload DROP NOT NULL;
CREATE INDEX events_payload_kept ON events (received_at) WHERE payload IS NOT NULL;

-- +goose Down
DROP INDEX events_payload_kept;
UPDATE events SET payload = ''::bytea WHERE payload IS NULL;
ALTER TABLE events ALTER COLUMN payload SET NOT NULL;
