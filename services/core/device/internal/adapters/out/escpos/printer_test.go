// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package escpos

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"abarow.com/services/core/device/internal/domain"
)

func TestCodePage(t *testing.T) {
	pc, err := ParseCodePage("pc857")
	if err != nil || pc.Number != 13 {
		t.Fatalf("pc857: %+v %v", pc, err)
	}
	// The Turkish letters are in their places in IBM 857; '?' where there is no counterpart,
	// a space for a control character: the width does not change.
	got := pc.Encode("ÇĞİÖŞÜ çğıöşü A1 ₺\x1b")
	want := []byte{0x80, 0xA6, 0x98, 0x99, 0x9E, 0x9A, ' ', 0x87, 0xA7, 0x8D, 0x94, 0x9F, 0x81, ' ', 'A', '1', ' ', '?', ' '}
	if !bytes.Equal(got, want) {
		t.Errorf("pc857 % x", got)
	}
	win, err := ParseCodePage("WPC1254:47")
	if err != nil || win.Number != 47 {
		t.Fatalf("wpc1254: %+v %v", win, err)
	}
	if got := win.Encode("ğİş"); !bytes.Equal(got, []byte{0xF0, 0xDD, 0xFE}) {
		t.Errorf("wpc1254 % x", got)
	}
	for _, bad := range []string{"cp999", "pc857:300", "pc857:x"} {
		if _, err := ParseCodePage(bad); err == nil {
			t.Errorf("%q kabul edildi", bad)
		}
	}
}

func TestRender(t *testing.T) {
	pc, _ := ParseCodePage("pc857")
	p := New(USB{}, 32, pc)
	got := p.Render(domain.Document{Lines: []string{"ŞEKER", "TOPLAM *49,00"}})
	want := append([]byte{0x1b, '@', 0x1b, 't', 13, 0x9E, 'E', 'K', 'E', 'R', '\n'}, []byte("TOPLAM *49,00\n")...)
	want = append(want, 0x1b, 'd', 4, 0x1d, 'V', 1)
	if !bytes.Equal(got, want) {
		t.Errorf("% x", got)
	}
}

