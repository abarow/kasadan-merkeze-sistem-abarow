#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# hq-cluster-units-test.sh checks what headquarters-install.sh --cluster writes, without a real
# install (deploy/install/headquarters-cluster.sh; docs/decisions/HA-HEADQUARTERS.md):
#
#   - the units and drop-in files of machine 2 go through Podman's Quadlet generator;
#   - the HAProxy configuration is accepted by HAProxy (haproxy -c);
#   - cluster-export on machine 1 and --cluster-import on machine 2 give the same keys, CA and
#     passwords; a machine that already has another CA is not overwritten.
#
# Docker is needed (the Quadlet generator and HAProxy run in their images).
set -euo pipefail
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
hostpath() { (cd "$1" && pwd -W 2>/dev/null) || (cd "$1" && pwd); }

# The install script's environment, with the commands that need root or Podman replaced.
here=$(pwd)
# shellcheck source=deploy/install/common.sh
. deploy/install/common.sh
# shellcheck source=deploy/install/headquarters-cluster.sh
. deploy/install/headquarters-cluster.sh
as() { "$@"; }
install() {
	local mode='' src=() dst
	while [ $# -gt 0 ]; do
		case "$1" in
		-o | -g) shift 2 ;;
		-m) mode=$2; shift 2 ;;
		*) src+=("$1"); shift ;;
		esac
	done
	dst=${src[-1]}
	unset 'src[-1]'
	cp "${src[@]}" "$dst"
	if [ -n "$mode" ] && [ -f "$dst" ]; then chmod "$mode" "$dst"; fi
}
secret() { :; }
user=$(id -un)
channel=0.1
quadlet="$here/deploy/quadlet/headquarters"
cluster=hq1.example.com,hq2.example.com,hq3.example.com
domain='' vip='' vip_interface='' rejoin='' screen=127.0.0.1:9090

machine() {
	home="$work/m$1" node=$1
	auth="$home/abarow/headquarters/auth" config="$home/abarow/headquarters/config" headquarters="$home/abarow/headquarters"
	mkdir -p "$auth" "$config" "$home/.config/containers/systemd"
}

echo "== validation"
machine 1
hosts=hq1.example.com
cluster_validate
[ "$(cluster_hosts)" = "hq1.example.com,abarow-etcd" ] || { echo "cluster_hosts: $(cluster_hosts)" >&2; exit 1; }
# The stores connect to the --vip address with TLS: it is in the certificate.
[ "$(vip=192.168.1.50/24; cluster_hosts)" = "hq1.example.com,abarow-etcd,192.168.1.50" ] ||
	{ echo "cluster_hosts with --vip: $(vip=192.168.1.50/24; cluster_hosts)" >&2; exit 1; }
[ "$(cluster_routes)" = "nats://hq1.example.com:6222,nats://hq2.example.com:6222,nats://hq3.example.com:6222" ] ||
	{ echo "cluster_routes: $(cluster_routes)" >&2; exit 1; }
for bad in "a,b" "a,a,b" "a,b,c,d"; do
	if (cluster=$bad; cluster_validate) >/dev/null 2>&1; then
		echo "--cluster $bad was accepted" >&2
		exit 1
	fi
done
for bad in 0 4 x; do
	if (node=$bad; cluster_validate) >/dev/null 2>&1; then
		echo "--node $bad was accepted" >&2
		exit 1
	fi
done
if (vip=192.168.1.50; cluster_validate) >/dev/null 2>&1; then
	echo "--vip without a prefix was accepted" >&2
	exit 1
fi

echo "== machine 1: keys, cluster-export"
# The keys and the CA as natsauth makes them on the first machine; the passwords at random.
echo '[]' >"$config/stores.json"
(cd tools/natsauth && go run . -role headquarters -out "$(hostpath "$auth")" -conf-dir /etc/nats -users ingest,catalog,inventory \
	-server-name headquarters-1 -domain headquarters -routes "$(cluster_routes)" -stores "$(hostpath "$config")/stores.json" \
	-leaf-hosts "$(cluster_hosts)") >/dev/null
for f in db-password db-replication-password db-restapi-password; do
	head -c 24 /dev/urandom | base64 >"$auth/$f"
done
cluster_export "$work/cluster.tar.gz" >/dev/null
m1=$auth

echo "== machine 2: --cluster-import, units"
machine 2
cluster_import "$work/cluster.tar.gz" >/dev/null
for f in $cluster_files; do
	cmp -s "$m1/$f" "$auth/$f" || { echo "$f differs after the import" >&2; exit 1; }
done
cmp -s "$work/m1/abarow/headquarters/config/stores.json" "$config/stores.json" || { echo "stores.json differs" >&2; exit 1; }
# Another CA is not overwritten.
machine 3
mkdir -p "$auth/tls"
echo other >"$auth/tls/ca.pem"
if (cluster_import "$work/cluster.tar.gz") >/dev/null 2>&1; then
	echo "a machine with another CA was overwritten" >&2
	exit 1
fi

