// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// beko is the Beko X30TR fiscal printer adapter: it serves the fiscal.v1 contract
// in the same form as the fiscal printer simulator and has the receipt recorded on
// the device through Token's Integration Hub. It runs the hubd bridge that wraps
// Token's library as a child process (see services/fiscal/beko/hub). It is a
// separate container; the device's USB device is given to it.
//
// Settings (environment variables):
//
//	ABAROW_BEKO_SECTIONS   VAT rate:section mapping, "100:1,1000:2,2000:3"
//	                       (the rate is in basis points; the sections are defined on the device). Required.
//	ABAROW_BEKO_CASH_NET=1 Cash is sent as the amount after the change is deducted.
//	ABAROW_BEKO_HELPER     path of hubd (default /usr/local/bin/hubd)
//	ABAROW_BEKO_COMPANY    company name reported to Token (default abarow)
//	ABAROW_BEKO_TIMEOUT    how long to wait for the sale result (default 90s)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	fiscalv1 "abarow.com/gen/com/abarow/fiscal/v1"

	"abarow.com/pkg/money"
	"abarow.com/pkg/service"
	"abarow.com/services/fiscal/beko/internal/adapters/in/grpcserver"
	"abarow.com/services/fiscal/beko/internal/adapters/out/hubproc"
	"abarow.com/services/fiscal/beko/internal/app"
	"abarow.com/services/fiscal/beko/internal/domain"
)

func main() {
	healthAddr := service.Env("ABAROW_FISCAL_HEALTH_ADDR", ":8081")
	service.Main("fiscal-beko", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		sections, err := parseSections(os.Getenv("ABAROW_BEKO_SECTIONS"))
		if err != nil {
			return err
		}
		timeout, err := time.ParseDuration(service.Env("ABAROW_BEKO_TIMEOUT", "90s"))
		if err != nil {
			return fmt.Errorf("ABAROW_BEKO_TIMEOUT is invalid: %w", err)
		}
		cfg := domain.Config{
			Sections: sections,
			CashNet:  os.Getenv("ABAROW_BEKO_CASH_NET") == "1",
		}
		bridge := hubproc.New(log, service.Env("ABAROW_BEKO_HELPER", "/usr/local/bin/hubd"), service.Env("ABAROW_BEKO_COMPANY", "abarow"))
		svc := app.NewService(bridge, cfg, timeout, 3*time.Second, log)
		go bridge.Run(ctx)
		go svc.Run(ctx)

		srv := service.NewGRPCServer()
		fiscalv1.RegisterFiscalServiceServer(srv, grpcserver.New(svc))
		return service.Serve(ctx, log, service.ServeConfig{
			GRPC:       srv,
			GRPCAddr:   service.Env("ABAROW_FISCAL_GRPC_ADDR", ":9000"),
			HealthAddr: healthAddr,
			// While the device is not connected the service runs but does not count as healthy: the register
			// sees that the fiscal printer is not ready.
			Health: func(context.Context) error { return svc.Ready() },
		})
	})
}

// parseSections reads a mapping of the form "1000:1,2000:2".
func parseSections(s string) (map[money.Rate]int, error) {
	if strings.TrimSpace(s) == "" {
		return nil, errors.New("ABAROW_BEKO_SECTIONS is required: the VAT rate (base points) and the ÖKC section no, e.g. 1000:1,2000:2")
	}
	out := map[money.Rate]int{}
	for _, part := range strings.Split(s, ",") {
		rate, section, ok := strings.Cut(strings.TrimSpace(part), ":")
		r, err1 := strconv.Atoi(rate)
		n, err2 := strconv.Atoi(section)
		if !ok || err1 != nil || err2 != nil || r < 0 || n <= 0 {
			return nil, fmt.Errorf("ABAROW_BEKO_SECTIONS is invalid: %q (rate:section expected)", part)
		}
		out[money.Rate(r)] = n
	}
	return out, nil
}
