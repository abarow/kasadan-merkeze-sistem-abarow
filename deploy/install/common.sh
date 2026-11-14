# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0
# shellcheck shell=bash

# common.sh holds the functions shared by the install scripts; register-install.sh,
# store-install.sh and headquarters-install.sh source it. It is not run on its own.
#
# The calling script sets these variables: channel (image channel, e.g. 0.1);
# optionally ghcr_user and ghcr_token_file (--ghcr-user, --ghcr-token-file).
# This file sets: user, uid, home.

user=abarow

say() { printf '==> %s\n' "$*"; }
die() {
	printf 'ERROR: %s\n' "$*" >&2
	exit 1
}

# valid_identity checks a register or store identity (it goes into NATS subjects:
# letters, digits, - and _; at most 63 characters).
valid_identity() {
	printf '%s' "$1" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$'
}

# preflight: root, Podman 5 (for Notify=healthy in Quadlet), aardvark-dns
# (containers finding each other by name) and nftables (the Podman network).
preflight() {
	[ "$(id -u)" -eq 0 ] || die "run as root (sudo)."
	command -v podman >/dev/null || die "podman is not installed."
	local version major found=
	version=$(podman version --format '{{.Client.Version}}')
	major=${version%%.*}
	[ "$major" -ge 5 ] || die "Podman 5 is required (installed: $version)."
	say "Podman $version"
	for d in /usr/libexec/podman /usr/lib/podman; do
		[ -x "$d/aardvark-dns" ] && found=1
	done
	[ -n "$found" ] || die "aardvark-dns is not installed: containers cannot find each other by name (apt install aardvark-dns)."
	command -v nft >/dev/null || die "nftables is not installed: the Podman network cannot be set up (apt install nftables)."
}

# ensure_user creates the abarow user (if missing), enables linger (so the
# services run without a login session) and waits for the user's systemd.
ensure_user() {
	if ! id "$user" >/dev/null 2>&1; then
		useradd --create-home --shell /bin/bash "$user"
		say "created user $user"
	fi
	loginctl enable-linger "$user"
	uid=$(id -u "$user")
	home=$(getent passwd "$user" | cut -d: -f6)
	for _ in $(seq 1 30); do
		[ -S "/run/user/$uid/bus" ] && break
		sleep 1
	done
	[ -S "/run/user/$uid/bus" ] || die "the systemd session of user $user did not start."
}

# as runs a command as the abarow user, with that user's systemd session.
as() {
	runuser -u "$user" -- env XDG_RUNTIME_DIR="/run/user/$uid" DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$uid/bus" "$@"
}

# install_units <directory>: copies the Quadlet files (.container, .network) in
# the directory to the user; if the channel is not 0.1, image tags are changed
# to the channel. Subdirectories and other files (e.g. .timer) are not copied.
install_units() {
	local dir=$1 target="$home/.config/containers/systemd" f
	as mkdir -p "$target"
	for f in "$dir"/abarow*.container "$dir"/abarow*.network; do
		[ -e "$f" ] || continue
		install -o "$user" -g "$user" -m 0644 "$f" "$target/"
		# shellcheck disable=SC2154 # the calling script sets channel
		if [ "$channel" != 0.1 ]; then
			sed -i "s|^\(Image=ghcr.io/abarow/abarow-[a-z-]*\):0\.1$|\1:$channel|" "$target/$(basename "$f")"
		fi
	done
}

# secret <name> <file>: creates or refreshes a Podman secret from a file.
secret() {
	as podman secret create --replace "$1" "$2" >/dev/null
}

# unit_names <directory>: the service names of the container units in the directory.
unit_names() {
	local f units=
	for f in "$1"/abarow-*.container; do
		units="$units $(basename "$f" .container).service"
	done
	printf '%s' "$units"
}

# start_units <unit>...: starts the units and checks that each one is really
# running. Dependencies are linked with Wants=: even if one service cannot start
# the others do; the one that does not start is reported separately.
start_units() {
	as systemctl --user daemon-reload
	# The "restarted too often" lock of earlier failed attempts is cleared.
	as systemctl --user reset-failed "$@" 2>/dev/null || true
	as systemctl --user start "$@" || true
	local failed='' unit
	for unit in "$@"; do
		if ! as systemctl --user is-active --quiet "$unit"; then
			failed="$failed $unit"
			printf '  %s is not running: %s\n' "$unit" "$(as journalctl --user -u "$unit" -n 30 --no-pager -o cat | grep -m1 -E '"level":"ERROR"|Error' || echo "see journalctl --user -u $unit for details")"
		fi
	done
	[ -z "$failed" ] || die "services that did not start:$failed"
}

