#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# dt-scan.sh installs OWASP Dependency-Track in Docker, configures it and scans
# the dependency lists (SBOMs). It is only for the development machine.
#
#   bash scripts/dt-scan.sh setup    # starts DT, sets the admin password and the API key
#   bash scripts/dt-scan.sh sbom     # makes a CycloneDX SBOM for every Go module and screen app
#   bash scripts/dt-scan.sh upload   # uploads the SBOMs to DT, waits for the scan, prints a summary
#   bash scripts/dt-scan.sh image    # builds the release images, turns them into SBOMs (Syft) and uploads them
#   bash scripts/dt-scan.sh all      # setup + sbom + upload
#   bash scripts/dt-scan.sh status   # the open counts of the projects
#   bash scripts/dt-scan.sh stop     # stops it (keeps the data); "purge" deletes the data too
#
# Interface: http://localhost:8180 (user admin, password tmp/dt-admin-password).
# The password and the API key are under tmp/; they do not enter the repository.
set -euo pipefail
cd "$(dirname "$0")/.."

compose=(docker compose -f tools/dependency-track/compose.yaml)
api="http://127.0.0.1:${ABAROW_DT_API_PORT:-8181}/api/v1"
out=tmp/dt-sbom
pwfile=tmp/dt-admin-password
keyfile=tmp/dt-api-key
version=$(git rev-parse --short HEAD)

