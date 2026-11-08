-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- The time columns become TIMESTAMPTZ as in the other schemas (docs/REVIEW.md D7). They
-- were fixed-width UTC text (COLLATE "C": the byte order was the time order); every value
-- written was RFC 3339, which PostgreSQL reads as it is. PostgreSQL keeps microseconds:
-- the nanoseconds of the text are rounded.
ALTER TABLE periods
    ALTER COLUMN opened_at TYPE TIMESTAMPTZ USING opened_at::timestamptz,
    ALTER COLUMN closed_at TYPE TIMESTAMPTZ USING closed_at::timestamptz,
    ALTER COLUMN details_removed_at TYPE TIMESTAMPTZ USING details_removed_at::timestamptz;
ALTER TABLE cashiers
    ALTER COLUMN locked_until TYPE TIMESTAMPTZ USING locked_until::timestamptz;
ALTER TABLE sessions
    ALTER COLUMN expires_at TYPE TIMESTAMPTZ USING expires_at::timestamptz;
ALTER TABLE shifts
    ALTER COLUMN opened_at TYPE TIMESTAMPTZ USING opened_at::timestamptz,
    ALTER COLUMN closed_at TYPE TIMESTAMPTZ USING closed_at::timestamptz,
    ALTER COLUMN details_removed_at TYPE TIMESTAMPTZ USING details_removed_at::timestamptz;
ALTER TABLE completed_sales
    ALTER COLUMN occurred_at TYPE TIMESTAMPTZ USING occurred_at::timestamptz;
ALTER TABLE voided_sales
    ALTER COLUMN occurred_at TYPE TIMESTAMPTZ USING occurred_at::timestamptz;
ALTER TABLE cash_movements
    ALTER COLUMN at TYPE TIMESTAMPTZ USING at::timestamptz;
ALTER TABLE approvals
    ALTER COLUMN expires_at TYPE TIMESTAMPTZ USING expires_at::timestamptz,
    ALTER COLUMN used_at TYPE TIMESTAMPTZ USING used_at::timestamptz;
ALTER TABLE drawer_opens
    ALTER COLUMN at TYPE TIMESTAMPTZ USING at::timestamptz;

-- +goose Down
ALTER TABLE drawer_opens
    ALTER COLUMN at TYPE TEXT COLLATE "C" USING to_char(at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"000Z"');
ALTER TABLE approvals
    ALTER COLUMN expires_at TYPE TEXT COLLATE "C" USING to_char(expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"000Z"'),
    ALTER COLUMN used_at TYPE TEXT COLLATE "C" USING to_char(used_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"000Z"');
ALTER TABLE cash_movements
    ALTER COLUMN at TYPE TEXT COLLATE "C" USING to_char(at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"000Z"');
ALTER TABLE voided_sales
    ALTER COLUMN occurred_at TYPE TEXT COLLATE "C" USING to_char(occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"000Z"');
ALTER TABLE completed_sales
    ALTER COLUMN occurred_at TYPE TEXT COLLATE "C" USING to_char(occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"000Z"');
ALTER TABLE shifts
    ALTER COLUMN opened_at TYPE TEXT COLLATE "C" USING to_char(opened_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"000Z"'),
    ALTER COLUMN closed_at TYPE TEXT COLLATE "C" USING to_char(closed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"000Z"'),
    ALTER COLUMN details_removed_at TYPE TEXT COLLATE "C" USING to_char(details_removed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"000Z"');
ALTER TABLE sessions
    ALTER COLUMN expires_at TYPE TEXT COLLATE "C" USING to_char(expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"000Z"');
ALTER TABLE cashiers
    ALTER COLUMN locked_until TYPE TEXT COLLATE "C" USING to_char(locked_until AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"000Z"');
ALTER TABLE periods
    ALTER COLUMN opened_at TYPE TEXT COLLATE "C" USING to_char(opened_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"000Z"'),
    ALTER COLUMN closed_at TYPE TEXT COLLATE "C" USING to_char(closed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"000Z"'),
    ALTER COLUMN details_removed_at TYPE TEXT COLLATE "C" USING to_char(details_removed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"000Z"');
