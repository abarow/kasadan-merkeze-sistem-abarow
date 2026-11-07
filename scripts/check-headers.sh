#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# check-headers.sh checks that every source file in the repository carries the
# copyright header: the founder line ("Copyright <year> Mehmet ERGULCU and the
# abarow contributors") and the SPDX line must be in the first 10 lines.
# Contributors may add their own lines; the founder line cannot be deleted
# (see CONTRIBUTING.md).
set -euo pipefail
cd "$(dirname "$0")/.."

founder='Copyright [0-9]{4} Mehmet ERGULCU and the abarow contributors'
spdx='SPDX-License-Identifier: Apache-2.0'

missing=0
while IFS= read -r f; do
	top=$(head -n 10 "$f")
	if ! grep -Eq "$founder" <<<"$top" || ! grep -Fq "$spdx" <<<"$top"; then
		echo "copyright header missing: $f"
		missing=$((missing + 1))
	fi
done < <(git ls-files '*.go' '*.proto' '*.sql' '*.sh' '*.js' '*.mjs' '*.svelte' '*.css' '*.html' '*.yaml' '*.yml' '*Containerfile')

if [ "$missing" -gt 0 ]; then
	echo "$missing files are missing the copyright header; see CONTRIBUTING.md for the format."
	exit 1
fi
echo "copyright headers OK"
