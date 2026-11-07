// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command restaurant is the composition root of the restaurant vertical module: the floor
// and tables, the check, kitchen and bar tickets, bill splitting. At the gateway
// with ABAROW_MODULE=restaurant it replaces the market module; the core
// services (catalog, sales, device, staff) are the same.
package main

import (
	"context"
	"log/slog"
	"time"
	_ "time/tzdata" // scratch image'da saat dilimi verisi yok

	"github.com/google/uuid"
	"google.golang.org/grpc"

	"abarow.com/pkg/service"
	"abarow.com/services/modules/restaurant/internal/adapters/in/httpapi"
	"abarow.com/services/modules/restaurant/internal/adapters/out/grpcclients"
	"abarow.com/services/modules/restaurant/internal/adapters/out/sqlite"
	"abarow.com/services/modules/restaurant/internal/app"
)

// retention is how long closed checks are kept: sales are permanent in sales,
// the check is only a working record.
const retention = 30 * 24 * time.Hour

func main() {
	healthAddr := service.Env("ABAROW_RESTAURANT_HEALTH_ADDR", ":8081")
	service.Main("restaurant", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		loc, err := time.LoadLocation(service.Env("ABAROW_TZ", "Europe/Istanbul"))
		if err != nil {
			return err
		}
		store, err := sqlite.Open(ctx, service.Env("ABAROW_RESTAURANT_DB", "/data/restaurant.db"))
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.DeleteClosedBefore(ctx, time.Now().Add(-retention)); err != nil {
			return err
		}

		dial := func(env, addr string) (*grpc.ClientConn, error) { return service.Dial(service.Env(env, addr)) }
		catalogConn, err := dial("ABAROW_CATALOG_ADDR", "catalog:9000")
		if err != nil {
			return err
		}
		defer catalogConn.Close()
		salesConn, err := dial("ABAROW_SALES_ADDR", "sales:9000")
		if err != nil {
			return err
		}
		defer salesConn.Close()
		// Kitchen, bar and check tickets are printed from device.
		deviceConn, err := dial("ABAROW_DEVICE_ADDR", "device:9000")
		if err != nil {
			return err
		}
		defer deviceConn.Close()
		// Cancelling a sent order and the floor layout need the manager's approval.
		staffConn, err := dial("ABAROW_STAFF_ADDR", "staff:9000")
		if err != nil {
			return err
		}
		defer staffConn.Close()

		svc := app.NewService(store, grpcclients.NewCatalog(catalogConn), grpcclients.NewSales(salesConn),
			grpcclients.NewPrinter(deviceConn), grpcclients.NewStaff(staffConn),
			func() string { return uuid.Must(uuid.NewV7()).String() })
		svc.Now = func() time.Time { return time.Now().In(loc) }
		return service.ServeHTTP(ctx, log, service.HTTPConfig{
			Handler:    httpapi.New(svc, service.Env("ABAROW_RESTAURANT_WEB_DIR", "/web")),
			Addr:       service.Env("ABAROW_RESTAURANT_ADDR", ":8080"),
			HealthAddr: healthAddr,
			Health:     store.Ping,
		})
	})
}
