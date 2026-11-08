# shellcheck shell=bash
# shellcheck disable=SC2154 # the variables are set by headquarters-install.sh, which sources this file
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# Headquarters on three machines (docs/decisions/HA-HEADQUARTERS.md); headquarters-install.sh
# sources this file when --cluster is given. Every machine runs the same services; NATS is a
# cluster, PostgreSQL is run by Patroni with etcd, every machine's HAProxy (abarow-db) sends the
# services to the primary, one copy of ingest, catalog-admin and inventory works (pkg/leader).
#
# The variables come from headquarters-install.sh: cluster (the three machines' names,
# comma-separated), node (1, 2 or 3), vip and vip_interface (keepalived, optional), auth,
# config, headquarters, home, user, here, screen.

# cluster_names prints the machines' names, one per line.
cluster_names() { printf '%s\n' "$cluster" | tr ',' '\n'; }
# cluster_self is this machine's name in the cluster.
cluster_self() { cluster_names | sed -n "${node}p"; }

cluster_validate() {
	local n h
	n=$(cluster_names | grep -c .)
	[ "$n" -eq 3 ] || die "--cluster needs the names of three machines (given $n): with two, the majority cannot decide when one is gone."
	for h in $(cluster_names); do
		printf '%s' "$h" | grep -Eq '^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$' || die "--cluster: \"$h\" is not a machine name or IP."
	done
	[ "$(cluster_names | sort -u | grep -c .)" -eq 3 ] || die "--cluster: the three names must be different."
	case "$node" in 1 | 2 | 3) ;; *) die "--node must be this machine's place in --cluster: 1, 2 or 3." ;; esac
	if [ -n "$vip" ]; then
		printf '%s' "$vip" | grep -Eq '^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$' || die "--vip must be an IPv4 address with its prefix (e.g. 192.168.1.50/24)."
	fi
	[ -z "$vip_interface" ] || [ -n "$vip" ] || die "--vip-interface is only given with --vip."
	[ "$domain" = "" ] || say "Note: with --domain every machine runs Caddy; only the machine holding the screen's address can get the certificate (see HA-HEADQUARTERS.md)."
}

# cluster_hosts adds to --address the names the certificate needs: this machine's cluster name
# (the other machines connect to it with that name), the local etcd's container name and the
# --vip address (the stores connect to it with TLS).
cluster_hosts() {
	local h out=$hosts
	for h in "$(cluster_self)" abarow-etcd ${vip:+"${vip%/*}"}; do
		case ",$out," in *",$h,"*) ;; *) out="$out,$h" ;; esac
	done
	printf '%s' "$out"
}

# The files the three machines share. The first machine makes them; cluster-export packs them,
# --cluster-import unpacks them on the other two before anything else is made there.
cluster_files="db-password db-replication-password db-restapi-password tls/ca.pem tls/ca.key ingest/nats.nk catalog/nats.nk inventory/nats.nk"

cluster_export() {
	local file=$1 tmp f
	[ "$node" = 1 ] || die "cluster-export is run on the first machine (--node 1)."
	tmp=$(mktemp -d)
	mkdir -p "$tmp/auth/tls" "$tmp/auth/ingest" "$tmp/auth/catalog" "$tmp/auth/inventory" "$tmp/config"
	for f in $cluster_files; do
		as test -s "$auth/$f" || { rm -rf "$tmp"; die "$auth/$f is missing: install the first machine first."; }
		as cat "$auth/$f" >"$tmp/auth/$f"
	done
	as cat "$config/stores.json" >"$tmp/config/stores.json" 2>/dev/null || echo '[]' >"$tmp/config/stores.json"
	(umask 077 && tar -czf "$file" -C "$tmp" auth config)
	rm -rf "$tmp"
	chmod 0600 "$file"
	say "Written: $file (the keys, the CA's private key and the passwords: copy it to the other two machines over a safe channel, then delete it)"
	say "Its digest (check it on the other machines): $(sha256sum <"$file" | cut -d' ' -f1)"
}

