#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# bench-headquarters.sh measures the performance of the headquarters database by
# the number of stores (docs/PERFORMANCE.md). An empty PostgreSQL container is
# started (the settings are the output of `reporting pgtune`,
# pkg/pgtune.ForHeadquarters; or PGTUNE_ARGS); services/headquarters/ingest/cmd/headquartersbench
# loads data that grows with the number of stores and applies load; the
# container's CPU and memory are sampled during the load.
# From the repository root:
#
#   bash scripts/bench-headquarters.sh <number-of-stores> [label]
#
# Output: $OUT/<label>-<stores>.json (the tool), .stats.csv (container), .conn.csv
# (number of connections). Environment variables:
#   OUT          output directory (default: tmp/bench-headquarters; not tracked in the repository)
#   DURATION     load duration (default 60s)
#   RATE         sales per second per register (default 0.2)
#   REGISTERS    registers per store (default 2)
#   PGTUNE_ARGS  these postgres flags instead of pgtune ("-c a=b ...")
#   PGTUNE_MEM   this container memory (MB) instead of pgtune
set -euo pipefail
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1

n=${1:?usage: bench-headquarters.sh <number-of-stores> [label]}
per=${REGISTERS:-2}
label=${2:-pgtune}
out=${OUT:-tmp/bench-headquarters}
mkdir -p "$out"
# On Windows let Go and bash see the same path (C:/...).
if command -v cygpath >/dev/null; then out=$(cygpath -m "$(cd "$out" && pwd)"); else out=$(cd "$out" && pwd); fi
name=abarow-bench-headquarters-db
port=25434

docker rm -f "$name" >/dev/null 2>&1 || true
if [ -z "${PGTUNE_ARGS:-}" ]; then
	tune=$(cd services/headquarters/reporting && go run ./cmd/reporting pgtune --stores "$n" --registers-per-store "$per")
	args=$(printf '%s\n' "$tune" | sed -n 's/^ARGS=//p')
	mem=$(printf '%s\n' "$tune" | sed -n 's/^MEMORY_MB=//p')
else
	args=$PGTUNE_ARGS
	mem=${PGTUNE_MEM:?PGTUNE_MEM must be given with PGTUNE_ARGS}
fi
echo "==> $n stores x $per registers ($label): memory ${mem}m; postgres $args"

# shellcheck disable=SC2086 # the flags are separated by spaces
docker run -d --name "$name" --memory="${mem}m" -p "127.0.0.1:$port:5432" \
	-e POSTGRES_PASSWORD=bench -e POSTGRES_DB=abarow -e POSTGRES_USER=abarow \
	postgres:18-alpine postgres $args >/dev/null
sampler=
cleanup() {
	[ -z "$sampler" ] || kill "$sampler" 2>/dev/null || true
	docker rm -f "$name" >/dev/null 2>&1 || true
}
trap cleanup EXIT
for _ in $(seq 1 60); do
	docker exec "$name" pg_isready -U abarow -d abarow >/dev/null 2>&1 && break
	sleep 1
done
# On the first start initdb runs a temporary server; wait until the real server is up.
sleep 3
until docker exec "$name" pg_isready -U abarow -d abarow >/dev/null 2>&1; do sleep 1; done

(cd services/headquarters/ingest && go build -o "$out/headquartersbench.exe" ./cmd/headquartersbench)
base="$out/$label-$n"
: >"$base.stats.csv"
: >"$base.conn.csv"
(
	while :; do
		docker stats --no-stream --format '{{.CPUPerc}},{{.MemUsage}}' "$name" >>"$base.stats.csv" 2>/dev/null || true
		docker exec "$name" psql -U abarow -d abarow -Atc "select count(*) from pg_stat_activity where backend_type='client backend'" >>"$base.conn.csv" 2>/dev/null || true
		sleep 1
	done
) &
sampler=$!
"$out/headquartersbench.exe" -dsn "postgres://abarow:bench@127.0.0.1:$port/abarow?sslmode=disable" \
	-stores "$n" -registers-per-store "$per" -duration "${DURATION:-60s}" -rate "${RATE:-0.2}" >"$base.json"
kill "$sampler" 2>/dev/null || true
# The memory peak (cgroup) and whether it was killed for lack of memory.
{
	echo "oom_killed=$(docker inspect -f '{{.State.OOMKilled}}' "$name")"
	echo "memory_peak_bytes=$(docker exec "$name" sh -c 'cat /sys/fs/cgroup/memory.peak 2>/dev/null || echo 0')"
} >"$base.cgroup"
docker exec "$name" psql -U abarow -d abarow -Atc "select name||'='||setting from pg_settings where name in ('shared_buffers','max_connections','work_mem')" >"$base.settings" 2>/dev/null || true
echo "==> $base.json"
