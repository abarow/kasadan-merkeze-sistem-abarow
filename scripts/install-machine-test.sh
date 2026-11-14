#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# install-machine-test.sh checks the install's machine checks in deploy/install/common.sh,
# without installing anything:
#
#   - memory_check, with the repository's Quadlet files and a made-up /proc/meminfo: the sum of
#     the containers' ceilings, a drop-in that changes a ceiling, the kiosk and the warning when
#     the machine's memory is not enough;
#   - disk_rotational, with findmnt and lsblk replaced: a spinning disk, an SSD, a btrfs
#     subvolume, a virtual machine and a disk that cannot be read.
set -euo pipefail
cd "$(dirname "$0")/.."
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# shellcheck source=deploy/install/common.sh
. deploy/install/common.sh
home="$work/home"
units="$home/.config/containers/systemd"
mkdir -p "$units"
memory_meminfo="$work/meminfo"
memory_kiosk_unit="$work/abarow-kiosk.service"

fail() {
	echo "install-machine-test: $*" >&2
	exit 1
}
meminfo() { printf 'MemTotal:       %s kB\nMemFree:          123456 kB\n' "$1" >"$memory_meminfo"; }
expect() {
	local out
	out=$(memory_check)
	printf '%s\n' "$out" | grep -qF -- "$1" || fail "want \"$1\", got: $out"
}

echo "== register"
cp deploy/quadlet/abarow-*.container "$units/"
# The register's ten containers: the optimum of docs/HARDWARE.md.
[ "$(memory_ceilings)" = 736 ] || fail "register ceilings: $(memory_ceilings)"
meminfo 4012345 # about 4 GB
expect "Memory: 3918 MB; the containers' ceilings (736 MB), the operating system need 992 MB at most"

echo "== register with the kiosk on 1 GB"
touch "$memory_kiosk_unit"
meminfo 1048576
expect "WARNING: the machine has 1024 MB of memory; the containers' ceilings total 736 MB, with the operating system (256 MB) and the kiosk (512 MB) 1504 MB."

echo "== register and store server on one machine; the database drop-in counts"
cp deploy/quadlet/store/abarow-*.container "$units/"
mkdir -p "$units/abarow-store-db.container.d"
# What store-install.sh writes for ten registers (pkg/pgtune); the unit file's 256m is not counted.
printf '[Container]\nExec=postgres -c shared_buffers=112MB\nPodmanArgs=--memory=576m --shm-size=192m\n' >"$units/abarow-store-db.container.d/tune.conf"
[ "$(memory_ceilings)" = $((736 + 544 - 256 + 576)) ] || fail "register and store ceilings: $(memory_ceilings)"
meminfo 4012345
expect "the containers' ceilings (1600 MB), the operating system and the kiosk (512 MB) need 2368 MB at most"

echo "== unreadable memory"
: >"$memory_meminfo"
expect "WARNING: the machine's memory could not be read"

echo "== disk type"
# The made-up machine: the containers' directory is on $fake_source, whose ROTA is $fake_rota.
findmnt() {
	[ "$*" = "-no SOURCE --target $home/.local/share/containers" ] || fail "findmnt $*"
	printf '%s\n' "$fake_source"
}
lsblk() {
	[ "$*" = "-dno ROTA /dev/sda2" ] || return 32
	printf '%s\n' "$fake_rota"
}
mkdir -p "$home/.local/share/containers"
disk_virt=false
disk() {
	local got
	fake_source=$1 fake_rota=$2
	got=$(disk_rotational)
	[ "$got" = "$3" ] || fail "disk $1 (ROTA $2): $got, want $3"
}
disk /dev/sda2 "   1" true
disk /dev/sda2 "   0" false
disk "/dev/sda2[/@home]" 1 true # btrfs
disk /dev/nvme0n1p2 1 false     # lsblk does not know it: SSD assumed
disk "" 1 false                 # findmnt found nothing
disk_virt=true
disk /dev/sda2 1 false # a virtual machine: its disk's flag is not trusted
disk_warning false | grep -q . && fail "a warning for an SSD"
disk_warning true | grep -qF "WARNING: the database is on a spinning disk" || fail "no warning for a spinning disk"

echo "== restart only for the settings that need it"
# The made-up database answers the pg_settings question like PostgreSQL 18 (tried on a real one):
# the settings it knows that do not need a restart. basic_archive's setting is not in pg_settings
# of a session. With db_down it does not answer.
db_down=''
as() {
	local sql=${*: -1} n=0 name
	[ "$1 $2 $3" = "podman exec abarow-store-db" ] && [ -z "$db_down" ] || return 125
	for name in $(printf '%s\n' "$sql" | sed -n "s/.*name IN (\(.*\))$/\1/p" | tr -d "'" | tr ',' ' '); do
		case $name in
		max_wal_size | min_wal_size | work_mem | random_page_cost | effective_io_concurrency | maintenance_io_concurrency) n=$((n + 1)) ;;
		esac
	done
	printf '%s\n' "$n"
}
tune() { printf '[Container]\nExec=postgres %s\nPodmanArgs=--memory=%sm --shm-size=64m\n' "$1" "$2"; }
base="-c shared_buffers=256MB -c max_connections=20 -c random_page_cost=1.1 -c max_wal_size=480MB -c basic_archive.archive_directory=/var/lib/postgresql"
restart() {
	local got
	got=$(tune_restart abarow-store-db "$(tune "$base" 1024)" "$(tune "$2" "${3:-1024}")")
	[ "$got" = "$1" ] || fail "restart for $2 (${3:-1024} MB): $got, want $1"
}
restart false "$base"
restart false "${base/480MB/496MB}" # a new register above the ceiling
restart false "${base/random_page_cost=1.1/random_page_cost=4 -c effective_io_concurrency=2}" # a setting added
restart true "${base/256MB/264MB}" # shared_buffers
restart true "$base" 1088 # the container's memory
restart true "${base/\/var\/lib\/postgresql/\/srv}" # not in pg_settings
restart true "${base/ -c max_connections=20/}" # a setting removed (back to the default)
db_down=1
restart true "${base/480MB/496MB}" # the database does not answer

echo "install-machine-test: OK"
