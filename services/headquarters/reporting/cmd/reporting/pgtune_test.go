// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"abarow.com/pkg/pgtune"
)

func TestPgtuneCommandStoresFlag(t *testing.T) {
	var out bytes.Buffer
	if err := pgtuneCommand([]string{"--stores", "50"}, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 || lines[0] != "MEMORY_MB=2560" || lines[2] != "SHM_MB=768" || !strings.Contains(lines[1], "-c shared_buffers=832MB") ||
		!strings.Contains(lines[1], "-c max_connections=30") || !strings.Contains(lines[1], "-c work_mem=32MB") {
		t.Errorf("50 stores:\n%s", out.String())
	}

	// The number of registers per store can be changed: 50 × 1 register = 50 registers.
	out.Reset()
	if err := pgtuneCommand([]string{"--stores", "50", "--registers-per-store", "1"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "-c shared_buffers=432MB") {
		t.Errorf("50 stores × 1 register:\n%s", out.String())
	}
}

// If the number of stores is not given it is read from the store list (the
// stores.json natsauth writes); if the file is missing no store is connected to headquarters.
func TestPgtuneCommandReadsStoreFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "stores.json")
	list := `[`
	for i := 1; i <= 5; i++ {
		if i > 1 {
			list += `,`
		}
		list += `{"id":"store-` + string(rune('0'+i)) + `","name":"","nkey":"U` + strings.Repeat(string(rune('A'+i)), 55) + `"}`
	}
	list += `]`
	if err := os.WriteFile(file, []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := pgtuneCommand([]string{"--stores-file", file}, &out); err != nil {
		t.Fatalf("%v\n%s", err, list)
	}
	// 5 stores × 2 registers = 10 registers: 64 + 8 × 6 = 112 MB.
	if !strings.Contains(out.String(), "shared_buffers=112MB") {
		t.Errorf("5 stores:\n%s", out.String())
	}

	out.Reset()
	if err := pgtuneCommand([]string{"--stores-file", filepath.Join(dir, "yok.json")}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "shared_buffers=64MB") {
		t.Errorf("with no store list:\n%s", out.String())
	}
}

func TestPgtuneCommandBadFlag(t *testing.T) {
	var out bytes.Buffer
	if err := pgtuneCommand([]string{"--bilinmeyen"}, &out); err == nil {
		t.Error("an unknown flag was accepted")
	}
}

// The base values in the Compose and Quadlet files are the same as pkg/pgtune.ForHeadquarters:
// if one changes and the other is forgotten, the test fails.
func TestDeployFilesMatchBaseTuning(t *testing.T) {
	base := pgtune.ForHeadquarters(pgtune.HeadquartersRegistersPerStore)
	read := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "deploy", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	quadlet := read("quadlet/headquarters/abarow-db.container")
	if !strings.Contains(quadlet, "Exec=postgres "+base.Args()+"\n") {
		t.Errorf("the Quadlet Exec line differs from pgtune:\n%s", quadlet)
	}
	if !strings.Contains(quadlet, fmt.Sprintf("PodmanArgs=--memory=%dm\n", base.MemoryLimitMB)) {
		t.Errorf("the Quadlet memory limit is not %d MB", base.MemoryLimitMB)
	}
	compose := read("compose/headquarters.yaml")
	var quoted []string
	for _, f := range append([]string{"postgres"}, base.Flags()...) {
		quoted = append(quoted, `"`+f+`"`)
	}
	if !strings.Contains(compose, "command: ["+strings.Join(quoted, ", ")+"]") {
		t.Error("the compose headquarters-db command differs from pgtune")
	}
	if !strings.Contains(compose, fmt.Sprintf("mem_limit: %dm", base.MemoryLimitMB)) {
		t.Errorf("the compose memory limit is not %d MB", base.MemoryLimitMB)
	}
}
