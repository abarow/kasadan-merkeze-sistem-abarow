// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package memory_test

import (
	"context"
	"sync"
	"testing"

	"abarow.com/services/fiscal/simulator/internal/adapters/out/memory"
)

func TestCounterIsSequentialAndSafe(t *testing.T) {
	var c memory.Counter
	var wg sync.WaitGroup
	seen := make([]bool, 101)
	var mu sync.Mutex
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := c.Next(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if seen[n] {
				t.Errorf("%d iki kez verildi", n)
			}
			seen[n] = true
		}()
	}
	wg.Wait()
	for i := 1; i <= 100; i++ {
		if !seen[i] {
			t.Errorf("%d hiç verilmedi", i)
		}
	}
}
