// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package pgtune computes the PostgreSQL settings on the store server by the
// store's number of registers. The database is small (a store's daily sales)
// and a few services connect to it; the default settings (128 MB buffers, 100
// connections) are larger than this job needs.
//
// The fixed points (decision 05.11.2026): up to 4 registers shared_buffers 64 MB,
// max_connections 20, work_mem 4 MB. The other settings derive from these:
//
//   - max_connections 20 = the services' connection pools (pgpool: report 8,
//     inventory 4, catalog-admin 4 = 16) + superuser_reserved_connections (3)
//   - 1 for backup/psql. 20 connections are not enough unless the pools are limited.
//   - The sales volume grows with the number of registers: after the fourth register each register
//     adds 8 MB to shared_buffers (at most 256 MB); the WAL size grows too.
//   - Container memory = 2 × shared_buffers + 128 MB (rounded up to a multiple
//     of 64 MB; at least shared_buffers + 6 MB for the connections in the pools
//   - 48 MB for the background share). At least shared_buffers of room is left for
//     the operating system page cache: in measurement (10 and 50 registers) memory hit the
//     ceiling when the limit was just shared_buffers + the connection share.
//   - effective_cache_size = container memory - 144 MB: it tells the planner the cache
//     it can actually use (telling it more than the container has makes it choose
//     a wrong plan).
//   - work_mem is 4 MB up to 4 registers, 8 MB up to 16 registers, 16 MB beyond that:
//     as the data grows the summary queries' hash joins no longer fit in 4 MB and spill
//     to disk (at 50 registers it was split into 4 parts). The memory limit is
//     set aside accordingly.
//   - maintenance_work_mem = shared_buffers / 4 (at most 64 MB), wal_buffers =
//     shared_buffers / 32 (1–16 MB).
//   - Parallel query is off: in the 50-register measurement, opening 2 workers per query
//     the workers did not fit in the container's 64 MB /dev/shm area ("could not resize
//     shared memory segment"; the month and product summaries gave errors) and the
//     record processing latency got worse (p95 680 → 1109 ms).
package pgtune

import (
	"fmt"
	"strings"
)

// Fixed points and limits.
const (
	// BaseRegisters is the number of registers for which the base settings are enough.
	BaseRegisters = 4
	// BaseSharedBuffersMB is shared_buffers up to the base number of registers.
	BaseSharedBuffersMB = 64
	// ExtraSharedBuffersMB is what each register adds after the base number of registers.
	ExtraSharedBuffersMB = 8
	// MaxSharedBuffersMB is the upper limit of shared_buffers.
	MaxSharedBuffersMB = 256
	// MaxConnections is the connection limit of the store server database.
	MaxConnections = 20
	// ReservedConnections are the connections set aside for the administrator
	// (superuser_reserved_connections; PostgreSQL's default).
	ReservedConnections = 3
	// WorkMemMB is the memory per sort and hash operation up to 4 registers.
	WorkMemMB = 4
	// PoolConnections is the total of the services' connection pools
	// (the pkg/pgpool defaults).
	PoolConnections = 16
	// perConnectionMB is the approximate private memory of a connected session.
	perConnectionMB = 6
	// overheadMB is the share of the postmaster and background workers.
	overheadMB = 48
	// roundMB is the step the container memory limit is rounded to.
	roundMB = 64
)

// Settings are the PostgreSQL settings (memory in MB).
type Settings struct {
	SharedBuffersMB      int
	MaxConnections       int
	WorkMemMB            int
	MaintenanceWorkMemMB int
	WalBuffersMB         int
	EffectiveCacheSizeMB int
	MinWalSizeMB         int
	MaxWalSizeMB         int
	// MemoryLimitMB is the recommended memory limit for the database container.
	MemoryLimitMB int
}

// ForStore computes the settings of a store (store server) with registers registers.
// A negative number counts as zero.
func ForStore(registers int) Settings {
	return compute(max(registers, 0), profile{
		maxShared: MaxSharedBuffersMB, pool: PoolConnections, maxConns: MaxConnections, maxMaintenance: 64, bigWorkMem: 16,
	})
}

