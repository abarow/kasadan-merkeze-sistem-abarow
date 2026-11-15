#!/bin/sh
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# backup-copy.sh copies a tier's (store, headquarters) database backups to a
# target off the machine: an external disk or a NAS (/mnt/backup/abarow) or
# another server over SSH (user@server:/path; user@server without a path is the
# directory the server decides with rrsync). abarow-backup-copy.service
# (abarow-store-backup-copy.service) runs it as the abarow user after every
# backup; the target is in ABAROW_BACKUP_TARGET (headquarters-install.sh and
# store-install.sh write it with --backup-target).
#
# Backups that are not at the target are copied: the ones that could not be
# copied earlier (the disk was not attached, the server was unreachable) go on
# the next run too. Files at the target are not overwritten, and a backup
# deleted locally is not deleted at the target. On an external disk copies older
# than ABAROW_BACKUP_COPY_DAYS days (90) are deleted; the server deletes the copies on a server.
set -eu
src=${1:?backup directory}
target=${ABAROW_BACKUP_TARGET:?target (ABAROW_BACKUP_TARGET)}
days=${ABAROW_BACKUP_COPY_DAYS:-90}
# Only completed backups (those being written end with .tmp); there are no
# subdirectories in the directory.
set -- --recursive --times --ignore-existing --chmod=F600 --include='*.dump' --include='base-*.tar.gz' --exclude='*' --out-format='%n'
# Copies run one at a time (the one after a backup and a manually started one
# can come together; a server restricted with rrsync refuses the second at the same time).
exec 9>"${XDG_RUNTIME_DIR:-/tmp}/abarow-backup-copy.lock"
flock 9

case "$target" in
/*)
	# If the disk is not attached the mount point is an empty directory: the copy
	# would be written to the same disk as the backups and would go with it if the disk fails.
	[ -d "$target" ] || { echo "target directory does not exist: $target (is the disk attached and mounted?)" >&2; exit 1; }
	if [ "$(stat -c %d "$target")" = "$(stat -c %d "$src")" ]; then
		echo "the target is on the same disk as the backups: $target (is the disk attached and mounted?); not copied" >&2
		exit 1
	fi
	copied=$(rsync "$@" "$src/" "$target/")
	find "$target" -maxdepth 1 -type f \( -name '*.dump' -o -name 'base-*.tar.gz' \) -mtime +"$days" -delete
	;;
*:* | *@*)
	# With a target without a path, it is written to the user's home directory
	# (to the directory the server decides if restricted with rrsync).
	case "$target" in
	*:*) dest="$target/" ;;
	*) dest="$target:" ;;
	esac
	# The key is generated only for this job (headquarters-install.sh); the
	# server's key is recorded at the first connection, and if it changes later the connection is refused.
	copied=$(rsync "$@" -e "ssh -i $HOME/.ssh/abarow-backup -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=30" "$src/" "$dest")
	;;
*)
	echo "invalid target: $target (/path, user@server or user@server:/path)" >&2
	exit 1
	;;
esac
echo "copied off the machine: $(printf '%s\n' "$copied" | grep -c -e '\.dump$' -e '\.tar\.gz$' || true) new backups → $target"