cluster_import() {
	local file=$1 tmp f
	[ -f "$file" ] || die "$file does not exist."
	tmp=$(mktemp -d)
	tar -xzf "$file" -C "$tmp" || { rm -rf "$tmp"; die "$file could not be opened."; }
	for f in $cluster_files; do
		[ -s "$tmp/auth/$f" ] || { rm -rf "$tmp"; die "$file has no $f: it must come from cluster-export on the first machine."; }
	done
	# A machine with another CA is not overwritten: the stores trust one CA.
	if as test -s "$auth/tls/ca.pem" && ! as cmp -s "$auth/tls/ca.pem" "$tmp/auth/tls/ca.pem"; then
		rm -rf "$tmp"
		die "this machine already has another headquarters CA ($auth/tls/ca.pem); it was not overwritten."
	fi
	for f in $cluster_files; do
		as mkdir -p "$auth/$(dirname "$f")"
		as rm -f "$auth/$f"
		install -o "$user" -g "$user" -m 0400 "$tmp/auth/$f" "$auth/$f"
	done
	install -o "$user" -g "$user" -m 0644 "$tmp/config/stores.json" "$config/stores.json"
	rm -rf "$tmp"
	say "The keys, the CA and the passwords of the first machine were taken"
}

# cluster_secrets makes the passwords the first machine needs (the others import them) and the
# Podman secrets.
cluster_secrets() {
	local f
	for f in db-replication-password db-restapi-password; do
		if ! as test -s "$auth/$f"; then
			[ "$node" = 1 ] || die "$auth/$f is missing: give the file of cluster-export on the first machine with --cluster-import."
			head -c 30 /dev/urandom | base64 -w0 | tr '/+' '_-' | as tee "$auth/$f" >/dev/null
			as chmod 0600 "$auth/$f"
		fi
	done
	secret abarow-db-replication-password "$auth/db-replication-password"
	secret abarow-db-restapi-password "$auth/db-restapi-password"
	secret abarow-headquarters-tls-ca "$auth/tls/ca.pem"
}

# cluster_routes are the NATS cluster addresses of the three machines.
cluster_routes() { cluster_names | sed 's|^\(.*\)$|nats://\1:6222|' | paste -sd, -; }

