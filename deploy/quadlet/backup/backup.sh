#!/bin/sh
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# backup.sh takes a backup of a tier's (store, headquarters) database and deletes
# backups older than ABAROW_BACKUP_DAYS days. It runs in the abarow-backup
# container (the postgres image, pg_dump); the connection details are in the PG* environment variables.
#
# The backup is in pg_dump's own format (compressed); to restore see
# deploy/quadlet/README.md ("Restoring from a backup").
#
# Headquarters on three machines: ABAROW_BACKUP_LOCAL is this machine's database (abarow-patroni).
# Only the machine whose database is primary takes the backup; the others would read the same
# data from it a second and a third time (docs/REVIEW-2.md P6). If this machine's database
# cannot be asked, the backup is taken anyway: two backups are better than none.
set -eu
days=${ABAROW_BACKUP_DAYS:-14}
if [ -n "${ABAROW_BACKUP_LOCAL:-}" ] &&
	[ "$(psql -h "$ABAROW_BACKUP_LOCAL" -X -tAc 'SELECT pg_is_in_recovery()' 2>/dev/null)" = t ]; then
	echo "this machine's database is a replica: the machine of the primary takes the backup"
	exit 0
fi
name="abarow-$(date +%Y-%m-%d-%H%M%S).dump"
# It is written under a temporary name first: a half-written backup is not taken for a complete one.
pg_dump --format=custom --file="/backup/.$name.tmp"
mv "/backup/.$name.tmp" "/backup/$name"
# If the new backup could not be taken (set -e) the old ones are not deleted.
find /backup -maxdepth 1 -type f -name 'abarow-*.dump' -mtime +"$days" -delete
find /backup -maxdepth 1 -type f -name '.abarow-*.tmp' -mtime +1 -delete
echo "backup taken: $name ($(du -h "/backup/$name" | cut -f1)); total $(find /backup -maxdepth 1 -name 'abarow-*.dump' | wc -l) backups"
