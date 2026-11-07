#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# image-sign.sh signs the images in the publisher's registry with the channel's
# cosign key (docs/UPDATE.md "Promotion"). Every unit verifies this signature
# when it pulls an image, with the key its level accepts (Podman policy.json):
#   test: the build key, pilot: the pilot or prod key, prod: the prod key.
#
# The signature is bound not to the registry's address but to a fixed identity
# ("abarow.invalid/<image>@<digest>"): the same signature is valid on the mirror of every tier.
#
# Usage:
#   image-sign.sh <registry-prefix> <digest-directory>
# Environment:
#   COSIGN_KEY        the cosign private key file (required)
#   COSIGN_PASSWORD   the key's password
#   DST_CREDS         user:password for the registry
#   COSIGN            the cosign command (default cosign)
#   COSIGN_ALLOW_HTTP=1  testing only: a plain HTTP registry
set -euo pipefail

if [ $# -ne 2 ]; then
	echo "usage: $0 <registry-prefix> <digest-directory>" >&2
	exit 2
fi
prefix=$1 dir=$2
: "${COSIGN_KEY:?COSIGN_KEY (the cosign private key file) is required}"
cosign=${COSIGN:-cosign}
identity_prefix="abarow.invalid"

safe() { printf '%s' "$1" | grep -Eq '^[A-Za-z0-9._/:-]+$' || { echo "unsafe value: $1" >&2; exit 1; }; }
safe "$prefix"

flags=(--key "$COSIGN_KEY" --yes --tlog-upload=false)
[ "${COSIGN_ALLOW_HTTP:-}" != 1 ] || flags+=(--allow-http-registry)
if [ -n "${DST_CREDS:-}" ]; then
	flags+=(--registry-username "${DST_CREDS%%:*}" --registry-password "${DST_CREDS#*:}")
fi

shopt -s nullglob
files=("$dir"/*.json)
[ ${#files[@]} -gt 0 ] || { echo "no digest record in $dir" >&2; exit 1; }

for f in "${files[@]}"; do
	repo=$(sed -n 's/.*"repo"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$f" | head -n 1)
	digest=$(sed -n 's/.*"digest"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$f" | head -n 1)
	name=${repo##*/}
	safe "$name"
	printf '%s' "$digest" | grep -Eq '^sha256:[0-9a-f]{64}$' || { echo "$f: invalid digest" >&2; exit 1; }
	echo "==> signing: $prefix/$name@${digest:0:19}…"
	# shellcheck disable=SC2086 # COSIGN can be several words (docker run ... cosign)
	$cosign sign "${flags[@]}" --sign-container-identity "$identity_prefix/$name@$digest" "$prefix/$name@$digest"
done
echo "done: ${#files[@]} images signed"
