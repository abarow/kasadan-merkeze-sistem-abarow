#!/bin/sh
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# dbmaint.sh is the weekly maintenance of a tier's (store, headquarters) database
# and its report in the journal (journalctl --user -u abarow-dbmaint):
#
#   - a B-tree index of ABAROW_REINDEX_MIN_MB (8) MB or more whose leaf pages are
#     less than half full (many deletes and updates: the outbox, the retention
#     cleaning) is rebuilt with REINDEX CONCURRENTLY, which does not stop the writes;
#   - the ten statements that took the most time in the week, the ten slowest on average
#     and the ten that read the most from disk (pg_stat_statements; the counters then
#     start again; the backup's COPY statements are left out), the largest tables with
#     their dead rows and last autovacuum, the indexes never used, the large tables read
#     mostly by full scans, and the state of the WAL archive are printed. The report only
#     reads: an index is added or dropped by a migration, measured, never here
#     (docs/decisions/DB-TUNING.md);
#   - it fails if the archive is failing (the newest failure is newer than the
#     newest archived file): the WAL then stays in the data directory and fills the disk.
#
#   dbmaint.sh <database container>
#
# It runs on the machine as the abarow user (abarow-dbmaint.service) through the
# database container's own socket. CONTAINER (podman) is the container tool.
#
# At headquarters on three machines every machine runs it on its own database. A replica is
# read-only: there only the archive is checked (every machine archives the WAL,
# archive_mode=always); the reindex, the statements and the tables are on the primary.
set -eu
db=${1:?database container}
tool=${CONTAINER:-podman}
min_mb=${ABAROW_REINDEX_MIN_MB:-8}
case "$min_mb" in '' | *[!0-9]*) echo "ABAROW_REINDEX_MIN_MB must be a number" >&2; exit 1 ;; esac
# The maintenance's own statements (the index scan of pgstattuple, REINDEX) are not counted in
# pg_stat_statements: they would lead the lists of the slowest statements.
sql() { "$tool" exec -e PGOPTIONS='-c pg_stat_statements.track=none' "$db" psql -U abarow -d abarow -X -q -v ON_ERROR_STOP=1 "$@"; }
# not_backup: the statements of the daily pg_dump (a COPY ... TO stdout per table).
not_backup="query !~* '^[[:space:]]*COPY .* TO stdout'"

primary() {
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
		FROM pg_stat_statements WHERE $not_backup ORDER BY total_exec_time DESC LIMIT 10"
	echo "== the statements slowest on average"
	sql -c "SELECT round(mean_exec_time) AS mean_ms, round(max_exec_time) AS max_ms, calls,
		left(regexp_replace(query, '\s+', ' ', 'g'), 100) AS query
		FROM pg_stat_statements WHERE $not_backup ORDER BY mean_exec_time DESC LIMIT 10"
	echo "== the statements that read the most from disk (not from the buffers)"
	sql -c "SELECT pg_size_pretty(shared_blks_read * current_setting('block_size')::bigint) AS read, calls,
		round(100.0 * shared_blks_hit / nullif(shared_blks_hit + shared_blks_read, 0)) AS hit_pct,
		left(regexp_replace(query, '\s+', ' ', 'g'), 100) AS query
		FROM pg_stat_statements WHERE shared_blks_read > 0 AND $not_backup ORDER BY shared_blks_read DESC LIMIT 10"
	sql -tA -c 'SELECT pg_stat_statements_reset()' >/dev/null
	echo "== the largest tables"
	sql -c "SELECT schemaname || '.' || relname AS table, pg_size_pretty(pg_total_relation_size(relid)) AS size,
		n_live_tup AS live, n_dead_tup AS dead, last_autovacuum::timestamp(0) AS autovacuum
		FROM pg_stat_user_tables ORDER BY pg_total_relation_size(relid) DESC LIMIT 15"
	# The table statistics are not reset weekly: they count since stats_reset.
	since=$(sql -tA -c "SELECT coalesce(stats_reset::timestamp(0)::text, 'the database was created')
		FROM pg_stat_database WHERE datname = current_database()")
	echo "== indexes never used on this machine since $since (unique indexes keep a rule and are left out)"
	sql -c "SELECT s.schemaname || '.' || s.indexrelname AS index, s.relname AS table,
		pg_size_pretty(pg_relation_size(s.indexrelid)) AS size
		FROM pg_stat_user_indexes s JOIN pg_index i ON i.indexrelid = s.indexrelid
		WHERE s.idx_scan = 0 AND NOT i.indisunique ORDER BY pg_relation_size(s.indexrelid) DESC LIMIT 15"
	echo "== tables of 10000 rows or more read mostly by full scans, since $since"
	sql -c "SELECT schemaname || '.' || relname AS table, seq_scan, seq_tup_read, coalesce(idx_scan, 0) AS idx_scan,
		n_live_tup AS live
		FROM pg_stat_user_tables WHERE n_live_tup >= 10000 AND seq_scan > coalesce(idx_scan, 0)
		ORDER BY seq_tup_read DESC LIMIT 10"
}

if [ "$(sql -tA -c 'SELECT pg_is_in_recovery()')" = t ]; then
	echo "== replica: the reindex and the statements are on the primary; only the archive is checked"
else
	primary
fi
echo "== WAL archive"
sql -c "SELECT archived_count, last_archived_wal, last_archived_time::timestamp(0), failed_count, last_failed_wal,
	last_failed_time::timestamp(0) FROM pg_stat_archiver"
if [ "$(sql -tA -c "SELECT coalesce(last_failed_time > coalesce(last_archived_time, '-infinity'), false) FROM pg_stat_archiver")" = t ]; then
	echo "the WAL archive is failing: the WAL stays in the data directory and fills the disk (see the database log)" >&2
	exit 1
fi
