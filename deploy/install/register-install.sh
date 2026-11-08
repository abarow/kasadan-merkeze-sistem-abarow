#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# register-install.sh prepares a Linux machine as an abarow register (as root,
# from the repository root):
#
#   sudo deploy/install/register-install.sh --seed /path/seed [--register register-1] [--store-id store-1]
#        [--store-url tls://192.168.1.10:7421 --store-ca ca.pem] [--no-kiosk]
#        [--update-url https://192.168.1.10:8443 [--level test|pilot|prod] [--channel-keys channel.json]]
#        [--barcode-scanner /dev/input/by-id/...-event-kbd] [--ghcr-user name --ghcr-token-file file]
#        [--printer /dev/usb/lp0 | --printer 192.168.1.50[:9100]] [--receipt-width 58|80] [--code-page pc857]
#        [--scale /dev/ttyUSB0 [--scale-protocol toledo8217|nci] [--scale-settings 9600,7E1]]
#        [--register-screen eDP-1 --customer-screen HDMI-A-1]
#        [--module market|restaurant [--kitchen-printer 192.168.1.51[:9100]] [--bar-printer 192.168.1.52[:9100]]]
#
# What it does (running it again is safe; it does not break what exists):
#   1. Checks that Podman 5, aardvark-dns and nftables are present.
#   2. Creates the abarow user and enables linger (so the services run without a
#      login session).
#   3. Copies the Quadlet files (with the register and store identities) and the
#      starting data (catalog.json, cashiers.json) to the user.
#   4. Generates the NATS keys (keeps them if they exist) and makes them Podman secrets.
#      If --store-url is given the register connects to the store server with a
#      TLS leaf connection; the register's key is printed and added at the store with
#      "store-install.sh register-add".
#   5. Starts the services (nothing updates by itself).
#   6. Unless --no-kiosk is given, sets up the cage + Chromium kiosk screen on tty1
#      (when a customer screen is given: sway, two windows on two screens).
#
# --update-url: the address of the update proxy at the store server (turned on
# with --update-url when installing the store server). When given, the register gets
# an update agent: it generates the device identity, registers with the upper tier
# (the approval code shows on the register screen and the headquarters admin
# approves it) and renews the image pull token. The store server's CA
# certificate (--store-ca) is needed. --level is the update level: prod
# (default), pilot or test. Nothing updates by itself.
#
# --barcode-scanner: the device of a scanner that works like a USB keyboard (its
# persistent name is under /dev/input/by-id/ and ends with "-event-kbd"). The
# device service takes the scanner for itself: its keystrokes do not reach the
# screen, the code goes to the basket wherever the cursor is. The abarow user is
# added to the input group. When not given, the scanner types into the barcode field like a keyboard.
#
# --printer: the ESC/POS receipt printer; on USB its device (/dev/usb/lp0; the
# abarow user is added to the lp group), on the network its address (port 9100
# if not given). The cash drawer is connected to the printer. --receipt-width is
# the paper width (58 mm: 32 columns, default; 80 mm: 48 columns). --code-page is
# the printer's Turkish character table: pc857 (default) or wpc1254; if the
# printer's ESC t number differs, like "pc857:61". When not given the printer is
# a simulator (documents are printed to the log).
#
# --scale: a scale connected to the register through a serial port (RS-232 or a
# USB-serial converter); when a weighed product is chosen the weight is read from
# it. If it has a persistent name give that (ls -l /dev/serial/by-id/). The abarow
# user is added to the dialout group. --scale-protocol is the protocol in the
# scale's settings: toledo8217 (default) or nci; --scale-settings is speed and
# frame (default 9600,7E1). When not given, weighed products are sold with the scale barcode.
#
# --register-screen, --customer-screen: a register with two screens (the HDMI
# second screen faces the customer). The values are the names of the screen
# outputs (ls /sys/class/drm/: HDMI-A-1 for card0-HDMI-A-1). The kiosk is set up
# with sway (apt install sway); the register screen and the touch panel are on
# the first output, the customer screen (basket, total, change) on the second.
# When not given, the kiosk is single screen (cage).
#
# --module: the vertical module of the register; market (default) or restaurant
# (tables, check, menu with pictures, kitchen and bar tickets). In a restaurant
# the menu is the products with categories in catalog.json; the pictures come from
# images/ in the seed directory (in the file "image": "images/kofte.jpg") or from
# headquarters. --kitchen-printer and --bar-printer are ESC/POS printers on the
# network (port 9100 if not given); when not given the ticket of that station is
# printed on the receipt printer. When the module is changed the service of the
# other module is removed; the data (sales, catalog, cashiers) stays.
#
# It installs no packages; the packages are installed beforehand with the
# distribution's package manager (Debian 13): apt install podman aardvark-dns
# nftables cage chromium. aardvark-dns lets containers find each other by name,
# nftables is needed for the Podman network; they are "recommended" Podman
# packages and may be missing in a minimal install.
set -euo pipefail

