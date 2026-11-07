// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package writer is the app.Printer adapter that writes the receipt to an io.Writer (e.g.
// standard output). It is used in development in place of a printer.
package writer

import (
	"context"
	"io"
	"log/slog"
	"sync"
)

// Printer implements app.Printer with an io.Writer.
type Printer struct {
	mu sync.Mutex
	w  io.Writer
}

// New returns a Printer that writes to w.
func New(w io.Writer) *Printer { return &Printer{w: w} }

// Drawer is used when there is no drawer; it only writes the opening to the log.
type Drawer struct {
	Log *slog.Logger
}

func (d Drawer) Open(_ context.Context, reason string) {
	d.Log.Info("the drawer would open (there is no device)", "reason", reason)
}

// Print writes the receipt text; concurrent receipts do not get mixed up.
func (p *Printer) Print(_ context.Context, _, text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, err := io.WriteString(p.w, text)
	return err
}
