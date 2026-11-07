#!/bin/sh
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# backup.sh takes a backup of a tier's (store, headquarters) database and deletes
# backups older than ABAROW_BACKUP_DAYS days. It runs in the abarow-backup
# container (the postgres image, pg_dump); the connection details are in the PG* environment variables.
#
# The backup is in pg_dump's own format (compressed); to restore see
# deploy/quadlet/README.md ("Restoring from a backup").
set -eu
days=${ABAROW_BACKUP_DAYS:-14}
name="abarow-$(date +%Y-%m-%d-%H%M%S).dump"
# It is written under a temporary name first: a half-written backup is not taken for a complete one.
pg_dump --format=custom --file="/backup/.$name.tmp"
mv "/backup/.$name.tmp" "/backup/$name"
# If the new backup could not be taken (set -e) the old ones are not deleted.
find /backup -maxdepth 1 -type f -name 'abarow-*.dump' -mtime +"$days" -delete
find /backup -maxdepth 1 -type f -name '.abarow-*.tmp' -mtime +1 -delete
echo "backup taken: $name ($(du -h "/backup/$name" | cut -f1)); total $(find /backup -maxdepth 1 -name 'abarow-*.dump' | wc -l) backups"
