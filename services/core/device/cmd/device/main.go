// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command device is the composition root of the peripherals service (receipt printer, cash
// drawer, barcode reader, scale).
//
// Printer (ABAROW_PRINTER): "simulator" (default; documents are printed to standard
// output, "docker compose logs device"), "escpos-usb:/dev/..."
// (an ESC/POS printer on USB) or "escpos-tcp:address:9100" (on the network). The code
// page is ABAROW_PRINTER_CODEPAGE (pc857, default; wpc1254; if the ESC t
// number differs "pc857:61"). The drawer is connected to the printer.
//
// The restaurant's station printers (ABAROW_KITCHEN_PRINTER, ABAROW_BAR_PRINTER):
// in the same form; the code page and the column count are the receipt printer's. If not
// installed the station's ticket is printed on the receipt printer.
//
// Barcode reader (ABAROW_SCANNER): "simulator" (default; a scan is imitated
// with SimulateScan), "evdev:/dev/abarow-scanner" (a reader on USB that works like a
// keyboard; the device is set aside for device) or "none".
//
// Scale (ABAROW_SCALE): "none" (default; a weighed product is sold with the scale
// barcode), "simulator" (the weight is set with SimulateWeight)
// or a scale on a serial port: "toledo8217:/dev/..." or "nci:/dev/...".
// The port setting is ABAROW_SCALE_SERIAL (default "9600,7E1").
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	devicev1 "abarow.com/gen/com/abarow/device/v1"

	"abarow.com/pkg/service"
	"abarow.com/services/core/device/internal/adapters/in/grpcserver"
	"abarow.com/services/core/device/internal/adapters/out/escpos"
	"abarow.com/services/core/device/internal/adapters/out/evdev"
	"abarow.com/services/core/device/internal/adapters/out/serialscale"
	"abarow.com/services/core/device/internal/adapters/out/simulator"
	"abarow.com/services/core/device/internal/app"
)

func main() {
	healthAddr := service.Env("ABAROW_DEVICE_HEALTH_ADDR", ":8081")
	service.Main("device", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		// 58 mm paper: 32 columns, 80 mm paper: 48 columns. Receipts and reports are laid out
		// for at least 32 columns.
		columns, err := strconv.Atoi(service.Env("ABAROW_PRINTER_COLUMNS", "48"))
		if err != nil || columns < 32 || columns > 80 {
			return fmt.Errorf("ABAROW_PRINTER_COLUMNS geçersiz: %q", os.Getenv("ABAROW_PRINTER_COLUMNS"))
		}
		var printer app.Printer = simulator.NewPrinter(os.Stdout, columns)
		var drawer app.Drawer = simulator.Drawer{Log: log}
		if kind := service.Env("ABAROW_PRINTER", "simulator"); kind != "simulator" {
			target, err := escpos.ParseTarget(kind)
			if err != nil {
				return err
			}
			page, err := escpos.ParseCodePage(service.Env("ABAROW_PRINTER_CODEPAGE", "pc857"))
			if err != nil {
				return err
			}
			p := escpos.New(target, columns, page)
			printer, drawer = p, escpos.Drawer{P: p}
			log.Info("receipt printer", "printer", target.String(), "code_page", page.Name, "columns", columns)
		}
		var scanner app.Scanner
		switch kind := service.Env("ABAROW_SCANNER", "simulator"); {
		case kind == "simulator":
			scanner = simulator.NewScanner()
		case kind == "none":
			scanner = simulator.NoScanner{}
		case strings.HasPrefix(kind, "evdev:") && len(kind) > len("evdev:"):
			scanner = &evdev.Scanner{Path: strings.TrimPrefix(kind, "evdev:"), Log: log}
		default:
			return fmt.Errorf("ABAROW_SCANNER geçersiz: %q (simulator, none ya da evdev:/dev/...)", kind)
		}
		svc := app.NewService(printer, drawer, scanner)
		for station, env := range map[string]string{"kitchen": "ABAROW_KITCHEN_PRINTER", "bar": "ABAROW_BAR_PRINTER"} {
			kind := os.Getenv(env)
			switch kind {
			case "":
				continue
			case "simulator":
				svc.WithStation(station, simulator.NewPrinter(os.Stdout, columns))
			default:
				target, err := escpos.ParseTarget(kind)
				if err != nil {
					return fmt.Errorf("%s: %w", env, err)
				}
				page, err := escpos.ParseCodePage(service.Env("ABAROW_PRINTER_CODEPAGE", "pc857"))
				if err != nil {
					return err
				}
				svc.WithStation(station, escpos.New(target, columns, page))
			}
			log.Info("station printer", "station", station, "printer", kind)
		}
		switch kind := service.Env("ABAROW_SCALE", "none"); {
		case kind == "none":
		case kind == "simulator":
			svc.WithScale(simulator.NewScale())
		default:
			name, path, _ := strings.Cut(kind, ":")
			proto, err := serialscale.ParseProtocol(name)
			if err != nil || !strings.HasPrefix(path, "/dev/") {
				return fmt.Errorf("ABAROW_SCALE geçersiz: %q (none, simulator, toledo8217:/dev/... ya da nci:/dev/...)", kind)
			}
			settings, err := serialscale.ParseSettings(service.Env("ABAROW_SCALE_SERIAL", "9600,7E1"))
			if err != nil {
				return err
			}
			svc.WithScale(serialscale.New(path, settings, proto))
			log.Info("terazi", "protocol", proto.Name, "port", path, "settings", service.Env("ABAROW_SCALE_SERIAL", "9600,7E1"))
		}
		go svc.RunScanner(ctx)
		// The printer's state is asked every 5 seconds (and after every print failure);
		// a change is written to the log and flows to the screen.
		go svc.RunPrinterMonitor(ctx, 5*time.Second)
		go func() {
			for st := range svc.WatchPrinter(ctx) {
				log.Info("printer state", "status", st.String())
			}
		}()

		srv := service.NewGRPCServer()
		devicev1.RegisterDeviceServiceServer(srv, grpcserver.New(svc))
		return service.Serve(ctx, log, service.ServeConfig{
			GRPC:       srv,
			GRPCAddr:   service.Env("ABAROW_DEVICE_GRPC_ADDR", ":9000"),
			HealthAddr: healthAddr,
		})
	})
}
