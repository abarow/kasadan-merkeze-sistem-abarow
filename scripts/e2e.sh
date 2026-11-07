#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# e2e.sh builds and tears down the Docker stack for the end-to-end tests
# (tools/e2e, Playwright). The stack is built with the same steps as
# scripts/store-sim.sh (identities top to bottom, keys from register to store,
# from store to headquarters); the difference is that scenarios can choose the
# stack and that this script runs neither register-sim nor the screen test: the
# tests run on top of the stack.
# From the repository root:
#
#   bash scripts/e2e.sh up standalone              # register + store (no headquarters)
#   bash scripts/e2e.sh up headquarters            # register + store + headquarters
#   bash scripts/e2e.sh up restaurant-standalone   # restaurant register, no headquarters
#   bash scripts/e2e.sh up restaurant-headquarters # restaurant register, with headquarters
#   bash scripts/e2e.sh up update                  # headquarters + update center (publisher, headquarters, store, register agents)
#   bash scripts/e2e.sh down <name>                # shut down, delete data
#
# The tests (cd tools/e2e && npm test) build and delete the stack themselves.
# Cleanup and receipt periods are shortened for the tests (ABAROW_CLEAN_MAX_AGE,
# ABAROW_CLEAN_INTERVAL); a real installation uses 30/90 days and 1 minute.
# Screen passwords and accounts are test values (like the test PINs) and are
# not used in a real installation.
set -euo pipefail
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1

cmd=${1:-}
name=${2:-}
case "$cmd" in
up | down) ;;
*)
	echo "usage: $0 up|down standalone|headquarters|restaurant-standalone|restaurant-headquarters|update" >&2
	exit 2
	;;
esac
case "$name" in
standalone | headquarters | restaurant-standalone | restaurant-headquarters | update) ;;
*)
	echo "unknown stack: $name" >&2
	exit 2
	;;
esac
headquarters=0 restaurant=0 update=0
[[ $name == *headquarters ]] && headquarters=1
[[ $name == update ]] && headquarters=1 update=1
[[ $name == restaurant-* ]] && restaurant=1

registers=(register-1)
export ABAROW_REPORT_PASSWORD=store-test
export ABAROW_HEADQUARTERS_USER=headquarters ABAROW_HEADQUARTERS_PASSWORD=headquarters-test
# Cleanup: events are kept for at least 6 s, one round every 2 s.
export ABAROW_CLEAN_MAX_AGE=${ABAROW_CLEAN_MAX_AGE:-6s} ABAROW_CLEAN_INTERVAL=${ABAROW_CLEAN_INTERVAL:-2s}