here=$(cd "$(dirname "$0")/../.." && pwd)
# shellcheck source-path=SCRIPTDIR source=common.sh
. "$here/deploy/install/common.sh"
# shellcheck source-path=SCRIPTDIR source=kiosk.sh
. "$here/deploy/install/kiosk.sh"

seed=
kiosk=1
scanner=
printer=
paper=58
codepage=pc857
scale=
scale_protocol=toledo8217
scale_serial=9600,7E1
register_output=
customer_output=
module=market
kitchen_printer=
bar_printer=
channel=0.1
register="register-1"
store_id="store-1"
store_url=
store_ca=
update=
level=prod
channel_keys=

usage() {
	sed -n '5,15p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
}

while [ $# -gt 0 ]; do
	case "$1" in
	--seed) seed=$2; shift 2 ;;
	--register) register=$2; shift 2 ;;
	--store-id) store_id=$2; shift 2 ;;
	--store-url) store_url=$2; shift 2 ;;
	--store-ca) store_ca=$2; shift 2 ;;
	--update-url) update=$2; shift 2 ;;
	--level) level=$2; shift 2 ;;
	--channel-keys) channel_keys=$2; shift 2 ;;
	--no-kiosk) kiosk=0; shift ;;
	--barcode-scanner) scanner=$2; shift 2 ;;
	--printer) printer=$2; shift 2 ;;
	--receipt-width) paper=$2; shift 2 ;;
	--code-page) codepage=$2; shift 2 ;;
	--scale) scale=$2; shift 2 ;;
	--scale-protocol) scale_protocol=$2; shift 2 ;;
	--scale-settings) scale_serial=$2; shift 2 ;;
	--register-screen) register_output=$2; shift 2 ;;
	--customer-screen) customer_output=$2; shift 2 ;;
	--module) module=$2; shift 2 ;;
	--kitchen-printer) kitchen_printer=$2; shift 2 ;;
	--bar-printer) bar_printer=$2; shift 2 ;;
	--channel) channel=$2; shift 2 ;;
	--ghcr-user) ghcr_user=$2; shift 2 ;;
	--ghcr-token-file) ghcr_token_file=$2; shift 2 ;;
	-h | --help) usage ;;
	*) echo "unknown option: $1" >&2; usage ;;
	esac
done

quadlet="$here/deploy/quadlet"
[ -f "$quadlet/abarow-gateway.container" ] || die "Quadlet files not found: $quadlet"
[ -n "$seed" ] || die "give the directory with catalog.json and cashiers.json with --seed."
for f in catalog.json cashiers.json; do
	[ -f "$seed/$f" ] || die "$seed/$f does not exist."
done
if [ "$(realpath "$seed")" = "$(realpath "$here/deploy/compose/seed")" ]; then
	die "deploy/compose/seed is only for testing (test PINs); give the business's own files."
