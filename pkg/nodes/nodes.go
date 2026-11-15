// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package nodes is the list of devices that connect to a tier from below as leaf: the
// registers at the store server (registers.json), the stores at headquarters
// (stores.json; each store's store server). natsauth manages the list (add,
// remove) and produces NATS's leaf users from it; report and ingest build their
// stream sources from this list.
package nodes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Node is a device connected to the tier above (a register or a store's store server).
type Node struct {
	// ID is the device's id (register-1, store-1): the register_id or store_id in the events
	// and the device's NATS JetStream domain are the same name.
	ID string `json:"id"`
	// Name is the name shown on the screen; ID is shown if empty.
	Name string `json:"name,omitempty"`
	// NKey is the public part of the key the device uses when connecting to the tier above
	// (starts with U). The private part does not leave the device.
	NKey string `json:"nkey"`
}

// DisplayName is the name shown on the screen.
func (r Node) DisplayName() string {
	if r.Name != "" {
		return r.Name
	}
	return r.ID
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`)

// ValidID says the id can be used as a device (and NATS domain) name:
// letters, digits, "-" and "_"; at most 63 characters (the machine name limit:
// for a single register the machine name is given as the server name). Since the name goes into
// NATS subjects it cannot contain a dot, space or wildcard.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Validate checks the list: the ids must be valid and unique, the keys must be in the
// public user key form and unique.
func Validate(regs []Node) error {
	ids := map[string]bool{}
	keys := map[string]bool{}
	for _, r := range regs {
		if !ValidID(r.ID) {
			return fmt.Errorf("invalid id %q: letters, digits, - and _ are allowed (at most 63 characters)", r.ID)
		}
		if ids[r.ID] {
			return fmt.Errorf("%s listede iki kez var", r.ID)
		}
		ids[r.ID] = true
		// Public user key: 56-character base32 starting with U.
		if len(r.NKey) != 56 || r.NKey[0] != 'U' || strings.Trim(r.NKey, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") != "" {
			return fmt.Errorf("%s: the key is invalid (it must be a public key starting with U)", r.ID)
		}
		if keys[r.NKey] {
			return fmt.Errorf("%s: the key is also registered on another device", r.ID)
		}
		keys[r.NKey] = true
	}
	return nil
}

// Load reads the list from the file. If the file does not exist the list is empty (no device
// has been added yet).
func Load(path string) ([]Node, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var regs []Node
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&regs); err != nil {
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}
	if err := Validate(regs); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return regs, nil
}

// Save sorts the list by id and writes it to the file. It is written to a temporary file
// first, then moved into place: a half-finished write does not corrupt the list.
func Save(path string, regs []Node) error {
	if err := Validate(regs); err != nil {
		return err
	}
	regs = slices.Clone(regs)
	slices.SortFunc(regs, func(a, b Node) int { return strings.Compare(a.ID, b.ID) })
	if regs == nil {
		regs = []Node{}
	}
	data, err := json.MarshalIndent(regs, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nodes-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	// The list is not secret (public keys); the report service reads it as another
	// user.
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Put adds the device to the list; if a device with the same id exists it replaces it
// (when the device is reinstalled and its key changes).
func Put(regs []Node, r Node) []Node {
	for i := range regs {
		if regs[i].ID == r.ID {
			regs = slices.Clone(regs)
			regs[i] = r
			return regs
		}
	}
	return append(slices.Clone(regs), r)
}

// Remove removes the device from the list; the second value says whether the device
// was in the list.
func Remove(regs []Node, id string) ([]Node, bool) {
	i := slices.IndexFunc(regs, func(r Node) bool { return r.ID == id })
	if i < 0 {
		return regs, false
	}
	return slices.Delete(slices.Clone(regs), i, i+1), true
}
