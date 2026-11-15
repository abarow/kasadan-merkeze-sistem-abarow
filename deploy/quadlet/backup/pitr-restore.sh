#!/bin/sh
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# pitr-restore.sh prepares a database data directory to be restored from a base
# backup (basebackup.sh) and the WAL archive up to a moment: when the database
# starts it replays the archive up to that moment, then opens for writing.
#
#   pitr-restore.sh <base backup file> [<moment>]
#
# The moment is a PostgreSQL time with its zone ("2026-11-12 14:30:00+03"); if
# none is given everything in the archive is replayed. It runs as root in a
# postgres container while the database is stopped, with the database volume at
# /var/lib/postgresql; the archive is the root of that volume (pkg/pgtune.ArchiveDir).
# The current data directory is not deleted: it is moved next to it
# (before-restore-<time>), so a wrong choice can be undone; remove it once the
# restore is checked.
set -eu
base=${1:?base backup file}
target=${2:-}
data=${PGDATA:?PGDATA}
[ -f "$base" ] || { echo "$base does not exist" >&2; exit 1; }
case "$target" in *"'"* | *'\'*) echo "invalid moment: $target" >&2; exit 1 ;; esac
if [ -f "$data/postmaster.pid" ]; then
	echo "$data/postmaster.pid exists: stop the database first" >&2
	exit 1
fi

if [ -d "$data" ] && [ -n "$(ls -A "$data")" ]; then
	aside="$(dirname "$data")/before-restore-$(date +%Y%m%d%H%M%S)"
	mv "$data" "$aside"
	echo "the current data directory was moved to $aside"
fi
mkdir -p "$data"
tar -xzf "$base" -C "$data"
# Replay the archive, then open for writing (a new timeline).
touch "$data/recovery.signal"
{
	echo "# pitr-restore.sh: removed with ALTER SYSTEM RESET once the restore is done"
	echo "restore_command = 'cp /var/lib/postgresql/%f %p'"
	echo "recovery_target_action = 'promote'"
	if [ -n "$target" ]; then
		echo "recovery_target_time = '$target'"
	fi
} >>"$data/postgresql.auto.conf"
chown -R postgres:postgres "$data"
chmod 0700 "$data"
echo "ready: start the database; it replays the archive up to ${target:-the end} and opens"
