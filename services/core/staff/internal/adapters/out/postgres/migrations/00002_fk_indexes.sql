-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- Indexes on the foreign key columns that had none: closing or removing a cashier
-- (sessions ON DELETE CASCADE) and the checks of the referenced rows do not scan the table.
CREATE INDEX cash_movements_cashier ON cash_movements (cashier_id);
CREATE INDEX approvals_manager ON approvals (manager_id);
CREATE INDEX drawer_opens_opened_by ON drawer_opens (opened_by);
CREATE INDEX sessions_cashier ON sessions (cashier_id);

-- +goose Down
DROP INDEX sessions_cashier;
DROP INDEX drawer_opens_opened_by;
DROP INDEX approvals_manager;
DROP INDEX cash_movements_cashier;
