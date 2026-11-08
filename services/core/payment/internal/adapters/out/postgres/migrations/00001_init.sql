-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- A single payment record per sale; repeated requests are answered with this record.
CREATE TABLE charges (
    sale_id    TEXT        PRIMARY KEY,
    payments   TEXT        NOT NULL, -- JSON
    charged_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE charges;