machine 2
cluster_units
units="$home/.config/containers/systemd"
# The base units the drop-in files belong to, as the install copies them.
cp deploy/quadlet/headquarters/abarow-*.container deploy/quadlet/headquarters/abarow-*.network deploy/quadlet/headquarters/backup/abarow-backup.container "$units/"
cluster_units >/dev/null
grep -q "^ContainerName=abarow-db$" "$units/abarow-db.container" || { echo "abarow-db is not HAProxy" >&2; exit 1; }
grep -q "^Environment=ABAROW_ETCD=abarow-etcd:2379,hq1.example.com:2379,hq3.example.com:2379$" "$units/abarow-patroni.container.d/node.conf" ||
	{ echo "patroni node.conf:" >&2; cat "$units/abarow-patroni.container.d/node.conf" >&2; exit 1; }
grep -q "^Environment=ETCD_INITIAL_CLUSTER=etcd-1=https://hq1.example.com:2380,etcd-2=https://hq2.example.com:2380,etcd-3=https://hq3.example.com:2380$" \
	"$units/abarow-etcd.container.d/node.conf" || { echo "etcd node.conf:" >&2; cat "$units/abarow-etcd.container.d/node.conf" >&2; exit 1; }
grep -q "^Environment=ABAROW_NATS_REPLICAS=3$" "$units/abarow-ingest.container.d/cluster.conf" || { echo "ingest has no replicas" >&2; exit 1; }
grep -q "^Environment=ABAROW_BACKUP_LOCAL=abarow-patroni$" "$units/abarow-backup.container.d/cluster.conf" || { echo "the backup does not know this machine's database" >&2; exit 1; }
# A machine that rejoins (--rejoin) starts its etcd as a member of the existing cluster.
! grep -q "ETCD_INITIAL_CLUSTER_STATE" "$units/abarow-etcd.container.d/node.conf" || { echo "the state is written without --rejoin" >&2; exit 1; }
rejoin=1 cluster_units >/dev/null
grep -q "^Environment=ETCD_INITIAL_CLUSTER_STATE=existing$" "$units/abarow-etcd.container.d/node.conf" || { echo "--rejoin: the state is not existing" >&2; exit 1; }
rejoin='' cluster_units >/dev/null
if ! grep -q "server hq-2 abarow-patroni:5432" "$headquarters/haproxy.cfg" || ! grep -q "server hq-1 hq1.example.com:5432" "$headquarters/haproxy.cfg"; then
	echo "haproxy.cfg:" >&2
	cat "$headquarters/haproxy.cfg" >&2
	exit 1
fi
case "$(cluster_db_url secret)" in
*"@abarow-db:5432/abarow?sslmode=verify-ca&sslrootcert=/etc/abarow/tls/ca.pem") ;;
*) echo "database address: $(cluster_db_url secret)" >&2; exit 1 ;;
esac

# The store lists compare by the ids and keys, not by the order or the names (cluster_stores_check).
one=$(cluster_stores_digest)
printf '[\n  {\n    "id": "store-2",\n    "nkey": "UB"\n  },\n  {\n    "id": "store-1",\n    "name": "Kadıköy",\n    "nkey": "UA"\n  }\n]\n' >"$config/stores.json"
a=$(cluster_stores_digest)
printf '[\n  {\n    "id": "store-1",\n    "nkey": "UA"\n  },\n  {\n    "id": "store-2",\n    "nkey": "UB"\n  }\n]\n' >"$config/stores.json"
b=$(cluster_stores_digest)
printf '[\n  {\n    "id": "store-1",\n    "nkey": "UA"\n  },\n  {\n    "id": "store-2",\n    "nkey": "UC"\n  }\n]\n' >"$config/stores.json"
c=$(cluster_stores_digest)
[ "$a" = "$b" ] && [ "$b" != "$c" ] && [ "$one" != "$a" ] || { echo "store list digests: $one $a $b $c" >&2; exit 1; }

echo "== Quadlet generator (units and drop-in files of machine 2)"
docker run --rm -v "$(hostpath "$units"):/q:ro" -e QUADLET_UNIT_DIRS=/q quay.io/podman/stable:v5.4 \
	/usr/libexec/podman/quadlet -dryrun -user >"$work/quadlet.out"
for u in abarow-etcd abarow-patroni abarow-db abarow-headquarters abarow-ingest; do
	grep -q "^---$u.service---" "$work/quadlet.out" || { echo "no unit $u" >&2; exit 1; }
done
# The drop-in files reach the units.
grep -A200 "^---abarow-ingest.service---" "$work/quadlet.out" | grep -q "ABAROW_NATS_REPLICAS=3" ||
	{ echo "the ingest drop-in did not reach the unit" >&2; exit 1; }
grep -A200 "^---abarow-headquarters.service---" "$work/quadlet.out" | grep -q -- "6222:6222" ||
	{ echo "the NATS cluster port did not reach the unit" >&2; exit 1; }

echo "== HAProxy configuration"
# The names of the other machines do not resolve here; init-addr none lets HAProxy start anyway.
docker run --rm -v "$(hostpath "$headquarters"):/c:ro" -v "$(hostpath "$m1")/tls/ca.pem:/etc/abarow/tls/ca.pem:ro" \
	haproxy:3.2-alpine haproxy -c -f /c/haproxy.cfg

echo "hq-cluster-units-test: OK"
