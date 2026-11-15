// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"io"

	"abarow.com/pkg/nodes"
	"abarow.com/pkg/pgtune"
	"abarow.com/pkg/service"
)

// pgtuneCommand prints the database settings by the store's number of registers
// (pkg/pgtune). The install script puts the output in the database container's
// unit drop-in file; it is run again when a register is added or removed:
//
//	report pgtune [--registers N | --registers-file registers.json] [--rotational]
//
// --rotational: the database is on a spinning disk (the install reads it; pkg/pgtune.Settings.Rotational).
//
// The output is three lines: MEMORY_MB=<container memory limit>,
// ARGS=<the -c flags to give to postgres> and SHM_MB=<size of /dev/shm>.
func pgtuneCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("pgtune", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registers := fs.Int("registers", -1, "kasa sayısı; verilmezse kasa listesinden okunur")
	rotational := fs.Bool("rotational", false, "veritabanı dönen diskte (kurulum okur)")
	file := fs.String("registers-file", service.Env("ABAROW_REGISTERS", "/config/registers.json"), "kasa listesi")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("kullanım: report pgtune [--registers N | --registers-file dosya] [--rotational]: %w", err)
	}
	n := *registers
	if n < 0 {
		regs, err := nodes.Load(*file)
		if err != nil {
			return err
		}
		n = len(regs)
	}
	s := pgtune.ForStore(n)
	s.Rotational = *rotational
	_, err := fmt.Fprintf(out, "MEMORY_MB=%d\nARGS=%s\nSHM_MB=%d\n", s.MemoryLimitMB, s.Args(), s.ShmMB)
	return err
}
