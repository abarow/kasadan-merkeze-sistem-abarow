#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# store-install.sh prepares a Linux machine as the store server (the back house
# of the store) and connects the registers to it (as root, from the repository root):
#
#   sudo deploy/install/store-install.sh --store-id store-1 --address 192.168.1.10[,store.local] [--password-file file]
#        [--headquarters tls://headquarters.example.com:7422[,tls://hq2...:7422,tls://hq3...:7422] --headquarters-ca ca.pem] [--backup-target /mnt/backup/abarow|user@server[:/path]]
#        [--update-url https://headquarters.example.com:8443 [--level test|pilot|prod] [--publisher-key root.pub] [--channel-keys channel.json]]
#        [--login local,oidc --oidc-issuer https://idp.example.com/realms/example --oidc-client abarow-store
#         --oidc-url https://store.example.com [--oidc-secret-file file]]
#        [--ghcr-user name --ghcr-token-file file]
#   sudo deploy/install/store-install.sh register-add register-2 <register key> ["Register 2"]
#   sudo deploy/install/store-install.sh register-remove register-2
#   sudo deploy/install/store-install.sh cashiers-load cashiers.json
#   sudo deploy/install/store-install.sh pin <cashier-code> [--pin-file file]
#   sudo deploy/install/store-install.sh email <manager-code> [address]
#   sudo deploy/install/store-install.sh identity-remove <manager-code>
#   sudo deploy/install/store-install.sh restore <backup file> [--yes]
#   sudo deploy/install/store-install.sh restore-to "2026-11-12 14:30:00+03" [--yes]
#   sudo deploy/install/store-install.sh reprocess
#   sudo deploy/install/store-install.sh backup-copy
#
# What the install does (running it again is safe; it does not break what exists):
#   1. Checks that Podman 5, aardvark-dns and nftables are present.
#   2. Creates the abarow user and enables linger.
#   3. Asks for the store password of the screen (or reads it from
#      --password-file) and makes only its digest a Podman secret. If the
#      password was given before it is not asked again; to change it give --password-file.
#   4. Generates the NATS keys and the leaf TLS (a CA and server certificate
#      specific to the store; the addresses are --address), keeps them if they
#      exist; makes them Podman secrets. If --headquarters is given the store
#      server connects to headquarters with a TLS leaf connection; the
#      headquarters certificate is verified with --headquarters-ca
#      (~abarow/abarow/headquarters/ca.pem on headquarters).
#   5. Copies the Quadlet files and starts the services. The store server's
#      database is PostgreSQL (abarow-store-db); its password is generated at
#      random and kept when the install is repeated. A daily backup (pg_dump, 14 days) is set up.
#   6. Prints the CA certificate to use when installing the registers and the
#      command; if --headquarters was given, also the command to add the store server to headquarters.
#
# register-add: adds a register to the store server with the key printed by
# the register install. register-remove: removes a register (e.g. the register
# was stolen); it can no longer connect, the sales it already sent stay in the
# reports. cashiers-load: loads the registers' cashier file (cashiers.json)
# into the store server; from then on the cashiers are managed on the store
# server screen and come down to the registers from there. pin: changes a
# cashier's PIN (asked, or read from --pin-file). While a manager account is
# open, the store password of the screen is off; this is the recovery path when
# no other manager exists for a manager who forgot their PIN. restore: restores
# the database from a backup (the current state is backed up first) and
# reprocesses the sales that came after the backup from the store stream.
# reprocess: does only that (report and stock read the store stream from the
# start; existing events are skipped, missing ones are added). Goods receipts
# and counts exist only in the database, so those after the backup do not come
# back. When --backup-target is given the backups are copied off the machine
# after every backup (an external disk, a NAS or another server over SSH);
# backup-copy does that right away.
#
# --login: how to sign in to the screen. The default is "local" (manager code +
# PIN; the store password while there is no manager). "oidc" signs in only with
# the corporate sign-in (OpenID Connect; Keycloak, Entra, Google, Authentik...),
# "local,oidc" allows both. The identity provider proves the identity; the person
# is defined beforehand as a manager in the Cashiers section and is recognized by
# the email address (given from the screen or with the `report set-email`
# command). The return address registered with the provider must be
# <--oidc-url>/api/auth/oidc/callback and must be HTTPS: the store server screen
# serves HTTPS (the certificate of the store CA); the provider is given
# https://<--address>:8090 as --oidc-url. With "oidc" only, if the provider
# cannot be reached nobody can sign in to the screen (registers and sales are not affected).
#
# The store server opens two ports on the store network: 7421 (the TLS leaf
# connection of the registers) and 8090 (the store server screen; with the
# manager PIN, or the store password while no manager is defined). If there is
# a firewall these ports must be opened to the store network.
set -euo pipefail

