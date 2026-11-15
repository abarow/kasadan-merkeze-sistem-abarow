// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package memory is the app.Counter adapter kept in memory. When the service restarts the
// numbers start from 1 again; that is enough for the simulator.
package memory

import (
	"context"
	"sync/atomic"
)

// Counter implements app.Counter in memory.
type Counter struct {
	n atomic.Int64
}

// Next returns the next number.
func (c *Counter) Next(context.Context) (int64, error) { return c.n.Add(1), nil }
