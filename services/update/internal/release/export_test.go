// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package release

import (
	"encoding/json"
	"os"
	"path/filepath"

	"abarow.com/services/update/internal/channel"
)

// WriteInFlight imitates the agent dying during an application in tests:
// it writes a half-finished operation to the persistent state.
func WriteInFlight(dir string, seq uint64, version string, changed []channel.Image, previous map[string]channel.Image) error {
	path := filepath.Join(dir, stateFile)
	var st state
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &st); err != nil {
			return err
		}
	}
	st.Op = &operation{
		Manifest: channel.Manifest{Channel: "prod", Version: version, Seq: seq, Images: changed},
		By:       "ayse", Previous: previous, Changed: changed,
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeFile(path, raw)
}