fi
if [ -n "$update" ]; then
	[ -n "$store_ca" ] || die "--update-url needs the store server's CA certificate (--store-ca)."
	update_validate "$level" "$update" "$store_ca"
	[ -z "$channel_keys" ] || update_release_validate "$channel_keys"
elif [ -n "$channel_keys" ]; then
	die "--channel-keys is only used with --update-url."
fi
valid_identity "$register" || die "the register identity \"$register\" is invalid: letters, digits, - and _ (e.g. register-2)."
valid_identity "$store_id" || die "the store identity \"$store_id\" is invalid: letters, digits, - and _ (e.g. store-1)."
if [ -n "$store_url" ]; then
	case "$store_url" in
	tls://*) ;;
	*) die "--store-url must start with tls:// (e.g. tls://192.168.1.10:7421); the connection is always TLS." ;;
	esac
	[ -n "$store_ca" ] || die "--store-url needs the store server's CA certificate (--store-ca; ~abarow/abarow/store/ca.pem on the store server)."
	[ -f "$store_ca" ] || die "$store_ca does not exist."
elif [ -n "$store_ca" ]; then
	die "--store-ca is only used with --store-url."
fi
if [ -n "$scanner" ]; then
	case "$scanner" in
	/dev/input/*) ;;
	*) die "--barcode-scanner must be a device under /dev/input/ (e.g. /dev/input/by-id/usb-...-event-kbd)." ;;
	esac
	[ -c "$scanner" ] || die "$scanner does not exist or is not an input device (is the scanner plugged in?): ls -l /dev/input/by-id/"
	getent group input >/dev/null || die "there is no input group (is udev installed?); without the group of the input devices the scanner cannot be read."
fi
case "$paper" in
58) columns=32 ;;
80) columns=48 ;;
*) die "--receipt-width must be 58 or 80 (the paper width, mm)." ;;
esac
printf '%s' "$codepage" | grep -Eq '^(pc857|wpc1254)(:[0-9]{1,3})?$' ||
	die "--code-page must be pc857 or wpc1254 (if the printer's ESC t number differs, e.g. pc857:61)."
# Two screens: the output names are given together; if a screen is not connected
# right now the install goes on (it must be connected at boot).
if [ -n "$register_output$customer_output" ]; then
	[ -n "$register_output" ] && [ -n "$customer_output" ] || die "--register-screen and --customer-screen are given together."
	[ "$kiosk" = 1 ] || die "--customer-screen needs the kiosk (it cannot be given with --no-kiosk)."
	[ "$register_output" != "$customer_output" ] || die "the register and customer screens must be different outputs."
	for o in "$register_output" "$customer_output"; do
		printf '%s' "$o" | grep -Eq '^[A-Za-z0-9-]+$' || die "the screen output \"$o\" is invalid (e.g. HDMI-A-1, eDP-1; ls /sys/class/drm/)."
		if ! compgen -G "/sys/class/drm/card*-$o" >/dev/null; then
			say "Warning: the output $o is not visible right now (ls /sys/class/drm/); the screen must be connected at boot."
		fi
	done
fi
# device_number <path>: the number (major:minor) of a character device.
device_number() {
	printf '%d:%d' "$((16#$(stat -L -c %t "$1")))" "$((16#$(stat -L -c %T "$1")))"
}
# Printer: on USB the device number (major:minor) is taken; if the printer is
# off right now, the number of usblp is known from its name (lpN: 180:N).
printer_env=simulator
printer_dev=
case "$printer" in
'') ;;
/dev/*)
	if [ -c "$printer" ]; then
		printer_dev=$(device_number "$printer")
	elif printf '%s' "$printer" | grep -Eq '^/dev/usb/lp[0-9]+$'; then
		printer_dev="180:${printer#/dev/usb/lp}"
		say "$printer does not exist right now (the printer is off or not plugged in); it will print when it comes on."
	else
		die "$printer does not exist or is not a character device (is the printer plugged in and on?): ls -l /dev/usb/"
	fi
	getent group lp >/dev/null || die "there is no lp group; without the group of the printer device the printer cannot be written to."
	printer_env=escpos-usb:/dev/abarow-printer
	;;
*)
	addr=$printer
	case "$addr" in *:*) ;; *) addr=$addr:9100 ;; esac
	printf '%s' "$addr" | grep -Eq '^[A-Za-z0-9.-]+:[0-9]{1,5}$' ||
		die "--printer must be a USB device (e.g. /dev/usb/lp0) or a network address (e.g. 192.168.1.50 or 192.168.1.50:9100)."
	printer_env=escpos-tcp:$addr
	;;
esac
case "$module" in
market | restaurant) ;;
*) die "--module must be market or restaurant." ;;
esac
# network_printer <option> <address>: the device setting of a network ESC/POS printer.
network_printer() {
	local addr=$2
	case "$addr" in *:*) ;; *) addr=$addr:9100 ;; esac
	printf '%s' "$addr" | grep -Eq '^[A-Za-z0-9.-]+:[0-9]{1,5}$' ||
		die "$1 must be a network address (e.g. 192.168.1.51 or 192.168.1.51:9100)."
	printf 'escpos-tcp:%s' "$addr"
}
kitchen_env=''
bar_env=''
if [ -n "$kitchen_printer$bar_printer" ] && [ "$module" != restaurant ]; then
	die "--kitchen-printer and --bar-printer are only used with --module restaurant."
fi
[ -z "$kitchen_printer" ] || kitchen_env=$(network_printer --kitchen-printer "$kitchen_printer")
[ -z "$bar_printer" ] || bar_env=$(network_printer --bar-printer "$bar_printer")
# Scale: the serial port must be plugged in (the number of a USB-serial converter
# is known once it is plugged in).
scale_dev=
if [ -n "$scale" ]; then
	case "$scale" in
	/dev/*) ;;
	*) die "--scale must be a serial port device (e.g. /dev/ttyUSB0 or /dev/serial/by-id/...)." ;;
	esac
	[ -c "$scale" ] || die "$scale does not exist or is not a character device (is the USB-serial converter plugged in?): ls -l /dev/serial/by-id/"
	case "$scale_protocol" in
	toledo8217 | nci) ;;
	*) die "--scale-protocol must be toledo8217 or nci (the protocol in the scale's settings)." ;;
	esac
	printf '%s' "$scale_serial" | grep -Eq '^(1200|2400|4800|9600|19200|38400),[78][NEO][12]$' ||
		die "--scale-settings must be speed,frame (e.g. 9600,7E1; speed 1200-38400, data bits 7/8, parity N/E/O, stop bits 1/2)."
	getent group dialout >/dev/null || die "there is no dialout group; without the group of the serial port the scale cannot be reached."
	scale_dev=$(device_number "$scale")
fi

# 1. Prerequisites, 2. user and linger; access to the images
preflight
ensure_user
# Peripherals: abarow must be able to read the barcode scanner (input group) and
# write to the USB printer (lp group). The services get the groups from the
# user's systemd session; when a group is added the session is restarted.
groups=
[ -z "$scanner" ] || groups="$groups input"
[ -z "$printer_dev" ] || groups="$groups lp"
[ -z "$scale_dev" ] || groups="$groups dialout"
added=
for g in $groups; do
	if ! id -nG "$user" | tr ' ' '\n' | grep -qx "$g"; then
		usermod -aG "$g" "$user"
		added="$added $g"
	fi
done
if [ -n "$added" ]; then
	systemctl restart "user@$uid.service"
	ensure_user
	say "$user was added to these groups:$added (peripherals)"
fi
ghcr_login

# 3. Quadlet files and starting data
units_dir="$home/.config/containers/systemd"
install_units "$quadlet"
# Restaurant register: the restaurant module instead of the market module; the gateway connects to it.
units=$(unit_names "$quadlet")
if [ "$module" = restaurant ]; then
	install_units "$quadlet/restaurant"
	as systemctl --user stop abarow-retail.service 2>/dev/null || true
	rm -f "$units_dir/abarow-retail.container"
	sed -i "s|abarow-retail\.service|abarow-restaurant.service|; s|^Environment=ABAROW_MODULE_URL=.*|Environment=ABAROW_MODULE_URL=http://abarow-restaurant:8080|; s|^Environment=ABAROW_MODULE=.*|Environment=ABAROW_MODULE=restaurant|" \
		"$units_dir/abarow-gateway.container"
	units=$(printf '%s' "$units" | sed 's/abarow-retail\.service/abarow-restaurant.service/')
elif [ -f "$units_dir/abarow-restaurant.container" ]; then
	as systemctl --user stop abarow-restaurant.service 2>/dev/null || true
	rm -f "$units_dir/abarow-restaurant.container"
fi
# Update agent (optional): registration and the image pull token; it does not update by itself.
if [ -n "$update" ]; then
	update_install "$quadlet/update" abarow-update "$update" "$store_ca" "$level" "$register" "$store_id" "$register"
	units="$units abarow-update.service"
	update_screen abarow-gateway ABAROW_UPDATE_AGENT_URL=http://abarow-update:8080
	if [ -n "$channel_keys" ]; then
		# New release: the agent polls the signed channel notice and applies it with the manager's approval. It does not apply while a shift is open at the register.
		update_release abarow-update "$channel_keys" abarow-update "ABAROW_UPDATE_LEVEL=$level" "ABAROW_UPDATE_GATE_URL=http://abarow-gateway:8080/api/update/gate"
		# The register pulls images from the store server's mirror (the network inside the store).
		registry_trust "$(url_host "$update"):5443" "$store_ca"
		agent_registry_config abarow-update "$(url_host "$update"):5443"
	else
		update_release_remove abarow-update
	fi
else
	update_remove abarow-update
	update_screen abarow-gateway
fi
sed -i "s|^Environment=ABAROW_REGISTER_ID=.*|Environment=ABAROW_REGISTER_ID=$register|; s|^Environment=ABAROW_STORE_ID=.*|Environment=ABAROW_STORE_ID=$store_id|" \
	"$units_dir"/abarow-*.container
dropins_before=$(dropin_digest)
# Connection to the store server: NATS also sees the store server's CA certificate.
dropin="$units_dir/abarow-nats.container.d"
if [ -n "$store_url" ]; then
	as mkdir -p "$dropin"
	as tee "$dropin/store.conf" >/dev/null <<'EOF'
# written by register-install.sh: leaf connection to the store server (TLS, the store's CA).
[Container]
Secret=abarow-nats-leaf-ca,type=mount,target=/etc/nats/leaf/ca.pem
EOF
else
	rm -f "$dropin/store.conf"
fi
# Headquarters' catalog comes down from the store server: catalog connects to
# NATS with its own identity and takes the catalog stream of the store (the
# JetStream domain of the store server) as its source. Products from headquarters
# are not changed by the starting file.
catalog_dropin="$units_dir/abarow-catalog.container.d"
if [ -n "$store_url" ]; then
	as mkdir -p "$catalog_dropin"
	as tee "$catalog_dropin/store.conf" >/dev/null <<EOF
# written by register-install.sh: headquarters' catalog comes down from the store server (store $store_id).
[Unit]
Wants=abarow-nats.service
After=abarow-nats.service

[Container]
Environment=ABAROW_NATS_URL=nats://abarow-nats:4222
Environment=ABAROW_NATS_NKEY=/run/secrets/nats.nk
Environment=ABAROW_CATALOG_SOURCE=$store_id
Secret=abarow-nats-catalog,type=mount,target=/run/secrets/nats.nk,uid=65532,mode=0400
EOF
else
	rm -f "$catalog_dropin/store.conf"
fi
# Cashiers are managed on the store server's screen and come down from it: staff
# takes the cashier subjects of the store's settings stream as its source.
# Cashiers coming from the store server are not changed by the starting file (cashiers.json).
staff_dropin="$units_dir/abarow-staff.container.d"
if [ -n "$store_url" ]; then
	as mkdir -p "$staff_dropin"
	as tee "$staff_dropin/store.conf" >/dev/null <<EOF
# written by register-install.sh: cashiers come down from the store server (store $store_id); the back
# house's delivery receipt is taken (an old event in the shift stream is
# deleted only with its receipt).
[Container]
Environment=ABAROW_CASHIERS_SOURCE=$store_id
Environment=ABAROW_RECEIPT_SOURCE=$store_id
EOF
else
	rm -f "$staff_dropin/store.conf"
fi
# Delivery receipt: the store server gives a receipt when it has processed a sale
# into its database; sales deletes the old event in the register stream only with
# its receipt. Without a store server no event is deleted.
sales_dropin="$units_dir/abarow-sales.container.d"
if [ -n "$store_url" ]; then
	as mkdir -p "$sales_dropin"
	as tee "$sales_dropin/store.conf" >/dev/null <<EOF
# written by register-install.sh: the delivery receipt of the store server (store $store_id) is taken.
[Container]
Environment=ABAROW_RECEIPT_SOURCE=$store_id
EOF
else
	rm -f "$sales_dropin/store.conf"
fi
# Peripherals in a single drop-in file (GroupAdd=keep-groups can be written once).
#
# USB printer and USB-serial scale: the device file is deleted when the device
# is turned off or unplugged and made again when it comes on; a file mounted into
# the container would not see the new one. The install makes a persistent device
# file with the same number: while the device is off, opening it gives an error,
# and when it comes on the device service uses it without restarting. The printer
# is reached through the lp group, the scale through the dialout group.
#
# Barcode scanner: the device service sees the input devices read-only (when the
# scanner is unplugged and plugged in the new device shows up too; the printer
# works while it is not plugged in) and reads with abarow's input group.
device_dropin="$units_dir/abarow-device.container.d"
nodes="$home/abarow/devices"
# persistent_device <name> <major:minor> <group>: the device file $nodes/<name> (deletes it if empty).
persistent_device() {
	local node="$nodes/$1" dev=$2 group=$3 major minor
	if [ -z "$dev" ]; then
		rm -f "$node"
		return
	fi
	as mkdir -p "$nodes"
	if findmnt -n -o OPTIONS --target "$nodes" | tr ',' '\n' | grep -qx nodev; then
		die "$nodes is on a partition closed to device files (nodev); a USB device cannot be opened from here."
	fi
	major=${dev%:*} minor=${dev#*:}
	if [ "$(stat -c '%F %t:%T %G' "$node" 2>/dev/null)" != "character special file $(printf '%x:%x' "$major" "$minor") $group" ]; then
		rm -f "$node"
		mknod -m 0660 "$node" c "$major" "$minor"
		chgrp "$group" "$node"
	fi
}
persistent_device printer "$printer_dev" lp
persistent_device scale "$scale_dev" dialout
as mkdir -p "$device_dropin"
{
	echo "# written by register-install.sh: receipt printer, barcode scanner and scale"
	echo "[Container]"
	echo "Environment=ABAROW_PRINTER=$printer_env"
	echo "Environment=ABAROW_PRINTER_COLUMNS=$columns"
	echo "Environment=ABAROW_PRINTER_CODEPAGE=$codepage"
	if [ -n "$printer_dev" ]; then
		echo "# printer $printer (device $printer_dev)"
		echo "Volume=$nodes/printer:/dev/abarow-printer"
	fi
	if [ -n "$scanner" ]; then
		echo "# barcode scanner $scanner"
		echo "Volume=/dev/input:/dev/input:ro"
		echo "Environment=ABAROW_SCANNER=evdev:$scanner"
	fi
	if [ -n "$scale_dev" ]; then
		echo "# scale $scale (device $scale_dev)"
		echo "Volume=$nodes/scale:/dev/abarow-scale"
		echo "Environment=ABAROW_SCALE=$scale_protocol:/dev/abarow-scale"
		echo "Environment=ABAROW_SCALE_SERIAL=$scale_serial"
	fi
	if [ -n "$kitchen_env" ]; then
		echo "# kitchen printer $kitchen_printer"
		echo "Environment=ABAROW_KITCHEN_PRINTER=$kitchen_env"
	fi
	if [ -n "$bar_env" ]; then
		echo "# bar printer $bar_printer"
		echo "Environment=ABAROW_BAR_PRINTER=$bar_env"
	fi
	if [ -n "$printer_dev$scanner$scale_dev" ]; then
		echo "GroupAdd=keep-groups"
	fi
} | as tee "$device_dropin/devices.conf" >/dev/null
# The user in the container (65532) maps to another sub-id on the host; the
# files are written 0644 so it can read them. cashiers.json contains PINs, so the
# home directory is closed to other users on the host (the container only sees
# the directory that is mounted).
chmod 0750 "$home"
as mkdir -p "$home/abarow/seed"
install -o "$user" -g "$user" -m 0644 "$seed/catalog.json" "$seed/cashiers.json" "$home/abarow/seed/"
# Product pictures ("image": "images/..." in catalog.json): the directory as it is.
if [ -d "$seed/images" ]; then
	as mkdir -p "$home/abarow/seed/images"
	find "$seed/images" -maxdepth 1 -type f -exec install -o "$user" -g "$user" -m 0644 {} "$home/abarow/seed/images/" \;
fi
say "Quadlet files and the starting data were copied (register $register, store $store_id, channel $channel)"

# 4. NATS keys. The keys are kept in a persistent directory (only abarow reads
# it): they do not change when the install is repeated, so the register entry at the store server stays valid.
auth="$home/abarow/nats-auth"
as mkdir -p "$auth"
as chmod 0700 "$auth"
natsauth=(as podman run --rm --user 0 -v "$auth:/auth:Z" "ghcr.io/abarow/abarow-natsauth:$channel")
users=sales,staff
if [ -n "$store_url" ]; then
	users=sales,staff,catalog
fi
args=(-out /auth -conf-dir /etc/nats -users "$users" -server-name "$register")
if [ -n "$store_url" ]; then
	as tee "$auth/store-ca.pem" <"$store_ca" >/dev/null
	args+=(-leaf "$store_url" -leaf-ca /auth/store-ca.pem)
fi
before=$( (cat "$auth/nats.conf" 2>/dev/null || true) | sha256sum)
"${natsauth[@]}" "${args[@]}"
# The register database password is generated once and kept across re-installs.
if ! as test -s "$auth/db-password"; then
	head -c 30 /dev/urandom | base64 -w0 | tr '/+' '_-' | as tee "$auth/db-password" >/dev/null
	as chmod 0600 "$auth/db-password"
fi
db_password=$(as cat "$auth/db-password")
secret abarow-register-db-password "$auth/db-password"
printf 'postgres://abarow:%s@abarow-register-db:5432/abarow?sslmode=disable' "$db_password" | as podman secret create --replace abarow-register-db-url - >/dev/null
secret abarow-nats-conf "$auth/nats.conf"
secret abarow-nats-sales "$auth/sales/nats.nk"
secret abarow-nats-staff "$auth/staff/nats.nk"
if [ -n "$store_url" ]; then
	secret abarow-nats-leaf-ca "$auth/leaf/ca.pem"
	secret abarow-nats-catalog "$auth/catalog/nats.nk"
fi
say "NATS keys are ready"
# If the configuration or the unit drop-in files changed, the services that use
# NATS are restarted with the new secrets and settings.
if [ "$before" != "$(sha256sum <"$auth/nats.conf")" ] || [ "$dropins_before" != "$(dropin_digest)" ]; then
	as systemctl --user daemon-reload
	for unit in abarow-nats abarow-sales abarow-staff abarow-catalog abarow-device abarow-gateway; do
		if as systemctl --user is-active --quiet "$unit.service"; then
			as systemctl --user restart "$unit.service" || true
		fi
	done
fi

# 5. Services and update. All units are started explicitly (After= sets the
# order): if only the gateway were started, the dependency of an already running
# service would not be restarted after an earlier failure.
# shellcheck disable=SC2086 # unit names contain no spaces
start_units $units
say "The services are running; screen http://127.0.0.1:8080"
if [ -n "$printer" ]; then
	say "Receipt printer $printer ($paper mm, $codepage). To try it, print an X report at the register; if the Turkish letters are wrong, change --code-page."
fi
if [ -n "$scale" ]; then
	say "Scale $scale ($scale_protocol, $scale_serial). To try it, put a product on the scale and choose a weighed product; if it says \"no response\" or \"could not be read\", match the scale's protocol and port settings with --scale-protocol and --scale-settings."
fi

# 6. Kiosk: Chromium on tty1; cage on a single screen, sway if there is a customer screen.
if [ "$kiosk" = 1 ]; then
	browser=$(command -v chromium || command -v chromium-browser || true)
	[ -n "$browser" ] || die "chromium is not installed (needed for the kiosk)."
	if [ -n "$customer_output" ]; then
		command -v sway >/dev/null || die "sway is not installed (for the two-screen kiosk: apt install sway)."
		mkdir -p /etc/abarow
		kiosk_two_screens /etc/abarow "$browser" "$register_output" "$customer_output"
		compositor="sway (register $register_output, customer $customer_output)"
		exec_start="$(command -v sway) -c /etc/abarow/kiosk-sway.conf"
	else
		command -v cage >/dev/null || die "cage is not installed (needed for the kiosk)."
		rm -f /etc/abarow/kiosk-sway.conf /etc/abarow/kiosk-browser.sh
		compositor=cage
		exec_start="/usr/bin/cage -s -- $browser $kiosk_flags http://127.0.0.1:8080"
	fi
	cat >/etc/systemd/system/abarow-kiosk.service <<EOF
# abarow register screen: $compositor + Chromium on tty1. Written by register-install.sh.
[Unit]
Description=abarow register screen (kiosk)
After=systemd-user-sessions.service user@$uid.service
Wants=user@$uid.service
Conflicts=getty@tty1.service

[Service]
User=$user
PAMName=login
TTYPath=/dev/tty1
StandardInput=tty
StandardError=journal
UtmpIdentifier=tty1
UtmpMode=user
Environment=XDG_RUNTIME_DIR=/run/user/$uid
# Until the screen is ready Chromium retries instead of showing a blank page (Restart).
ExecStart=$exec_start
Restart=always
RestartSec=3

[Install]
WantedBy=graphical.target
EOF
	systemctl daemon-reload
	systemctl disable getty@tty1.service >/dev/null 2>&1 || true
	systemctl enable abarow-kiosk.service >/dev/null
	systemctl set-default graphical.target >/dev/null
	say "Kiosk installed (abarow-kiosk.service, $compositor); it starts after a reboot"
fi

if [ -n "$store_url" ]; then
	key=$("${natsauth[@]}" -out /auth -show-leaf-key | tr -d '\r' | tail -n 1)
	say "Add this register to the store server (on the store server machine, from the repository root):"
	printf '\n    sudo deploy/install/store-install.sh register-add %s %s\n\n' "$register" "$key"
	say "Until the register is added to the store server it keeps selling; sales accumulate at the register."
fi
say "Install complete. Warning: the fiscal printer simulator must not be used alone in live sales (see README)."
