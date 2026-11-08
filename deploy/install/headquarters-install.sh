#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# headquarters-install.sh prepares a Linux machine (the business's own server or
# a VPS) as headquarters and connects the stores to it (as root, from the
# repository root):
#
#   sudo deploy/install/headquarters-install.sh --address headquarters.example.com[,203.0.113.10] [--screen 127.0.0.1:9090] [--domain reports.example.com [--email it@example.com]] [--backup-target /mnt/backup/abarow|user@server[:/path]] [--account ayse --password-file file] [--login local,oidc --oidc-issuer https://idp.example.com/realms/example --oidc-client abarow-headquarters [--oidc-secret-file file]] [--ghcr-user name --ghcr-token-file file] [--publisher-url https://update.example.com:8443 --publisher-ca ca.pem [--level test|pilot|prod] [--publisher-key root.pub] [--channel-keys channel.json]]
#   sudo deploy/install/headquarters-install.sh store-add store-2 <store server key> ["Kadıköy"]
#   sudo deploy/install/headquarters-install.sh store-remove store-2
#   sudo deploy/install/headquarters-install.sh account add|password|disable|enable|list [user] ["Full Name"] [--password-file file]
#   sudo deploy/install/headquarters-install.sh account email <user> [address]
#   sudo deploy/install/headquarters-install.sh account identity-remove <user>
#   sudo deploy/install/headquarters-install.sh restore <backup file> [--yes]
#   sudo deploy/install/headquarters-install.sh reprocess
#   sudo deploy/install/headquarters-install.sh catalog-load <catalog.json>
#   sudo deploy/install/headquarters-install.sh backup-copy
#
# What the install does (running it again is safe; it does not break what exists):
#   1. Checks that Podman 5, aardvark-dns and nftables are present.
#   2. Creates the abarow user and enables linger.
#   3. Generates the database password at random (keeps it if it exists); makes it a Podman secret.
#   4. Generates the NATS keys and the leaf TLS (a CA and server certificate
#      specific to headquarters; the addresses are --address), keeps them if they exist.
#   5. Copies the Quadlet files and starts the services; sets up the daily
#      database backup and takes the first backup. If --domain is given it
#      installs Caddy, which serves the screen over HTTPS under that name (it
#      gets the certificate from Let's Encrypt and renews it itself); if not
#      given it removes a previously installed Caddy. If --backup-target is given
#      it copies the backups off the machine (an external disk, a NAS or another
#      server over SSH) after every backup.
#   6. If --account is given it creates the headquarters screen account.
#   7. Prints the CA certificate and the command to connect the stores.
#
# store-add: adds a store to headquarters with the key printed by the store
# server install. store-remove: removes a store; it can no longer connect, the
# sales it already sent stay in the reports. account: the accounts of the
# headquarters screen (the password is asked, or read from --password-file); it
# is for the first admin and for recovery, the added user is an admin. The admin
# manages the other users (roles, stores) from the screen.
#
# --login: how to sign in to the screen. The default is "local" (user name and
# password). "oidc" signs in only with the corporate sign-in (OpenID Connect;
# Keycloak, Entra, Google, Authentik...), "local,oidc" allows both. The identity
# provider proves the identity; the user is defined beforehand, their role and
# store authority stay in abarow and they are recognized at the first sign-in by
# the email address (the address is given from the screen or with `account
# email`). The return address registered with the provider must be
# https://<--domain>/api/auth/oidc/callback; the corporate sign-in needs
# --domain (HTTPS). With "oidc" only, if the provider cannot be reached nobody
# can sign in to the screen (stores and sales are not affected).
#
# restore: restores the database from a backup. The current state is backed up
# first (so you can go back if the wrong file is chosen); then the events that
# came after the backup are reprocessed from the headquarters stream (90 days).
# In a non-interactive run --yes is needed. reprocess: only reprocesses the
# headquarters stream from the start (existing events are skipped, missing ones
# are added). catalog-load: loads the registers' starting catalog (catalog.json)
# into headquarters; the information and prices of existing products change to
# what is in the file, and they go down to the registers. The pictures of products
# that have one in the file ("image": "images/kofte.png", relative to the file's
# directory) are loaded too. backup-copy: copies the backups to the target right
# away (normally this happens by itself after every backup; use it to try after authorizing the target).
#
# Headquarters opens only 7422 to the outside (the TLS leaf connection of the
# stores); this port must be opened to the stores (or the internet) in the
# firewall. By default the screen can only be opened from this machine
# (127.0.0.1:9090) so that passwords and reports do not go onto the network over
# plain HTTP. For remote access use --domain (HTTPS; the domain must point to
# this machine and ports 80 and 443 must be open) or a VPN (see
# deploy/quadlet/README.md); to open it to a trusted local network use --screen 0.0.0.0:9090.
set -euo pipefail

