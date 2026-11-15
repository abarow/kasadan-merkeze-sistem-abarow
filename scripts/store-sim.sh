#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# store-sim.sh runs the three tiers together (Docker Compose): the back
# office (headquarters), a store server connected to it and two registers
# connected to the store server.
# From the repository root:
#
#   bash scripts/store-sim.sh          # build, start, run register-sim on both registers
#   bash scripts/store-sim.sh logs     # logs
#   bash scripts/store-sim.sh down     # shut down, delete data
#
# The registers are separate Compose projects (abarow-register-1, abarow-register-2),
# the store server is abarow-store, headquarters is abarow-headquarters. The register
# screens are http://localhost:8091 and :8092; the store server screen (store
# reports) is http://localhost:8090; the headquarters screen (headquarters
# reports) is http://localhost:9090.
#
# The steps of a real install are taken here in the same order: every register
# generates its own leaf key and its public part is added to the store server's
# register list; the store server also generates its own key and its public part
# is added to headquarters' store list.
#
# The store server screen password and the test manager, the headquarters screen
# account and the headquarters database password are test values (like the test
# PINs); they are not used in a real install. The screen test adds the test
# manager to the store server; while a manager exists the store password is off,
# so when the simulation is run again in the same environment register-sim and the screen test sign in with this manager.
set -euo pipefail
cd "$(dirname "$0")/.."
# On Windows, keep Git Bash from converting paths such as /config that go to the container
# into Windows paths.
export MSYS_NO_PATHCONV=1

registers=(register-1 register-2)
export ABAROW_REPORT_PASSWORD=store-test
export ABAROW_REPORT_MANAGER=sim-manager:86420
export ABAROW_HEADQUARTERS_USER=headquarters ABAROW_HEADQUARTERS_PASSWORD=headquarters-test

store=store-1
hq_compose() { docker compose -f deploy/compose/headquarters.yaml "$@"; }
store_compose() { docker compose -f deploy/compose/store.yaml -f deploy/compose/store-headquarters.yaml "$@"; }

# register_compose <id> <compose command...>: runs in the register's Compose project.
register_compose() {
	local id=$1 n=${1#register-}
	shift
	ABAROW_REGISTER_ID=$id ABAROW_UI_PORT=$((8090 + n)) ABAROW_SALES_PORT=$((9010 + n)) ABAROW_NATS_PORT=$((4230 + n)) \
		docker compose -p "abarow-$id" -f deploy/compose/compose.yaml -f deploy/compose/register-store.yaml "$@"
}

say() { printf '==> %s\n' "$*"; }

case "${1:-}" in
down)
	for id in "${registers[@]}"; do
		register_compose "$id" down --volumes
	done
	store_compose down --volumes
	hq_compose down --volumes
	docker network rm abarow-store >/dev/null 2>&1 || true
	exit 0
	;;
logs)
	hq_compose logs --no-color
	store_compose logs --no-color
	for id in "${registers[@]}"; do
		register_compose "$id" logs --no-color nats sales
	done
	exit 0
	;;
"") ;;
*)
	echo "usage: $0 [logs|down]" >&2
	exit 2
	;;
esac

docker network inspect abarow-store >/dev/null 2>&1 || docker network create abarow-store >/dev/null
say "Building images"
hq_compose build
store_compose build
register_compose "${registers[0]}" build
register_compose "${registers[0]}" --profile sim build register-sim

# Identities are generated top to bottom: the store server takes the headquarters'
# CA certificate, the registers take the back house's; register-sim takes the
# store server key from its volume.
hq_compose run --rm -T nats-auth
store_compose run --rm -T nats-auth
# The store server's leaf key is added to headquarters' store list.
key=$(store_compose run --rm -T --no-deps nats-auth -out /auth -show-leaf-key | tr -d '\r' | tail -n 1)
hq_compose run --rm -T --no-deps nats-auth -stores /config/stores.json \
	-add-store "$store" -store-key "$key" -store-name "Mağaza 1"
# The digest of the screen password (report only sees the digest).
ABAROW_REPORT_PASSWORD_HASH=$(printf '%s\n' "$ABAROW_REPORT_PASSWORD" | store_compose run --rm -T --no-deps report hash-password | tr -d '\r' | tail -n 1)
export ABAROW_REPORT_PASSWORD_HASH

for id in "${registers[@]}"; do
	# The register generates its leaf key (the same one if it exists) and prints its public part.
	key=$(register_compose "$id" run --rm -T --no-deps nats-auth -out /auth -show-leaf-key | tr -d '\r' | tail -n 1)
	store_compose run --rm -T --no-deps nats-auth -registers /config/registers.json \
		-add-register "$id" -register-key "$key" -register-name "Kasa ${id#register-}"