# cluster_units installs the units of the three machines and the drop-in files of the services.
cluster_units() {
	local units_dir="$home/.config/containers/systemd" self etcd_hosts initial i h d
	self=$(cluster_self)
	install -o "$user" -g "$user" -m 0644 "$quadlet/cluster/abarow-etcd.container" "$quadlet/cluster/abarow-patroni.container" "$units_dir/"
	# HAProxy takes abarow-db's place: the services keep their address and their units.
	install -o "$user" -g "$user" -m 0644 "$quadlet/cluster/abarow-db-proxy.container" "$units_dir/abarow-db.container"
	# shellcheck disable=SC2154 # the calling script sets channel
	if [ "$channel" != 0.1 ]; then
		sed -i "s|^\(Image=ghcr.io/abarow/abarow-patroni\):0\.1$|\1:$channel|" "$units_dir/abarow-patroni.container"
	fi

	initial='' etcd_hosts=abarow-etcd:2379 i=0
	for h in $(cluster_names); do
		i=$((i + 1))
		initial="$initial${initial:+,}etcd-$i=https://$h:2380"
		[ "$i" = "$node" ] || etcd_hosts="$etcd_hosts,$h:2379"
	done
	as mkdir -p "$units_dir/abarow-etcd.container.d" "$units_dir/abarow-patroni.container.d"
	as tee "$units_dir/abarow-etcd.container.d/node.conf" >/dev/null <<EOF
# written by headquarters-install.sh --cluster: this machine in the etcd of the three machines.
[Container]
Environment=ETCD_NAME=etcd-$node
Environment=ETCD_ADVERTISE_CLIENT_URLS=https://$self:2379
Environment=ETCD_INITIAL_ADVERTISE_PEER_URLS=https://$self:2380
Environment=ETCD_INITIAL_CLUSTER=$initial
EOF
	as tee "$units_dir/abarow-patroni.container.d/node.conf" >/dev/null <<EOF
# written by headquarters-install.sh --cluster: this machine in the database of the three machines.
[Container]
Environment=ABAROW_NODE=hq-$node
Environment=ABAROW_NODE_ADDRESS=$self
Environment=ABAROW_ETCD=$etcd_hosts
EOF

	# HAProxy: this machine's PostgreSQL by its container name, the others by their names. A
	# running HAProxy is restarted when its configuration changes.
	local proxy_before
	proxy_before=$( (cat "$headquarters/haproxy.cfg" 2>/dev/null || true) | sha256sum)
	{
		echo "# written by headquarters-install.sh --cluster; manual changes are lost when the install is repeated."
		echo "global"
		echo "    maxconn 400"
		echo "resolvers dns"
		echo "    parse-resolv-conf"
		echo "    hold valid 5s"
		echo "defaults"
		echo "    mode tcp"
		echo "    timeout connect 4s"
		echo "    timeout client 30m"
		echo "    timeout server 30m"
		echo "    timeout check 3s"
		echo "listen primary"
		echo "    bind :5432"
		echo "    option httpchk"
		echo "    http-check send meth GET uri /primary"
		echo "    http-check expect status 200"
		echo "    default-server inter 2s fall 3 rise 2 on-marked-down shutdown-sessions resolvers dns init-addr last,libc,none check-ssl ca-file /etc/abarow/tls/ca.pem"
		i=0
		for h in $(cluster_names); do
			i=$((i + 1))
			if [ "$i" = "$node" ]; then
				echo "    server hq-$i abarow-patroni:5432 check port 8008"
			else
				echo "    server hq-$i $h:5432 check port 8008"
			fi
		done
	} | as tee "$headquarters/haproxy.cfg" >/dev/null
	if [ "$proxy_before" != "$(sha256sum <"$headquarters/haproxy.cfg")" ] && as systemctl --user is-active --quiet abarow-db.service 2>/dev/null; then
		as systemctl --user restart abarow-db.service || true
	fi

	# NATS: the cluster port and the CA the machines check each other with.
	as mkdir -p "$units_dir/abarow-headquarters.container.d"
	as tee "$units_dir/abarow-headquarters.container.d/cluster.conf" >/dev/null <<'EOF'
# written by headquarters-install.sh --cluster: the NATS cluster of the three machines.
[Container]
Secret=abarow-headquarters-tls-ca,type=mount,target=/etc/nats/tls/ca.pem
# Opened only to the other two headquarters machines (firewall).
PublishPort=6222:6222
EOF
	# The services: the database connection is TLS (the CA); the streams have three copies.
	for d in abarow-ingest abarow-catalog-admin abarow-inventory-headquarters abarow-reporting abarow-backup; do
		as mkdir -p "$units_dir/$d.container.d"
		{
			echo "# written by headquarters-install.sh --cluster: the database is reached through HAProxy with TLS."
			echo "[Container]"
			echo "Secret=abarow-headquarters-tls-ca,type=mount,target=/etc/abarow/tls/ca.pem"
			case "$d" in
			abarow-reporting | abarow-backup) ;;
			*) echo "Environment=ABAROW_NATS_REPLICAS=3" ;;
			esac
			if [ "$d" = abarow-backup ]; then
				echo "Environment=PGSSLMODE=verify-ca"
				echo "Environment=PGSSLROOTCERT=/etc/abarow/tls/ca.pem"
			fi
		} | as tee "$units_dir/$d.container.d/cluster.conf" >/dev/null
	done
}

# cluster_db_url is the services' database address: this machine's HAProxy, TLS.
cluster_db_url() {
	printf 'postgres://abarow:%s@abarow-db:5432/abarow?sslmode=verify-ca&sslrootcert=/etc/abarow/tls/ca.pem' "$1"
}

