-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- The unit's latest status report (running version, image digests). The report is state,
-- not identity: it is written independently of the registration decisions, old rows stay empty.
ALTER TABLE update_registrations ADD COLUMN report JSONB;

-- +goose Down
ALTER TABLE update_registrations DROP COLUMN report;
