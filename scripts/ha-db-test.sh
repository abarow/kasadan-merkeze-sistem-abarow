#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# ha-db-test.sh tries the headquarters database on three machines with Docker
# (deploy/compose/ha-db.yaml; docs/decisions/HA-HEADQUARTERS.md):
#
#  1. While a writer (pkg/pgdb TestFailover) writes through HAProxy, the primary is killed.
#     Another machine must take over, the writes must resume, and every acknowledged
#     write must be on the new primary. The old primary comes back as a replica.
#  2. Both replicas are stopped: the primary must not accept a write (synchronous and strict;
#     a commit is not on one machine only). When they come back the writes go on.
set -euo pipefail
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1

work=$(mktemp -d)
hostpath() { (cd "$1" && pwd -W 2>/dev/null) || (cd "$1" && pwd); }
export ABAROW_HA_SECRETS
ABAROW_HA_SECRETS=$(hostpath "$work")
compose() { docker compose -f deploy/compose/ha-db.yaml "$@"; }
cleanup() {
	compose down -v >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT
compose down -v >/dev/null 2>&1 || true

password() { head -c 24 /dev/urandom | base64 | tr '/+' '_-'; }
password >"$work/db-password"
password >"$work/replication-password"
password >"$work/restapi-password"
db_password=$(cat "$work/db-password")

# Every machine gets a certificate of headquarters' CA, as natsauth gives it in a real install:
# the first machine makes the CA, the others get it before natsauth runs there. The
# certificate's names are the machine's containers.
for i in 1 2 3; do
	m="$ABAROW_HA_SECRETS/m$i"
	if [ "$i" -gt 1 ]; then
		mkdir -p "$work/m$i/tls"
		cp "$work/m1/tls/ca.pem" "$work/m1/tls/ca.key" "$work/m$i/tls/"
	fi
	(cd tools/natsauth && go run . -role headquarters -out "$m" -conf-dir /auth -users ingest -server-name "headquarters-$i" \
		-domain headquarters -stores "$m/stores.json" -leaf-hosts "ha-db-$i,ha-etcd-$i,ha-proxy-$i") >/dev/null
done
ssl="sslmode=verify-ca sslrootcert=/w/m1/tls/ca.pem"
network=abarow-ha_default

# Patroni's view of the cluster as "member<TAB>role<TAB>state" lines (a role can have a
# space: "Sync Standby"), asked at a running machine.
members() {
	local c
	for c in ha-db-1 ha-db-2 ha-db-3; do
		if docker exec "$c" patronictl -c /tmp/patroni.yml list -f tsv 2>/dev/null |
			awk -F'\t' 'NR == 1 { for (i = 1; i <= NF; i++) col[$i] = i; next }
				{ print $col["Member"] "\t" $col["Role"] "\t" $col["State"] }'; then
			return 0
		fi
	done
	return 1
}
leader() { members | awk -F'\t' '$2 == "Leader" && $3 == "running" { print $1 }'; }
# healthy: a leader, two streaming replicas, one of them synchronous.
healthy() {
	local m
	m=$(members) || return 1
	[ "$(printf '%s\n' "$m" | awk -F'\t' '$2 == "Leader" && $3 == "running"' | wc -l)" -eq 1 ] &&
		[ "$(printf '%s\n' "$m" | awk -F'\t' '$2 != "Leader" && $3 == "streaming"' | wc -l)" -eq 2 ] &&
		printf '%s\n' "$m" | grep -q "Sync Standby"
}
wait_healthy() {
	for _ in $(seq 180); do
		healthy && return 0
		sleep 1
	done
	members >&2 || true
	compose logs --tail 40 >&2
	echo "the cluster did not become healthy: $1" >&2
	exit 1
}
container() { echo "ha-db-${1#hq-}"; }
psql_proxy() {
	docker run --rm --network "$network" -v "$ABAROW_HA_SECRETS:/w:ro" -e PGPASSWORD="$db_password" -e PGCONNECT_TIMEOUT=5 postgres:18-alpine \
		psql "host=ha-proxy-1 user=abarow dbname=abarow $ssl" -v ON_ERROR_STOP=1 -tAc "$1"
}

echo "== building and starting three machines"
compose build -q
compose up -d
wait_healthy "start"
members

echo "== 0. TLS between the machines"
[ "$(psql_proxy "SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()")" = t ] ||
	{ echo "the service connection is not TLS" >&2; exit 1; }
[ "$(psql_proxy "SELECT count(*) FROM pg_stat_replication r JOIN pg_stat_ssl s USING (pid) WHERE s.ssl")" = 2 ] ||
	{ echo "the replication is not TLS on both replicas" >&2; exit 1; }
[ "$(psql_proxy "SHOW archive_mode")" = always ] || { echo "archive_mode is not always" >&2; exit 1; }
if docker run --rm --network "$network" -e PGPASSWORD="$db_password" -e PGCONNECT_TIMEOUT=5 postgres:18-alpine \
	psql "host=ha-proxy-1 user=abarow dbname=abarow sslmode=disable" -tAc "SELECT 1" >/dev/null 2>&1; then
	echo "a connection without TLS was accepted" >&2
	exit 1
fi
echo "service connection, replication: TLS; a connection without TLS is refused"

echo "== 0b. base backup and weekly maintenance on the machines"
primary=$(leader)
replica=$(members | awk -F'\t' -v p="$primary" '$1 != p { print $1; exit }')
mkdir -p "$work/base"
CONTAINER=docker sh deploy/quadlet/backup/basebackup.sh "$(container "$replica")" "$work/base"
CONTAINER=docker sh deploy/quadlet/backup/dbmaint.sh "$(container "$primary")" | grep "the largest tables" >/dev/null ||
	{ echo "the maintenance did not run on the primary" >&2; exit 1; }
CONTAINER=docker sh deploy/quadlet/backup/dbmaint.sh "$(container "$replica")" | grep "^== replica" >/dev/null ||
	{ echo "the maintenance on a replica did not skip to the archive check" >&2; exit 1; }
echo "base backup from the replica $replica; maintenance on the primary $primary and on the replica"

echo "== 1. the primary is killed while writing"
(cd pkg && CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOARCH)" go test -c -o "$ABAROW_HA_SECRETS/failover" ./pgdb)
docker run --rm --network "$network" -v "$(hostpath "$work"):/w" \
	-e ABAROW_TEST_HA_DSN="postgres://abarow:$db_password@ha-proxy-1:5432/abarow?sslmode=verify-ca&sslrootcert=/w/m1/tls/ca.pem" -e ABAROW_TEST_HA_SECONDS=90 \
	--entrypoint /w/failover postgres:18-alpine -test.run TestFailover -test.v -test.count=1 &
writer=$!
sleep 20
old=$(leader)
[ -n "$old" ] || { echo "no leader before the kill" >&2; exit 1; }
echo "killing the primary $old"
docker kill "$(container "$old")" >/dev/null
if ! wait "$writer"; then
	echo "the writer failed" >&2
	exit 1
fi
new=$(leader)
[ -n "$new" ] && [ "$new" != "$old" ] || { echo "no new primary (old $old, now '$new')" >&2; exit 1; }
echo "new primary $new; $old comes back"
docker start "$(container "$old")" >/dev/null
wait_healthy "$old rejoining"
members | cut -f1 | grep -qx "$old" || { echo "$old is not in the cluster" >&2; exit 1; }

echo "== 2. both replicas are stopped: no write may complete"
primary=$(leader)
replicas=$(members | awk -F'\t' -v p="$primary" '$1 != p { print $1 }')
for r in $replicas; do docker stop "$(container "$r")" >/dev/null; done
psql_proxy "INSERT INTO failover (id) VALUES (-1)" >/dev/null 2>&1 &
pending=$!
sleep 15
# Still waiting is right; having ended with an error is right too; having succeeded is not.
if ! kill -0 "$pending" 2>/dev/null && wait "$pending"; then
	echo "a write completed with both replicas gone" >&2
	exit 1
fi
echo "the write does not complete (as it should); the replicas come back"
for r in $replicas; do docker start "$(container "$r")" >/dev/null; done
wait_healthy "replicas returning"
for _ in $(seq 60); do
	if psql_proxy "INSERT INTO failover (id) VALUES (-2)" >/dev/null 2>&1; then
		echo "ha-db-test: OK"
		exit 0
	fi
	sleep 1
done
echo "writes did not resume after the replicas returned" >&2
exit 1