here=$(cd "$(dirname "$0")/../.." && pwd)
# shellcheck source-path=SCRIPTDIR source=common.sh
. "$here/deploy/install/common.sh"

quadlet="$here/deploy/quadlet/headquarters"
[ -f "$quadlet/abarow-ingest.container" ] || die "Quadlet files not found: $quadlet"

usage() {
	sed -n '9,18p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
}

# The install settings are kept in /etc/abarow/headquarters.env, owned by root;
# the other commands use them. The file is not executed, it is read line by line.
settings=/etc/abarow/headquarters.env
hosts=
screen=127.0.0.1:9090
domain=
acme_email=
backup_target=
login=
oidc_issuer=
oidc_client=
oidc_secret_file=
channel=0.1
publisher=
publisher_ca=
mirror_root=
channel_keys=
level=prod
account=
password_file=
confirmed=
command=install

case "${1:-}" in
store-add | store-remove | account | restore | reprocess | catalog-load | backup-copy)
	command=$1
	shift
	;;
esac

# Options; the positional arguments (of the commands) stay in args.
args=()
while [ $# -gt 0 ]; do
	case "$1" in
	--address) hosts=$2; shift 2 ;;
	--screen) screen=$2; shift 2 ;;
	--domain) domain=$(printf '%s' "$2" | tr '[:upper:]' '[:lower:]'); shift 2 ;;
	--email) acme_email=$2; shift 2 ;;
	--backup-target) backup_target=${2%/}; backup_target=${backup_target%:}; shift 2 ;;
	--login) login=$2; shift 2 ;;
	--oidc-issuer) oidc_issuer=${2%/}; shift 2 ;;
	--oidc-client) oidc_client=$2; shift 2 ;;
	--oidc-secret-file) oidc_secret_file=$2; shift 2 ;;
	--account) account=$2; shift 2 ;;
	--password-file) password_file=$2; shift 2 ;;
	--publisher-url) publisher=$2; shift 2 ;;
	--publisher-ca) publisher_ca=$2; shift 2 ;;
	--publisher-key) mirror_root=$2; shift 2 ;;
	--channel-keys) channel_keys=$2; shift 2 ;;
	--level) level=$2; shift 2 ;;
	--channel) channel=$2; shift 2 ;;
	--ghcr-user) ghcr_user=$2; shift 2 ;;
	--ghcr-token-file) ghcr_token_file=$2; shift 2 ;;
	--yes) confirmed=1; shift ;;
	-h | --help) usage ;;
	--*) echo "unknown option: $1" >&2; usage ;;
	*) args+=("$1"); shift ;;
	esac
done
set -- "${args[@]+"${args[@]}"}"
[ -z "$password_file" ] || [ -f "$password_file" ] || die "$password_file does not exist."