# dropin_digest: a digest of the user's unit drop-in files. If the install
# changed them, the related services are restarted: a running service does not
# pick up new settings without restarting (start_units only starts stopped ones).
dropin_digest() {
	(cat "$home"/.config/containers/systemd/*.d/*.conf 2>/dev/null || true) | sha256sum
}

# update_validate <level> <url> <ca-file>: checks the update agent options.
update_validate() {
	case "$1" in
	test | pilot | prod) ;;
	*) die "--level must be test, pilot or prod (default prod)." ;;
	esac
	case "$2" in
	https://*) ;;
	*) die "--update-url must start with https:// (e.g. https://192.168.1.10:8443); the connection is always TLS." ;;
	esac
	[ -f "$3" ] || die "$3 does not exist (the CA certificate of the update server)."
}

# update_install <unit-dir> <unit-name> <url> <ca-file> <level> <name> [store] [register]:
# installs the update agent: copies the unit, writes the server's CA and the
# settings specific to this machine (url, level, name, store, register) to the
# unit's drop-in file. The agent does not update anything by itself; it only
# registers and gets a token.
update_install() {
	local dir=$1 unit=$2 url=$3 ca=$4 level=$5 name=$6 store=${7:-} register=${8:-}
	local data="$home/abarow/update" dropin="$home/.config/containers/systemd/$2.container.d"
	# The name and identities are written in quotes in the unit file; characters that could break the unit are refused.
	printf '%s' "$name" | grep -Eq '^[^"\$%[:cntrl:]]{0,64}$' || die "the name (\"$name\") must be at most 64 characters and must not contain quotes, backslash, \$ or %."
	install_units "$dir"
	as mkdir -p "$data" "$dropin"
	# Approval secret: shared by the screen services and the agent (it must exist even if the agent gets no release authority).
	if ! as podman secret exists abarow-update-secret 2>/dev/null; then
		head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' | as podman secret create abarow-update-secret - >/dev/null
	fi
	as tee "$data/ca.pem" <"$ca" >/dev/null
	as chmod 0644 "$data/ca.pem"
	as tee "$dropin/update.conf" >/dev/null <<EOF
# written by the install script: update settings specific to this machine.
[Container]
Environment="ABAROW_UPDATE_SERVER_URL=$url"
Environment="ABAROW_UPDATE_LEVEL=$level"
Environment="ABAROW_UPDATE_NAME=$name"
Environment="ABAROW_STORE_ID=$store"
Environment="ABAROW_REGISTER_ID=$register"
EOF
	# Hardware fingerprint: machine-id is always bound. The DMI serial numbers
	# (board, product) cannot be read by a rootless container if only root can
	# read them; if they are world-readable they are added to the fingerprint.
	if [ -r /sys/class/dmi/id/board_serial ] && [ $((0$(stat -c %a /sys/class/dmi/id/board_serial) & 4)) -ne 0 ]; then
		printf '# DMI serial numbers are readable: added to the fingerprint.\nVolume=/sys/class/dmi/id:/host/sys/class/dmi/id:ro\n' |
			as tee "$dropin/dmi.conf" >/dev/null
	else
		as rm -f "$dropin/dmi.conf"
		say "WARNING: the DMI serial numbers are not readable by the rootless container; the device fingerprint relies on machine-id only (a full disk copy would go unnoticed)."
	fi
}

# mirror_validate <file>: checks the publisher's root public key (43 base64url characters).
mirror_validate() {
	[ -f "$1" ] || die "$1 does not exist (the publisher's root public key)."
	grep -Eq '^[A-Za-z0-9_-]{43}$' "$1" || die "$1 is not the publisher's root public key (43 base64url characters expected; the publisher's server prints it with generate-key)."
}

# url_host <https://host:port>: the host name of a URL.
url_host() {
	local h=${1#*://}
	h=${h%%/*}
	printf '%s' "${h%%:*}"
}

# mirror_install <unit-dir> <tier> <address> <root-file> <zot-template> <upstream-mirror> <upstream-ca>:
# installs the image mirror: Zot (the registry) and the auth service. Devices in
# the lower tier pull images only by connecting to the publisher (with the
# abarow token). The address is the one clients use to reach the auth service
# (bearer.realm in zot.json). The upstream mirror (address:port) is the upper
# tier Zot pulls requested images from on demand; the upstream CA verifies its
# certificate. The agent writes the token needed to reach the upper tier
# (identity.json; see services/update/internal/feed).
mirror_install() {
	local dir=$1 tier=$2 address=$3 root=$4 template=$5 upstream=$6 upstream_ca=$7
	local data="$home/abarow/mirror" dropin="$home/.config/containers/systemd/abarow-$2-mirror-auth.container.d"
	local agent_dropin="$home/.config/containers/systemd/abarow-$2-update.container.d"
	install_units "$dir"
	as mkdir -p "$data/ca" "$dropin" "$agent_dropin"
	sed "s|@ADDRESS@|$address|; s|@UPSTREAM@|$upstream|" "$template" | as tee "$data/zot.json" >/dev/null
	as chmod 0644 "$data/zot.json"
	as tee "$data/ca/ca.crt" <"$upstream_ca" >/dev/null
	# The identity file must exist when Zot starts; the agent fills it when it gets a token.
	[ -s "$data/identity.json" ] || printf '{}\n' | as tee "$data/identity.json" >/dev/null
	as chmod 0666 "$data/identity.json" "$data/zot.json"
	printf '# written by the install script: the publisher root public key.\n[Container]\nEnvironment="ABAROW_UPDATE_ROOT_PUB=%s"\n' "$(cat "$root")" |
		as tee "$dropin/mirror-root.conf" >/dev/null
	# When the agent renews its token it writes the identity file that connects the local mirror to the upper tier.
	printf '# written by the install script: token feed for the local mirror.\n[Container]\nEnvironment="ABAROW_UPDATE_MIRROR_DIR=/mirror"\nEnvironment="ABAROW_UPDATE_MIRROR_UPSTREAM=%s"\nVolume=%%h/abarow/mirror:/mirror:Z\n' "$upstream" |
		as tee "$agent_dropin/mirror-feed.conf" >/dev/null
}

# registry_trust <address:port> <ca-file>: writes the CA into the abarow user's
# certs.d directory so that Podman trusts this registry's certificate.
registry_trust() {
	local d="$home/.config/containers/certs.d/$1"
	as mkdir -p "$d"
	as tee "$d/ca.crt" <"$2" >/dev/null
}

# agent_registry_config <unit-name> <address:port>: writes where the agent pulls
# images from (its own mirror or the upper tier's mirror); the channel prefix is in the mirror path.
agent_registry_config() {
	local dropin="$home/.config/containers/systemd/$1.container.d"
	as mkdir -p "$dropin"
	printf '# written by the install script: images are pulled from this mirror.\n[Container]\nEnvironment="ABAROW_UPDATE_REGISTRY=%s"\n' "$2" |
		as tee "$dropin/registry.conf" >/dev/null
}

# mirror_remove <tier>: stops and removes a previously installed image mirror (the images stay).
mirror_remove() {
	local target="$home/.config/containers/systemd" b
	for b in abarow-$1-mirror abarow-$1-mirror-auth; do
		if [ -f "$target/$b.container" ]; then
			as systemctl --user stop "$b.service" 2>/dev/null || true
			rm -f "$target/$b.container"
			rm -rf "$target/$b.container.d"
		fi
	done
	rm -f "$target/abarow-$1-update.container.d/mirror-feed.conf"
}

# update_release_validate <file>: checks that the trust file (the channel keys)
# is JSON and carries a build, pilot or prod key.
update_release_validate() {
	[ -f "$1" ] || die "$1 does not exist (the publisher's channel keys file)."
	# A key can be a plain string, a {"key", "not_before", "not_after"} object or an array
	# of them (key rotation, docs/UPDATE.md); the agent validates fully when it starts.
	grep -Eq '"(build|pilot|prod)"[[:space:]]*:' "$1" && grep -Eq '"[A-Za-z0-9_-]{43}"' "$1" ||
		die "$1 is not a channel keys file (JSON: {\"prod\":[\"<43-character base64url public key>\"], ...}; see docs/UPDATE.md \"Key rotation\")."
}

# update_release <unit-name> <trust-file> <agent-own-unit> [VARIABLE=value ...]:
# gives the agent the authority to apply new releases: writes the channel keys,
# creates the approval secret (shared with the screen services) and mounts the
# rootless Podman socket, the user D-Bus socket and the Quadlet directory into
# the agent. This is the authority to manage all containers and units of the
# abarow user (see docs/UPDATE.md "Authority"); the agent only touches units
# with the abarow- prefix.
update_release() {
	local unit=$1 trust=$2 own=$3 data="$home/abarow/update" dropin="$home/.config/containers/systemd/$1.container.d" v
	shift 3
	as mkdir -p "$data/trust" "$dropin" "$home/.config/containers"
	# The directory, not the file, is mounted into the container: even if the trust
	# file is replaced by renaming (mv) during key rotation, the agent sees the new file.
	as tee "$data/trust/trust.json" <"$trust" >/dev/null
	as chmod 0644 "$data/trust/trust.json"
	if ! as podman secret exists abarow-update-secret 2>/dev/null; then
		head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' | as podman secret create abarow-update-secret - >/dev/null
	fi
	{
		printf '# written by the install script: the agent'"'"'s authority to apply new releases.\n[Container]\n'
		printf 'Environment="ABAROW_UPDATE_RUNTIME=podman"\nEnvironment="ABAROW_UPDATE_TRUST_FILE=/etc/abarow/trust.json"\nEnvironment="ABAROW_UPDATE_OWN_UNIT=%s"\n' "$own"
		for v in "$@"; do printf 'Environment="%s"\n' "$v"; done
		# The sockets are in the user's runtime directory (%t); in the rootless user
		# namespace User=0 is the abarow user on the host (not real root).
		printf 'Volume=%%t/podman/podman.sock:/run/podman/podman.sock\nVolume=%%t/bus:/run/dbus/bus\n'
		printf 'Volume=%%h/.config/containers/systemd:/units\nVolume=%%h/abarow/update/trust:/etc/abarow:ro,Z\n'
		printf 'Secret=abarow-update-secret,type=env,target=ABAROW_UPDATE_APPROVE_SECRET\nUser=0\nSecurityLabelDisable=true\n'
		# If the trust file has cosign keys, the agent writes the image signature policy (policy.json) for Podman.
		if grep -q '"cosign"' "$trust"; then
			printf 'Environment="ABAROW_UPDATE_POLICY_DIR=/containers"\nVolume=%%h/.config/containers:/containers\n'
		fi
	} | as tee "$dropin/release.conf" >/dev/null
}

# update_release_remove <unit-name>: removes the agent's authority to apply new releases.
update_release_remove() {
	as rm -f "$home/.config/containers/systemd/$1.container.d/release.conf"
}

# update_screen <screen-unit> [VARIABLE=value ...]: writes the connection of a
# screen service (gateway, report, reporting) to the update agent; with no
# variables (update not installed) it removes the connection. With these
# addresses the screen service shows the status and the approval code; without
# an address it says "not installed".
update_screen() {
	local unit=$1 dropin="$home/.config/containers/systemd/$1.container.d" v
	shift
	if [ $# -eq 0 ]; then
		as rm -f "$dropin/update-screen.conf"
		return 0
	fi
	as mkdir -p "$dropin"
	{
		printf '# written by the install script: the screen connection of the update agent.\n[Container]\n'
		for v in "$@"; do printf 'Environment="%s"\n' "$v"; done
		# The approval of a new release reaches the agent with this secret (created at install).
		printf 'Secret=abarow-update-secret,type=env,target=ABAROW_UPDATE_APPROVE_SECRET\n'
	} | as tee "$dropin/update-screen.conf" >/dev/null
}

# update_remove <unit-name>: stops and removes a previously installed update agent.
update_remove() {
	local target="$home/.config/containers/systemd"
	if [ -f "$target/$1.container" ]; then
		as systemctl --user stop "$1.service" 2>/dev/null || true
		rm -f "$target/$1.container"
		rm -rf "$target/$1.container.d"
	fi
}

# ghcr_login: images are pulled from ghcr.io; if the repository is private a
# login is needed (a GitHub user name and a token with read:packages). The login
# is written to the abarow user's persistent auth file: the install, the start
# of the services and a manual update (podman pull) read it from there (the file
# in $XDG_RUNTIME_DIR is deleted when the machine shuts down). Then it checks
# that the first image of the install can be pulled; if not, it says what to do.
ghcr_login() {
	local authfile="$home/.config/containers/auth.json" image="ghcr.io/abarow/abarow-natsauth:$channel"
	if [ -n "${ghcr_token_file:-}" ]; then
		[ -f "$ghcr_token_file" ] || die "$ghcr_token_file does not exist."
		[ -n "${ghcr_user:-}" ] || die "--ghcr-token-file also needs the GitHub user name (--ghcr-user)."
		as mkdir -p "$home/.config/containers"
		head -n 1 "$ghcr_token_file" | as podman login --authfile "$authfile" --username "$ghcr_user" --password-stdin ghcr.io >/dev/null ||
			die "login to ghcr.io failed: check the user name and the token (the token needs read:packages)."
		as chmod 0600 "$authfile"
		say "ghcr.io login saved ($authfile)"
	fi
	as podman image exists "$image" || as podman pull -q "$image" >/dev/null ||
		die "could not pull $image. While the repository is private a GHCR login is needed: --ghcr-user <GitHub user name> --ghcr-token-file <file with a read:packages token> (see deploy/quadlet/README.md)."
}

# --- Copying backups off the machine (--backup-target) ---------------------
# The store (store-install.sh) and headquarters (headquarters-install.sh) use the
# same functions; what is tier specific is the directory, the name of the backup
# service and the command name.

# backup_target_validate <target>: checks the --backup-target value and the
# tools it needs (rsync, and ssh for an SSH target).
backup_target_validate() {
	printf '%s' "$1" | grep -Eq '^(/[^[:space:]]+|[A-Za-z0-9._-]+@[A-Za-z0-9.-]+(:[^[:space:]]+)?|[A-Za-z0-9.-]+:[^[:space:]]+)$' ||
		die "--backup-target must be a path (external disk, NAS: /mnt/backup/abarow), user@server or user@server:/path."
	command -v rsync >/dev/null || die "rsync is needed to copy the backups (apt install rsync)."
	case "$1" in
	/*) ;;
	*) command -v ssh >/dev/null || die "ssh is needed to copy the backups to a server (apt install openssh-client)." ;;
	esac
}

# target_writable <target>: lets the abarow user write to a local target
# (external disk); on a newly attached disk the directory may belong to root.
target_writable() {
	case "$1" in
	/*)
		as test -w "$1" || chown "$user:$user" "$1" 2>/dev/null || true
		as test -w "$1" || die "the abarow user cannot write to $1."
		;;
	esac
}

# backup_copy <command> <tier-directory> <backup-directory> <target>: copies
# the backups to the target now; if that fails it says why and how to authorize
# the key on a server target. <command> is the name of the install script
# (headquarters-install.sh, store-install.sh).
backup_copy() {
	local cmd=$1 dir=$2 backups=$3 target=$4
	if as env ABAROW_BACKUP_TARGET="$target" "$dir/backup-copy.sh" "$backups"; then
		return 0
	fi
	printf 'WARNING: the backups could not be copied to %s (see the error above). Backups continue to be taken on this machine.\n' "$target" >&2
	case "$target" in
	/*) say "Once the disk is attached and mounted, the missing copies go at the next backup (or: sudo deploy/install/$cmd backup-copy)." ;;
	*)
		# With a key restricted by rrsync the server decides the directory; the
		# client's path is interpreted relative to it, so the target is given without a path.
		local key server=${target%%:*} sdir=/srv/abarow-backup
		key=$(cat "$home/.ssh/abarow-backup.pub")
		[ "$server" = "$target" ] || sdir=${target#*:}
		say "On the server, add this machine's key to the ~/.ssh/authorized_keys file of the user who does the copying."
		say "Recommended (on a Linux server with rsync 3.3+ and python3; rrsync is in the rsync package): the key can only write to the directory $sdir; it cannot read, delete or overwrite. The server decides the directory; the target is given without a path (--backup-target $server):"
		printf '\n    command="rrsync -wo -no-del -no-overwrite %s",restrict %s\n\n' "$sdir" "$key"
		say "On a server without rrsync (e.g. a NAS) the target is given with its path (--backup-target $server:/path) and the key is added unrestricted:"
		printf '\n    restrict %s\n\n' "$key"
		say "Then try: sudo deploy/install/$cmd backup-copy"
		;;
	esac
	return 1
}

# backup_copy_install <command> <tier-directory> <backup-directory> <backup-service> <copy-service-file> <target>:
# sets up copying the backups off the machine after every successful backup (the
# backup service's drop-in file) and tries the first copy. The backup service is
# abarow-backup or abarow-store-backup.
backup_copy_install() {
	local cmd=$1 dir=$2 backups=$3 unit=$4 copy_unit=$5 target=$6
	local copy_key="$home/.ssh/abarow-backup" dropin="$home/.config/containers/systemd/$unit.container.d/copy.conf"
	install -o "$user" -g "$user" -m 0755 "$here/deploy/quadlet/backup/backup-copy.sh" "$dir/backup-copy.sh"
	install -o "$user" -g "$user" -m 0644 "$copy_unit" "$home/.config/systemd/user/"
	printf 'ABAROW_BACKUP_TARGET=%s\n' "$target" | as tee "$dir/backup-target.env" >/dev/null
	as mkdir -p "$(dirname "$dropin")"
	printf '# %s --backup-target: after every successful backup the backups are copied off the machine.\n[Unit]\nOnSuccess=%s-copy.service\n' "$cmd" "$unit" |
		as tee "$dropin" >/dev/null
	case "$target" in
	/*)
		[ -d "$target" ] || die "--backup-target $target does not exist: attach the external disk, mount it and create the directory."
		target_writable "$target"
		;;
	*)
		if [ ! -f "$copy_key" ]; then
			as mkdir -p "$home/.ssh"
			as chmod 0700 "$home/.ssh"
			as ssh-keygen -q -t ed25519 -N '' -C "abarow-backup@$(hostname)" -f "$copy_key"
			say "SSH key generated to copy the backups ($copy_key)"
		fi
		;;
	esac
	as systemctl --user daemon-reload
	backup_copy "$cmd" "$dir" "$backups" "$target" || true
}

# backup_copy_remove <tier-directory> <backup-service>: when the install is
# repeated without --backup-target, removes the old copy settings.
backup_copy_remove() {
	local dir=$1 unit=$2 dropin="$home/.config/containers/systemd/$2.container.d/copy.conf"
	[ -f "$dropin" ] || return 0
	rm -f "$dropin" "$home/.config/systemd/user/$unit-copy.service" "$dir/backup-target.env"
	as systemctl --user daemon-reload
	say "Copying the backups off the machine is turned off (no --backup-target given)."
}

# backup_copy_command <command> <tier-directory> <backup-directory> <target>:
# the "backup-copy" command: copies the backups right away.
backup_copy_command() {
	local cmd=$1 dir=$2 backups=$3 target=$4
	[ -n "$target" ] || die "there is no off-machine backup target: --backup-target must be given when installing with $cmd."
	[ -d "$target" ] || [ "${target#/}" = "$target" ] || die "$target does not exist: attach the external disk, mount it and create the directory."
	target_writable "$target"
	backup_copy "$cmd" "$dir" "$backups" "$target" || return 1
}

# weekly_db_jobs_install <tier-directory> <unit-directory> <prefix>: installs the
# weekly base backup (basebackup.sh, <prefix>-basebackup) with the restore script and
# the weekly database maintenance (dbmaint.sh, <prefix>-dbmaint). At the first install
# the first base backup is taken right away, so a restore to a moment works from the
# install day on.
weekly_db_jobs_install() {
	# db: the database container the jobs run in (on headquarters on three machines
	# abarow-patroni, every machine's own database); start=false: the first base backup is not
	# taken now (the database is not open yet).
	local dir=$1 units=$2 prefix=$3 db=${4:-} start=${5:-true} job
	install -o "$user" -g "$user" -m 0755 "$here/deploy/quadlet/backup/basebackup.sh" "$dir/basebackup.sh"
	install -o "$user" -g "$user" -m 0755 "$here/deploy/quadlet/backup/dbmaint.sh" "$dir/dbmaint.sh"
	install -o "$user" -g "$user" -m 0644 "$here/deploy/quadlet/backup/pitr-restore.sh" "$dir/pitr-restore.sh"
	for job in basebackup dbmaint; do
		install -o "$user" -g "$user" -m 0644 "$units/$prefix-$job.service" "$units/$prefix-$job.timer" "$home/.config/systemd/user/"
		# Only the container name (abarow-db followed by a space, a dot or the line's end), not
		# abarow-dbmaint.
		[ -z "$db" ] || sed -i "s/$prefix-db\([ .]\|$\)/$db\1/g" "$home/.config/systemd/user/$prefix-$job.service"
	done
	as systemctl --user daemon-reload
	as systemctl --user enable --now "$prefix-basebackup.timer" "$prefix-dbmaint.timer"
	if [ "$start" = true ] && ! ls "$dir"/backups/base-*.tar.gz >/dev/null 2>&1; then
		as systemctl --user start "$prefix-basebackup.service" ||
			die "could not take the first base backup (journalctl --user -u $prefix-basebackup)"
		say "Weekly base backup and maintenance set up (the first base backup was taken; the WAL archive is on)"
	fi
}

# restore_to <command> <tier-directory> <database-container> <data-volume> <backup-unit> <moment> <unit>...:
# the "restore-to" command. Restores the database to the moment from the newest base
# backup taken before it and the WAL archive (pitr-restore.sh). The current state is
# backed up first (pg_dump) and its data directory is kept in the volume
# (before-restore-<time>). The units are the services that use the database; they are
# stopped and the caller starts them again.
restore_to() {
	local cmd=$1 dir=$2 db=$3 volume=$4 backup_unit=$5 moment=$6 base='' f stamp
	shift 6
	stamp=$(date -d "$moment" +%Y-%m-%d-%H%M%S 2>/dev/null) ||
		die "$cmd restore-to: the moment \"$moment\" is not a time (example: \"2026-11-12 14:30:00+03\")"
	[ "$(date -d "$moment" +%s)" -le "$(date +%s)" ] || die "$cmd restore-to: $moment is in the future."
	for f in "$dir"/backups/base-*.tar.gz; do
		[ -e "$f" ] || continue
		# The names are the local time the backup started; sort order is time order.
		[ "$(basename "$f" .tar.gz)" \< "base-$stamp" ] && base=$(basename "$f")
	done
	[ -n "$base" ] || die "$cmd restore-to: there is no base backup before $moment (the oldest kept one is the limit; ls $dir/backups)."
	as systemctl --user is-active --quiet "$db.service" || die "$db is not running."
	as systemctl --user start "$backup_unit.service" || die "could not back up the current state; nothing was restored."
	say "The current state was backed up ($dir/backups)"
	as systemctl --user stop "$@" "$db.service"
	as podman run --rm -v "$volume:/var/lib/postgresql" -v "$dir/backups:/backup:ro,Z" -v "$dir/pitr-restore.sh:/pitr-restore.sh:ro,Z" \
		docker.io/library/postgres:18-alpine sh /pitr-restore.sh "/backup/$base" "$moment" ||
		die "the restore could not be prepared; the services are off. Start them again: systemctl --user start $db.service"
	as systemctl --user start "$db.service" || die "$db did not start after the restore (journalctl --user -u $db)"
	# The archive is replayed up to the moment; then the database opens for writing.
	local i open=false
	for i in $(seq 3600); do
		if [ "$(as podman exec "$db" psql -U abarow -d abarow -tAc 'SELECT pg_is_in_recovery()' 2>/dev/null)" = f ]; then
			open=true
			break
		fi
		sleep 1
	done
	$open || die "$db is still replaying the archive after an hour (journalctl --user -u $db)"
	as podman exec "$db" psql -q -U abarow -d abarow -c 'ALTER SYSTEM RESET restore_command' -c 'ALTER SYSTEM RESET recovery_target_action' \
		-c 'ALTER SYSTEM RESET recovery_target_time' -c 'SELECT pg_reload_conf()' >/dev/null
	say "The database was restored to $moment (from $base). The previous data directory is kept in the $volume volume (before-restore-*); remove it once you have checked the restore."
	# A base backup of the restored state (a new timeline) is the next restore's start.
	as systemctl --user start "${backup_unit%-backup}-basebackup.service" ||
		say "WARNING: the base backup after the restore could not be taken (journalctl --user -u ${backup_unit%-backup}-basebackup)"
}

# oidc_url_validate <option> <url>: checks that the corporate sign-in (OIDC)
# address is HTTPS (for local development HTTP is allowed only on localhost).
# The address is written to the unit drop-in file, so it cannot contain spaces, quotes, \ or %.
oidc_url_validate() {
	printf '%s' "$2" | grep -Eq '^(https://[^/[:space:]"\\%]+|http://(localhost|127\.0\.0\.1)(:[0-9]+)?)(/[^[:space:]"\\%?#]*)?$' ||
		die "$1 must be an HTTPS address (e.g. https://idp.example.com/realms/example)."
}

# oidc_validate <login> <issuer> <client> <return-url>: checks the --login and
# --oidc-* options. An empty login and "local" are the service's own sign-in
# only. The return address is the address the screen is reached at from outside.
oidc_validate() {
	local login=$1 issuer=$2 client=$3 url=$4
	case "$login" in
	"" | local)
		[ -z "$issuer$client" ] || die "the --oidc-* options are only given with --login oidc or local,oidc."
		return 0
		;;
	oidc | local,oidc | oidc,local) ;;
	*) die "--login must be local, oidc or local,oidc." ;;
	esac
	[ -n "$issuer" ] || die "--oidc-issuer (the address of the identity provider) is needed for --login ${login}."
	[ -n "$client" ] || die "--oidc-client (the client identity registered with the provider) is needed for --login ${login}."
	oidc_url_validate --oidc-issuer "$issuer"
	printf '%s' "$client" | grep -Eq '^[A-Za-z0-9._@:-]+$' || die "--oidc-client is invalid: it can contain letters, digits and . _ @ : -"
	oidc_url_validate "the external address of the screen" "$url"
}

# oidc_dropin <script> <file> <secret-name> <login> <issuer> <client> <url>:
# writes the corporate sign-in (OIDC) to the service's unit drop-in file. If the
# login is local it deletes the file. The return address is built by appending
# /api/auth/oidc/callback to the external address of the screen; this address
# must be registered with the provider.
oidc_dropin() {
	local script=$1 file=$2 secret_name=$3 login=$4 issuer=$5 client=$6 url=${7%/}
	case "$login" in
	"" | local)
		rm -f "$file"
		return 0
		;;
	esac
	as mkdir -p "$(dirname "$file")"
	{
		printf '# written by %s: corporate sign-in (OIDC). The provider proves the identity; the role and\n' "$script"
		printf '# the store authority are read from the abarow user record (docs/decisions/LOGIN.md).\n'
		printf '[Container]\nEnvironment=ABAROW_LOGIN=%s\n' "$login"
		printf 'Environment=ABAROW_OIDC_ISSUER=%s\n' "$issuer"
		printf 'Environment=ABAROW_OIDC_CLIENT_ID=%s\n' "$client"
		printf 'Environment=ABAROW_OIDC_REDIRECT_URL=%s/api/auth/oidc/callback\n' "$url"
		if as podman secret exists "$secret_name"; then
			printf 'Secret=%s,type=mount,target=/run/secrets/oidc-secret,uid=65532,mode=0400\n' "$secret_name"
			printf 'Environment=ABAROW_OIDC_CLIENT_SECRET_FILE=/run/secrets/oidc-secret\n'
		fi
	} | as tee "$file" >/dev/null
}

# oidc_secret <secret-name> <file>: makes the client secret a Podman secret from
# a file (root reads the file; the trailing newline is dropped).
oidc_secret() {
	[ -f "$2" ] || die "$2 does not exist."
	tr -d '\r\n' <"$2" | as podman secret create --replace "$1" - >/dev/null
}

# oidc_warning <login>: warns only if the corporate sign-in is the only one.
oidc_warning() {
	case "$1" in
	oidc) say "WARNING: only the corporate sign-in is on; if the identity provider cannot be reached nobody can sign in to the screen (sales are not affected). To sign in during a provider outage too, use --login local,oidc." ;;
	esac
}

# The memory the install counts besides the containers (docs/DESIGN.md, the register's RAM
# budget): the operating system (Debian minimal with systemd, conmon per container) and, if
# the kiosk is installed, cage or sway with Chromium. memory_meminfo and memory_kiosk_unit
# are variables so that the test can give its own.
memory_os_mb=256
memory_kiosk_mb=512
memory_meminfo=/proc/meminfo
memory_kiosk_unit=/etc/systemd/system/abarow-kiosk.service

# memory_ceilings: the sum of the memory ceilings (--memory) of the abarow containers
# installed for the user, in MB. Every tier on the machine is counted (a register and a
# store server on one machine have one user). A drop-in file adds its PodmanArgs after the
# unit's and Podman takes the last --memory, so per unit the last one counts.
memory_ceilings() {
	local f m total=0
	for f in "$home"/.config/containers/systemd/abarow*.container; do
		[ -e "$f" ] || continue
		m=$(cat "$f" "$f.d"/*.conf 2>/dev/null | sed -n 's/^PodmanArgs=.*--memory=\([0-9][0-9]*\)m.*/\1/p' | tail -n 1)
		total=$((total + ${m:-0}))
	done
	printf '%s' "$total"
}

# memory_check: compares the machine's memory (MemTotal) with the containers' memory
# ceilings, the operating system's share and the kiosk. If it does not fit it warns but
# does not stop: the ceilings are upper limits and in use the services take much less
# (docs/HARDWARE.md, minimum and optimum), but under load the kernel may stop a process
# (OOM) instead of a container reaching its own ceiling.
memory_check() {
	local have ceilings need kiosk=0 with=''
	have=$(sed -n 's/^MemTotal: *\([0-9][0-9]*\) kB$/\1/p' "$memory_meminfo")
	[ -n "$have" ] || {
		say "WARNING: the machine's memory could not be read ($memory_meminfo); the memory check was skipped."
		return 0
	}
	have=$((have / 1024))
	ceilings=$(memory_ceilings)
	if [ -e "$memory_kiosk_unit" ]; then
		kiosk=$memory_kiosk_mb
		with=" and the kiosk ($kiosk MB)"
	fi
	need=$((ceilings + memory_os_mb + kiosk))
	if [ "$need" -gt "$have" ]; then
		say "WARNING: the machine has $have MB of memory; the containers' ceilings total $ceilings MB, with the operating system ($memory_os_mb MB)$with $need MB. Under load the kernel may stop a service (OOM); see docs/HARDWARE.md for the memory a machine needs."
	else
		say "Memory: $have MB; the containers' ceilings ($ceilings MB), the operating system$with need $need MB at most"
	fi
}

# disk_rotational: prints true if the user's containers (and the database volume with them)
# are on a spinning disk, false otherwise; pgtune is given --rotational then (pkg/pgtune,
# docs/decisions/DB-TUNING.md). In a virtual machine the virtual disk often says "rotational"
# whatever is under it (KVM's virtio does by default), so there the SSD settings are kept, as
# when the disk cannot be read. disk_virt is a variable so that the test can give its own.
disk_virt="systemd-detect-virt --vm --quiet"
disk_rotational() {
	local dir="$home/.local/share/containers" src rota
	[ -d "$dir" ] || dir=$home
	if $disk_virt 2>/dev/null; then
		printf false
		return 0
	fi
	# A btrfs subvolume is written as /dev/sda2[/@home].
	src=$(findmnt -no SOURCE --target "$dir" 2>/dev/null | sed 's/\[.*//' | head -n 1)
	rota=$(lsblk -dno ROTA "$src" 2>/dev/null | tr -d ' ' | head -n 1)
	if [ "$rota" = 1 ]; then printf true; else printf false; fi
}

# disk_warning <true|false>: the summary's warning for a database on a spinning disk.
disk_warning() {
	[ "$1" = true ] || return 0
	say "WARNING: the database is on a spinning disk (HDD); its settings follow it (random_page_cost 4, effective_io_concurrency 2), but reports and writes are much slower than on an SSD. An SSD is recommended (docs/HARDWARE.md)."
}

# tune_pairs <drop-in>: the "name=value" settings of the drop-in's Exec line, sorted.
tune_pairs() {
	printf '%s\n' "$1" | sed -n 's/^Exec=//p' | grep -o -- '-c [^ ]*' | sed 's/^-c //' | LC_ALL=C sort
}

# tune_restart <database-container> <old-drop-in> <new-drop-in>: prints true if the database
# must be restarted for the new settings (the tune.conf drop-in's contents), false if not.
#
# The settings are on the command line (-c), which a reload does not change; but a setting
# that does not need a restart can wait for the database's next restart (an update, a
# reboot). Above the shared_buffers ceiling (pkg/pgtune) a new register or store changes
# only max_wal_size, and the database is not stopped for that. Whether a setting needs a
# restart is asked of the running database (pg_settings.context postmaster): a changed
# memory limit, a setting the database does not know or a database that does not answer
# mean a restart, as before.
tune_restart() {
	local db=$1 old=$2 new=$3 names list known
	if [ "$(printf '%s\n' "$old" | grep '^PodmanArgs=' || true)" != "$(printf '%s\n' "$new" | grep '^PodmanArgs=' || true)" ]; then
		printf true
		return 0
	fi
	names=$(LC_ALL=C comm -3 <(tune_pairs "$old") <(tune_pairs "$new") | sed 's/^[[:space:]]*//; s/=.*//' | LC_ALL=C sort -u)
	if [ -z "$names" ]; then
		printf false
		return 0
	fi
	if printf '%s\n' "$names" | grep -qvE '^[a-z_.]+$'; then
		printf true
		return 0
	fi
	# shellcheck disable=SC2086 # the names are checked above: letters, _ and .
	list=$(printf "'%s'," $names)
	known=$(as podman exec "$db" psql -U abarow -d abarow -XtAc "SELECT count(*) FROM pg_settings WHERE context <> 'postmaster' AND name IN (${list%,})" 2>/dev/null || true)
	if [ "$known" = "$(printf '%s\n' "$names" | wc -l | tr -d ' ')" ]; then printf false; else printf true; fi
}
