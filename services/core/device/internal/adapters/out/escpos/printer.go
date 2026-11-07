// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package escpos is a receipt printer that speaks ESC/POS (most thermal
// printers): over USB (/dev/usb/lp0) or on the network (TCP 9100). The document is plain text;
// the code page is selected, the lines are printed, the paper is advanced and cut. The cash
// drawer is connected to the printer and is opened with a command sent to the printer. The
// state (paper, cover) is asked with DLE EOT.
package escpos

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"abarow.com/services/core/device/internal/app"
	"abarow.com/services/core/device/internal/domain"
)

// timeout is the longest time for a document to go to the printer: if the printer is off
// or out of paper the request does not hang, it returns an error.
const timeout = 10 * time.Second

// ESC/POS commands.
var (
	initialize = []byte{0x1b, '@'}                  // ESC @: reset the printer
	feedCut    = []byte{0x1b, 'd', 4, 0x1d, 'V', 1} // ESC d 4: advance 4 lines; GS V 1: partial cut
	// ESC p 0 25 250: drawer pin 2, open for 50 ms, wait 500 ms.
	kickDrawer = []byte{0x1b, 'p', 0, 25, 250}
	// DLE EOT 1, 2, 4: instant state queries (printer, reason for offline,
	// paper sensor). The printer answers even when out of paper or with the cover
	// open; each answer is a single byte.
	askStatus = []byte{0x10, 0x04, 1, 0x10, 0x04, 2, 0x10, 0x04, 4}
)

// Transport is a connection to the printer; it is opened for each document and
// state query.
type Transport interface {
	Open(ctx context.Context) (io.ReadWriteCloser, error)
	String() string
}

// USB is the printer's device file (usblp: /dev/usb/lp0).
type USB struct{ Path string }

func (u USB) String() string { return u.Path }

func (u USB) Open(context.Context) (io.ReadWriteCloser, error) {
	// Reading is opened too: the state answer is read from the device (a bidirectional printer).
	f, err := os.OpenFile(u.Path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	// If the device can block, the write time is limited (paper out, printer off);
	// if it cannot (a plain file) it is unlimited.
	_ = f.SetWriteDeadline(time.Now().Add(timeout))
	return f, nil
}

// TCP is a printer on the network (usually port 9100, "raw").
type TCP struct{ Addr string }

func (t TCP) String() string { return t.Addr }

func (t TCP) Open(ctx context.Context) (io.ReadWriteCloser, error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "tcp", t.Addr)
	if err != nil {
		return nil, err
	}
	_ = c.SetWriteDeadline(time.Now().Add(timeout))
	return c, nil
}

// Printer implements app.Printer with ESC/POS.
type Printer struct {
	transport Transport
	columns   int
	page      CodePage

	mu sync.Mutex // so the documents do not mix: one does not start before the other finishes
}

var (
	_ app.Printer             = (*Printer)(nil)
	_ app.PrinterStatusReader = (*Printer)(nil)
)

// New returns an ESC/POS printer with columns columns.
func New(t Transport, columns int, page CodePage) *Printer {
	return &Printer{transport: t, columns: columns, page: page}
}

func (p *Printer) Name() string { return "escpos" }
func (p *Printer) Columns() int { return p.columns }

// Render is the bytes of the document that go to the printer: reset, code page,
// lines, advance and cut.
func (p *Printer) Render(doc domain.Document) []byte {
	var b []byte
	b = append(b, initialize...)
	b = append(b, 0x1b, 't', p.page.Number)
	for _, l := range doc.Lines {
		b = append(b, p.page.Encode(l)...)
		b = append(b, '\n')
	}
	return append(b, feedCut...)
}

// Print prints the validated document.
func (p *Printer) Print(ctx context.Context, doc domain.Document) error {
	return p.send(ctx, p.Render(doc))
}