if [ "$command" = install ]; then
	[ $# -eq 0 ] || usage
	[ -n "$hosts" ] || die "give the domain name or IP the stores will connect to headquarters at with --address (e.g. headquarters.example.com; several separated by commas)."
	printf '%s' "$screen" | grep -Eq '^[0-9.]+:[0-9]+$' || die "--screen must be address:port (e.g. 127.0.0.1:9090 or 0.0.0.0:9090)."
	# The certificate is issued to a domain name: an IP will not do, it has at least one dot.
	[ -z "$domain" ] || printf '%s' "$domain" | grep -Eq '^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$' ||
		die "--domain must be a domain name (e.g. reports.example.com; an IP will not do)."
	[ -z "$acme_email" ] || [ -n "$domain" ] || die "--email is only given with --domain."
	[ -z "$acme_email" ] || printf '%s' "$acme_email" | grep -Eq '^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$' ||
		die "--email must be an email address."
	if [ -n "$publisher" ]; then
		[ -n "$publisher_ca" ] || die "--publisher-url needs the publisher's CA certificate (--publisher-ca)."
		update_validate "$level" "$publisher" "$publisher_ca"
		[ -z "$mirror_root" ] || mirror_validate "$mirror_root"
		[ -z "$channel_keys" ] || update_release_validate "$channel_keys"
	elif [ -n "$publisher_ca" ] || [ -n "$mirror_root" ] || [ -n "$channel_keys" ]; then
		die "--publisher-ca, --publisher-key and --channel-keys are only used with --publisher-url."
	fi
	[ -z "$backup_target" ] || backup_target_validate "$backup_target"
	# The corporate sign-in (OIDC) has an HTTPS return address registered with
	# the provider: the screen is served over HTTPS with --domain.
	case "$login" in
	oidc | local,oidc | oidc,local) [ -n "$domain" ] || die "the corporate sign-in (--login $login) needs the HTTPS address of the screen: give --domain." ;;
	esac
	oidc_validate "$login" "$oidc_issuer" "$oidc_client" "https://$domain"
	[ -z "$oidc_secret_file" ] || [ -n "$oidc_issuer" ] || die "--oidc-secret-file is only given with the corporate sign-in (--login oidc)."
fi

preflight
ensure_user

headquarters="$home/abarow/headquarters"
auth="$headquarters/auth"       # NATS keys, the private key of the CA, the database password: only abarow reads it
config="$headquarters/config"   # stores.json: ingest reads it too
backups="$headquarters/backups" # daily database backups
as mkdir -p "$auth" "$config" "$backups"
as chmod 0700 "$auth" "$backups"
chmod 0750 "$home"

setting() { sed -n "s/^$1=//p" "$settings" | head -n 1; }
if [ "$command" = install ]; then
	mkdir -p "$(dirname "$settings")"
	cat >"$settings" <<EOF
# written by headquarters-install.sh; store-add, store-remove and account work with these settings.
hosts=$hosts
screen=$screen
domain=$domain
acme_email=$acme_email
backup_target=$backup_target
channel=$channel
publisher=$publisher
mirror=${mirror_root:+1}
level=$level
login=$login
oidc_issuer=$oidc_issuer
oidc_client=$oidc_client
EOF
else
	[ -f "$settings" ] || die "headquarters is not installed: first run headquarters-install.sh --address ..."
	hosts=$(setting hosts)
	screen=$(setting screen)
	domain=$(setting domain)
	acme_email=$(setting acme_email)
	backup_target=$(setting backup_target)
	channel=$(setting channel)
	publisher=$(setting publisher)
	mirror_root=$(setting mirror)
	level=$(setting level)
	login=$(setting login)
	oidc_issuer=$(setting oidc_issuer)
	oidc_client=$(setting oidc_client)
fi
[ "$command" != install ] || ghcr_login

# read_password <prompt>: reads the password from --password-file or asks twice.
read_password() {
	if [ -n "$password_file" ]; then
		head -n 1 "$password_file"
		return
	fi
	[ -t 0 ] || die "the password cannot be asked (not interactive); give --password-file."
	local p again
	read -r -s -p "$1 (at least 10 characters): " p
	echo >&2
	read -r -s -p "Password (again): " again
	echo >&2
	[ "$p" = "$again" ] || die "the passwords are not the same."
	printf '%s\n' "$p"
}

