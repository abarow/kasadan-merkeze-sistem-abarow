// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command retail is the composition root of the market module.
package main

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"abarow.com/pkg/service"
	"abarow.com/services/modules/retail/internal/adapters/in/httpapi"
	"abarow.com/services/modules/retail/internal/adapters/out/grpcclients"
	"abarow.com/services/modules/retail/internal/adapters/out/postgres"
	"abarow.com/services/modules/retail/internal/app"
)

func main() {
	healthAddr := service.Env("ABAROW_RETAIL_HEALTH_ADDR", ":8081")
	service.Main("retail", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		// Baskets are persistent: even if the service restarts the cashier continues from
		// their basket.
		dsn, err := service.MustEnv("ABAROW_DATABASE_URL")
		if err != nil {
			return err
		}
		store, err := postgres.Open(ctx, dsn)
		if err != nil {
			return err
		}
		defer store.Close()

		catalogConn, err := service.Dial(service.Env("ABAROW_CATALOG_ADDR", "catalog:9000"))
		if err != nil {
			return err
		}
		defer catalogConn.Close()
		salesConn, err := service.Dial(service.Env("ABAROW_SALES_ADDR", "sales:9000"))
		if err != nil {
			return err
		}
		defer salesConn.Close()

		svc := app.NewService(store, grpcclients.NewCatalog(catalogConn), grpcclients.NewSales(salesConn),
			func() string { return uuid.Must(uuid.NewV7()).String() })
		// The scale at the register is in device: when a weighed product is chosen the weight is read
		// from there. If ABAROW_DEVICE_ADDR is missing the scale is taken as absent.
		if addr := service.Env("ABAROW_DEVICE_ADDR", ""); addr != "" {
			deviceConn, err := service.Dial(addr)
			if err != nil {
				return err
			}
			defer deviceConn.Close()
			svc.WithScale(grpcclients.NewScale(deviceConn))
		}
		return service.ServeHTTP(ctx, log, service.HTTPConfig{
			Handler:    httpapi.New(svc, service.Env("ABAROW_RETAIL_WEB_DIR", "/web")),
			Addr:       service.Env("ABAROW_RETAIL_ADDR", ":8080"),
			HealthAddr: healthAddr,
			Health:     store.Ping,
		})
	})
}
