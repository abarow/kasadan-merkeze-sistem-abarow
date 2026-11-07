// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package feed feeds the unit's local image mirror (Zot) with the agent's token so that it
// can pull from the tier above. Zot uses a credentials file (credentialsFile)
// to sync from the tier above; since the token's lifetime is short the file is
// rewritten on every renewal.
//
// Zot reads the credentials file only at start and when the configuration file changes
// (hot reload); changing only the credentials file is not enough (tried with Zot v2.1.x).
// So the line ending at the end of the configuration file is changed too: the content stays
// the same, Zot reloads the configuration.
package feed

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Writer is the agent's OnToken hook.
type Writer struct {
	// Dir is the directory shared by the agent and Zot (/etc/zot in Zot).
	Dir string
	// Upstream is the "address:port" of the tier above's mirror (the key of the credentials file).
	Upstream string
	// Config is the name of Zot's configuration file in Dir; zot.json if empty.
	Config string
}

// OnToken writes the token to the credentials file and makes Zot reload.
func (w Writer) OnToken(token string, _ time.Time) error {
	if w.Dir == "" || w.Upstream == "" {
		return errors.New("besleme: dizin ve üst katman adresi gerekli")
	}
	raw, err := json.Marshal(map[string]map[string]string{w.Upstream: {"username": "abarow", "password": token}})
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(w.Dir, "identity.json"), raw, 0o644); err != nil {
		return err
	}
	name := w.Config
	if name == "" {
		name = "zot.json"
	}
	return nudge(filepath.Join(w.Dir, name))
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// nudge rewrites the configuration file with the same content but changed bytes (the
// trailing line ending is added or removed). If the file does not exist (the mirror is not installed
// yet) it does not fail. It is written in place: Zot watches the file path, a rename
// opens a new file (inode) and may miss the watch.
func nudge(path string) error {
	cur, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var next []byte
	if bytes.HasSuffix(cur, []byte("\n\n")) {
		next = cur[:len(cur)-1]
	} else {
		next = append(append([]byte{}, cur...), '\n')
	}
	return os.WriteFile(path, next, 0o644)
}