here=$(cd "$(dirname "$0")/../.." && pwd)
# shellcheck source-path=SCRIPTDIR source=common.sh
. "$here/deploy/install/common.sh"

quadlet="$here/deploy/quadlet/store"
[ -f "$quadlet/abarow-report.container" ] || die "Quadlet files not found: $quadlet"

usage() {
	sed -n '5,22p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
}

# The install settings are kept in /etc/abarow/store.env, owned by root;
# register-add uses them. The file is not executed, it is read line by line.
settings=/etc/abarow/store.env
store_id=
hosts=
channel=0.1
password_file=
headquarters=
headquarters_ca=
update=
mirror_root=
channel_keys=
level=prod
backup_target=
login=
oidc_issuer=
oidc_client=
oidc_url=
oidc_secret_file=
confirmed=
command=install

case "${1:-}" in
register-add | register-remove | cashiers-load | pin | email | identity-remove | restore | restore-to | reprocess | backup-copy)
	command=$1
	shift
	;;
esac

if [ "$command" = install ]; then
	while [ $# -gt 0 ]; do
		case "$1" in
		--store-id) store_id=$2; shift 2 ;;
		--address) hosts=$2; shift 2 ;;
		--password-file) password_file=$2; shift 2 ;;
		--headquarters) headquarters=$2; shift 2 ;;
		--headquarters-ca) headquarters_ca=$2; shift 2 ;;
		--update-url) update=$2; shift 2 ;;
		--level) level=$2; shift 2 ;;
		--publisher-key) mirror_root=$2; shift 2 ;;
		--channel-keys) channel_keys=$2; shift 2 ;;
		--backup-target) backup_target=${2%/}; backup_target=${backup_target%:}; shift 2 ;;
		--login) login=$2; shift 2 ;;
		--oidc-issuer) oidc_issuer=${2%/}; shift 2 ;;
		--oidc-client) oidc_client=$2; shift 2 ;;
		--oidc-url) oidc_url=${2%/}; shift 2 ;;
		--oidc-secret-file) oidc_secret_file=$2; shift 2 ;;
		--channel) channel=$2; shift 2 ;;
		--ghcr-user) ghcr_user=$2; shift 2 ;;
		--ghcr-token-file) ghcr_token_file=$2; shift 2 ;;
		-h | --help) usage ;;
		*) echo "unknown option: $1" >&2; usage ;;
		esac
	done
	[ -n "$store_id" ] || die "give the store identity with --store-id (e.g. store-1)."
	valid_identity "$store_id" || die "the store identity \"$store_id\" is invalid: letters, digits, - and _."
	[ -n "$hosts" ] || die "give the IP or name the registers will connect to the store server at with --address (e.g. 192.168.1.10; several separated by commas)."
	[ -z "$password_file" ] || [ -f "$password_file" ] || die "$password_file does not exist."
	if [ -n "$headquarters" ]; then
		case "$headquarters" in
		tls://*) ;;
		*) die "--headquarters must start with tls:// (e.g. tls://headquarters.example.com:7422); the connection is always TLS." ;;
		esac
		[ -n "$headquarters_ca" ] || die "--headquarters needs the headquarters CA certificate (--headquarters-ca; ~abarow/abarow/headquarters/ca.pem on headquarters)."
		[ -f "$headquarters_ca" ] || die "$headquarters_ca does not exist."
	elif [ -n "$headquarters_ca" ]; then
		die "--headquarters-ca is only used with --headquarters."
	fi
	if [ -n "$update" ]; then
		[ -n "$headquarters_ca" ] || die "--update-url needs the headquarters CA certificate (--headquarters --headquarters-ca)."
		update_validate "$level" "$update" "$headquarters_ca"
		[ -z "$mirror_root" ] || mirror_validate "$mirror_root"
		[ -z "$channel_keys" ] || update_release_validate "$channel_keys"
	elif [ -n "$mirror_root" ] || [ -n "$channel_keys" ]; then
		die "--publisher-key and --channel-keys are only used with --update-url."
	fi
	[ -z "$backup_target" ] || backup_target_validate "$backup_target"
	oidc_validate "$login" "$oidc_issuer" "$oidc_client" "$oidc_url"
	[ -z "$oidc_secret_file" ] || [ -n "$oidc_issuer" ] || die "--oidc-secret-file is only given with the corporate sign-in (--login oidc)."