store=store-1
# In the update stack, the update services are added to every tier.
hq_compose() {
	local files=(-f deploy/compose/headquarters.yaml)
	[ "$update" = 1 ] && files+=(-f deploy/compose/update-headquarters.yaml)
	docker compose "${files[@]}" "$@"
}
store_compose() {
	local files=(-f deploy/compose/store.yaml)
	[ "$headquarters" = 1 ] && files+=(-f deploy/compose/store-headquarters.yaml)
	[ "$update" = 1 ] && files+=(-f deploy/compose/update-store.yaml)
	docker compose "${files[@]}" "$@"
}
register_compose() {
	local id=$1 n=${1#register-}
	shift
	local files=(-f deploy/compose/compose.yaml -f deploy/compose/register-store.yaml)
	[ "$restaurant" = 1 ] && files+=(-f deploy/compose/restaurant.yaml)
	[ "$update" = 1 ] && files+=(-f deploy/compose/update-register.yaml)
	ABAROW_REGISTER_ID=$id ABAROW_UI_PORT=$((8090 + n)) ABAROW_SALES_PORT=$((9010 + n)) ABAROW_NATS_PORT=$((4230 + n)) \
		docker compose -p "abarow-$id" "${files[@]}" "$@"
}
say() { printf '==> %s\n' "$*"; }

if [ "$cmd" = down ]; then
	for id in "${registers[@]}"; do
		register_compose "$id" down --volumes
	done
	store_compose down --volumes
	[ "$headquarters" = 1 ] && COMPOSE_PROFILES=update-server hq_compose down --volumes --remove-orphans
	docker network rm abarow-store >/dev/null 2>&1 || true
	exit 0
fi

docker network inspect abarow-store >/dev/null 2>&1 || docker network create abarow-store >/dev/null
say "Building images ($name)"
[ "$headquarters" = 1 ] && hq_compose build
store_compose build
register_compose "${registers[0]}" build

# Identities are generated top to bottom: the store takes the headquarters CA
# certificate, the registers take the store's.
[ "$headquarters" = 1 ] && hq_compose run --rm -T nats-auth
if [ "$update" = 1 ]; then
	# The publisher root key, the channel keys (build, pilot, prod) and the trust
	# file the agents rely on: tools/e2e/out/trust. The tests sign the notice with
	# these keys and write it to the channel directory.
	trust="${ABAROW_E2E_TRUST:-tools/e2e/out/trust}"
	rm -rf "$trust"
	mkdir -p "$trust/keys" "$trust/channel"
	trust_abs="$(cd "$trust" && { pwd -W 2>/dev/null || pwd; })"
	server_image=ghcr.io/abarow/abarow-update-server:dev
	gen() { docker run --rm --user 0:0 -v "$trust_abs/keys:/keys" "$server_image" generate-key "/keys/$1.key" | tr -d '\r' | sed 's/^.*: //'; }
	root_pub=$(gen root) build_pub=$(gen build) pilot_pub=$(gen pilot) prod_pub=$(gen prod)
	# cosign key pairs for image signatures (build, pilot, prod); the public keys are in the trust file.
	mkdir -p "$trust/cosign"
	for r in build pilot prod; do
		docker run --rm --user 0:0 -v "$trust_abs/cosign:/k" -w /k -e COSIGN_PASSWORD= ghcr.io/sigstore/cosign/cosign:v2.4.1 generate-key-pair --output-key-prefix "$r" >/dev/null
	done
	pem() { awk 'BEGIN{ORS="\\n"}1' "$trust/cosign/$1.pub"; }
	printf '{"build":["%s"],"pilot":["%s"],"prod":["%s"],"cosign":{"build":"%s","pilot":"%s","prod":"%s"}}
' 		"$build_pub" "$pilot_pub" "$prod_pub" "$(pem build)" "$(pem pilot)" "$(pem prod)" >"$trust/trust.json"
	# The publisher's registry: a single authentication (publisher-auth token); CI writes with its write token, the units with the abarow token.
	mkdir -p "$trust/registry"
	# CI's write token: the server only keeps its SHA-256 digest (publisher-auth); the token is fixed in the test.
	ABAROW_PUBLISHER_CI_SHA256=$(printf '%s' 'ci-test-token-0123456789abcdef0123456789abcdef' | sha256sum | cut -d' ' -f1)
	export ABAROW_PUBLISHER_CI_SHA256
	cat >"$trust/registry/zot.json" <<'ZOT'
{"distSpecVersion":"1.1.1","storage":{"rootDirectory":"/var/lib/registry"},
 "http":{"address":"0.0.0.0","port":"5000",
  "auth":{"bearer":{"realm":"http://publisher-auth:8083/token","service":"abarow-zot","cert":"/etc/mirror/mirror.crt"}}},
 "log":{"level":"info"}}
ZOT
	export ABAROW_PUBLISHER_ROOT_PUB="$root_pub"
	say "Update keys generated ($trust)"
fi
store_compose run --rm -T nats-auth
if [ "$headquarters" = 1 ]; then
	key=$(store_compose run --rm -T --no-deps nats-auth -out /auth -show-leaf-key | tr -d '\r' | tail -n 1)
	hq_compose run --rm -T --no-deps nats-auth -stores /config/stores.json \
		-add-store "$store" -store-key "$key" -store-name "Mağaza 1"
fi
ABAROW_REPORT_PASSWORD_HASH=$(printf '%s\n' "$ABAROW_REPORT_PASSWORD" | store_compose run --rm -T --no-deps report hash-password | tr -d '\r' | tail -n 1)
export ABAROW_REPORT_PASSWORD_HASH
for id in "${registers[@]}"; do
	key=$(register_compose "$id" run --rm -T --no-deps nats-auth -out /auth -show-leaf-key | tr -d '\r' | tail -n 1)
	store_compose run --rm -T --no-deps nats-auth -registers /config/registers.json \
		-add-register "$id" -register-key "$key" -register-name "Kasa ${id#register-}"
done

# Started bottom to top; once the upper tier is up, the leaf connections are
# made immediately (see scripts/store-sim.sh for details).
for id in "${registers[@]}"; do
	say "Starting $id"
	register_compose "$id" up --detach --wait
	register_compose "$id" restart nats >/dev/null
	register_compose "$id" up --detach --wait
done
say "Starting the store"
store_compose up --detach --wait
store_compose restart store >/dev/null
store_compose up --detach --wait
if [ "$headquarters" = 1 ]; then
	say "Starting headquarters"
	hq_compose up --detach --wait
	hq_compose restart headquarters >/dev/null
	hq_compose up --detach --wait
	# The headquarters catalog: the register test catalog is loaded once.
	if [ "$restaurant" = 0 ]; then
		hq_compose run --rm -T --no-deps catalog-admin import <deploy/compose/seed/catalog.json
	fi
	# The test account of the headquarters screen.
	printf '%s\n' "$ABAROW_HEADQUARTERS_PASSWORD" | hq_compose run --rm -T --no-deps reporting user add "$ABAROW_HEADQUARTERS_USER" "Merkez Deneme" >/dev/null 2>&1 ||
		printf '%s\n' "$ABAROW_HEADQUARTERS_PASSWORD" | hq_compose run --rm -T --no-deps reporting user passwd "$ABAROW_HEADQUARTERS_USER"
fi
# Update center: the publisher approves the headquarters registration (the
# headquarters owner reads the code from the headquarters agent's status; in a
# real installation the publisher does this from their own screen), then the
# headquarters registry starts. The tests (or a person) approve the store and
# register registrations from the screen.
if [ "$update" = 1 ]; then
	say "Update: the publisher approves the headquarters"
	admin="http://127.0.0.1:${ABAROW_PUBLISHER_ADMIN_PORT:-8182}"
	hdr=(-H 'X-Abarow-User: publisher' -H 'X-Abarow-Role: admin' -H 'X-Abarow-Stores: *' -H 'Content-Type: application/json')
	device='' code=''
	for _ in $(seq 1 60); do
		out=$(curl -fsS "http://127.0.0.1:${ABAROW_HEADQUARTERS_AGENT_PORT:-8183}/api/update/status" 2>/dev/null || true)
		device=$(printf '%s' "$out" | sed -n 's/.*"device":"\([^"]*\)".*/\1/p')
		code=$(printf '%s' "$out" | sed -n 's/.*"code":"\([^"]*\)".*/\1/p')
		[ -n "$device" ] && [ -n "$code" ] && break
		sleep 1
	done
	[ -n "$device" ] || { echo "the headquarters agent gave no status" >&2; exit 1; }
	curl -fsS "${hdr[@]}" -X POST "$admin/api/update/registrations/$device/approve" -d "{\"code\":\"$code\"}" >/dev/null
	COMPOSE_PROFILES=update-server hq_compose up --detach --wait headquarters-server
	COMPOSE_PROFILES=update-server hq_compose up --detach --wait
	# The lower agents started while the registry was down and began backing off; have them retry right away.
	store_compose restart store-agent >/dev/null
	for id in "${registers[@]}"; do
		register_compose "$id" restart update >/dev/null
	done
fi
say "Stack ready ($name): register http://localhost:8091, store http://localhost:${ABAROW_REPORT_PORT:-8090}$([ "$headquarters" = 1 ] && echo ", headquarters http://localhost:${ABAROW_REPORTING_PORT:-9090}")"
