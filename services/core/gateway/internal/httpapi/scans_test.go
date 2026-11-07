// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi_test

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	devicev1 "abarow.com/gen/com/abarow/device/v1"

	"abarow.com/services/core/gateway/internal/httpapi"
)

// fakeDevice is a fake device that passes the scan imitation on to listeners.
type fakeDevice struct {
	devicev1.UnimplementedDeviceServiceServer
	codes chan string
}

func (f *fakeDevice) WatchScans(_ *devicev1.WatchScansRequest, stream grpc.ServerStreamingServer[devicev1.WatchScansResponse]) error {
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case code := <-f.codes:
			if err := stream.Send(&devicev1.WatchScansResponse{Code: code}); err != nil {
				return err
			}
		}
	}
}

func (f *fakeDevice) SimulateScan(_ context.Context, req *devicev1.SimulateScanRequest) (*devicev1.SimulateScanResponse, error) {
	if req.GetCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "barkod 1–128 yazdırılabilir ASCII karakter olmalı")
	}
	f.codes <- req.GetCode()
	return &devicev1.SimulateScanResponse{}, nil
}

// The scan imitation comes to the screen's stream as a "scan" event; the stream asks
// for a session.
func TestScanStream(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	devicev1.RegisterDeviceServiceServer(gs, &fakeDevice{codes: make(chan string, 4)})
	go gs.Serve(lis)
	defer gs.Stop()
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cfg := httpapi.Config{Module: "retail", FiscalMode: "simulator"}
	srv := httptest.NewServer(httpapi.New(&fakeSales{}, &fakeStaff{shift: ayseShift}, devicev1.NewDeviceServiceClient(conn), http.NotFoundHandler(), cfg))
	defer srv.Close()

	if code, _ := do(t, http.MethodGet, srv.URL+"/api/device/scans", "", ""); code != http.StatusUnauthorized {
		t.Errorf("stream without a session: %d", code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/device/scans", nil)
	req.Header.Set("Authorization", "Bearer kasiyer")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %s %s", resp.Status, resp.Header.Get("Content-Type"))
	}
	lines := bufio.NewScanner(resp.Body)
	lines.Scan() // ": abarow" — the stream opened, it is listening to device

	// The scan is imitated when the stream connects to device (connecting may take a moment:
	// it is retried until the scan comes).
	got := make(chan string, 1)
	go func() {
		for lines.Scan() {
			if data, ok := strings.CutPrefix(lines.Text(), "data: "); ok {
				got <- data
				return
			}
		}
	}()
	for {
		if code, body := do(t, http.MethodPost, srv.URL+"/api/device/scans", "kasiyer", `{"code":"8690000000012"}`); code != http.StatusNoContent {
			t.Fatalf("okutma taklidi: %d %s", code, body)
		}
		select {
		case data := <-got:
			if data != `{"code":"8690000000012"}` {
				t.Errorf("olay verisi %s", data)
			}
			if code, _ := do(t, http.MethodPost, srv.URL+"/api/device/scans", "kasiyer", `{"code":""}`); code != http.StatusUnprocessableEntity {
				t.Errorf("invalid code: %d", code)
			}
			return
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("the scan did not reach the stream")
		}
	}
}

// If there is no device the stream and the imitation are closed with a clear error.
func TestScanWithoutDevice(t *testing.T) {
	srv := server(&fakeSales{})
	defer srv.Close()
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		if code, body := do(t, m, srv.URL+"/api/device/scans", "kasiyer", `{"code":"1"}`); code != http.StatusNotFound || !strings.Contains(body, "ABAROW_DEVICE_ADDR") {
			t.Errorf("%s: %d %s", m, code, body)
		}
	}
}

// printerDevice is a fake device that reports the printer state too.
type printerDevice struct {
	fakeDevice
	states chan devicev1.PrinterState
}

func (f *printerDevice) WatchPrinter(_ *devicev1.WatchPrinterRequest, stream grpc.ServerStreamingServer[devicev1.WatchPrinterResponse]) error {
	messages := map[devicev1.PrinterState]string{devicev1.PrinterState_PRINTER_STATE_PAPER_OUT: "Kağıt bitti"}
	st := devicev1.PrinterState_PRINTER_STATE_READY
	for {
		if err := stream.Send(&devicev1.WatchPrinterResponse{State: st, Message: messages[st]}); err != nil {
			return err
		}
		select {
		case <-stream.Context().Done():
			return nil
		case st = <-f.states:
		}
	}
}

func (f *printerDevice) SimulatePrinter(_ context.Context, req *devicev1.SimulatePrinterRequest) (*devicev1.SimulatePrinterResponse, error) {
	f.states <- req.GetState()
	return &devicev1.SimulatePrinterResponse{}, nil
}

// The printer's state comes to the stream as a "printer" event: first the current state,
// then the change.
func TestPrinterStream(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	devicev1.RegisterDeviceServiceServer(gs, &printerDevice{fakeDevice: fakeDevice{codes: make(chan string, 4)}, states: make(chan devicev1.PrinterState, 4)})
	go gs.Serve(lis)
	defer gs.Stop()
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cfg := httpapi.Config{Module: "retail", FiscalMode: "simulator"}
	srv := httptest.NewServer(httpapi.New(&fakeSales{}, &fakeStaff{shift: ayseShift}, devicev1.NewDeviceServiceClient(conn), http.NotFoundHandler(), cfg))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/device/scans", nil)
	req.Header.Set("Authorization", "Bearer kasiyer")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	events := make(chan string, 4)
	go func() {
		lines := bufio.NewScanner(resp.Body)
		event := ""
		for lines.Scan() {
			if name, ok := strings.CutPrefix(lines.Text(), "event: "); ok {
				event = name
			}
			if data, ok := strings.CutPrefix(lines.Text(), "data: "); ok {
				events <- event + " " + data
			}
		}
	}()
	wait := func(want string) {
		t.Helper()
		select {
		case got := <-events:
			if got != want {
				t.Errorf("event %s, want %s", got, want)
			}
		case <-ctx.Done():
			t.Fatalf("%s did not come", want)
		}
	}
	wait(`printer {"message":"","state":"ready"}`)
	if code, body := do(t, http.MethodPost, srv.URL+"/api/device/printer", "kasiyer", `{"state":"paper_out"}`); code != http.StatusNoContent {
		t.Fatalf("durum taklidi: %d %s", code, body)
	}
	wait(`printer {"message":"Kağıt bitti","state":"paper_out"}`)
	for _, bad := range []string{`{"state":"unknown"}`, `{"state":"kağıt"}`} {
		if code, _ := do(t, http.MethodPost, srv.URL+"/api/device/printer", "kasiyer", bad); code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", bad, code)
		}
	}
}
