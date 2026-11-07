// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command gateway is the screen's single entry point: it translates core requests into
// sales and forwards the others to the active vertical module.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"

	devicev1 "abarow.com/gen/com/abarow/device/v1"
	salesv1 "abarow.com/gen/com/abarow/sales/v1"
	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/pkg/service"
	"abarow.com/services/core/gateway/internal/httpapi"
)

func main() {
	healthAddr := service.Env("ABAROW_GATEWAY_HEALTH_ADDR", ":8081")
	service.Main("gateway", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		salesConn, err := service.Dial(service.Env("ABAROW_SALES_ADDR", "sales:9000"))
		if err != nil {
			return err
		}
		defer salesConn.Close()
		staffConn, err := service.Dial(service.Env("ABAROW_STAFF_ADDR", "staff:9000"))
		if err != nil {
			return err
		}
		defer staffConn.Close()

		// The barcode reader is on device; without it the scan flow to the screen is off.
		var device devicev1.DeviceServiceClient
		if addr := os.Getenv("ABAROW_DEVICE_ADDR"); addr != "" {
			deviceConn, err := service.Dial(addr)
			if err != nil {
				return err
			}
			defer deviceConn.Close()
			device = devicev1.NewDeviceServiceClient(deviceConn)
		}

		moduleURL, err := url.Parse(service.Env("ABAROW_MODULE_URL", "http://retail:8080"))
		if err != nil {
			return err
		}
		module := httputil.NewSingleHostReverseProxy(moduleURL)
		module.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			log.Warn("could not reach the module", "path", r.URL.Path, "err", err)
			http.Error(w, `{"error":"modül yanıt vermiyor"}`, http.StatusBadGateway)
		}

		// Update agent (optional): the status and the approval code are shown on the register screen.
		var update http.Handler
		if s := os.Getenv("ABAROW_UPDATE_AGENT_URL"); s != "" {
			agentURL, err := url.Parse(s)
			if err != nil {
				return fmt.Errorf("ABAROW_UPDATE_AGENT_URL geçersiz: %w", err)
			}
			proxy := httputil.NewSingleHostReverseProxy(agentURL)
			proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
				log.Warn("could not reach the update agent", "err", err)
				http.Error(w, `{"error":"güncelleme ajanı yanıt vermiyor"}`, http.StatusBadGateway)
			}
			update = proxy
		}

		cfg := httpapi.Config{
			Update:       update,
			UpdateSecret: os.Getenv("ABAROW_UPDATE_APPROVE_SECRET"),
			Module:       service.Env("ABAROW_MODULE", "retail"),
			FiscalMode:   service.Env("ABAROW_FISCAL_MODE", "simulator"),
			StoreID:      service.Env("ABAROW_STORE_ID", ""),
			RegisterID:   service.Env("ABAROW_REGISTER_ID", ""),
		}
		return service.ServeHTTP(ctx, log, service.HTTPConfig{
			Handler:    httpapi.New(salesv1.NewSalesServiceClient(salesConn), staffv1.NewStaffServiceClient(staffConn), device, module, cfg),
			Addr:       service.Env("ABAROW_GATEWAY_ADDR", ":8080"),
			HealthAddr: healthAddr,
		})
	})
}
