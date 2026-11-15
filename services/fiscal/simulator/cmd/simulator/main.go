// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command simulator is the composition root of the fiscal printer simulator.
// If ABAROW_DEVICE_ADDR is given it prints receipts on the printer of the device service
// and has the drawer opened on a cash receipt; the receipt width is taken from the printer.
// If it is not given it prints the receipts to standard output (ABAROW_FISCAL_WIDTH).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // scratch image'da saat dilimi verisi yok

	devicev1 "abarow.com/gen/com/abarow/device/v1"
	fiscalv1 "abarow.com/gen/com/abarow/fiscal/v1"

	"abarow.com/pkg/service"
	"abarow.com/services/fiscal/simulator/internal/adapters/in/grpcserver"
	"abarow.com/services/fiscal/simulator/internal/adapters/out/device"
	"abarow.com/services/fiscal/simulator/internal/adapters/out/memory"
	"abarow.com/services/fiscal/simulator/internal/adapters/out/writer"
	"abarow.com/services/fiscal/simulator/internal/app"
	"abarow.com/services/fiscal/simulator/internal/domain"
)

func main() {
	healthAddr := service.Env("ABAROW_FISCAL_HEALTH_ADDR", ":8081")
	service.Main("fiscal-simulator", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		var (
			printer app.Printer = writer.New(os.Stdout)
			drawer  app.Drawer  = writer.Drawer{Log: log}
			width   int
			err     error
		)
		if addr := os.Getenv("ABAROW_DEVICE_ADDR"); addr != "" {
			conn, err := service.Dial(addr)
			if err != nil {
				return err
			}
			defer conn.Close()
			client := devicev1.NewDeviceServiceClient(conn)
			printer, drawer = device.Printer{Client: client}, device.Drawer{Client: client, Log: log}
			if width, err = columns(ctx, log, client, 30*time.Second); err != nil {
				return fmt.Errorf("yazıcı genişliği device'tan alınamadı: %w", err)
			}
		} else if width, err = strconv.Atoi(service.Env("ABAROW_FISCAL_WIDTH", "48")); err != nil {
			return err
		}
		loc, err := time.LoadLocation(service.Env("ABAROW_TZ", "Europe/Istanbul"))
		if err != nil {
			return err
		}
		var header []string
		if h := os.Getenv("ABAROW_FISCAL_HEADER"); h != "" {
			header = strings.Split(h, "|")
		}
		layout := domain.Layout{Width: width, Header: header, Location: loc}

		srv := service.NewGRPCServer()
		fiscalv1.RegisterFiscalServiceServer(srv, grpcserver.New(app.NewService(&memory.Counter{}, printer, drawer, layout)))
		return service.Serve(ctx, log, service.ServeConfig{
			GRPC:       srv,
			GRPCAddr:   service.Env("ABAROW_FISCAL_GRPC_ADDR", ":9000"),
			HealthAddr: healthAddr,
		})
	})
}

// columns takes the printer's line width from device. At start, even though device
// looks healthy it may not respond yet (e.g. while the machine is loading);
// it is tried once a second for the duration of wait.
func columns(ctx context.Context, log *slog.Logger, client devicev1.DeviceServiceClient, wait time.Duration) (int, error) {
	deadline := time.Now().Add(wait)
	warned := false
	for {
		width, err := device.Columns(ctx, client)
		if err == nil || time.Now().After(deadline) {
			return width, err
		}
		if !warned {
			log.Warn("could not get the printer width; will retry", "err", err)
			warned = true
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