done

# Started bottom to top: once the upper tier is up, the leaf connections are made
# immediately and the stream sources are connected without waiting. While the
# upper tier is down the lower ones keep working. When run again, nats-auth
# regenerates the configuration (e.g. the permissions or ports of a new release),
# but compose does not recreate the NATS container left from the previous run.
# That is why NATS is restarted: SIGHUP does not refresh the leaf port, the leaf
# address or the permissions of connected leaf connections (in a real install the
# install scripts restart it too).
for id in "${registers[@]}"; do
	say "Starting $id"
	register_compose "$id" up --detach --wait
	register_compose "$id" restart nats >/dev/null
	register_compose "$id" up --detach --wait
done
say "Starting the store server"
store_compose up --detach --wait
store_compose restart store >/dev/null
store_compose up --detach --wait
say "Starting headquarters"
hq_compose up --detach --wait
hq_compose restart headquarters >/dev/null
hq_compose up --detach --wait
export ABAROW_HEADQUARTERS_INGEST_URL=http://ingest:8080
# register-sim compares the store server's daily report with headquarters'
# summary (the sales ledger must give the same numbers in both tiers).
export ABAROW_HEADQUARTERS_REPORTING_URL=http://reporting:8080
# Headquarters' catalog: the registers' test catalog is loaded once; a product
# that exists only at headquarters is added as well: register-sim checks that it
# goes down to the register.
hq_compose run --rm -T --no-deps catalog-admin import <deploy/compose/seed/catalog.json
printf '%s' '[{"id": "headquarters-test", "name": "Merkez Deneme Ürünü", "unit": "piece", "unit_price_kurus": 999, "tax_rate_bp": 2000, "barcodes": ["2990000000994"]},
	{"id": "headquarters-test-2", "name": "Mağaza Fiyatlı Deneme Ürünü", "unit": "piece", "unit_price_kurus": 500, "tax_rate_bp": 2000, "barcodes": ["2990000000995"]}]' |
	hq_compose run --rm -T --no-deps catalog-admin import
export ABAROW_HEADQUARTERS_PRODUCT=2990000000994:999
# register-sim gives the second product a store price at the store server; it must be valid at the register.
export ABAROW_STORE_PRICE=headquarters-test-2:2990000000995:450
# register-sim adds a cashier at the store server; the cashier must be able to
# sign in at the register, and not once the account is closed.
export ABAROW_STORE_CASHIERS=1
# register-sim checks that the sale is deducted from the store stock, and tries goods receipt and counting.
export ABAROW_STORE_STOCK=1
# register-sim tries product management on the store server screen: read-only
# while connected, a product added after the connection is closed goes down to the register.
export ABAROW_STORE_CATALOG=1
# The test account of the headquarters screen (its password is refreshed if it exists).
printf '%s\n' "$ABAROW_HEADQUARTERS_PASSWORD" | hq_compose run --rm -T --no-deps reporting user add "$ABAROW_HEADQUARTERS_USER" "Merkez Deneme" >/dev/null 2>&1 ||
	printf '%s\n' "$ABAROW_HEADQUARTERS_PASSWORD" | hq_compose run --rm -T --no-deps reporting user passwd "$ABAROW_HEADQUARTERS_USER"

failed=0
for id in "${registers[@]}"; do
	say "$id: register-sim"
	register_compose "$id" run --rm register-sim || failed=1
done
say "Store server screen (browser test)"
store_compose --profile ui-test run --rm ui-smoke || failed=1
say "Headquarters screen (browser test)"
hq_compose --profile ui-test run --rm ui-smoke || failed=1
# The headquarters test sent a transfer from the headquarters warehouse to the
# store; the store server receives it.
say "Store server: the transfer from headquarters (browser test)"
store_compose --profile ui-test run --rm -e ABAROW_SMOKE_STEP=transfer ui-smoke || failed=1

say "Store server screen: http://localhost:${ABAROW_REPORT_PORT:-8090} (test manager: ${ABAROW_REPORT_MANAGER%%:*} / PIN ${ABAROW_REPORT_MANAGER#*:})"
say "Headquarters screen: http://localhost:${ABAROW_REPORTING_PORT:-9090} (test account: $ABAROW_HEADQUARTERS_USER / $ABAROW_HEADQUARTERS_PASSWORD)"
exit $failed