fi

preflight
ensure_user

store="$home/abarow/store"
auth="$store/auth"       # NATS keys, the private key of the CA: only abarow reads it
config="$store/config"   # registers.json: report reads it too
backups="$store/backups" # daily database backups
as mkdir -p "$auth" "$config" "$backups"
as chmod 0700 "$auth" "$backups"
chmod 0750 "$home"

setting() { sed -n "s/^$1=//p" "$settings" | head -n 1; }
if [ "$command" = install ]; then
	mkdir -p "$(dirname "$settings")"
	cat >"$settings" <<EOF
# written by store-install.sh; register-add and register-remove configure with these settings.
store_id=$store_id
hosts=$hosts
channel=$channel
headquarters=$headquarters
update=$update
mirror=${mirror_root:+1}
level=$level
backup_target=$backup_target
login=$login
oidc_issuer=$oidc_issuer
oidc_client=$oidc_client
oidc_url=$oidc_url
EOF
	if [ -n "$headquarters" ]; then
		as tee "$auth/headquarters-ca.pem" <"$headquarters_ca" >/dev/null
	fi
else
	[ -f "$settings" ] || die "the store server is not installed: first run store-install.sh --store-id ... --address ..."
	store_id=$(setting store_id)
	hosts=$(setting hosts)
	channel=$(setting channel)
	headquarters=$(setting headquarters)
	update=$(setting update)
	mirror_root=$(setting mirror)
	level=$(setting level)
	backup_target=$(setting backup_target)
	login=$(setting login)
	oidc_issuer=$(setting oidc_issuer)
	oidc_client=$(setting oidc_client)
	oidc_url=$(setting oidc_url)
fi
[ "$command" != install ] || ghcr_login

natsauth=(as podman run --rm --user 0 -v "$auth:/auth:Z" -v "$config:/config:Z" "ghcr.io/abarow/abarow-natsauth:$channel")