func (p *Printer) send(ctx context.Context, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	w, err := p.transport.Open(ctx)
	if err != nil {
		return fmt.Errorf("yazıcıya (%s) bağlanılamadı%s: %w", p.transport, offHint(err), err)
	}
	if _, err := w.Write(data); err != nil {
		w.Close()
		return fmt.Errorf("yazıcıya (%s) yazılamadı%s: %w", p.transport, stuckHint(err), err)
	}
	return w.Close()
}

// offHint is the reason the cashier will understand when it cannot connect: over USB the device
// returns "missing", on the network the connection is refused or times out.
func offHint(err error) string {
	for _, e := range []error{syscall.ENXIO, syscall.ENODEV, syscall.ECONNREFUSED, syscall.EHOSTUNREACH, os.ErrDeadlineExceeded} {
		if errors.Is(err, e) {
			return " (yazıcı kapalı ya da bağlı değil)"
		}
	}
	return ""
}

// stuckHint: it connected but data does not go; when the paper is out or the cover is
// open the printer does not take the data, the write times out.
func stuckHint(err error) string {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return " (yazıcı veri almıyor: kağıt bitmiş ya da kapak açık olabilir)"
	}
	return ""
}

// State asks the printer for its state. If it cannot connect it is off; if it connects and
// gets no answer (a printer that does not report state or is one-way) it is unknown.
func (p *Printer) State(ctx context.Context) domain.PrinterState {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, err := p.transport.Open(ctx)
	if err != nil {
		return domain.PrinterOffline
	}
	defer c.Close()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(2 * time.Second)
	}
	if d, ok := c.(interface{ SetDeadline(time.Time) error }); ok {
		_ = d.SetDeadline(deadline)
	}
	var resp [3]byte
	if _, err := c.Write(askStatus); err != nil {
		return domain.PrinterUnknown
	}
	if _, err := io.ReadFull(c, resp[:]); err != nil {
		return domain.PrinterUnknown
	}
	return parseStatus(resp)
}

// parseStatus reads the answers to DLE EOT 1, 2 and 4; the most severe problem is
// reported. An answer that does not match the format (0xx1xx10) counts as unknown.
func parseStatus(r [3]byte) domain.PrinterState {
	for _, b := range r {
		if b&0x93 != 0x12 {
			return domain.PrinterUnknown
		}
	}
	printer, cause, paper := r[0], r[1], r[2]
	switch {
	case cause&0x04 != 0: // cover open
		return domain.PrinterCoverOpen
	case cause&0x20 != 0, paper&0x60 != 0: // paper out, printing stopped
		return domain.PrinterPaperOut
	case cause&0x40 != 0: // error (cutter, heating)
		return domain.PrinterError
	case printer&0x08 != 0 && cause&0x08 == 0: // offline, not the paper feed button
		return domain.PrinterError
	case paper&0x0c != 0: // paper low
		return domain.PrinterPaperNearEnd
	}
	return domain.PrinterReady
}

// Drawer is the cash drawer connected to the printer.
type Drawer struct{ P *Printer }

var _ app.Drawer = Drawer{}

// Open opens the drawer (a command goes to the printer; the paper does not advance).
func (d Drawer) Open(ctx context.Context, _ string) error {
	return d.P.send(ctx, kickDrawer)
}

// ParseTarget turns the setting "escpos-usb:/dev/abarow-printer" or
// "escpos-tcp:192.168.1.50:9100" into a connection.
func ParseTarget(s string) (Transport, error) {
	switch {
	case strings.HasPrefix(s, "escpos-usb:/dev/"):
		return USB{Path: strings.TrimPrefix(s, "escpos-usb:")}, nil
	case strings.HasPrefix(s, "escpos-tcp:"):
		addr := strings.TrimPrefix(s, "escpos-tcp:")
		if _, port, err := net.SplitHostPort(addr); err != nil || port == "" {
			return nil, fmt.Errorf("yazıcı adresi %q geçersiz (ör. escpos-tcp:192.168.1.50:9100)", addr)
		}
		return TCP{Addr: addr}, nil
	}
	return nil, fmt.Errorf("yazıcı %q bilinmiyor (simulator, escpos-usb:/dev/... ya da escpos-tcp:adres:9100)", s)
}
