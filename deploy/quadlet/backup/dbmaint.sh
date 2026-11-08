#!/bin/sh
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# dbmaint.sh is the weekly maintenance of a tier's (store, headquarters) database
# and its report in the journal (journalctl --user -u abarow-dbmaint):
#
#   - a B-tree index of ABAROW_REINDEX_MIN_MB (8) MB or more whose leaf pages are
#     less than half full (many deletes and updates: the outbox, the retention
#     cleaning) is rebuilt with REINDEX CONCURRENTLY, which does not stop the writes;
#   - the ten statements that took the most time in the week (pg_stat_statements;
#     the counters then start again), the largest tables with their dead rows and
#     last autovacuum, and the state of the WAL archive are printed;
#   - it fails if the archive is failing (the newest failure is newer than the
#     newest archived file): the WAL then stays in the data directory and fills the disk.
#
#   dbmaint.sh <database container>
#
# It runs on the machine as the abarow user (abarow-dbmaint.service) through the
# database container's own socket. CONTAINER (podman) is the container tool.
set -eu
db=${1:?database container}
tool=${CONTAINER:-podman}
min_mb=${ABAROW_REINDEX_MIN_MB:-8}
case "$min_mb" in '' | *[!0-9]*) echo "ABAROW_REINDEX_MIN_MB must be a number" >&2; exit 1 ;; esac
sql() { "$tool" exec "$db" psql -U abarow -d abarow -X -q -v ON_ERROR_STOP=1 "$@"; }

sql -c 'CREATE EXTENSION IF NOT EXISTS pg_stat_statements' -c 'CREATE EXTENSION IF NOT EXISTS pgstattuple'

for idx in $(sql -tA -c "SELECT format('%I.%I', n.nspname, c.relname)
	FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace JOIN pg_am a ON a.oid = c.relam
	WHERE c.relkind = 'i' AND a.amname = 'btree' AND n.nspname NOT IN ('pg_catalog', 'information_schema')
	AND pg_relation_size(c.oid) >= $min_mb * 1024 * 1024 AND (pgstatindex(c.oid)).avg_leaf_density < 50"); do
	echo "reindex: $idx"
	sql -c "REINDEX INDEX CONCURRENTLY $idx"
done

echo "== the statements that took the most time since the last maintenance"
sql -c "SELECT round(total_exec_time / 1000) AS total_s, calls, round(mean_exec_time) AS mean_ms,
	left(regexp_replace(query, '\s+', ' ', 'g'), 100) AS query
	FROM pg_stat_statements ORDER BY total_exec_time DESC LIMIT 10"
sql -tA -c 'SELECT pg_stat_statements_reset()' >/dev/null
echo "== the largest tables"
sql -c "SELECT schemaname || '.' || relname AS table, pg_size_pretty(pg_total_relation_size(relid)) AS size,
	n_live_tup AS live, n_dead_tup AS dead, last_autovacuum::timestamp(0) AS autovacuum
	FROM pg_stat_user_tables ORDER BY pg_total_relation_size(relid) DESC LIMIT 15"
echo "== WAL archive"
sql -c "SELECT archived_count, last_archived_wal, last_archived_time::timestamp(0), failed_count, last_failed_wal,
	last_failed_time::timestamp(0) FROM pg_stat_archiver"
if [ "$(sql -tA -c "SELECT coalesce(last_failed_time > coalesce(last_archived_time, '-infinity'), false) FROM pg_stat_archiver")" = t ]; then
	echo "the WAL archive is failing: the WAL stays in the data directory and fills the disk (see the database log)" >&2
	exit 1
fi
