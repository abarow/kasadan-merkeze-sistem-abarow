#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# channel-test.sh tests the tools of the promotion pipeline (docs/UPDATE.md
# "Promotion") end to end: it makes a notice from fake image digests
# (channel-notice.py), signs it with the pilot and prod keys, verifies it from
# the point of view of each level and sees that a wrong key is rejected. These
# are the same commands promotion.yml uses; it does not need GitHub.
set -euo pipefail
cd "$(dirname "$0")/.."

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
update_server() { (cd services/update && GOWORK=off go run ./cmd/server "$@"); }
pub() { sed -n 's/^.*: //p' | tr -d '\r'; }

mkdir -p "$work/digests"
printf '{"unit":"abarow-sales","repo":"ghcr.io/abarow/abarow-sales","digest":"sha256:%064d"}\n' 1 >"$work/digests/sales.json"
printf '{"unit":"abarow-update","repo":"ghcr.io/abarow/abarow-update","digest":"sha256:%064d"}\n' 2 >"$work/digests/update.json"

build_pub=$(update_server generate-key "$work/build.key" | pub)
pilot_pub=$(update_server generate-key "$work/pilot.key" | pub)
prod_pub=$(update_server generate-key "$work/prod.key" | pub)
printf '{"build":["%s"],"pilot":["%s"],"prod":["%s"]}\n' "$build_pub" "$pilot_pub" "$prod_pub" >"$work/trust.json"

manifest() { python3 scripts/channel-notice.py --digests "$work/digests" --channel "$1" --version 0.4.0 --seq "$2" --urgency security --notes "test" >"$work/$1.manifest.json"; }
manifest pilot 7
manifest prod 7

# Promotion to pilot: the pilot key. The pilot level accepts it; the prod level does not.
update_server sign-channel pilot "$work/pilot.key" "$work/pilot.manifest.json" >"$work/pilot.json"
update_server verify-channel "$work/trust.json" pilot "$work/pilot.json"
if update_server verify-channel "$work/trust.json" prod "$work/pilot.json" 2>/dev/null; then
	echo "ERROR: the prod level accepted a notice signed by pilot" >&2
	exit 1
fi

# Promotion to prod: the prod key; the prod notice also feeds the pilot machines (the prod signature on the pilot channel).
update_server sign-channel prod "$work/prod.key" "$work/prod.manifest.json" >"$work/prod.json"
update_server verify-channel "$work/trust.json" prod "$work/prod.json"
update_server sign-channel prod "$work/prod.key" "$work/pilot.manifest.json" >"$work/pilot-prod-signed.json"
update_server verify-channel "$work/trust.json" pilot "$work/pilot-prod-signed.json"

# Second signature: the prod signature is added to the pilot notice; both are accepted.
update_server countersign-channel prod "$work/prod.key" "$work/pilot.json" >"$work/pilot-2sigs.json"
update_server verify-channel "$work/trust.json" pilot "$work/pilot-2sigs.json"

# The build key cannot sign prod.
update_server sign-channel build "$work/build.key" "$work/prod.manifest.json" >"$work/prod-build.json"
if update_server verify-channel "$work/trust.json" prod "$work/prod-build.json" 2>/dev/null; then
	echo "ERROR: the prod level accepted a notice signed by the build key" >&2
	exit 1
fi
echo "promotion tools OK"
