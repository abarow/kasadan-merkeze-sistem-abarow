// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Komut catalog, katalog servisinin composition root'udur.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"

	"abarow.com/pkg/menu"
	"abarow.com/pkg/money"
	"abarow.com/pkg/service"
	"abarow.com/services/core/catalog/internal/adapters/in/grpcserver"
	"abarow.com/services/core/catalog/internal/adapters/in/natscatalog"
	"abarow.com/services/core/catalog/internal/adapters/out/postgres"
	"abarow.com/services/core/catalog/internal/app"
	"abarow.com/services/core/catalog/internal/domain"
)

func main() {
	healthAddr := service.Env("ABAROW_CATALOG_HEALTH_ADDR", ":8081")
	service.Main("catalog", healthAddr, func(ctx context.Context, log *slog.Logger) error {
		dsn, err := service.MustEnv("ABAROW_DATABASE_URL")
		if err != nil {
			return err
		}
		store, err := postgres.Open(ctx, dsn)
		if err != nil {
			return err
		}
		defer store.Close()
		svc := app.NewService(store)

		// The initial file is loaded at every start; it does not change products that came from
		// headquarters (in a business without headquarters the catalog is managed with this file).
		if path := os.Getenv("ABAROW_CATALOG_SEED"); path != "" {
			n, err := seed(ctx, svc, path)
			if err != nil {
				return err
			}
			log.Info("catalog loaded", "file", path, "products", n)
		}

		// Headquarters' catalog: it is taken as a source from the store server's catalog stream
		// (ABAROW_CATALOG_SOURCE: the store server's JetStream domain, that is,
		// the store id).
		if source := os.Getenv("ABAROW_CATALOG_SOURCE"); source != "" {
			js, closeNATS, err := connectNATS()
			if err != nil {
				return err
			}
			defer closeNATS()
			go natscatalog.New(js, svc, source, log).Run(ctx)
		}

		srv := service.NewGRPCServer()
		catalogv1.RegisterCatalogServiceServer(srv, grpcserver.New(svc))
		return service.Serve(ctx, log, service.ServeConfig{
			GRPC:       srv,
			GRPCAddr:   service.Env("ABAROW_CATALOG_GRPC_ADDR", ":9000"),
			HealthAddr: healthAddr,
			Health:     store.Ping,
		})
	})
}

// seedProduct is a product in the initial file: unit_price_kurus is the price valid from the
// beginning; prices are future-dated prices (e.g. {"unit_price_kurus":
// 1650, "from": "2026-11-02T00:00:00+03:00"}); image is the product image's
// file (relative to the file's folder; e.g. "images/kofte.jpg").
type seedProduct struct {
	domain.Product
	Image  string `json:"image"`
	Prices []struct {
		UnitPrice money.Money `json:"unit_price_kurus"`
		From      time.Time   `json:"from"`
	} `json:"prices"`
}

func seed(ctx context.Context, svc *app.Service, path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var items []seedProduct
	if err := json.Unmarshal(data, &items); err != nil {
		return 0, fmt.Errorf("could not read %s: %w", path, err)
	}
	products := make([]domain.Product, len(items))
	for i, it := range items {
		p := it.Product
		p.Prices = []domain.Price{{Source: domain.HeadquartersPrice, Amount: p.UnitPrice}}
		for _, sp := range it.Prices {
			if sp.From.IsZero() {
				return 0, fmt.Errorf("%s: %s: the effective moment (from) of the future-dated price is missing", path, p.ID)
			}
			p.Prices = append(p.Prices, domain.Price{Source: domain.HeadquartersPrice, Amount: sp.UnitPrice, From: sp.From})
		}
		if it.Image != "" {
			img, err := os.ReadFile(filepath.Join(filepath.Dir(path), filepath.FromSlash(it.Image)))
			if err != nil {
				return 0, fmt.Errorf("%s: %s: could not read the image: %w", path, p.ID, err)
			}
			if p.ImageType, p.ImageSHA256, err = menu.CheckImage(img); err != nil {
				return 0, fmt.Errorf("%s: %s (%s): %w", path, p.ID, it.Image, err)
			}
			p.ImageData = img
		}
		products[i] = p
	}
	return len(products), svc.Seed(ctx, products)
}

// connectNATS connects to NATS with the service's identity (tools/natsauth produces it);
// it makes the connection itself even while NATS is down.
func connectNATS() (jetstream.JetStream, func(), error) {
	opts := []nats.Option{nats.Name("abarow-catalog"), nats.MaxReconnects(-1), nats.RetryOnFailedConnect(true)}
	if seed := os.Getenv("ABAROW_NATS_NKEY"); seed != "" {
		opt, err := nats.NkeyOptionFromSeed(seed)
		if err != nil {
			return nil, nil, fmt.Errorf("could not read the NATS key: %w", err)
		}
		opts = append(opts, opt)
	}
	nc, err := nats.Connect(service.Env("ABAROW_NATS_URL", "nats://nats:4222"), opts...)
	if err != nil {
		return nil, nil, err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, err
	}
	return js, nc.Close, nil
}
