// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command payment is the composition root of the payment service.
package main

import (
	"context"
	"log/slog"
	"strconv"

	paymentv1 "abarow.com/gen/com/abarow/payment/v1"

	"abarow.com/pkg/money"
	"abarow.com/pkg/service"
	"abarow.com/services/core/payment/internal/adapters/in/grpcserver"
	"abarow.com/services/core/payment/internal/adapters/out/postgres"
	"abarow.com/services/core/payment/internal/adapters/out/terminalsim"
	"abarow.com/services/core/payment/internal/app"
)

func main() {
	healthAddr := service.Env("ABAROW_PAYMENT_HEALTH_ADDR", ":8081")
	service.Main("payment", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		dsn, err := service.MustEnv("ABAROW_DATABASE_URL")
		if err != nil {
			return err
		}
		store, err := postgres.Open(ctx, dsn)
		if err != nil {
			return err
		}
		defer store.Close()

		// For now there is only the POS simulator; a real bank POS will be added as a
		// separate adapter.
		limit, err := strconv.ParseInt(service.Env("ABAROW_PAYMENT_SIM_LIMIT_KURUS", "0"), 10, 64)
		if err != nil {
			return err
		}
		terminal := &terminalsim.Terminal{Limit: money.Money(limit), Log: log}

		srv := service.NewGRPCServer()
		paymentv1.RegisterPaymentServiceServer(srv, grpcserver.New(app.NewService(store, terminal, nil)))
		return service.Serve(ctx, log, service.ServeConfig{
			GRPC:       srv,
			GRPCAddr:   service.Env("ABAROW_PAYMENT_GRPC_ADDR", ":9000"),
			HealthAddr: healthAddr,
			Health:     store.Ping,
		})
	})
}
