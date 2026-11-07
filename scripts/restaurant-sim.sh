#!/usr/bin/env bash
# Copyright 2026 Mehmet ERGULCU and the abarow contributors
# SPDX-License-Identifier: Apache-2.0

# restaurant-sim.sh runs the restaurant register (Docker Compose) and tries its
# screen in a real browser: the floor layout, the menu with pictures, a product
# with options, kitchen and bar tickets, cancellation, the check, splitting the
# bill and payment, moving a table.
# From the repository root:
#
#   bash scripts/restaurant-sim.sh          # build, start, browser test
#   bash scripts/restaurant-sim.sh logs     # logs (kitchen tickets are in device)
#   bash scripts/restaurant-sim.sh down     # shut down, delete data
#
# The project is abarow-restaurant; the screen is http://localhost:8089 (the
# market register on 8088 can run at the same time).
set -euo pipefail
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1

restaurant_compose() {
	ABAROW_UI_PORT=8089 ABAROW_SALES_PORT=9019 ABAROW_NATS_PORT=4239 \
		docker compose -p abarow-restaurant -f deploy/compose/compose.yaml -f deploy/compose/restaurant.yaml "$@"
}

case "${1:-}" in
down)
	restaurant_compose down --volumes
	exit 0
	;;
logs)
	restaurant_compose logs --no-color
	exit 0
	;;
esac

printf '==> %s\n' "Building and starting the restaurant register"
restaurant_compose up --build --detach --wait
printf '==> %s\n' "Restaurant screen (browser test)"
restaurant_compose --profile ui-test run --rm ui-smoke
printf '==> %s\n' "Restaurant screen: http://localhost:8089 (test cashier: Kasiyer Ayşe / PIN 1111, manager: Müdür Ali / 9999)"
