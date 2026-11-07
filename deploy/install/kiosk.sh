# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0
# shellcheck shell=bash

# kiosk.sh writes the kiosk files of the register screen; register-install.sh sources it.
# On a single screen the kiosk is cage + Chromium (register-install.sh writes the unit).
# When a customer screen (an HDMI second screen) is given, cage is not enough:
# it shows a single window. In that case sway places two windows on the two outputs.

# kiosk_flags are the Chromium kiosk flags.
kiosk_flags="--kiosk --noerrdialogs --no-first-run --disable-translate --disable-features=TranslateUI --overscroll-history-navigation=0 --password-store=basic"

# kiosk_two_screens <directory> <browser> <register-output> <customer-output>:
# writes kiosk-sway.conf and kiosk-browser.sh into the directory.
#
# The register window and the customer window are in the same Chromium process:
# the customer screen receives its state from the cashier screen through a
# BroadcastChannel (the same browser is needed). The windows are placed on their
# outputs by their page titles ("abarow kasa" and "abarow müşteri ekranı", the
# titles of the Turkish screens) and go full screen. The touch screen is mapped
# to the register output; the keyboard (and a keyboard-like scanner) always
# types into the register window. No shortcuts are defined: the cashier cannot leave the kiosk.
kiosk_two_screens() {
	local dir=$1 browser=$2 register=$3 customer=$4
	cat >"$dir/kiosk-browser.sh" <<EOF
#!/bin/sh
# written by register-install.sh: Chromium in the two-screen kiosk. One process
# opens two windows; if Chromium closes, both are reopened together.
url=\${ABAROW_KIOSK_URL:-http://127.0.0.1:8080}
while :; do
	$browser $kiosk_flags --ozone-platform=wayland "\$url" &
	main=\$!
	# A second call to the running process opens a new window and returns at once.
	sleep 3
	$browser $kiosk_flags --ozone-platform=wayland --new-window "\$url/customer.html"
	wait "\$main"
	sleep 3
done
EOF
	chmod 0755 "$dir/kiosk-browser.sh"
	cat >"$dir/kiosk-sway.conf" <<EOF
# written by register-install.sh: two-screen kiosk (register $register, customer $customer).
default_border none
default_floating_border none
focus_follows_mouse no
seat * hide_cursor 3000
output $register position 0 0
output $customer position 16384 0
input type:touch map_to_output $register
input type:tablet_tool map_to_output $register
# The customer window takes no focus: the keyboard and the scanner type into the register window.
no_focus [title="^abarow müşteri"]
# The register window must stay full screen: when Chromium is not full screen it
# shows the tab and address bars. The customer window may first open on the
# register output and go full screen, so when it is moved the register window goes full screen again.
for_window [title="^abarow kasa"] move container to output $register, fullscreen enable, focus
for_window [title="^abarow müşteri"] move container to output $customer, fullscreen enable; [title="^abarow kasa"] fullscreen enable, focus
exec $dir/kiosk-browser.sh
EOF
}