# The cashier file is loaded into the database of the running report; report
# publishes the changes to the registers and to headquarters.
if [ "$command" = cashiers-load ]; then
	[ $# -eq 1 ] || die "usage: store-install.sh cashiers-load <cashiers.json>"
	[ -f "$1" ] || die "$1 does not exist."
	as systemctl --user is-active --quiet abarow-report.service || die "abarow-report is not running."
	as podman exec -i abarow-report /service import-cashiers <"$1" ||
		die "could not load the cashiers (if the store server was installed with an old install, first run store-install.sh again with the install options)"
	say "Cashiers are now managed on the store server screen; the cashier file on the registers no longer changes these cashiers."
	exit 0
fi

if [ "$command" = pin ]; then
	pin_file=
	if [ "${2:-}" = --pin-file ]; then
		pin_file=${3:-}
		[ -f "$pin_file" ] || die "${pin_file:---pin-file} does not exist."
		set -- "$1"
	fi
	[ $# -eq 1 ] || die "usage: store-install.sh pin <cashier-code> [--pin-file file]"
	if [ -n "$pin_file" ]; then
		pin=$(head -n 1 "$pin_file")
	else
		[ -t 0 ] || die "the PIN cannot be asked (not interactive); give --pin-file."
		read -r -s -p "New PIN for $1 (4-8 digits): " pin
		echo >&2
		read -r -s -p "PIN (again): " again
		echo >&2
		[ "$pin" = "$again" ] || die "the PINs are not the same."
	fi
	as systemctl --user is-active --quiet abarow-report.service || die "abarow-report is not running."
	printf '%s\n' "$pin" | as podman exec -i abarow-report /service set-pin "$1" || die "could not change the PIN."
	exit 0
fi

# email: the email address by which the manager is recognized at the first
# corporate sign-in (OIDC) (removed if no address is given); in an install with
# no local sign-in it is the only way for the first manager. identity-remove:
# removes the manager's corporate identity link (they lost their identity); at
# the next sign-in they are recognized again by the email address.
if [ "$command" = email ] || [ "$command" = identity-remove ]; then
	as systemctl --user is-active --quiet abarow-report.service || die "abarow-report is not running."
	if [ "$command" = email ]; then
		[ $# -eq 1 ] || [ $# -eq 2 ] || die "usage: store-install.sh email <manager-code> [address]"
		as podman exec abarow-report /service set-email "$@" </dev/null || die "could not save the email address."
	else
		[ $# -eq 1 ] || die "usage: store-install.sh identity-remove <manager-code>"
		as podman exec abarow-report /service unlink "$1" </dev/null || die "could not remove the identity link."
	fi
	exit 0
fi

# replay: deletes the report and stock consumers; the running services read the
# store stream from the start.
replay() {
	local unit
	for unit in report inventory; do
		as systemctl --user is-active --quiet "abarow-$unit.service" || die "abarow-$unit is not running."
	done
	as podman exec abarow-report /service replay
	as podman exec abarow-inventory /service replay
}

if [ "$command" = backup-copy ]; then
	[ $# -eq 0 ] || die "usage: store-install.sh backup-copy"
	backup_copy_command store-install.sh "$store" "$backups" "$backup_target" || exit 1
	exit 0
fi

if [ "$command" = reprocess ]; then
	[ $# -eq 0 ] || die "usage: store-install.sh reprocess"
	replay
	exit 0
fi

if [ "$command" = restore ]; then
	if [ "${2:-}" = --yes ]; then
		confirmed=1
		set -- "$1"
	fi
	[ $# -eq 1 ] || die "usage: store-install.sh restore <backup file> [--yes] (backups: $backups)"
	[ -f "$1" ] || die "$1 does not exist."
	if [ -z "$confirmed" ]; then
		[ -t 0 ] || die "confirmation cannot be asked (not interactive); give --yes."
		read -r -p "The store database will be replaced with the backup $(basename "$1") (the current state is backed up first). Type yes to continue: " answer
		[ "$answer" = yes ] || die "cancelled."
	fi
	# The backup is read from the backup directory the container sees.
	dump=$(basename "$1")
	if [ "$(cd "$(dirname "$1")" && pwd)" != "$backups" ]; then
		dump="restore-$(date +%Y%m%d%H%M%S).dump"
		install -o "$user" -g "$user" -m 0600 "$1" "$backups/$dump"
	fi
	as systemctl --user is-active --quiet abarow-store-db.service || die "abarow-store-db is not running."
	as systemctl --user start abarow-store-backup.service || die "could not back up the current state; nothing was restored."
	say "The current state was backed up ($backups)"
	as systemctl --user stop abarow-report.service abarow-inventory.service
	as podman exec abarow-store-db psql -q -U abarow -d postgres -c 'DROP DATABASE abarow WITH (FORCE)' -c 'CREATE DATABASE abarow' >/dev/null
	as podman run --rm --network abarow-store --secret abarow-store-db-password,type=env,target=PGPASSWORD \
		-v "$backups:/b:ro,Z" docker.io/library/postgres:18-alpine \
		pg_restore -h abarow-store-db -U abarow -d abarow --no-owner "/b/$dump" ||
		die "the restore was left half done; the services are off. The backup of the previous state is in $backups (the newest file)."
	say "The database was restored from the backup $dump"
	# shellcheck disable=SC2046 # unit names contain no spaces
	start_units $(unit_names "$quadlet")
	replay
	say "The sales that came after the backup are being reprocessed from the store stream"
	exit 0
fi

if [ "$command" = restore-to ]; then
	if [ "${2:-}" = --yes ]; then
		confirmed=1
		set -- "$1"
	fi
	[ $# -eq 1 ] || die 'usage: store-install.sh restore-to "2026-11-12 14:30:00+03" [--yes]'
	if [ -z "$confirmed" ]; then
		[ -t 0 ] || die "confirmation cannot be asked (not interactive); give --yes."
		read -r -p "The store database will be taken back to $1; what was written after it is lost (the current state is backed up first). Type yes to continue: " answer
		[ "$answer" = yes ] || die "cancelled."
	fi
	restore_to store-install.sh "$store" abarow-store-db abarow-store-db-data abarow-store-backup "$1" \
		abarow-report.service abarow-inventory.service abarow-store-catalog-admin.service
	# shellcheck disable=SC2046 # unit names contain no spaces
	start_units $(unit_names "$quadlet")
	replay
	say "The sales that came after $1 are being reprocessed from the store stream"
	exit 0
fi

case "$command" in
register-add)
	[ $# -ge 2 ] || die "usage: store-install.sh register-add <register-id> <key> [name]"
	"${natsauth[@]}" -registers /config/registers.json -add-register "$1" -register-key "$2" -register-name "${3:-}"
	;;
register-remove)
	[ $# -eq 1 ] || die "usage: store-install.sh register-remove <register-id>"
	"${natsauth[@]}" -registers /config/registers.json -remove-register "$1"
	;;
install)
	# 3. The screen password: only the bcrypt digest is stored.
	if [ -n "$password_file" ] || ! as podman secret exists abarow-report-password; then
		if [ -n "$password_file" ]; then
			password=$(head -n 1 "$password_file")
		else
			[ -t 0 ] || die "the password cannot be asked (not interactive); give --password-file."
			read -r -s -p "Store password of the store server screen (at least 8 characters): " password
			echo
			read -r -s -p "Password (again): " again
			echo
			[ "$password" = "$again" ] || die "the passwords are not the same."
		fi
		hash=$(printf '%s\n' "$password" | as podman run --rm -i "ghcr.io/abarow/abarow-report:$channel" hash-password) ||
			die "could not produce the password digest"
		unset password again
		printf '%s' "$hash" | as podman secret create --replace abarow-report-password - >/dev/null
		say "Screen password set"
	fi
	# The client secret of the corporate sign-in (if any); when it is not given
	# at a repeated install the old one is kept.
	if [ -n "$oidc_secret_file" ]; then
		oidc_secret abarow-store-oidc-secret "$oidc_secret_file"
		say "Corporate sign-in client secret saved"
	fi
	# Database password: generated at random, kept when the install is repeated
	# (the database is created with this password on its first start).
	if ! as test -s "$auth/db-password"; then
		# 30 random bytes, 40 characters that cause no trouble in an address (A-Z a-z 0-9 _ -).
		head -c 30 /dev/urandom | base64 -w0 | tr '/+' '_-' | as tee "$auth/db-password" >/dev/null
		as chmod 0600 "$auth/db-password"
		say "Database password generated"
	fi
	db_password=$(as cat "$auth/db-password")
	secret abarow-store-db-password "$auth/db-password"
	printf 'postgres://abarow:%s@abarow-store-db:5432/abarow?sslmode=disable' "$db_password" | as podman secret create --replace abarow-store-db-url - >/dev/null
	unset db_password
	;;
esac

# 4. NATS configuration and the leaf TLS (regenerated when the register list
# changes too; the keys and the CA are kept).
before=$( (cat "$auth/nats.conf" 2>/dev/null || true) | sha256sum)
args=(-role store -out /auth -conf-dir /etc/nats -users report,inventory,catalog -server-name "$store_id"
	-registers /config/registers.json -leaf-hosts "$hosts")
if [ -n "$headquarters" ]; then
	args+=(-leaf "$headquarters" -leaf-ca /auth/headquarters-ca.pem)
fi
"${natsauth[@]}" "${args[@]}"
secret abarow-store-nats-conf "$auth/nats.conf"
secret abarow-store-tls-cert "$auth/tls/server.pem"
secret abarow-store-tls-key "$auth/tls/server.key"
secret abarow-store-nats-report "$auth/report/nats.nk"
secret abarow-store-nats-inventory "$auth/inventory/nats.nk"
secret abarow-store-nats-catalog "$auth/catalog/nats.nk"
if [ -n "$headquarters" ]; then
	secret abarow-store-leaf-ca "$auth/leaf/ca.pem"
fi
# The CA certificate the registers will use when installed (the public part;
# the private key stays in $auth/tls).
as install -m 0644 "$auth/ca/ca.pem" "$store/ca.pem"
say "NATS configuration is ready"

# 5. Services. They are restarted if the configuration, the register list or the
# unit drop-in files changed.
dropins_before=$(dropin_digest)
install_units "$quadlet"
install_units "$quadlet/backup"
# Update agent (optional): registers with headquarters, offers a proxy for the registers; it does not update by itself.
update_unit=
if [ -n "$update" ]; then
	update_unit=abarow-store-update.service
	if [ "$command" = install ]; then
		update_install "$quadlet/update" abarow-store-update "$update" "$headquarters_ca" "$level" "Store $store_id" "$store_id"
		update_screen abarow-report ABAROW_UPDATE_AGENT_URL=http://abarow-store-update:8080
		if [ -n "$channel_keys" ]; then
			update_release abarow-store-update "$channel_keys" abarow-store-update "ABAROW_UPDATE_LEVEL=$level"
		else
			update_release_remove abarow-store-update
		fi
		# Image mirror (optional): the registers pull images from here, by connecting to the publisher.
		if [ -n "$mirror_root" ]; then
			mirror_install "$quadlet/mirror" store "${hosts%%,*}" "$mirror_root" "$here/deploy/quadlet/mirror/zot.json.template" "$(url_host "$update"):5443" "$headquarters_ca"
			# The store server's agent pulls images from its own mirror (the mirror fetches from headquarters on demand).
			if [ -n "$channel_keys" ]; then
				registry_trust "${hosts%%,*}:5443" "$store/ca.pem"
				agent_registry_config abarow-store-update "${hosts%%,*}:5443"
			fi
		else
			mirror_remove store
			# Without a mirror the agent pulls images directly from headquarters' mirror.
			if [ -n "$channel_keys" ]; then
				registry_trust "$(url_host "$update"):5443" "$headquarters_ca"
				agent_registry_config abarow-store-update "$(url_host "$update"):5443"
			fi
		fi
	fi
elif [ "$command" = install ]; then
	update_remove abarow-store-update
	update_screen abarow-report
	mirror_remove store
fi
# The backup script is the same in both tiers (pg_dump, PG* environment variables).
install -o "$user" -g "$user" -m 0755 "$here/deploy/quadlet/backup/backup.sh" "$store/backup.sh"
as mkdir -p "$home/.config/systemd/user"
install -o "$user" -g "$user" -m 0644 "$quadlet/backup/abarow-store-backup.timer" "$home/.config/systemd/user/"
# Store identity: report writes it to the cashier and price messages, inventory to the stock movements.
report_dropin="$home/.config/containers/systemd/abarow-report.container.d"
inventory_dropin="$home/.config/containers/systemd/abarow-inventory.container.d"
for d in "$report_dropin" "$inventory_dropin"; do
	as mkdir -p "$d"
	as tee "$d/store.conf" >/dev/null <<EOF
# written by store-install.sh: the store identity of the store server (cashiers,
# store prices and stock movements go down to the registers and up to headquarters with this identity).
[Container]
Environment=ABAROW_STORE_ID=$store_id
EOF
done
# Connection to headquarters: NATS also sees the headquarters CA certificate;
# report takes headquarters' catalog into the store for the registers.
dropin="$home/.config/containers/systemd/abarow-store.container.d"
if [ -n "$headquarters" ]; then
	as mkdir -p "$dropin"
	as tee "$dropin/headquarters.conf" >/dev/null <<'EOF'
# written by store-install.sh: leaf connection to headquarters (TLS, the headquarters CA).
[Container]
Secret=abarow-store-leaf-ca,type=mount,target=/etc/nats/leaf/ca.pem
EOF
	for d in "$report_dropin" "$inventory_dropin"; do
		as tee "$d/headquarters.conf" >/dev/null <<EOF
# written by store-install.sh: headquarters' catalog (JetStream domain headquarters)
# is taken into the store for the registers; store prices go down to the registers
# and are reported to headquarters; inventory takes its products from the catalog.
[Container]
Environment=ABAROW_CATALOG_SOURCE=headquarters
EOF
	done
else
	rm -f "$dropin/headquarters.conf" "$report_dropin/headquarters.conf" "$inventory_dropin/headquarters.conf"
fi
# Corporate sign-in (OIDC): only report offers the screen sign-in.
oidc_dropin store-install.sh "$report_dropin/oidc.conf" abarow-store-oidc-secret "$login" "$oidc_issuer" "$oidc_client" "$oidc_url"
# The database settings are computed from the number of registers (report
# pgtune, pkg/pgtune): rewritten when a register is added or removed; if a
# setting that needs a restart changed (shared_buffers, max_connections) the
# database is restarted, the others wait for its next restart (tune_restart). The values in the Quadlet file are the base for up to four registers.
db_dropin="$home/.config/containers/systemd/abarow-store-db.container.d"
# On a spinning disk the I/O settings follow it (disk_rotational).
db_rotational=$(disk_rotational)
rotational_flag=
[ "$db_rotational" = false ] || rotational_flag=--rotational
tune=$(as podman run --rm --network none -v "$config:/config:ro,Z" "ghcr.io/abarow/abarow-report:$channel" pgtune ${rotational_flag:+"$rotational_flag"}) ||
	die "could not compute the database settings (report pgtune)"
db_memory=$(printf '%s\n' "$tune" | sed -n 's/^MEMORY_MB=//p')
db_flags=$(printf '%s\n' "$tune" | sed -n 's/^ARGS=//p')
db_shm=$(printf '%s\n' "$tune" | sed -n 's/^SHM_MB=//p')
[ -n "$db_memory" ] && [ -n "$db_flags" ] || die "could not read the database settings: $tune"
tune_before=$(cat "$db_dropin/tune.conf" 2>/dev/null || true)
as mkdir -p "$db_dropin"
as tee "$db_dropin/tune.conf" >/dev/null <<EOF
# written by store-install.sh: database settings according to the number of
# registers of the store (report pgtune; pkg/pgtune). Rewritten when a register is added or removed.
[Container]
Exec=postgres $db_flags
PodmanArgs=--memory=${db_memory}m --shm-size=${db_shm:-64}m
EOF
tune_after=$(cat "$db_dropin/tune.conf" 2>/dev/null || true)
tune_changed=false
[ "$tune_before" = "$tune_after" ] || tune_changed=true
as systemctl --user daemon-reload
if $tune_changed && as systemctl --user is-active --quiet abarow-store-db.service; then
	if [ "$(tune_restart abarow-store-db "$tune_before" "$tune_after")" = true ]; then
		as systemctl --user restart abarow-store-db.service || true
	else
		say "The database's new settings need no restart; they take effect at its next restart (an update, a reboot)"
	fi
fi
if [ "$before" != "$(sha256sum <"$auth/nats.conf")" ] || [ "$command" != install ] || [ "$dropins_before" != "$(dropin_digest)" ]; then
	for unit in abarow-store abarow-report abarow-inventory abarow-store-catalog-admin; do
		if as systemctl --user is-active --quiet "$unit.service"; then
			as systemctl --user restart "$unit.service" || true
		fi
	done
fi
# shellcheck disable=SC2046,SC2086 # unit names contain no spaces
mirror_units=
[ -z "$mirror_root" ] || [ -z "$update" ] || mirror_units="abarow-store-mirror-auth.service abarow-store-mirror.service"
start_units $(unit_names "$quadlet") $update_unit $mirror_units
if [ "$command" = install ]; then
	as systemctl --user enable --now abarow-store-backup.timer
	# The first backup is taken right away: that the backup works is seen at install time.
	as systemctl --user start abarow-store-backup.service ||
		die "could not take the database backup (journalctl --user -u abarow-store-backup)"
	say "Daily backup set up ($backups; the first backup was taken)"
	# The weekly base backup (restore-to) and the database maintenance.
	weekly_db_jobs_install "$store" "$quadlet/backup" abarow-store
	# Copying the backups off the machine (--backup-target): after every
	# successful backup (the drop-in file of abarow-store-backup).
	if [ -n "$backup_target" ]; then
		backup_copy_install store-install.sh "$store" "$backups" abarow-store-backup "$quadlet/backup/abarow-store-backup-copy.service" "$backup_target"
	else
		backup_copy_remove "$store" abarow-store-backup
	fi
fi

# 6. Summary
memory_check
disk_warning "$db_rotational"
first=${hosts%%,*}
case "$command" in
install)
	say "The store server is running; screen https://$first:8090 (with the store password; once managers are defined, with their own code and PIN)"
	say "So that the browser shows no warning, install the store CA certificate ($store/ca.pem) as a trusted root on the managers' devices."
	case "$login" in
	oidc | local,oidc | oidc,local)
		say "Corporate sign-in is on ($login). The return address to register with the provider: $oidc_url/api/auth/oidc/callback"
		say "The manager's email address is given in the Cashiers section; if there is no local sign-in: sudo deploy/install/store-install.sh email <manager-code> <address>"
		oidc_warning "$login"
		;;
	esac
	say "To connect the registers, copy the CA certificate to the register: $store/ca.pem"
	say "The digest of the CA certificate (it must be the same at the register): $(sha256sum <"$store/ca.pem" | cut -d' ' -f1)"
	say "At the register (from the repository root):"
	printf '\n    sudo deploy/install/register-install.sh --seed /path/seed --register register-1 --store-id %s --store-url tls://%s:7421 --store-ca ca.pem\n\n' "$store_id" "$first"
	say "Cashiers are managed in the \"Cashiers\" section of the store server screen; to load the registers' cashier file:"
	printf '\n    sudo deploy/install/store-install.sh cashiers-load /path/seed/cashiers.json\n\n'
	if [ -n "$headquarters" ]; then
		key=$("${natsauth[@]}" -out /auth -show-leaf-key | tr -d '\r' | tail -n 1)
		say "Add this store to headquarters (on headquarters, from the repository root; headquarters on three machines: on all three):"
		printf '\n    sudo deploy/install/headquarters-install.sh store-add %s %s\n\n' "$store_id" "$key"
		say "Until the store is added to headquarters it keeps working; sales accumulate at the store server."
	fi
	;;
register-add) say "$1 was added; the register connects within a few seconds" ;;
register-remove) say "$1 was removed; the register can no longer connect" ;;
esac
