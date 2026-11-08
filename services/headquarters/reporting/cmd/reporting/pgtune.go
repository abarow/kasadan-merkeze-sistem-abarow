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

// pgtuneCommand prints the database settings by the number of stores connected to
// headquarters (pkg/pgtune.ForHeadquarters). The install script puts the output in the
// database container's unit drop-in file; it is run again when a store is added or
// removed:
//
//	reporting pgtune [--stores N | --stores-file stores.json] [--registers-per-store K]
//
// Since the number of registers per store is not known the default is 2. The output is three
// lines: MEMORY_MB=<container memory limit>, ARGS=<the -c flags to give to postgres> and
// SHM_MB=<size of /dev/shm>.
func pgtuneCommand(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("pgtune", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	stores := fs.Int("stores", -1, "mağaza sayısı; verilmezse mağaza listesinden okunur")
	file := fs.String("stores-file", service.Env("ABAROW_STORES", "/config/stores.json"), "mağaza listesi")
	per := fs.Int("registers-per-store", pgtune.HeadquartersRegistersPerStore, "mağaza başına kasa")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("kullanım: reporting pgtune [--stores N | --stores-file dosya] [--registers-per-store K]: %w", err)
	}
	n := *stores
	if n < 0 {
		list, err := nodes.Load(*file)
		if err != nil {
			return err
		}
		n = len(list)
	}
	s := pgtune.ForHeadquarters(n * max(*per, 1))
	_, err := fmt.Fprintf(out, "MEMORY_MB=%d\nARGS=%s\nSHM_MB=%d\n", s.MemoryLimitMB, s.Args(), s.ShmMB)
	return err
}