# reporting <command>...: the account command of the headquarters screen (in the
# running reporting container; the database address is there).
reporting() { as podman exec -i abarow-reporting /service user "$@"; }

# replay: deletes ingest's consumer; the running ingest reads the headquarters
# stream from the start.
replay() {
	as systemctl --user is-active --quiet abarow-ingest.service || die "abarow-ingest is not running."
	as podman exec abarow-ingest /service replay
}

if [ "$command" = backup-copy ]; then
	[ $# -eq 0 ] || die "usage: headquarters-install.sh backup-copy"
	backup_copy_command headquarters-install.sh "$headquarters" "$backups" "$backup_target" || exit 1
	exit 0
fi

if [ "$command" = catalog-load ]; then
	[ $# -eq 1 ] || die "usage: headquarters-install.sh catalog-load <catalog.json>"
	[ -f "$1" ] || die "$1 does not exist."
	as systemctl --user is-active --quiet abarow-catalog-admin.service || die "abarow-catalog-admin is not running."
	as podman exec -i abarow-catalog-admin /service import setup <"$1"
	# The pictures of the products that have one in the file (the path is relative to the file's directory).
	dir=$(dirname "$1")
	as podman exec -i abarow-catalog-admin /service image-list <"$1" | while read -r id path; do
		[ -f "$dir/$path" ] || die "$id: picture file does not exist: $dir/$path"
		as podman exec -i abarow-catalog-admin /service image "$id" setup <"$dir/$path"
	done
	exit 0
fi

if [ "$command" = reprocess ]; then
	[ $# -eq 0 ] || die "usage: headquarters-install.sh reprocess"
	replay
	exit 0
fi

if [ "$command" = restore ]; then
	[ $# -eq 1 ] || die "usage: headquarters-install.sh restore <backup file> (backups: $backups)"
	[ -f "$1" ] || die "$1 does not exist."
	if [ -z "$confirmed" ]; then
		[ -t 0 ] || die "confirmation cannot be asked (not interactive); give --yes."
		read -r -p "The headquarters database will be replaced with the backup $(basename "$1") (the current state is backed up first). Type yes to continue: " answer
		[ "$answer" = yes ] || die "cancelled."
	fi
	# The backup is read from the backup directory the container sees.
	dump=$(basename "$1")
	if [ "$(cd "$(dirname "$1")" && pwd)" != "$backups" ]; then
		dump="restore-$(date +%Y%m%d%H%M%S).dump"
		install -o "$user" -g "$user" -m 0600 "$1" "$backups/$dump"
	fi
	as systemctl --user is-active --quiet abarow-db.service || die "abarow-db is not running."
	as systemctl --user start abarow-backup.service || die "could not back up the current state; nothing was restored."
	say "The current state was backed up ($backups)"
	as systemctl --user stop abarow-reporting.service abarow-ingest.service
	as podman exec abarow-db psql -q -U abarow -d postgres -c 'DROP DATABASE abarow WITH (FORCE)' -c 'CREATE DATABASE abarow' >/dev/null
	as podman run --rm --network abarow-headquarters --secret abarow-db-password,type=env,target=PGPASSWORD \
		-v "$backups:/b:ro,Z" docker.io/library/postgres:18-alpine \
		pg_restore -h abarow-db -U abarow -d abarow --no-owner "/b/$dump" ||
		die "the restore was left half done; the services are off. The backup of the previous state is in $backups (the newest file)."
	say "The database was restored from the backup $dump"
	# shellcheck disable=SC2046 # unit names contain no spaces
	start_units $(unit_names "$quadlet")
	replay
	say "The events that came after the backup are being reprocessed from the headquarters stream"
	exit 0
fi

if [ "$command" = account ]; then
	as systemctl --user is-active --quiet abarow-reporting.service || die "abarow-reporting is not running."
	case "${1:-}" in
	add)
		[ $# -eq 2 ] || [ $# -eq 3 ] || die "usage: headquarters-install.sh account add <user> [\"Full Name\"]"
		read_password "Password for $2" | reporting add "$2" "${3:-}"
		;;
	password)
		[ $# -eq 2 ] || die "usage: headquarters-install.sh account password <user>"
		read_password "New password for $2" | reporting passwd "$2"
		;;
	disable | enable)
		[ $# -eq 2 ] || die "usage: headquarters-install.sh account $1 <user>"
		if [ "$1" = disable ]; then reporting disable "$2" </dev/null; else reporting enable "$2" </dev/null; fi
		;;
	list) reporting list </dev/null ;;
	email)
		[ $# -eq 2 ] || [ $# -eq 3 ] || die "usage: headquarters-install.sh account email <user> [address]"
		reporting email "$2" ${3:+"$3"} </dev/null
		;;
	identity-remove)
		[ $# -eq 2 ] || die "usage: headquarters-install.sh account identity-remove <user>"
		reporting unlink "$2" </dev/null
		;;
	*) die "usage: headquarters-install.sh account add|password|disable|enable|list|email|identity-remove ..." ;;
	esac
	exit 0
fi

natsauth=(as podman run --rm --user 0 -v "$auth:/auth:Z" -v "$config:/config:Z" "ghcr.io/abarow/abarow-natsauth:$channel")

case "$command" in
store-add)
	[ $# -ge 2 ] || die "usage: headquarters-install.sh store-add <store-id> <key> [name]"
	valid_identity "$1" || die "the store identity \"$1\" is invalid: letters, digits, - and _."
	"${natsauth[@]}" -stores /config/stores.json -add-store "$1" -store-key "$2" -store-name "${3:-}"
	;;
store-remove)
	[ $# -eq 1 ] || die "usage: headquarters-install.sh store-remove <store-id>"
	"${natsauth[@]}" -stores /config/stores.json -remove-store "$1"
	;;
install)
	# The client secret of the corporate sign-in (if any); when it is not given
	# at a repeated install the old one is kept.
	if [ -n "$oidc_secret_file" ]; then
		oidc_secret abarow-headquarters-oidc-secret "$oidc_secret_file"
		say "Corporate sign-in client secret saved"
	fi
	# 3. Database password: generated at random, kept when the install is
	# repeated (the database is created with this password on its first start).
	if ! as test -s "$auth/db-password"; then
		# 30 random bytes, 40 characters that cause no trouble in an address (A-Z a-z 0-9 _ -).
		head -c 30 /dev/urandom | base64 -w0 | tr '/+' '_-' | as tee "$auth/db-password" >/dev/null
		as chmod 0600 "$auth/db-password"
		say "Database password generated"
	fi
	db_password=$(as cat "$auth/db-password")
	secret abarow-db-password "$auth/db-password"
	printf 'postgres://abarow:%s@abarow-db:5432/abarow?sslmode=disable' "$db_password" | as podman secret create --replace abarow-db-url - >/dev/null
	unset db_password
	;;
esac

# 4. NATS configuration and the leaf TLS (regenerated when the store list
# changes too; the keys and the CA are kept).
before=$( (cat "$auth/nats.conf" 2>/dev/null || true) | sha256sum)
"${natsauth[@]}" -role headquarters -out /auth -conf-dir /etc/nats -users ingest,catalog,inventory -server-name headquarters \
	-stores /config/stores.json -leaf-hosts "$hosts"
secret abarow-headquarters-nats-conf "$auth/nats.conf"
secret abarow-headquarters-tls-cert "$auth/tls/server.pem"
secret abarow-headquarters-tls-key "$auth/tls/server.key"
secret abarow-headquarters-nats-ingest "$auth/ingest/nats.nk"
secret abarow-headquarters-nats-catalog "$auth/catalog/nats.nk"
secret abarow-headquarters-nats-inventory "$auth/inventory/nats.nk"
# The CA certificate the stores will use when installed (the public part; the
# private key stays in $auth/tls).
as install -m 0644 "$auth/ca/ca.pem" "$headquarters/ca.pem"
say "NATS configuration is ready"

# 5. Services. They are restarted if the configuration or the store list changed.
units_dir="$home/.config/containers/systemd"
dropins_before=$(dropin_digest)
install_units "$quadlet"
install_units "$quadlet/backup"
# Update (optional): the agent registers with the publisher; the server approves the store and register registrations.
# Nothing updates by itself.
update_units=
if [ -n "$publisher" ]; then
	update_units="abarow-headquarters-update.service abarow-headquarters-update-server.service"
	[ -z "$mirror_root" ] || update_units="$update_units abarow-headquarters-mirror-auth.service abarow-headquarters-mirror.service"
	if [ "$command" = install ]; then
		update_install "$quadlet/update" abarow-headquarters-update "$publisher" "$publisher_ca" "$level" "Headquarters"
		update_screen abarow-reporting ABAROW_UPDATE_ADMIN_URL=http://abarow-headquarters-update-server:8082 ABAROW_UPDATE_AGENT_URL=http://abarow-headquarters-update:8080
		if [ -n "$channel_keys" ]; then
			update_release abarow-headquarters-update "$channel_keys" abarow-headquarters-update "ABAROW_UPDATE_LEVEL=$level"
		else
			update_release_remove abarow-headquarters-update
		fi
		# Image mirror (optional): the store servers pull images from here, by connecting to the publisher.
		if [ -n "$mirror_root" ]; then
			mirror_install "$quadlet/mirror" headquarters "${hosts%%,*}" "$mirror_root" "$here/deploy/quadlet/mirror/zot.json.template" "$(url_host "$publisher"):5443" "$publisher_ca"
			# Headquarters' agent pulls images from its own mirror (the mirror fetches from the publisher on demand).
			if [ -n "$channel_keys" ]; then
				registry_trust "${hosts%%,*}:5443" "$headquarters/ca.pem"
				agent_registry_config abarow-headquarters-update "${hosts%%,*}:5443"
			fi
		else
			mirror_remove headquarters
			# Without a mirror the agent pulls images directly from the publisher's registry.
			if [ -n "$channel_keys" ]; then
				registry_trust "$(url_host "$publisher"):5443" "$publisher_ca"
				agent_registry_config abarow-headquarters-update "$(url_host "$publisher"):5443"
			fi
		fi
	fi
elif [ "$command" = install ]; then
	update_remove abarow-headquarters-update-server
	update_remove abarow-headquarters-update
	update_screen abarow-reporting
	mirror_remove headquarters
fi
sed -i "s|^PublishPort=.*:8080$|PublishPort=$screen:8080|" "$units_dir/abarow-reporting.container"
install -o "$user" -g "$user" -m 0755 "$here/deploy/quadlet/backup/backup.sh" "$headquarters/backup.sh"
as mkdir -p "$home/.config/systemd/user"
install -o "$user" -g "$user" -m 0644 "$quadlet/backup/abarow-backup.timer" "$home/.config/systemd/user/"
# Corporate sign-in (OIDC): only reporting offers the screen sign-in. The return
# address is the HTTPS address of the screen (--domain).
oidc_dropin headquarters-install.sh "$units_dir/abarow-reporting.container.d/oidc.conf" abarow-headquarters-oidc-secret "$login" "$oidc_issuer" "$oidc_client" "https://$domain"
# The database settings are computed from the number of connected stores
# (reporting pgtune, pkg/pgtune.ForHeadquarters): rewritten when a store is added
# or removed; if the settings changed the database is restarted (shared_buffers
# and max_connections do not change without a restart). The values in the Quadlet
# file are the base for a single store.
db_dropin="$units_dir/abarow-db.container.d"
tune=$(as podman run --rm --network none -v "$config:/config:ro,Z" "ghcr.io/abarow/abarow-reporting:$channel" pgtune) ||
	die "could not compute the database settings (reporting pgtune)"
db_memory=$(printf '%s\n' "$tune" | sed -n 's/^MEMORY_MB=//p')
db_flags=$(printf '%s\n' "$tune" | sed -n 's/^ARGS=//p')
db_shm=$(printf '%s\n' "$tune" | sed -n 's/^SHM_MB=//p')
[ -n "$db_memory" ] && [ -n "$db_flags" ] || die "could not read the database settings: $tune"
tune_before=$( (cat "$db_dropin/tune.conf" 2>/dev/null || true) | sha256sum)
as mkdir -p "$db_dropin"
as tee "$db_dropin/tune.conf" >/dev/null <<EOF
# written by headquarters-install.sh: database settings according to the number
# of connected stores (reporting pgtune; pkg/pgtune.ForHeadquarters). Rewritten when a store is added or removed.
[Container]
Exec=postgres $db_flags
PodmanArgs=--memory=${db_memory}m --shm-size=${db_shm:-64}m
EOF
tune_changed=false
[ "$tune_before" = "$( (cat "$db_dropin/tune.conf" 2>/dev/null || true) | sha256sum)" ] || tune_changed=true
as systemctl --user daemon-reload
if $tune_changed && as systemctl --user is-active --quiet abarow-db.service; then
	as systemctl --user restart abarow-db.service || true
fi
if [ "$dropins_before" != "$(dropin_digest)" ] && as systemctl --user is-active --quiet abarow-reporting.service; then
	as systemctl --user restart abarow-reporting.service || true
fi
if [ "$before" != "$(sha256sum <"$auth/nats.conf")" ] || [ "$command" != install ]; then
	for unit in abarow-headquarters abarow-ingest abarow-catalog-admin abarow-inventory-headquarters; do
		if as systemctl --user is-active --quiet "$unit.service"; then
			as systemctl --user restart "$unit.service" || true
		fi
	done
fi
# shellcheck disable=SC2046,SC2086 # unit names contain no spaces
start_units $(unit_names "$quadlet") $update_units

# 5b. HTTPS for the screen (--domain): Caddy, in front of abarow-reporting.
caddy_unit="$units_dir/abarow-caddy.container"
caddyfile="$headquarters/caddy/Caddyfile"
caddy_digest() { (cat "$caddyfile" "$caddy_unit" 2>/dev/null || true) | sha256sum; }
if [ -n "$domain" ]; then
	# Let rootless Podman open 80 and 443 (the default limit is 1024). The setting
	# is written right away; at boot systemd-sysctl applies it from sysctl.d (on a
	# plain Debian neither the sysctl command nor the sysctl.d directory exists).
	port_start=/proc/sys/net/ipv4/ip_unprivileged_port_start
	if [ "$(cat "$port_start")" -gt 80 ]; then
		mkdir -p /etc/sysctl.d
		cat >/etc/sysctl.d/90-abarow-https.conf <<'EOF'
# headquarters-install.sh --domain: the Caddy container of the abarow user opens 80 and 443.
net.ipv4.ip_unprivileged_port_start=80
EOF
		echo 80 >"$port_start"
	fi
	caddy_before=$(caddy_digest)
	as mkdir -p "$headquarters/caddy"
	{
		echo "# written by headquarters-install.sh (--domain, --email); manual changes are deleted when the install is repeated."
		[ -z "$acme_email" ] || printf '{\n\temail %s\n}\n\n' "$acme_email"
		printf '%s {\n' "$domain"
		printf '\tencode zstd gzip\n'
		# The browser opens this name over HTTPS only for a year.
		printf '\theader Strict-Transport-Security "max-age=31536000"\n'
		printf '\treverse_proxy abarow-reporting:8080\n'
		printf '}\n'
	} | as tee "$caddyfile" >/dev/null
	install -o "$user" -g "$user" -m 0644 "$quadlet/https/abarow-caddy.container" "$units_dir/"
	as systemctl --user daemon-reload
	if [ "$caddy_before" != "$(caddy_digest)" ] && as systemctl --user is-active --quiet abarow-caddy.service; then
		as systemctl --user restart abarow-caddy.service || true
	fi
	start_units abarow-caddy.service
elif [ -f "$caddy_unit" ]; then
	as systemctl --user stop abarow-caddy.service || true
	rm -f "$caddy_unit"
	as systemctl --user daemon-reload
	say "HTTPS (Caddy) removed: --domain was not given. The certificates stay in the abarow-caddy-data volume."
fi
if [ "$command" = install ]; then
	as systemctl --user enable --now abarow-backup.timer
	# The first backup is taken right away: that the backup works is seen at install time.
	as systemctl --user start abarow-backup.service ||
		die "could not take the database backup (journalctl --user -u abarow-backup)"
	say "Daily backup set up ($backups; the first backup was taken)"
fi

# 5c. Copying the backups off the machine (--backup-target): after every
# successful backup (the drop-in file of abarow-backup).
if [ "$command" = install ]; then
	if [ -n "$backup_target" ]; then
		backup_copy_install headquarters-install.sh "$headquarters" "$backups" abarow-backup "$quadlet/backup/abarow-backup-copy.service" "$backup_target"
	else
		backup_copy_remove "$headquarters" abarow-backup
	fi
fi

# 6. The first account
if [ -n "$account" ]; then
	if reporting list </dev/null | cut -f1 | grep -qx "$(printf '%s' "$account" | tr '[:upper:]' '[:lower:]')"; then
		read_password "New password for $account" | reporting passwd "$account"
	else
		read_password "Password for $account" | reporting add "$account"
	fi
fi

# 7. Summary
first=${hosts%%,*}
case "$command" in
install)
	say "Headquarters is running; screen http://$screen"
	case "$login" in
	oidc | local,oidc | oidc,local)
		say "Corporate sign-in is on ($login). The return address to register with the provider: https://$domain/api/auth/oidc/callback"
		say "The users' email addresses are given in the Users section; if there is no local sign-in: sudo deploy/install/headquarters-install.sh account email <user> <address>"
		oidc_warning "$login"
		;;
	esac
	if [ -n "$domain" ]; then
		# The certificate shows up in Caddy's storage (in a directory by provider);
		# getting it takes from a few seconds to a few minutes.
		# shellcheck disable=SC2016 # $1 is expanded by sh in the container
		certificate() { as podman exec abarow-caddy sh -c 'ls /data/caddy/certificates/*/"$1"/"$1".crt' _ "$domain" >/dev/null 2>&1; }
		for _ in $(seq 1 60); do
			certificate && break
			sleep 2
		done
		if certificate; then
			say "Screen: https://$domain (the certificate was obtained; Caddy renews it itself)"
		else
			say "Screen: https://$domain — the certificate could not be obtained yet. The domain must point to this machine's IP, and 80 and 443 must be open to the internet; Caddy keeps trying."
			say "Caddy's log: sudo -u $user XDG_RUNTIME_DIR=/run/user/$uid podman logs abarow-caddy"
		fi
	fi
	if [ -z "$(reporting list </dev/null)" ]; then
		say "The screen has no account; to create one: sudo deploy/install/headquarters-install.sh account add <user> \"Full Name\""
	fi
	[ -n "$backup_target" ] || say "The backups are only on this machine; if the disk fails they go with it. To copy them off the machine: --backup-target"
	say "To connect the stores, copy the CA certificate to the store server: $headquarters/ca.pem"
	say "The digest of the CA certificate (it must be the same at the store server): $(sha256sum <"$headquarters/ca.pem" | cut -d' ' -f1)"
	say "At the store server (from the repository root; --store-id and --address are the store's):"
	printf '\n    sudo deploy/install/store-install.sh --store-id store-1 --address 192.168.1.10 --headquarters tls://%s:7422 --headquarters-ca ca.pem\n\n' "$first"
	;;
store-add) say "$1 was added; the store connects within a few seconds and its accumulated sales arrive" ;;
store-remove) say "$1 was removed; the store can no longer connect" ;;
esac
