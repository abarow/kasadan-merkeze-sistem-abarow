// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package grpcserver_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	devicev1 "abarow.com/gen/com/abarow/device/v1"

	"abarow.com/pkg/grpctest"
	"abarow.com/services/core/device/internal/adapters/in/grpcserver"
	"abarow.com/services/core/device/internal/adapters/out/simulator"
	"abarow.com/services/core/device/internal/app"
)

type brokenDrawer struct{}

func (brokenDrawer) Open(context.Context, string) error { return errors.New("çekmece bağlı değil") }

// start sets up a device service with simulator hardware and returns its client.
func start(t *testing.T, drawer app.Drawer, scale bool, scanner app.Scanner) (devicev1.DeviceServiceClient, *bytes.Buffer) {
	t.Helper()
	var paper bytes.Buffer
	svc := app.NewService(simulator.NewPrinter(&paper, 32), drawer, scanner)
	if scale {
		svc.WithScale(simulator.NewScale())
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go svc.RunScanner(ctx)
	go svc.RunPrinterMonitor(ctx, 20*time.Millisecond)
	for deadline := time.Now().Add(2 * time.Second); svc.PrinterState() == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	conn := grpctest.Conn(t, func(s *grpc.Server) { devicev1.RegisterDeviceServiceServer(s, grpcserver.New(svc)) })
	return devicev1.NewDeviceServiceClient(conn), &paper
}

func ok() app.Drawer { return simulator.Drawer{Log: slog.New(slog.DiscardHandler)} }

func code(err error) codes.Code { return status.Code(err) }

func TestPrintDrawerStatus(t *testing.T) {
	c, paper := start(t, ok(), false, simulator.NoScanner{})
	ctx := context.Background()

	if _, err := c.Print(ctx, &devicev1.PrintRequest{Title: "Fiş", Lines: []string{"ÖRNEK"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(paper.String(), "ÖRNEK") {
		t.Errorf("paper output: %q", paper.String())
	}
	if _, err := c.Print(ctx, &devicev1.PrintRequest{Lines: []string{strings.Repeat("x", 40)}}); code(err) != codes.InvalidArgument {
		t.Errorf("wide line: %v", err)
	}
	if _, err := c.OpenDrawer(ctx, &devicev1.OpenDrawerRequest{Reason: "satış"}); err != nil {
		t.Fatal(err)
	}

	st, err := c.GetStatus(ctx, &devicev1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if st.GetPrinter() != "simulator" || st.GetColumns() != 32 || st.GetPrinted() != 1 || st.GetDrawerOpened() != 1 ||
		st.GetPrinterState() != devicev1.PrinterState_PRINTER_STATE_READY {
		t.Errorf("durum: %v", st)
	}

	// If the drawer fails the service returns unavailable (Unavailable).
	bad, _ := start(t, brokenDrawer{}, false, simulator.NoScanner{})
	if _, err := bad.OpenDrawer(ctx, &devicev1.OpenDrawerRequest{}); code(err) != codes.Unavailable {
		t.Errorf("faulty drawer: %v", err)
	}
}

func TestPrinterStates(t *testing.T) {
	c, _ := start(t, ok(), false, simulator.NoScanner{})
	ctx := context.Background()

	if _, err := c.SimulatePrinter(ctx, &devicev1.SimulatePrinterRequest{State: devicev1.PrinterState_PRINTER_STATE_PAPER_OUT}); err != nil {
		t.Fatal(err)
	}
	// When the paper runs out printing is refused and the state message is reported.
	if _, err := c.Print(ctx, &devicev1.PrintRequest{Lines: []string{"x"}}); code(err) != codes.Unavailable {
		t.Errorf("printing without paper: %v", err)
	}
	st, _ := c.GetStatus(ctx, &devicev1.GetStatusRequest{})
	if st.GetPrinterState() != devicev1.PrinterState_PRINTER_STATE_PAPER_OUT || st.GetPrinterMessage() != "Kağıt bitti" {
		t.Errorf("durum: %v", st)
	}
	if _, err := c.SimulatePrinter(ctx, &devicev1.SimulatePrinterRequest{State: devicev1.PrinterState(99)}); code(err) != codes.InvalidArgument {
		t.Errorf("invalid state: %v", err)
	}
	if _, err := c.SimulatePrinter(ctx, &devicev1.SimulatePrinterRequest{State: devicev1.PrinterState_PRINTER_STATE_READY}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Print(ctx, &devicev1.PrintRequest{Lines: []string{"x"}}); err != nil {
		t.Errorf("ready printer: %v", err)
	}
}

func TestWatchPrinter(t *testing.T) {
	c, _ := start(t, ok(), false, simulator.NoScanner{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := c.WatchPrinter(ctx, &devicev1.WatchPrinterRequest{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := stream.Recv()
	if err != nil || first.GetState() != devicev1.PrinterState_PRINTER_STATE_READY {
		t.Fatalf("ilk durum: %v %v", first, err)
	}
	if _, err := c.SimulatePrinter(ctx, &devicev1.SimulatePrinterRequest{State: devicev1.PrinterState_PRINTER_STATE_COVER_OPEN}); err != nil {
		t.Fatal(err)
	}
	next, err := stream.Recv()
	if err != nil || next.GetState() != devicev1.PrinterState_PRINTER_STATE_COVER_OPEN || next.GetMessage() == "" {
		t.Fatalf("changed state: %v %v", next, err)
	}
}

func TestScale(t *testing.T) {
	ctx := context.Background()

	// NotFound when no scale is connected.
	none, _ := start(t, ok(), false, simulator.NoScanner{})
	if _, err := none.ReadWeight(ctx, &devicev1.ReadWeightRequest{}); code(err) != codes.NotFound {
		t.Errorf("terazisiz okuma: %v", err)
	}
	if _, err := none.SimulateWeight(ctx, &devicev1.SimulateWeightRequest{Grams: 100}); code(err) != codes.FailedPrecondition {
		t.Errorf("simulation without a scale: %v", err)
	}

	c, _ := start(t, ok(), true, simulator.NoScanner{})
	// Empty scale: no product.
	if _, err := c.ReadWeight(ctx, &devicev1.ReadWeightRequest{}); code(err) != codes.FailedPrecondition {
		t.Errorf("empty scale: %v", err)
	}
	if _, err := c.SimulateWeight(ctx, &devicev1.SimulateWeightRequest{Grams: 1250}); err != nil {
		t.Fatal(err)
	}
	r, err := c.ReadWeight(ctx, &devicev1.ReadWeightRequest{})
	if err != nil || r.GetGrams() != 1250 {
		t.Errorf("weighing: %v %v", r, err)
	}
	if _, err := c.SimulateWeight(ctx, &devicev1.SimulateWeightRequest{Grams: 900, Moving: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadWeight(ctx, &devicev1.ReadWeightRequest{}); code(err) != codes.FailedPrecondition {
		t.Errorf("oynayan terazi: %v", err)
	}
	st, _ := c.GetStatus(ctx, &devicev1.GetStatusRequest{})
	if st.GetScale() != "simulator" || st.GetWeighed() != 1 {
		t.Errorf("terazi durumu: %v", st)
	}
}

func TestScans(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, _ := start(t, ok(), false, simulator.NewScanner())
	stream, err := c.WatchScans(ctx, &devicev1.WatchScansRequest{})
	if err != nil {
		t.Fatal(err)
	}
	// The code is sent again until the listener registers (registration is asynchronous).
	got := make(chan string, 1)
	go func() {
		m, err := stream.Recv()
		if err == nil {
			got <- m.GetCode()
		}
	}()
	deadline := time.After(3 * time.Second)
	for {
		if _, err := c.SimulateScan(ctx, &devicev1.SimulateScanRequest{Code: "8690000000012"}); err != nil {
			t.Fatal(err)
		}
		select {
		case code := <-got:
			if code != "8690000000012" {
				t.Fatalf("okunan kod %q", code)
			}
			goto done
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatal("the scanned code did not reach the stream")
		}
	}
done:
	if _, err := c.SimulateScan(ctx, &devicev1.SimulateScanRequest{Code: "abc\n"}); code(err) != codes.InvalidArgument {
		t.Errorf("invalid code: %v", err)
	}

	// A scan cannot be imitated on a real reader (not the simulator).
	real, _ := start(t, ok(), false, simulator.NoScanner{})
	if _, err := real.SimulateScan(ctx, &devicev1.SimulateScanRequest{Code: "123"}); code(err) != codes.FailedPrecondition {
		t.Errorf("scan without a simulator: %v", err)
	}
}
