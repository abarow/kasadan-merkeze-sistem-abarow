// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package pgtune

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestBasePoint(t *testing.T) {
	// Decision 05.11.2026: up to 4 registers 64 MB, 20 connections, 4 MB work_mem.
	for _, n := range []int{-3, 0, 1, 2, 4} {
		s := ForStore(n)
		if s.SharedBuffersMB != 64 || s.MaxConnections != 20 || s.WorkMemMB != 4 {
			t.Errorf("%d registers: %+v", n, s)
		}
	}
}

func TestDerived(t *testing.T) {
	s := ForStore(2)
	if s.EffectiveCacheSizeMB != 112 || s.MaintenanceWorkMemMB != 16 || s.WalBuffersMB != 2 ||
		s.MinWalSizeMB != 32 || s.MaxWalSizeMB != 256 {
		t.Errorf("derived settings: %+v", s)
	}
	// 2×64 + 128 = 256 MB; the cache told to the planner is 256 - 144 = 112 MB.
	if s.MemoryLimitMB != 256 {
		t.Errorf("memory limit %d, want 256", s.MemoryLimitMB)
	}
}

// The memory limit is 2 × shared_buffers + 128 MB (a multiple of 64): at least
// shared_buffers of room is left for the page cache. effective_cache_size does not exceed the limit.
func TestMemoryLimitFormula(t *testing.T) {
	for n, want := range map[int]int{1: 256, 4: 256, 5: 320, 9: 384, 10: 576, 20: 896, 49: 1024, 50: 1024, 1000: 1024} {
		s := ForStore(n)
		if s.MemoryLimitMB != want {
			t.Errorf("%d registers: memory limit %d, want %d", n, s.MemoryLimitMB, want)
		}
		inLimit := 0
		if s.ParallelWorkers > 0 {
			inLimit = s.ShmMB // /dev/shm is not page cache
		}
		if s.EffectiveCacheSizeMB != s.MemoryLimitMB-inLimit-144 || s.EffectiveCacheSizeMB >= s.MemoryLimitMB {
			t.Errorf("%d registers: effective_cache_size %d, limit %d", n, s.EffectiveCacheSizeMB, s.MemoryLimitMB)
		}
	}
}

// work_mem grows in steps with the number of registers; parallel query opens at 10 registers
// and takes more workers at 50, with a /dev/shm that fits it (see the package comment).
func TestWorkMemTiersAndParallel(t *testing.T) {
	for n, want := range map[int]int{0: 4, 4: 4, 5: 8, 16: 8, 17: 16, 50: 16} {
		if got := ForStore(n).WorkMemMB; got != want {
			t.Errorf("%d registers: work_mem %d, want %d", n, got, want)
		}
	}
	for _, n := range []int{1, 4, 9} {
		s := ForStore(n)
		for _, want := range []string{"-c max_parallel_workers_per_gather=0", "-c max_parallel_workers=0", "-c max_worker_processes=4"} {
			if !strings.Contains(s.Args(), want) {
				t.Errorf("%d registers: no %q: %s", n, want, s.Args())
			}
		}
		if s.ShmMB != DefaultShmMB {
			t.Errorf("%d registers: /dev/shm %d", n, s.ShmMB)
		}
	}
	for _, c := range []struct{ n, workers, shm int }{{10, 2, 192}, {16, 2, 192}, {17, 2, 384}, {49, 2, 384}, {50, 4, 384}, {1000, 4, 384}} {
		s := ForStore(c.n)
		for _, want := range []string{
			fmt.Sprintf("-c max_parallel_workers_per_gather=%d", c.workers),
			fmt.Sprintf("-c max_parallel_workers=%d", c.workers),
			fmt.Sprintf("-c max_worker_processes=%d", 4+c.workers),
		} {
			if !strings.Contains(s.Args(), want) {
				t.Errorf("%d registers: no %q: %s", c.n, want, s.Args())
			}
		}
		if s.ShmMB != c.shm {
			t.Errorf("%d registers: /dev/shm %d, want %d", c.n, s.ShmMB, c.shm)
		}
	}
	if got := ForHeadquarters(100).ShmMB; got != 768 {
		t.Errorf("headquarters, 100 registers: /dev/shm %d", got)
	}
}

func TestScalesWithRegisters(t *testing.T) {
	prev := ForStore(4)
	for _, n := range []int{5, 8, 16, 24, 100} {
		s := ForStore(n)
		if s.SharedBuffersMB < prev.SharedBuffersMB || s.MemoryLimitMB < prev.MemoryLimitMB {
			t.Errorf("%d registers: the settings got smaller: %+v < %+v", n, s, prev)
		}
		prev = s
	}
	if got := ForStore(8).SharedBuffersMB; got != 96 {
		t.Errorf("8 registers: shared_buffers %d, want 96", got)
	}
	if got := ForStore(1000).SharedBuffersMB; got != MaxSharedBuffersMB {
		t.Errorf("many registers: shared_buffers %d, upper limit %d", got, MaxSharedBuffersMB)
	}
	if got := ForStore(100).MaxWalSizeMB; got != 1600 {
		t.Errorf("100 registers: max_wal_size %d, want 1600", got)
	}
	if ForStore(1000).MaintenanceWorkMemMB != 64 || ForStore(1000).WalBuffersMB != 8 {
		t.Errorf("upper limits: %+v", ForStore(1000))
	}
}

// The memory limit covers the settings' own memory need: buffers plus the
// connections in the pools plus the background share.
func TestMemoryLimitCoversNeed(t *testing.T) {
	for n := 0; n <= 64; n++ {
		s := ForStore(n)
		need := s.SharedBuffersMB + perConnectionMB*PoolConnections + overheadMB
		if s.MemoryLimitMB < need || s.MemoryLimitMB%roundMB != 0 {
			t.Errorf("%d registers: limit %d, need %d", n, s.MemoryLimitMB, need)
		}
	}
}