// Constants for headquarters. Headquarters holds the data of all the stores
// (~110 thousand sales per register per year; queries look at the recent months) and the database
// grows with the number of stores: 50 stores × 2 registers × 30 days = 900 thousand sales, 2 GB.
const (
	// HeadquartersRegistersPerStore is the default number of registers per store for the headquarters
	// setting (2 in the measurements; used when the real number is not known).
	HeadquartersRegistersPerStore = 2
	// MaxHeadquartersSharedBuffersMB is the upper limit of shared_buffers at headquarters.
	MaxHeadquartersSharedBuffersMB = 1024
	// HeadquartersPoolConnections is the total of the services' pools at headquarters (reporting 8,
	// ingest 4, catalog-admin 4, inventory 4).
	HeadquartersPoolConnections = 20
	// HeadquartersMaxConnections is the headquarters database's connection limit: pools (20) +
	// reserved (3) + backup/psql (1) + a share for new services.
	HeadquartersMaxConnections = 30
)

// ForHeadquarters computes the headquarters database's settings by the stores' total number
// of registers (registers). The same logic as the store server formula, with higher
// ceilings: shared_buffers goes up to 1 GB, work_mem to 32 MB.
// In measurement (50 stores, 100 registers, 2 GB of data) with the store server settings (256 MB buffers,
// 640 MB memory) the monthly summary took 27 s and the product ranking never finished; with
// large memory the monthly summary came down to 15 s and the record processing p95 from 2.5 s to 0.75 s.
func ForHeadquarters(registers int) Settings {
	return compute(max(registers, 0), profile{
		maxShared: MaxHeadquartersSharedBuffersMB, pool: HeadquartersPoolConnections, maxConns: HeadquartersMaxConnections, maxMaintenance: 128, bigWorkMem: 32,
	})
}

// profile holds the constants where ForStore and ForHeadquarters diverge.
type profile struct {
	maxShared, pool, maxConns, maxMaintenance int
	// bigWorkMem is work_mem after 40 registers (at the store server 16 MB after 16
	// registers; at headquarters 32 MB after 40 registers).
	bigWorkMem int
}

func compute(registers int, p profile) Settings {
	shared := min(BaseSharedBuffersMB+ExtraSharedBuffersMB*max(registers-BaseRegisters, 0), p.maxShared)
	overhead := perConnectionMB*p.pool + overheadMB
	limit := roundUp(max(shared+overhead, 2*shared+128), roundMB)
	wm := workMem(registers)
	if p.bigWorkMem > 16 && registers > 40 {
		wm = p.bigWorkMem
	}
	return Settings{
		SharedBuffersMB:      shared,
		MaxConnections:       p.maxConns,
		WorkMemMB:            wm,
		MaintenanceWorkMemMB: min(shared/4, p.maxMaintenance),
		WalBuffersMB:         min(max(shared/32, 1), 16),
		EffectiveCacheSizeMB: limit - overhead,
		MinWalSizeMB:         32,
		MaxWalSizeMB:         max(256, 16*registers),
		MemoryLimitMB:        limit,
	}
}

// workMem is the sort/hash memory by the number of registers (the store server base).
func workMem(registers int) int {
	switch {
	case registers <= BaseRegisters:
		return WorkMemMB
	case registers <= 16:
		return 2 * WorkMemMB
	}
	return 4 * WorkMemMB
}

func roundUp(n, step int) int { return (n + step - 1) / step * step }

// Flags are the "-c setting=value" pairs to be given to the postgres command.
func (s Settings) Flags() []string {
	kv := []struct {
		k string
		v string
	}{
		{"shared_buffers", mb(s.SharedBuffersMB)},
		{"max_connections", fmt.Sprint(s.MaxConnections)},
		{"work_mem", mb(s.WorkMemMB)},
		{"maintenance_work_mem", mb(s.MaintenanceWorkMemMB)},
		{"wal_buffers", mb(s.WalBuffersMB)},
		{"effective_cache_size", mb(s.EffectiveCacheSizeMB)},
		{"min_wal_size", mb(s.MinWalSizeMB)},
		{"max_wal_size", mb(s.MaxWalSizeMB)},
		{"checkpoint_completion_target", "0.9"},
		{"autovacuum_max_workers", "2"},
		{"max_worker_processes", "4"},
		{"max_parallel_workers", "0"},
		{"max_parallel_workers_per_gather", "0"},
		{"max_parallel_maintenance_workers", "0"},
	}
	out := make([]string, 0, 2*len(kv))
	for _, e := range kv {
		out = append(out, "-c", e.k+"="+e.v)
	}
	return out
}

// Args is Flags on a single line (for the unit file's Exec line).
func (s Settings) Args() string { return strings.Join(s.Flags(), " ") }

func mb(n int) string { return fmt.Sprintf("%dMB", n) }
