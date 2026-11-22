#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# deploy-site.sh publishes the project page (docs/site/index.html) to
# Cloudflare Pages (abarow.com). Only index.html is uploaded; the other files in
# docs/site (PLAN.md) stay private. From the repository root:
#
#   bash scripts/deploy-site.sh
#
# The Cloudflare settings come from an env file that is NOT in the repository
# (the token is a secret). Environment variables:
#   ENV_FILE                 env file (default: ../.env, next to the repository folder)
#   CLOUDFLARE_API_TOKEN     API token (Account: Cloudflare Pages: Edit)
#   CLOUDFLARE_ACCOUNT_ID    Cloudflare account id
#   CLOUDFLARE_PAGES_PROJECT Pages project name (default: abarow)
#   CLOUDFLARE_PAGES_BRANCH  production branch (default: main)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT/../.env}"

if [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
fi

: "${CLOUDFLARE_API_TOKEN:?CLOUDFLARE_API_TOKEN is not set (see ENV_FILE: $ENV_FILE)}"
: "${CLOUDFLARE_ACCOUNT_ID:?CLOUDFLARE_ACCOUNT_ID is not set (see ENV_FILE: $ENV_FILE)}"
PROJECT="${CLOUDFLARE_PAGES_PROJECT:-abarow}"
BRANCH="${CLOUDFLARE_PAGES_BRANCH:-main}"
export WRANGLER_SEND_METRICS="${WRANGLER_SEND_METRICS:-false}"

SRC="$ROOT/docs/site/index.html"
[ -f "$SRC" ] || { echo "not found: $SRC" >&2; exit 1; }

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
cp "$SRC" "$STAGE/index.html"

npx --yes wrangler@latest pages deploy "$STAGE" \
  --project-name "$PROJECT" --branch "$BRANCH" --commit-dirty=true