say() { printf '%s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

# json <field>: reads a field by dotted path from the JSON on standard input.
json() {
	python -c 'import json,sys
d=json.load(sys.stdin)
for k in sys.argv[1].split("."):
    d=d[int(k)] if isinstance(d,list) else d.get(k)
print("" if d is None else d)' "$1"
}

wait_for_api() {
	say "Waiting for the DT API (the first start takes a few minutes, the advisories are downloaded)..."
	for _ in $(seq 1 120); do
		curl -fsS "${api%/v1}/version" >/dev/null 2>&1 && return 0
		sleep 5
	done
	die "the DT API did not come up: ${compose[*]} logs apiserver"
}

jwt() {
	curl -fsS -X POST "$api/user/login" -d username=admin --data-urlencode "password=$(cat "$pwfile")"
}

setup() {
	mkdir -p tmp
	[ -f "$pwfile" ] || head -c 18 /dev/urandom | base64 | tr -d '/+=\n' >"$pwfile"
	"${compose[@]}" up -d
	wait_for_api
	pw=$(cat "$pwfile")
	# On the first start it comes with admin/admin and cannot be used until the password is changed.
	curl -fsS -X POST "$api/user/forceChangePassword" -d username=admin -d password=admin \
		--data-urlencode "newPassword=$pw" --data-urlencode "confirmPassword=$pw" >/dev/null 2>&1 || true
	tok=$(jwt) || die "the admin login failed ($pwfile)"
	auth=(-H "Authorization: Bearer $tok")

	# Open sources: OSV (Go and npm) and OSS Index. GitHub Advisories and NVD need
	# a key; if you have one, use the interface: Administration > Analyzers.
	for kv in 'vuln-source google.osv.enabled Go;npm' 'vuln-source google.osv.alias.sync.enabled true'; do
		read -r g n v <<<"$kv"
		curl -fsS "${auth[@]}" -H 'Content-Type: application/json' -X POST "$api/configProperty" \
			-d "{\"groupName\":\"$g\",\"propertyName\":\"$n\",\"propertyValue\":\"$v\"}" >/dev/null
	done

	# The Automation team uploads the SBOMs and reads the result; its key is written under tmp/.
	team=$(curl -fsS "${auth[@]}" "$api/team" | python -c 'import json,sys
print(next(t["uuid"] for t in json.load(sys.stdin) if t["name"]=="Automation"))')
	for perm in PROJECT_CREATION_UPLOAD VIEW_VULNERABILITY PORTFOLIO_MANAGEMENT; do
		curl -fsS "${auth[@]}" -X POST "$api/permission/$perm/team/$team" >/dev/null 2>&1 || true
	done
	if [ ! -s "$keyfile" ]; then
		curl -fsS "${auth[@]}" -X PUT "$api/team/$team/key" | json key >"$keyfile"
	fi
	say "DT is ready: interface http://localhost:${ABAROW_DT_UI_PORT:-8180}  user admin  password file $pwfile"
}

sbom() {
	mkdir -p "$out"
	rm -f "$out"/*.json
	# The module path has the form "abarow.com/<directory>".
	for mod in $(go list -m); do
		rel=${mod#abarow.com/}
		name=$(printf '%s' "$rel" | tr '/' '_')
		say "Go: $rel"
		(cd "$rel" && GOWORK=off go run github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@v1.9.0 			mod -json -licenses=false -output "$OLDPWD/$out/go_$name.json" >/dev/null)
	done
	for pkg in $(git ls-files 'ui/apps/*/package-lock.json' 'tools/*/package-lock.json'); do
		dir=$(dirname "$pkg")
		name=$(printf '%s' "$dir" | tr '/' '_')
		say "npm: $dir"
		(cd "$dir" && npx --yes @cyclonedx/cyclonedx-npm@2.1.0 --package-lock-only \
			--output-format JSON --output-file "$OLDPWD/$out/npm_$name.json" >/dev/null)
	done
	say "$(ls "$out" | wc -l) SBOMs: $out"
}

upload() {
	[ -s "$keyfile" ] || die "first: bash scripts/dt-scan.sh setup"
	key=$(cat "$keyfile")
	for f in "$out"/*.json; do
		[ -e "$f" ] || die "no SBOM: bash scripts/dt-scan.sh sbom"
		name=$(basename "$f" .json)
		curl -fsS -X POST "$api/bom" -H "X-Api-Key: $key" \
			-F autoCreate=true -F "projectName=abarow/$name" -F "projectVersion=$version" -F "bom=@$f" >/dev/null
		say "uploaded: abarow/$name"
	done
	say "Waiting for the analysis..."
	sleep 20
	status
}

# image builds every image of the release workflow (release.yml) locally, turns
# it into an SBOM with Syft and uploads it to DT as an "abarow-image/<name>"
# project. The contents of the image (the modules of the Go binary and the
# version of the Go standard library) become visible this way. Syft runs as a
# Docker image; it reads the tar file and does not touch the Docker socket.
# Third-party images (nats, postgres) are added if they exist locally.
syft=anchore/syft:v1.18.1
image() {
	[ -s "$keyfile" ] || die "first: bash scripts/dt-scan.sh setup"
	key=$(cat "$keyfile")
	dir=tmp/dt-image
	mkdir -p "$dir"
	rm -f "$dir"/*.json "$dir"/*.tar
	export MSYS_NO_PATHCONV=1
	work=$(pwd -W 2>/dev/null || pwd)
	python - >"$dir/matrix.txt" <<'PYEOF'
import re
for l in open(".github/workflows/release.yml", encoding="utf-8"):
    m = re.search(r"\{ image: ([\w-]+), module: ([\w/.-]+), pkg: ([\w/.-]+)(?:, ui: ([\w/.-]+))? \}", l)
    if m:
        print(" ".join(g or "-" for g in m.groups()))
PYEOF
	while read -r name module pkg ui; do
		file=go-service
		[ "$ui" = - ] || file=module-ui
		say "building image: $name"
		docker build -q -f "deploy/container/$file.Containerfile" --build-arg "MODULE=$module" --build-arg "PKG=$pkg" \
			--build-arg "UI=$([ "$ui" = - ] || echo "$ui")" -t "abarow-scan/$name:$version" . >/dev/null
		sbom_image "abarow-scan/$name:$version" "$name" "$version"
	done < <(tr -d '\r' <"$dir/matrix.txt")
	for ext in nats:2.12-alpine postgres:18-alpine; do
		if docker image inspect "$ext" >/dev/null 2>&1; then
			sbom_image "$ext" "$(printf '%s' "${ext%%:*}")" "${ext#*:}"
		fi
	done
	say "$(ls "$dir"/*.json | wc -l) image SBOMs: $dir"
}

# fix_purl: Syft writes versioned Go module paths as "pkg:golang/x/y@v5.1.0#v5";
# DT takes this for "x/y" (the old, unfixed path) and produces false findings.
# The correct form is "pkg:golang/x/y/v5@v5.1.0" (go-jose, pgx, nats-server).
# It also drops the CPE of Go components (see below).
fix_purl() {
	python - "$1" <<'PYEOF'
import json, re, sys
p = sys.argv[1]
d = json.load(open(p, encoding="utf-8"))
def fix(purl):
    m = re.match(r"^(pkg:golang/[^@#]+)@([^#?]+)#(v\d+)(\?.*)?$", purl or "")
    return f"{m.group(1)}/{m.group(3)}@{m.group(2)}{m.group(4) or ''}" if m else purl
for c in d.get("components", []):
    if c.get("purl"):
        c["purl"] = fix(c["purl"])
    if c.get("bom-ref"):
        c["bom-ref"] = fix(c["bom-ref"])
    # Go modules match by purl (OSV); the CPE Syft adds brings the NVD records of
    # C++/Java/Python packages with the same name as false findings (seen for protobuf).
    # The Go standard library (stdlib) keeps matching by CPE too.
    if str(c.get("purl", "")).startswith("pkg:golang/") and not str(c.get("purl")).startswith("pkg:golang/stdlib"):
        c.pop("cpe", None)
json.dump(d, open(p, "w", encoding="utf-8"))
PYEOF
}

# sbom_image <image> <name> <version>: writes the image to a tar, makes CycloneDX with Syft, uploads it to DT.
sbom_image() {
	docker save "$1" -o "$dir/$2.tar"
	docker run --rm -v "$work/$dir:/w" "$syft" scan "docker-archive:/w/$2.tar" -o "cyclonedx-json=/w/$2.json" -q >/dev/null
	rm -f "$dir/$2.tar"
	fix_purl "$dir/$2.json"
	curl -fsS -X POST "$api/bom" -H "X-Api-Key: $key" \
		-F autoCreate=true -F "projectName=abarow-image/$2" -F "projectVersion=$3" -F "bom=@$dir/$2.json" >/dev/null
	say "uploaded: abarow-image/$2"
}

# status counts the findings of the projects straight from the findings API. The
# project's "metrics" summary is updated minutes after the analysis; it is not
# trusted (on the first scan all projects wrongly showed 0).
status() {
	[ -s "$keyfile" ] || die "first: bash scripts/dt-scan.sh setup"
	DT_API="$api" DT_KEY=$(cat "$keyfile") DT_VERSION="$version" PYTHONIOENCODING=utf-8 python -c '
import collections, json, os, urllib.request
api, key, ver = os.environ["DT_API"], os.environ["DT_KEY"], os.environ["DT_VERSION"]
def get(path):
    req = urllib.request.Request(api + path, headers={"X-Api-Key": key})
    return json.load(urllib.request.urlopen(req))
uniq = collections.defaultdict(set)
print("%-34s %6s  %s" % ("project", "finds", "severity"))
for p in sorted(get("/project?pageSize=500"), key=lambda p: p["name"]):
    # Only this commit version; the old versions of earlier uploads are skipped.
    if not p["name"].startswith(("abarow/", "abarow-image/")) or p.get("version") not in (ver, "2.12-alpine", "18-alpine"):
        continue
    fs = get("/finding/project/" + p["uuid"])
    sev = collections.Counter(f["vulnerability"]["severity"] for f in fs)
    print("%-34s %6d  %s" % (p["name"], len(fs), dict(sev) or ""))
    for f in fs:
        v = f["vulnerability"]
        uniq[(f["component"]["name"], f["component"]["version"], v["vulnId"], v["severity"])].add(p["name"])
print()
print("Unique findings (component, version, id, severity: in how many projects):")
for k, ps in sorted(uniq.items()):
    print("  %s %s %s %s: %d" % (*k, len(ps)))
if not uniq:
    print("  none")
'
}

case "${1:-}" in
setup) setup ;;
sbom) sbom ;;
upload) upload ;;
image) image ;;
all) setup; sbom; upload ;;
status) status ;;
stop) "${compose[@]}" down ;;
purge) "${compose[@]}" down --volumes; rm -f "$keyfile" ;;
*) sed -n '8,19p' "$0"; exit 2 ;;
esac