// Printer on the network: the document and the drawer command go over the connection.
func TestTCPPrinter(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	jobs := make(chan []byte, 2)
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			b, _ := io.ReadAll(c)
			c.Close()
			jobs <- b
		}
	}()
	target, err := ParseTarget("escpos-tcp:" + lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	pc, _ := ParseCodePage("pc857")
	p := New(target, 48, pc)
	if err := p.Print(context.Background(), domain.Document{Lines: []string{"Çay"}}); err != nil {
		t.Fatal(err)
	}
	if err := (Drawer{P: p}).Open(context.Background(), "nakit"); err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{p.Render(domain.Document{Lines: []string{"Çay"}}), {0x1b, 'p', 0, 25, 250}} {
		select {
		case got := <-jobs:
			if !bytes.Equal(got, want) {
				t.Errorf("yazıcıya giden % x, beklenen % x", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("yazıcıya bir şey gitmedi")
		}
	}
	// If the printer is off an error (the request does not hang).
	lis.Close()
	if err := p.Print(context.Background(), domain.Document{Lines: []string{"x"}}); err == nil || !strings.Contains(err.Error(), "bağlanılamadı") {
		t.Errorf("kapalı yazıcı: %v", err)
	}
}

// A USB printer is a device file; a plain file in the test.
func TestUSBPrinter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lp0")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	pc, _ := ParseCodePage("pc857")
	p := New(USB{Path: path}, 32, pc)
	if err := p.Print(context.Background(), domain.Document{Lines: []string{"Ekmek"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, p.Render(domain.Document{Lines: []string{"Ekmek"}})) {
		t.Errorf("% x", got)
	}
	if err := New(USB{Path: filepath.Join(t.TempDir(), "yok")}, 32, pc).Print(context.Background(), domain.Document{Lines: []string{"x"}}); err == nil {
		t.Error("olmayan aygıta basıldı")
	}
}

// stuck is a printer that is out of paper: the connection opens, data does not go.
type stuck struct{ openErr error }

func (s stuck) String() string { return "test" }
func (s stuck) Open(context.Context) (io.ReadWriteCloser, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	return s, nil
}
func (stuck) Write([]byte) (int, error) { return 0, os.ErrDeadlineExceeded }
func (stuck) Read([]byte) (int, error)  { return 0, os.ErrDeadlineExceeded }
func (stuck) Close() error              { return nil }

// On error the reason the cashier will understand is written.
func TestErrorHint(t *testing.T) {
	pc, _ := ParseCodePage("pc857")
	for _, c := range []struct {
		t    Transport
		want string
	}{
		{stuck{}, "kağıt bitmiş ya da kapak açık"},
		{stuck{openErr: &os.PathError{Op: "open", Path: "/dev/abarow-yazici", Err: syscall.ENXIO}}, "kapalı ya da bağlı değil"},
		// Printer on the network is off: the connection times out (not the paper).
		{stuck{openErr: &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}}, "kapalı ya da bağlı değil"},
	} {
		err := New(c.t, 32, pc).Print(context.Background(), domain.Document{Lines: []string{"x"}})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v; beklenen %q", err, c.want)
		}
	}
}

func TestParseTarget(t *testing.T) {
	for _, s := range []string{"escpos-usb:/dev/usb/lp0", "escpos-tcp:192.168.1.50:9100", "escpos-tcp:yazici.local:9100"} {
		if _, err := ParseTarget(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	for _, s := range []string{"escpos-usb:lp0", "escpos-tcp:192.168.1.50", "lpt1"} {
		if _, err := ParseTarget(s); err == nil {
			t.Errorf("%q kabul edildi", s)
		}
	}
}

func TestParseStatus(t *testing.T) {
	const ok = 0x12 // sabit bitler, sorun yok
	for _, c := range []struct {
		r    [3]byte
		want domain.PrinterState
	}{
		{[3]byte{ok, ok, ok}, domain.PrinterReady},
		{[3]byte{ok, ok, ok | 0x0c}, domain.PrinterPaperNearEnd},
		{[3]byte{ok | 0x08, ok | 0x20, ok | 0x60}, domain.PrinterPaperOut},
		// With the cover open the paper may look out too: the cover is reported.
		{[3]byte{ok | 0x08, ok | 0x04 | 0x20, ok | 0x60}, domain.PrinterCoverOpen},
		{[3]byte{ok | 0x08, ok | 0x40, ok}, domain.PrinterError},
		// While the paper feed button is pressed it looks offline: not a problem.
		{[3]byte{ok | 0x08, ok | 0x08, ok}, domain.PrinterReady},
		// An answer that does not match the format.
		{[3]byte{0x00, ok, ok}, domain.PrinterUnknown},
	} {
		if got := parseStatus(c.r); got != c.want {
			t.Errorf("% x → %v, beklenen %v", c.r, got, c.want)
		}
	}
}

// The printer on the network is asked its state: if it answers, its state; if it does
// not, unknown; if it is off, off.
func TestTCPState(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	answer := make(chan []byte, 2)
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			q := make([]byte, len(askStatus))
			if _, err := io.ReadFull(c, q); err == nil && bytes.Equal(q, askStatus) {
				c.Write(<-answer)
			}
			c.Close()
		}
	}()
	pc, _ := ParseCodePage("pc857")
	p := New(TCP{Addr: lis.Addr().String()}, 32, pc)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	answer <- []byte{0x12, 0x12 | 0x20, 0x12 | 0x60}
	if got := p.State(ctx); got != domain.PrinterPaperOut {
		t.Errorf("kağıt bitti: %v", got)
	}
	answer <- nil // a printer that does not answer
	if got := p.State(ctx); got != domain.PrinterUnknown {
		t.Errorf("yanıtsız: %v", got)
	}
	lis.Close()
	if got := p.State(ctx); got != domain.PrinterOffline {
		t.Errorf("kapalı: %v", got)
	}
}
