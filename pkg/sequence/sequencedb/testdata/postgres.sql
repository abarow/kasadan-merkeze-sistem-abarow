-- Copyright 2026 Mehmet ERGULCU and the abarow contributors
-- SPDX-License-Identifier: Apache-2.0

CREATE TABLE event_ranges (
    store_id    TEXT   NOT NULL,
    register_id TEXT   NOT NULL,
    producer    TEXT   NOT NULL,
    from_seq    BIGINT NOT NULL,
    to_seq      BIGINT NOT NULL,
    PRIMARY KEY (store_id, register_id, producer, from_seq),
    CHECK (from_seq <= to_seq)
);
CREATE UNIQUE INDEX event_ranges_to ON event_ranges (store_id, register_id, producer, to_seq);
