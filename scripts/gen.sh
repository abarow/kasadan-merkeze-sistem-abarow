#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# gen.sh lints the proto contracts and generates the Go code under gen/.
# The generated code is committed to the repository; these tools are not needed to build.
set -euo pipefail
cd "$(dirname "$0")/../proto"

buf="go run github.com/bufbuild/buf/cmd/buf@v1.75.0"
$buf lint
$buf generate
