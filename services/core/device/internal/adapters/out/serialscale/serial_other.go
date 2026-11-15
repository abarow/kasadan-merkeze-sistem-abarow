// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package serialscale

import "errors"

// openSerial does not exist outside Linux (the register is Linux; the simulator is used
// on a development machine).
func openSerial(string, Settings) (port, error) {
	return nil, errors.New("seri port terazi yalnızca Linux'ta")
}
