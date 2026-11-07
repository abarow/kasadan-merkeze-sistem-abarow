-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- The periods between two Z reports; closed_at is empty in the open period.
CREATE TABLE periods (
    z_no      INTEGER PRIMARY KEY AUTOINCREMENT,
    opened_at TEXT NOT NULL,
    closed_at TEXT
);

-- Cashiers; the PIN is stored only as its bcrypt digest. origin: where the cashier
-- came from (the file in the install or the store server screen); the file does not change
-- a cashier that came from the store server. disabled: if the account is closed they cannot sign in.
CREATE TABLE cashiers (
    id              TEXT PRIMARY KEY,
    name            TEXT    NOT NULL,
    role            TEXT    NOT NULL CHECK (role IN ('cashier', 'manager')),
    pin_hash        BLOB    NOT NULL,
    failed_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until    TEXT,
    origin          TEXT    NOT NULL DEFAULT 'seed' CHECK (origin IN ('seed', 'store')),
    disabled        INTEGER NOT NULL DEFAULT 0
);

-- Sessions; not the key itself but its SHA-256 digest is stored.
CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    cashier_id TEXT NOT NULL REFERENCES cashiers (id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL
);

-- Cashier shifts. There is a single drawer at the register: only one shift
-- can be open at a time (shifts_one_open).
CREATE TABLE shifts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    cashier_id   TEXT    NOT NULL REFERENCES cashiers (id),
    opened_at    TEXT    NOT NULL,
    closed_at    TEXT,
    opening_cash INTEGER NOT NULL,
    counted_cash INTEGER NOT NULL DEFAULT 0,
    closed_by    TEXT    NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX shifts_one_open ON shifts ((closed_at IS NULL)) WHERE closed_at IS NULL;
CREATE INDEX shifts_cashier ON shifts (cashier_id, opened_at);

-- The sales that enter the report (from sales' events). If an event comes again
-- the sale_id primary key prevents a second record. A sale is tied to the shift if its cashier
-- is the shift's owner and it was made while the shift was open;
-- shift_id is empty for a sale with no cashier.
CREATE TABLE completed_sales (
    sale_id       TEXT PRIMARY KEY,
    z_no          INTEGER NOT NULL REFERENCES periods (z_no),
    receipt_no    INTEGER NOT NULL,
    total         INTEGER NOT NULL,
    tax_total     INTEGER NOT NULL,
    change_amount INTEGER NOT NULL,
    taxes         TEXT    NOT NULL, -- JSON
    payments      TEXT    NOT NULL, -- JSON
    occurred_at   TEXT    NOT NULL,
    cashier_id    TEXT    NOT NULL DEFAULT '',
    shift_id      INTEGER REFERENCES shifts (id)
);
CREATE INDEX completed_sales_z ON completed_sales (z_no);
CREATE INDEX completed_sales_shift ON completed_sales (shift_id);

CREATE TABLE voided_sales (
    sale_id     TEXT PRIMARY KEY,
    z_no        INTEGER NOT NULL REFERENCES periods (z_no),
    total       INTEGER NOT NULL,
    occurred_at TEXT    NOT NULL,
    cashier_id  TEXT    NOT NULL DEFAULT '',
    shift_id    INTEGER REFERENCES shifts (id)
);
CREATE INDEX voided_sales_z ON voided_sales (z_no);
CREATE INDEX voided_sales_shift ON voided_sales (shift_id);

-- Cash that enters (in) or leaves (out) the drawer apart from sales in an open shift;
-- it enters the shift's expected cash.
CREATE TABLE cash_movements (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    shift_id   INTEGER NOT NULL REFERENCES shifts (id),
    kind       TEXT    NOT NULL CHECK (kind IN ('in', 'out')),
    amount     INTEGER NOT NULL CHECK (amount > 0),
    note       TEXT    NOT NULL,
    cashier_id TEXT    NOT NULL REFERENCES cashiers (id),
    at         TEXT    NOT NULL
);
CREATE INDEX cash_movements_shift ON cash_movements (shift_id);

-- Manager approvals: authority given with the manager's PIN for a single operation
-- while in a cashier session, short-lived and single-use. Not the key itself but its
-- SHA-256 digest is stored; the records show who requested and who
-- approved.
CREATE TABLE approvals (
    token_hash   TEXT PRIMARY KEY,
    manager_id   TEXT NOT NULL REFERENCES cashiers (id),
    action       TEXT NOT NULL,
    requested_by TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    used_at      TEXT
);

-- Openings of the drawer without a sale (loss prevention): who opened (the manager),
-- kimin oturumunda, neden.
CREATE TABLE drawer_opens (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    shift_id     INTEGER NOT NULL REFERENCES shifts (id),
    opened_by    TEXT    NOT NULL REFERENCES cashiers (id),
    requested_by TEXT    NOT NULL,
    reason       TEXT    NOT NULL,
    at           TEXT    NOT NULL
);
CREATE INDEX drawer_opens_shift ON drawer_opens (shift_id);

-- Events waiting to be published (shift opening and closing). The event is
-- written in the same transaction as the shift record; the relay publishes in order
-- (bkz. pkg/outbox).
CREATE TABLE outbox (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    id           TEXT NOT NULL UNIQUE,
    type         TEXT NOT NULL,
    subject      TEXT NOT NULL,
    store_id     TEXT NOT NULL,
    register_id  TEXT NOT NULL,
    payload      BLOB NOT NULL,
    content_type TEXT NOT NULL,
    occurred_at  TEXT NOT NULL,
    published_at TEXT
);
CREATE INDEX outbox_unpublished ON outbox (seq) WHERE published_at IS NULL;

-- Delivery receipt (pkg/delivery): when the store server processes an event into the database it
-- gives a UUIDv7 receipt id for the event; the receipt is written here next to the event.
-- An old event in the register stream is deleted only if its receipt is recorded here.
CREATE TABLE delivery_received (
    event_id    TEXT PRIMARY KEY,
    receipt_id  TEXT NOT NULL UNIQUE,
    issued_at   TEXT NOT NULL,
    received_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE delivery_received;
DROP TABLE outbox;
DROP TABLE drawer_opens;
DROP TABLE approvals;
DROP TABLE cash_movements;
DROP TABLE voided_sales;
DROP TABLE completed_sales;
DROP TABLE shifts;
DROP TABLE sessions;
DROP TABLE cashiers;
DROP TABLE periods;
