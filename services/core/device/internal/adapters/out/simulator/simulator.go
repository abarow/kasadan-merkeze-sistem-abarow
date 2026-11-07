// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package simulator is the simulator that stands in for the printer, drawer and barcode
// reader: it writes the documents to an io.Writer (standard output), writes the drawer
// opening to the log; scans are imitated from tests. In Docker it is seen with
// "docker compose logs device".
package simulator

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"abarow.com/services/core/device/internal/app"
	"abarow.com/services/core/device/internal/domain"
)

// Printer implements app.Printer with an io.Writer. Its state can be set
// (SimulatePrinter): a simulator that is out of paper, has the cover open, is off or has an
// error does not print; printing fails as on a real printer.
type Printer struct {
	mu      sync.Mutex
	w       io.Writer
	columns int
	state   domain.PrinterState
}

var (
	_ app.Printer             = (*Printer)(nil)
	_ app.PrinterStatusReader = (*Printer)(nil)
	_ app.PrinterSimulator    = (*Printer)(nil)
)

// NewPrinter imitates a ready printer with columns columns.
func NewPrinter(w io.Writer, columns int) *Printer {
	return &Printer{w: w, columns: columns, state: domain.PrinterReady}
}

func (p *Printer) Name() string { return "simulator" }
func (p *Printer) Columns() int { return p.columns }

// State, ayarlanan durumdur.
func (p *Printer) State(context.Context) domain.PrinterState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// SetState, durumu ayarlar.
func (p *Printer) SetState(st domain.PrinterState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state = st
}

// Print writes the document with paper margins; concurrent documents do not mix.
func (p *Printer) Print(_ context.Context, doc domain.Document) error {
	if st := p.State(context.Background()); st.Blocks() {
		return fmt.Errorf("%s (simülatör)", st.Message())
	}
	edge := "+" + strings.Repeat("-", p.columns) + "+"
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", edge, doc.Title)
	for _, l := range doc.Lines {
		b.WriteString("|" + l + strings.Repeat(" ", p.columns-len([]rune(l))) + "|\n")
	}
	b.WriteString(edge + " ✂\n")
	p.mu.Lock()
	defer p.mu.Unlock()
	_, err := io.WriteString(p.w, b.String())
	return err
}

// Drawer writes the drawer opening to the log.
type Drawer struct {
	Log *slog.Logger
}

var _ app.Drawer = Drawer{}

func (d Drawer) Open(_ context.Context, reason string) error {
	d.Log.Info("drawer opened (simulator)", "reason", reason)
	return nil
}

// Scanner is a barcode reader whose scans are imitated.
type Scanner struct {
	codes chan string
}

var (
	_ app.Scanner       = (*Scanner)(nil)
	_ app.ScanSimulator = (*Scanner)(nil)
)

// NewScanner returns a simulator reader.
func NewScanner() *Scanner { return &Scanner{codes: make(chan string, 16)} }

func (s *Scanner) Name() string { return "simulator" }

// Run gives the imitated scans to emit until ctx ends.
func (s *Scanner) Run(ctx context.Context, emit func(string)) {
	for {
		select {
		case <-ctx.Done():
			return
		case code := <-s.codes:
			emit(code)
		}
	}
}

// Simulate imitates a scan; if the queue is full the scan is dropped (on a real
// reader a read nobody listens to is lost too).
func (s *Scanner) Simulate(code string) {
	select {
	case s.codes <- code:
	default:
	}
}

// Scale is a scale whose reading is set; it is empty at first.
type Scale struct {
	mu sync.Mutex
	r  domain.Reading
}

var (
	_ app.Scale          = (*Scale)(nil)
	_ app.ScaleSimulator = (*Scale)(nil)
)

// NewScale returns an empty simulator scale.
func NewScale() *Scale { return &Scale{r: domain.Reading{Stable: true}} }

func (s *Scale) Name() string { return "simulator" }

func (s *Scale) Read(context.Context) (domain.Reading, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.r, nil
}

func (s *Scale) SetReading(r domain.Reading) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.r = r
}

// NoScanner is a register with no reader connected.
type NoScanner struct{}

var _ app.Scanner = NoScanner{}

func (NoScanner) Name() string { return "none" }

func (NoScanner) Run(ctx context.Context, _ func(string)) { <-ctx.Done() }
