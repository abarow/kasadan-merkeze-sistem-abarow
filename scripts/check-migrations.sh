#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# check-migrations.sh fails if a migration file that already existed at the base commit
# was changed, renamed or deleted. A database that applied the old file would never
# apply the change: every schema change goes into a new file (00002_..., 00003_...).
#
#   bash scripts/check-migrations.sh <base-commit>
set -euo pipefail
cd "$(dirname "$0")/.."

base=${1:-}
if [ -z "$base" ] || ! git cat-file -e "$base^{commit}" 2>/dev/null; then
	echo "There is no base commit to compare with; skipped."
	exit 0
fi
changed=$(git diff --name-status --diff-filter=MDR "$base" HEAD -- '*/migrations/*.sql')
if [ -n "$changed" ]; then
	echo "These migration files existed at $base and were changed; add a new file instead:"
	echo "$changed"
	exit 1
fi
echo "migrations OK (only new files since $base)"
