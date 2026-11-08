#!/bin/sh
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# basebackup.sh takes a physical backup (base backup) of a tier's (store,
# headquarters) database and removes the WAL files in the archive that no kept
# backup needs any more. Together with the WAL archive (archive_mode; the root of
# the data volume, /var/lib/postgresql, pkg/pgtune.ArchiveDir) it lets the
# database be restored to any moment after the oldest kept base backup
# (pitr-restore.sh). The daily pg_dump backup (backup.sh) stays: it restores a
# single table and survives a major version upgrade, a base backup does not.
#
#   basebackup.sh <database container> <backup directory>
#
# It runs on the machine as the abarow user (abarow-basebackup.service); the
# backup is read through the database container's own socket, so no password and
# no replication connection from outside is needed. ABAROW_BASEBACKUP_KEEP (2) is
# the number of base backups kept. CONTAINER (podman) is the container tool.
set -eu
db=${1:?database container}
dir=${2:?backup directory}
keep=${ABAROW_BASEBACKUP_KEEP:-2}
tool=${CONTAINER:-podman}
case "$keep" in '' | *[!0-9]* | 0) echo "ABAROW_BASEBACKUP_KEEP must be 1 or more" >&2; exit 1 ;; esac

name="base-$(date +%Y-%m-%d-%H%M%S).tar.gz"
# A tar of the data directory with the WAL needed to make it consistent (-X fetch):
# the backup can be restored even without the archive. It is written under a
# temporary name first: a half-written backup is not taken for a complete one.
"$tool" exec "$db" pg_basebackup -U abarow -D - -F tar -X fetch -z -c fast >"$dir/.$name.tmp"
mv "$dir/.$name.tmp" "$dir/$name"

# The newest $keep backups stay. If the new backup could not be taken (set -e)
# nothing is removed.
ls -1 "$dir"/base-*.tar.gz | sort | head -n "-$keep" | while read -r old; do rm -f "$old"; done
find "$dir" -maxdepth 1 -type f -name '.base-*.tmp' -mtime +1 -delete

# The archive is needed from the start of the oldest kept backup on;
# pg_archivecleanup removes only WAL files older than that (no other file).
oldest=$(ls -1 "$dir"/base-*.tar.gz | sort | head -n 1)
first=$(tar -xzOf "$oldest" backup_label | sed -n 's/^START WAL LOCATION: .* (file \([0-9A-F]*\))$/\1/p')
[ -n "$first" ] || { echo "no start WAL in $oldest; the archive was not cleaned" >&2; exit 1; }
"$tool" exec "$db" pg_archivecleanup /var/lib/postgresql "$first"
echo "base backup taken: $name ($(du -h "$dir/$name" | cut -f1)); archive kept from $first"
