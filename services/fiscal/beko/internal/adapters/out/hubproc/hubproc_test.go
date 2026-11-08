// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package hubproc

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/money"
	"abarow.com/services/fiscal/beko/internal/app"
	"abarow.com/services/fiscal/beko/internal/domain"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestMain also runs the test binary as its own bridge imitation
// (HUBPROC_FAKE=1): it speaks hubd's protocol, there is no library.
func TestMain(m *testing.M) {
	if os.Getenv("HUBPROC_FAKE") == "1" {
		fakeBridge()
		return
	}
	os.Exit(m.Run())
}

func frame(w io.Writer, kind, ref, payload string) {
	fmt.Fprintf(w, "%s %s %d\n%s", kind, ref, len(payload), payload)
}

func fakeBridge() {
	frame(os.Stdout, "ready", "0", "")
	r := bufio.NewReader(os.Stdin)
	for {
		head, err := r.ReadString('\n')
		if err != nil {
			return
		}
		f := strings.Fields(head)
		n, _ := strconv.Atoi(f[2])
		payload := make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			return
		}
		switch f[0] {
		case "echo":
			frame(os.Stdout, "ok", f[1], string(payload))
		case "fail":
			frame(os.Stdout, "error", f[1], "cihaz yok")
		case "event":
			frame(os.Stdout, "state", "1", "X30TR")
			frame(os.Stdout, "ok", f[1], "")
		case "crash":
			os.Exit(3)
		}
	}
}

func fake(t *testing.T) *Process {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HUBPROC_FAKE", "1")
	p := New(quiet(), exe)
	p.Restart = 20 * time.Millisecond
	return p
}

func next(t *testing.T, p *Process) app.Event {
	t.Helper()
	select {
	case ev := <-p.Events():
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event came")
		return app.Event{}
	}
}

func TestCallAndEvents(t *testing.T) {
	p := fake(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	if ev := next(t, p); ev.Kind != "ready" {
		t.Fatalf("first event %+v", ev)
	}
	// The body is binary safe: line endings and Turkish characters are preserved.
	body := "ilk satır\nikinci ş ğ İ\x00son"
	out, err := p.Call(ctx, "echo", []byte(body))
	if err != nil || string(out) != body {
		t.Errorf("echo: %q %v", out, err)
	}
	if _, err := p.Call(ctx, "fail", nil); err == nil || err.Error() != "cihaz yok" {
		t.Errorf("error answer: %v", err)
	}
	// The event comes before the operation's response and is read from a separate channel.
	if _, err := p.Call(ctx, "event", nil); err != nil {
		t.Fatal(err)
	}
	if ev := next(t, p); ev.Kind != "state" || ev.Ref != "1" || string(ev.Payload) != "X30TR" {
		t.Errorf("state event: %+v", ev)
	}
}

func TestRestart(t *testing.T) {
	p := fake(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	next(t, p) // ready
	// When the bridge dies the pending call gets an error, an "exit" event comes, the bridge
	// reopens.
	if _, err := p.Call(ctx, "crash", nil); err == nil {
		t.Error("an answer came from the crashed bridge")
	}
	if ev := next(t, p); ev.Kind != "exit" {
		t.Errorf("crash event: %+v", ev)
	}
	if ev := next(t, p); ev.Kind != "ready" {
		t.Errorf("reopening: %+v", ev)
	}
	if out, err := p.Call(ctx, "echo", []byte("tamam")); err != nil || string(out) != "tamam" {
		t.Errorf("reopened bridge: %q %v", out, err)
	}
	cancel()
	for range 5 { // a call after the close event is rejected
		if _, err := p.Call(context.Background(), "echo", nil); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRealBridge runs the real hubd bridge against a fake SDK
// (make -C services/fiscal/beko/hub fake; ABAROW_BEKO_HELPER=.../build/hubd-fake):
// the whole pipeline from Go to the C++ bridge, from there to the (fake) library and back.
func TestRealBridge(t *testing.T) {
	path := os.Getenv("ABAROW_BEKO_HELPER")
	if path == "" {
		t.Skip("ABAROW_BEKO_HELPER is not set (make -C services/fiscal/beko/hub fake)")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the bridge runs only on Linux")
	}
	if _, err := exec.LookPath(path); err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(path)
	p := New(quiet(), abs, "test")
	cfg := domain.Config{Sections: map[money.Rate]int{1000: 1, 2000: 2}}
	svc := app.NewService(p, cfg, 5*time.Second, 50*time.Millisecond, quiet())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	go svc.Run(ctx)
	for range 200 {
		if svc.Ready() == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := svc.Ready(); err != nil {
		t.Fatal(err)
	}

	r := domain.Receipt{SaleID: "satis-1", Total: 1500, Payments: []domain.Payment{{Method: "cash", Amount: 2000}}, Change: 500,
		Lines: []domain.Line{{Name: "ŞEKER", Qty: 1000, UnitPrice: 1500, Total: 1500, TaxRate: 2000}}}
	no, err := svc.Register(ctx, r)
	if err != nil || no != "Z0007-000001" {
		t.Fatalf("receipt: %q %v", no, err)
	}
	out, err := p.Call(ctx, "device", nil)
	if err != nil || string(out) != "0" {
		t.Errorf("etkin cihaz: %q %v", out, err)
	}

	r2 := r
	r2.SaleID = "satis-2"
	r2.Lines = []domain.Line{{Name: "RED ürünü", Qty: 1000, UnitPrice: 1500, Total: 1500, TaxRate: 2000}}
	if _, err := svc.Register(ctx, r2); err == nil || !strings.Contains(err.Error(), "RED") {
		t.Errorf("refused sale: %v", err)
	}
}

// TestRealBridgeEncoding checks that the basket reaches the device as ISO-8859-9 bytes
// (Token's example converts it this way too): Ş = 0xDE, ş = 0xFE, İ = 0xDD.
func TestRealBridgeEncoding(t *testing.T) {
	path := os.Getenv("ABAROW_BEKO_HELPER")
	if path == "" || runtime.GOOS != "linux" {
		t.Skip("ABAROW_BEKO_HELPER is not set or this is not Linux")
	}
	abs, _ := filepath.Abs(path)
	p := New(quiet(), abs, "test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	for next(t, p).Kind != "ready" {
	}
	out, err := p.Call(ctx, "basket", []byte(`{"basketID":"k1","items":[{"name":"ŞEKER ş İ"}]}`))
	if err != nil || string(out) != "1" {
		t.Fatalf("basket: %q %v", out, err)
	}
	for {
		ev := next(t, p)
		if ev.Kind != "serial" || ev.Ref != "3" {
			continue
		}
		want := hex.EncodeToString([]byte("ŞEKER ş İ"))
		_ = want // it must not be the UTF-8 form
		latin5 := hex.EncodeToString([]byte{0xde, 'E', 'K', 'E', 'R', ' ', 0xfe, ' ', 0xdd})
		if !strings.Contains(string(ev.Payload), latin5) || strings.Contains(string(ev.Payload), want) {
			t.Errorf("the bytes sent to the device are not ISO-8859-9: %s", ev.Payload)
		}
		return
	}
}
