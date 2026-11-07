#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# check.sh runs the format, build, vet and test checks in all the modules in go.work.
# The root directory is not a module, so module paths are used instead of "go test ./...".
set -euo pipefail
cd "$(dirname "$0")/.."

patterns=$(go list -m | sed 's|$|/...|')

unformatted=$(gofmt -l $(go list -m -f '{{.Dir}}'))
if [ -n "$unformatted" ]; then
	echo "files that need gofmt:"
	echo "$unformatted"
	exit 1
fi

go build $patterns
go vet $patterns
go test $patterns

# In the container every module is built without go.work; catch go.mod/go.sum
# gaps here. vet compiles but writes no binary.
for dir in $(go list -m -f '{{.Dir}}'); do
	(cd "$dir" && GOWORK=off go vet ./...)
done
