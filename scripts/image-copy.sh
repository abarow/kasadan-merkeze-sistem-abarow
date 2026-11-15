#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# image-copy.sh copies images from one registry to another without changing the
# digest. It is the only way to write images into the publisher's registry:
#
#   release workflow (release.yml):   ghcr.io/<owner>  ->  <publisher registry>/test
#   promotion workflow (promotion.yml): <publisher>/test  ->  <publisher>/pilot  ->  <publisher>/prod
#
# Promotion copies bytes: the same digest is also put under a channel's path,
# there is no rebuild. After the copy it is verified that the digest at the
# target is the same as at the source; if it differs the script fails.
#
# Usage:
#   image-copy.sh <source-prefix> <target-prefix> <tag> <digest-directory>
# The digest directory holds the {unit, repo, digest} records release.yml uploads
# (<name>.json); the name of an image is the last part of the repository path (abarow-sales).
#
# Environment:
#   SRC_CREDS, DST_CREDS     user:password (anonymous if missing)
#   SKOPEO_TLS_VERIFY=false  testing only: a plain HTTP or self-signed registry
#   SKOPEO                   the skopeo command (default skopeo)
set -euo pipefail

if [ $# -ne 4 ]; then
	echo "usage: $0 <source-prefix> <target-prefix> <tag> <digest-directory>" >&2
	exit 2
fi
src=$1 dst=$2 tag=$3 dir=$4
skopeo=${SKOPEO:-skopeo}
tls=(--src-tls-verify="${SKOPEO_TLS_VERIFY:-true}" --dest-tls-verify="${SKOPEO_TLS_VERIFY:-true}")
inspect_tls=(--tls-verify="${SKOPEO_TLS_VERIFY:-true}")
src_creds=() dst_creds=() src_inspect=() dst_inspect=()
[ -z "${SRC_CREDS:-}" ] || { src_creds=(--src-creds "$SRC_CREDS"); src_inspect=(--creds "$SRC_CREDS"); }
[ -z "${DST_CREDS:-}" ] || { dst_creds=(--dest-creds "$DST_CREDS"); dst_inspect=(--creds "$DST_CREDS"); }

# Path parts carry only safe characters (the record file counts as untrusted data).
safe() { printf '%s' "$1" | grep -Eq '^[A-Za-z0-9._/:-]+$' || { echo "unsafe value: $1" >&2; exit 1; }; }
safe "$src"
safe "$dst"
safe "$tag"

shopt -s nullglob
files=("$dir"/*.json)
[ ${#files[@]} -gt 0 ] || { echo "no digest record in $dir" >&2; exit 1; }

for f in "${files[@]}"; do
	repo=$(sed -n 's/.*"repo"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$f" | head -n 1)
	digest=$(sed -n 's/.*"digest"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$f" | head -n 1)
	[ -n "$repo" ] && [ -n "$digest" ] || { echo "$f: no repo or digest" >&2; exit 1; }
	name=${repo##*/}
	safe "$name"
	printf '%s' "$digest" | grep -Eq '^sha256:[0-9a-f]{64}$' || { echo "$f: invalid digest" >&2; exit 1; }
	echo "==> $name@${digest:0:19}… $src -> $dst:$tag"
	"$skopeo" copy --all --preserve-digests "${tls[@]}" "${src_creds[@]}" "${dst_creds[@]}" \
		"docker://$src/$name@$digest" "docker://$dst/$name:$tag"
	got=$("$skopeo" inspect --raw "${inspect_tls[@]}" "${dst_inspect[@]}" "docker://$dst/$name:$tag" | sha256sum | cut -d' ' -f1)
	[ "sha256:$got" = "$digest" ] || { echo "ERROR: $name has a different digest at the target (sha256:$got)" >&2; exit 1; }
done
echo "done: ${#files[@]} images"
