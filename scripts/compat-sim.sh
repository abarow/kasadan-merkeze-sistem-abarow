#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# compat-sim.sh runs the register with services of two releases side by side (docs/REVIEW.md
# M2). The update center updates, and rolls back, one service at a time, so a register runs
# a mix for a while: an older service on a database a newer one has migrated, a newer
# gateway in front of an older service. The given services are built from the base commit
# and replace the current ones in the running register (deploy/compose/compose.yaml, which
# must be up); the register-sim scenarios then run against the mix.
#
#   bash scripts/compat-sim.sh <base-commit> <service>...
set -euo pipefail
cd "$(dirname "$0")/.."

base=${1:-}
shift || true
[ $# -gt 0 ] || { echo "usage: compat-sim.sh <base-commit> <service>..." >&2; exit 2; }
if [ -z "$base" ] || ! git cat-file -e "$base^{commit}" 2>/dev/null; then
	echo "There is no base commit to compare with; skipped."
	exit 0
fi
if [ "$(git rev-parse "$base")" = "$(git rev-parse HEAD)" ]; then
	echo "The base is the current commit; skipped."
	exit 0
fi
compose="docker compose -f deploy/compose/compose.yaml"
work=$(mktemp -d)
git worktree add --detach "$work" "$base" >/dev/null
trap 'git worktree remove --force "$work"' EXIT

# The base's own Compose file builds the services as that release did; the images get the
# same names, so the running register takes them.
docker compose -f "$work/deploy/compose/compose.yaml" build "$@"
$compose up --detach --no-build --wait "$@"
echo "== register-sim with $* from $(git rev-parse --short "$base"), the rest from $(git rev-parse --short HEAD)"
$compose run --rm register-sim