# cluster_tune_apply writes changed database settings to the cluster's shared settings and
# restarts the machines that need it (Patroni restarts the replicas first). The flags are the
# "-c name=value" list of reporting pgtune.
cluster_tune_apply() {
	local flags=$1 args=() kv
	as systemctl --user is-active --quiet abarow-patroni.service || return 0
	# shellcheck disable=SC2086 # the flags are separate words
	set -- $flags
	while [ $# -ge 2 ]; do
		kv=$2
		[ "$kv" != archive_mode=on ] || kv=archive_mode=always
		args+=(-p "$kv")
		shift 2
	done
	if as podman exec abarow-patroni patronictl -c /tmp/patroni.yml edit-config --force "${args[@]}" >/dev/null &&
		as podman exec abarow-patroni patronictl -c /tmp/patroni.yml restart abarow-headquarters --pending --force >/dev/null; then
		say "The database settings were applied to the three machines"
	else
		say "The database settings could not be applied now (the database is not up yet?); they are applied when the install is repeated"
	fi
}

# cluster_db_ready waits a little for the database: until two of the three machines are installed
# there is none, and the first backup and the account wait.
cluster_db_ready() {
	local i
	# A query through this machine's HAProxy, with TLS, as a service connects.
	for i in $(seq 1 30); do
		as podman run --rm --network abarow-headquarters --secret abarow-db-password,type=env,target=PGPASSWORD \
			--secret abarow-headquarters-tls-ca,type=mount,target=/ca.pem docker.io/library/postgres:18-alpine \
			psql "host=abarow-db user=abarow dbname=abarow sslmode=verify-ca sslrootcert=/ca.pem connect_timeout=3" -tAc 'SELECT 1' >/dev/null 2>&1 &&
			return 0
		[ "$i" = 30 ] || sleep 2
	done
	return 1
}

# keepalived_install gives the screen's and the stores' address (--vip) to one of the three
# machines that answers: the machine whose screen (reporting) and NATS port (7422) answer. Every
# machine starts as a backup and the address does not move back when a machine returns (nopreempt):
# a returning machine does not cut the connections again.
keepalived_install() {
	local conf=/etc/keepalived/keepalived.conf iface=$vip_interface peers='' h ip port before
	if ! command -v keepalived >/dev/null; then
		if command -v apt-get >/dev/null; then
			DEBIAN_FRONTEND=noninteractive apt-get install -y -q keepalived >/dev/null
		elif command -v dnf >/dev/null; then
			dnf install -y -q keepalived >/dev/null
		else
			die "keepalived could not be installed (no apt-get or dnf); install it and repeat the install."
		fi
	fi
	[ -n "$iface" ] || iface=$(ip route show default | awk '{ for (i = 1; i <= NF; i++) if ($i == "dev") { print $(i + 1); exit } }')
	[ -n "$iface" ] || die "the network interface for --vip could not be found; give --vip-interface."
	# The other machines are told directly (unicast): many networks drop VRRP multicast.
	for h in $(cluster_names | sed "${node}d"); do
		ip=$(getent ahostsv4 "$h" | awk 'NR == 1 { print $1 }')
		[ -n "$ip" ] || die "the IP of $h could not be found (for keepalived)."
		peers="$peers        $ip\n"
	done
	port=${screen##*:}
	before=$( (cat "$conf" 2>/dev/null || true) | sha256sum)
	mkdir -p /etc/keepalived
	{
		echo "# written by headquarters-install.sh --cluster --vip; manual changes are lost when the install is repeated."
		echo "global_defs {"
		echo "    script_user root"
		echo "    enable_script_security"
		echo "}"
		echo "vrrp_script abarow_answers {"
		echo "    script \"/bin/bash -c '</dev/tcp/127.0.0.1/$port && </dev/tcp/127.0.0.1/7422'\""
		echo "    interval 2"
		echo "    fall 3"
		echo "    rise 2"
		echo "}"
		echo "vrrp_instance abarow_headquarters {"
		echo "    state BACKUP"
		echo "    nopreempt"
		echo "    interface $iface"
		echo "    virtual_router_id 51"
		echo "    priority $((200 - 50 * node))"
		echo "    advert_int 1"
		echo "    unicast_peer {"
		printf '%b' "$peers"
		echo "    }"
		echo "    virtual_ipaddress {"
		echo "        $vip dev $iface"
		echo "    }"
		echo "    track_script {"
		echo "        abarow_answers"
		echo "    }"
		echo "}"
	} >"$conf"
	chmod 0644 "$conf"
	systemctl enable --now keepalived >/dev/null 2>&1 || die "keepalived could not be started (journalctl -u keepalived)."
	[ "$before" = "$(sha256sum <"$conf")" ] || systemctl restart keepalived
	say "The address ${vip%/*} is held by one of the three machines that answers (keepalived)"
}

keepalived_remove() {
	[ -f /etc/keepalived/keepalived.conf ] && grep -q 'headquarters-install.sh --cluster --vip' /etc/keepalived/keepalived.conf || return 0
	systemctl disable --now keepalived >/dev/null 2>&1 || true
	rm -f /etc/keepalived/keepalived.conf
	say "keepalived removed: --vip was not given"
}
