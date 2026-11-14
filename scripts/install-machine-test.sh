#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# install-memory-test.sh checks the install's memory check (memory_check in
# deploy/install/common.sh) with the repository's Quadlet files and a made-up /proc/meminfo:
# the sum of the containers' ceilings, a drop-in that changes a ceiling, the kiosk and the
# warning when the machine's memory is not enough. Nothing is installed.
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
	echo "install-memory-test: $*" >&2
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

echo "install-memory-test: OK"