// Connection budget: the pools, the administrator share and the backup/psql connection
// fit in max_connections.
func TestConnectionBudget(t *testing.T) {
	if PoolConnections+ReservedConnections+1 > MaxConnections {
		t.Errorf("pools %d + reserved %d + 1 > %d", PoolConnections, ReservedConnections, MaxConnections)
	}
}

func TestFlags(t *testing.T) {
	args := ForStore(2).Args()
	for _, want := range []string{
		"-c shared_buffers=64MB", "-c max_connections=20", "-c work_mem=4MB",
		"-c effective_cache_size=112MB", "-c max_parallel_workers_per_gather=0",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("no %q: %s", want, args)
		}
	}
	flags := ForStore(2).Flags()
	if len(flags)%2 != 0 {
		t.Fatalf("the flags should come in pairs: %v", flags)
	}
	for i := 0; i < len(flags); i += 2 {
		if flags[i] != "-c" || !strings.Contains(flags[i+1], "=") {
			t.Errorf("bayrak %d: %q %q", i/2, flags[i], flags[i+1])
		}
	}
}

// Headquarters setting: the same logic, higher ceilings; since the database grows with the number of
// stores the memory grows up to 1 GB of buffers.
func TestForHeadquarters(t *testing.T) {
	// 1 store × 2 registers = 2 registers: the base.
	s := ForHeadquarters(2)
	if s.SharedBuffersMB != 64 || s.MaxConnections != 30 || s.WorkMemMB != 4 || s.MemoryLimitMB != 256 {
		t.Errorf("2 registers: %+v", s)
	}
	// 50 stores × 2 registers = 100 registers: 64 + 8 × 96 = 832 MB of buffers; memory 2 × 832 + 128 = 1792 MB
	// and 768 MB of /dev/shm for parallel query.
	s = ForHeadquarters(100)
	if s.SharedBuffersMB != 832 || s.MemoryLimitMB != 2560 || s.WorkMemMB != 32 || s.MaxWalSizeMB != 1600 || s.MaintenanceWorkMemMB != 128 {
		t.Errorf("100 registers: %+v", s)
	}
	if got := ForHeadquarters(100000).SharedBuffersMB; got != MaxHeadquartersSharedBuffersMB {
		t.Errorf("tavan: %d", got)
	}
	// Memory always covers the buffers, the pool connections and the background share.
	for n := 0; n <= 400; n += 7 {
		s := ForHeadquarters(n)
		need := s.SharedBuffersMB + perConnectionMB*HeadquartersPoolConnections + overheadMB
		if s.MemoryLimitMB < need || s.MemoryLimitMB%roundMB != 0 || s.EffectiveCacheSizeMB >= s.MemoryLimitMB {
			t.Errorf("%d registers: %+v (need %d)", n, s, need)
		}
	}
	// Not smaller than the store server at any number of registers.
	for n := 0; n <= 200; n++ {
		if h, b := ForHeadquarters(n), ForStore(n); h.SharedBuffersMB < b.SharedBuffersMB || h.MemoryLimitMB < b.MemoryLimitMB || h.WorkMemMB < b.WorkMemMB {
			t.Errorf("%d registers: headquarters %+v is smaller than the store server %+v", n, h, b)
		}
	}
	if HeadquartersPoolConnections+ReservedConnections+1 > HeadquartersMaxConnections {
		t.Error("the headquarters connection budget was exceeded")
	}
}

// Headquarters on three machines: the pools of the three machines, Patroni's connections and
// the reserve fit; memory covers the larger pool; one machine is ForHeadquarters.
func TestForHeadquartersCluster(t *testing.T) {
	if ForHeadquartersCluster(100, 1) != ForHeadquarters(100) {
		t.Error("one machine differs from ForHeadquarters")
	}
	for _, n := range []int{0, 10, 100, 400} {
		s := ForHeadquartersCluster(n, 3)
		if need := 3*HeadquartersPoolConnections + PatroniConnections + ReservedConnections + 1; s.MaxConnections < need {
			t.Errorf("%d registers: max_connections %d < %d", n, s.MaxConnections, need)
		}
		if need := s.SharedBuffersMB + perConnectionMB*3*HeadquartersPoolConnections + overheadMB; s.MemoryLimitMB < need {
			t.Errorf("%d registers: memory %d < %d", n, s.MemoryLimitMB, need)
		}
		if one := ForHeadquarters(n); s.SharedBuffersMB != one.SharedBuffersMB || s.WorkMemMB != one.WorkMemMB {
			t.Errorf("%d registers: %+v differs from one machine %+v", n, s, one)
		}
	}
}

// The register's Compose and Quadlet files carry the same values as ForRegister.
func TestRegisterDeployFiles(t *testing.T) {
	r := ForRegister()
	read := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile("../../deploy/" + rel)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if q := read("quadlet/abarow-register-db.container"); !strings.Contains(q, "Exec=postgres "+r.Args()+"\n") ||
		!strings.Contains(q, fmt.Sprintf("PodmanArgs=--memory=%dm\n", r.MemoryLimitMB)) {
		t.Errorf("the Quadlet file differs from ForRegister:\n%s", q)
	}
	var quoted []string
	for _, f := range append([]string{"postgres"}, r.Flags()...) {
		quoted = append(quoted, `"`+f+`"`)
	}
	c := read("compose/compose.yaml")
	if !strings.Contains(c, "command: ["+strings.Join(quoted, ", ")+"]") || !strings.Contains(c, fmt.Sprintf("mem_limit: %dm", r.MemoryLimitMB)) {
		t.Error("the Compose register-db differs from ForRegister")
	}
}
