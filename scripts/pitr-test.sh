#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# pitr-test.sh tries the point-in-time restore of the store server and headquarters
# database with Docker: the WAL archive settings of pkg/pgtune, a base backup with
# deploy/quadlet/backup/basebackup.sh and a restore with pitr-restore.sh to a moment
# between two writes. The row written before the moment must be there, the one after
# it must not, and the restored database must accept writes.
set -euo pipefail
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1

image=postgres:18-alpine
db=abarow-pitr-test
work=$(mktemp -d)
cleanup() {
	docker rm -fv "$db" >/dev/null 2>&1 || true
	docker volume rm abarow-pitr-test-data >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT
mkdir -p "$work/backups"
# The paths Docker sees (on Windows Git Bash gives /tmp/..., Docker needs C:/...).
hostpath() { (cd "$1" && pwd -W 2>/dev/null) || (cd "$1" && pwd); }
flags=$(cd services/store && go run ./cmd/report pgtune --registers 2 | sed -n 's/^ARGS=//p')
case "$flags" in *archive_mode=on*) ;; *) echo "pgtune gives no WAL archive settings: $flags" >&2; exit 1 ;; esac

start() {
	# shellcheck disable=SC2086 # the flags are separate words
	docker run -d --name "$db" -e POSTGRES_USER=abarow -e POSTGRES_PASSWORD=test -e POSTGRES_DB=abarow \
		-v abarow-pitr-test-data:/var/lib/postgresql "$image" postgres $flags >/dev/null
	for _ in $(seq 120); do
		if docker exec "$db" pg_isready -U abarow -d abarow >/dev/null 2>&1 &&
			[ "$(docker exec "$db" psql -U abarow -d abarow -tAc 'SELECT pg_is_in_recovery()' 2>/dev/null)" = f ]; then
			return 0
		fi
		sleep 1
	done
	docker logs "$db" >&2
	echo "the database did not open" >&2
	exit 1
}
sql() { docker exec "$db" psql -U abarow -d abarow -v ON_ERROR_STOP=1 -tAc "$1"; }

start
sql "CREATE TABLE t (id int PRIMARY KEY, at timestamptz NOT NULL DEFAULT clock_timestamp())" >/dev/null
sql "INSERT INTO t (id) VALUES (1)" >/dev/null
CONTAINER=docker sh deploy/quadlet/backup/basebackup.sh "$db" "$work/backups"
sql "INSERT INTO t (id) VALUES (2)" >/dev/null
sleep 1
moment=$(sql "SELECT clock_timestamp()")
sleep 1
sql "INSERT INTO t (id) VALUES (3)" >/dev/null
# The last segment goes to the archive now (archive_timeout would do it later).
seg=$(sql "SELECT pg_walfile_name(pg_switch_wal())")
for _ in $(seq 30); do
	[ "$(sql "SELECT coalesce(last_archived_wal, '') >= '$seg' FROM pg_stat_archiver")" = t ] && break
	sleep 1
done
if [ "$(sql "SELECT coalesce(last_archived_wal, '') >= '$seg' AND last_failed_wal IS NULL FROM pg_stat_archiver")" != t ]; then
	sql "SELECT * FROM pg_stat_archiver" >&2
	echo "$seg was not archived" >&2
	exit 1
fi
docker stop -t 30 "$db" >/dev/null
docker rm "$db" >/dev/null

base=$(ls "$work/backups" | grep '^base-.*\.tar\.gz$' | head -n 1)
docker run --rm -v abarow-pitr-test-data:/var/lib/postgresql -v "$(hostpath "$work/backups"):/backup:ro" \
	-v "$(hostpath deploy/quadlet/backup)/pitr-restore.sh:/pitr-restore.sh:ro" "$image" sh /pitr-restore.sh "/backup/$base" "$moment"
start
sql "ALTER SYSTEM RESET restore_command" >/dev/null
sql "ALTER SYSTEM RESET recovery_target_action" >/dev/null
sql "ALTER SYSTEM RESET recovery_target_time" >/dev/null
got=$(sql "SELECT string_agg(id::text, ',' ORDER BY id) FROM t")
if [ "$got" != "1,2" ]; then
	echo "after the restore to $moment the rows are $got; 1,2 expected" >&2
	exit 1
fi
sql "INSERT INTO t (id) VALUES (4)" >/dev/null
if docker exec "$db" grep -q restore_command /var/lib/postgresql/18/docker/postgresql.auto.conf; then
	echo "the restore settings stayed in postgresql.auto.conf" >&2
	exit 1
fi
echo "point-in-time restore OK (rows $got at $moment, writes accepted)"
