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

func TestPgtuneCommandRegistersFlag(t *testing.T) {
	var out bytes.Buffer
	if err := pgtuneCommand([]string{"--registers", "2"}, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || lines[0] != "MEMORY_MB=256" ||
		!strings.HasPrefix(lines[1], "ARGS=-c shared_buffers=64MB -c max_connections=20 -c work_mem=4MB ") {
		t.Errorf("output:\n%s", out.String())
	}

	out.Reset()
	if err := pgtuneCommand([]string{"--registers", "12"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "shared_buffers=128MB") || !strings.Contains(out.String(), "MEMORY_MB=384") {
		t.Errorf("12 kasa:\n%s", out.String())
	}
}

// If the number of registers is not given it is read from the register list (the
// registers.json natsauth writes); if the file is missing the store has no registers.
func TestPgtuneCommandReadsRegisterFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "registers.json")
	regs := `[`
	for i := 1; i <= 6; i++ {
		if i > 1 {
			regs += `,`
		}
		regs += `{"id":"register-` + string(rune('0'+i)) + `","name":"","nkey":"U` + strings.Repeat(string(rune('A'+i)), 55) + `"}`
	}
	regs += `]`
	if err := os.WriteFile(file, []byte(regs), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := pgtuneCommand([]string{"--registers-file", file}, &out); err != nil {
		t.Fatalf("%v\n%s", err, regs)
	}
	// 6 kasa: 64 + 2×8 = 80 MB.
	if !strings.Contains(out.String(), "shared_buffers=80MB") {
		t.Errorf("6 kasa:\n%s", out.String())
	}

	out.Reset()
	if err := pgtuneCommand([]string{"--registers-file", filepath.Join(dir, "yok.json")}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "shared_buffers=64MB") {
		t.Errorf("kasa listesi yokken:\n%s", out.String())
	}
}

func TestPgtuneCommandBadFlag(t *testing.T) {
	var out bytes.Buffer
	if err := pgtuneCommand([]string{"--bilinmeyen"}, &out); err == nil {
		t.Error("bilinmeyen bayrak kabul edildi")
	}
}

// The base values in the Compose and Quadlet files are the same as pkg/pgtune:
// if one changes and the other is forgotten, the test fails.
func TestDeployFilesMatchBaseTuning(t *testing.T) {
	base := pgtune.ForStore(pgtune.BaseRegisters)
	read := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	quadlet := read("quadlet/store/abarow-store-db.container")
	if !strings.Contains(quadlet, "Exec=postgres "+base.Args()+"\n") {
		t.Errorf("the Quadlet Exec line differs from pgtune:\n%s", quadlet)
	}
	if !strings.Contains(quadlet, fmt.Sprintf("PodmanArgs=--memory=%dm\n", base.MemoryLimitMB)) {
		t.Errorf("the Quadlet memory limit is not %d MB:\n%s", base.MemoryLimitMB, quadlet)
	}

	compose := read("compose/store.yaml")
	var quoted []string
	for _, f := range append([]string{"postgres"}, base.Flags()...) {
		quoted = append(quoted, `"`+f+`"`)
	}
	if !strings.Contains(compose, "command: ["+strings.Join(quoted, ", ")+"]") {
		t.Errorf("the compose store-db command differs from pgtune")
	}
	if !strings.Contains(compose, fmt.Sprintf("mem_limit: %dm", base.MemoryLimitMB)) {
		t.Errorf("the compose memory limit is not %d MB", base.MemoryLimitMB)
	}
}
