// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package policy

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Install writes the generated files to the Podman configuration directory (~/.config/containers):
// policy.json and registries.d/abarow.yaml. If someone else's policy.json is in the directory
// it returns ErrForeign and nothing is written. The files are written atomically.
// It returns whether anything changed.
func Install(dir string, f Files) (changed bool, err error) {
	polPath := filepath.Join(dir, "policy.json")
	if cur, err := os.ReadFile(polPath); err == nil {
		if !Ours(cur) {
			return false, ErrForeign
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	c1, err := writeIfChanged(polPath, f.Policy)
	if err != nil {
		return false, err
	}
	c2, err := writeIfChanged(filepath.Join(dir, "registries.d", "abarow.yaml"), f.RegistriesD)
	if err != nil {
		return false, err
	}
	return c1 || c2, nil
}

func writeIfChanged(path string, data []byte) (bool, error) {
	if cur, err := os.ReadFile(path); err == nil && string(cur) == string(data) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}
