// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natscatalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"

	"abarow.com/pkg/pgdb/pgdbtest"
	"abarow.com/services/core/catalog/internal/adapters/out/postgres"
	"abarow.com/services/core/catalog/internal/app"
	"abarow.com/services/core/catalog/internal/domain"
)

func state(t *testing.T, code, name string, price int64, discontinued bool, future ...time.Time) []byte {
	t.Helper()
	s := &catalogv1.ProductState{
		EventId:      code + name,
		Product:      &catalogv1.Product{Id: code, Name: name, Unit: catalogv1.Unit_UNIT_PIECE, TaxRateBp: 100, Barcodes: []string{"869" + code}},
		Prices:       []*catalogv1.PriceChange{{UnitPriceKurus: price}},
		Discontinued: discontinued,
	}
	for _, f := range future {
		s.Prices = append(s.Prices, &catalogv1.PriceChange{UnitPriceKurus: price + 100, From: timestamppb.New(f)})
	}
	data, err := proto.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDecode(t *testing.T) {
	monday := time.Date(2099, 11, 2, 0, 0, 0, 0, time.UTC)
	p, err := Decode("application/protobuf", state(t, "1", "Ekmek", 1500, false, monday))
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "1" || p.Name != "Ekmek" || p.Unit != domain.Piece || p.TaxRate != 100 || len(p.Barcodes) != 1 || len(p.Prices) != 2 ||
		p.Prices[0] != (domain.Price{Source: domain.HeadquartersPrice, Amount: 1500}) || !p.Prices[1].From.Equal(monday) {
		t.Errorf("product %+v", p)
	}
	if _, err := Decode("application/json", []byte("{}")); !errors.Is(err, errUndecodable) {
		t.Errorf("JSON: %v", err)
	}
	if _, err := Decode("application/protobuf", []byte{0xff}); !errors.Is(err, errUndecodable) {
		t.Errorf("bozuk mesaj: %v", err)
	}

	// Menu details and image.
	data, err := proto.Marshal(&catalogv1.ProductState{Product: &catalogv1.Product{
		Id: "kofte", Name: "Köfte", Unit: catalogv1.Unit_UNIT_PIECE, Category: "Ana yemekler", MenuOrder: 20, Station: "kitchen",
		ImageSha256: "abc", ImageType: "image/jpeg",
		OptionGroups: []*catalogv1.OptionGroup{{Id: "pisirme", Name: "Pişirme", Min: 1, Max: 1,
			Options: []*catalogv1.Option{{Id: "az", Name: "az"}, {Id: "iyi", Name: "iyi", PriceDeltaKurus: 500}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Decode("application/protobuf", data)
	if err != nil || m.Category != "Ana yemekler" || m.MenuOrder != 20 || m.Station != "kitchen" || m.ImageSHA256 != "abc" ||
		len(m.Options) != 1 || m.Options[0].Max != 1 || m.Options[0].Options[1].PriceDelta != 500 {
		t.Errorf("menu product %+v %v", m, err)
	}
	data, _ = proto.Marshal(&catalogv1.ProductImage{ProductId: "kofte", Sha256: "abc", ContentType: "image/jpeg", Data: []byte{1, 2}})
	if img, err := DecodeImage("application/protobuf", data); err != nil || img.ProductID != "kofte" || len(img.Data) != 2 {
		t.Errorf("image %+v %v", img, err)
	}
}

func startServer(t *testing.T, opts *server.Options) *server.Server {
	t.Helper()
	opts.Host, opts.Port, opts.JetStream, opts.StoreDir, opts.NoLog, opts.NoSigs = "127.0.0.1", -1, true, t.TempDir(), true, true
	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS did not start")
	}
	t.Cleanup(s.Shutdown)
	return s
}

func jsFor(t *testing.T, s *server.Server) jetstream.JetStream {
	t.Helper()
	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return js
}

// The catalog stream on the store server comes down to the register over the leaf connection; at the register
// the products are applied to the catalog.
func TestRun(t *testing.T) {
	if testing.Short() {
		t.Skip("runs NATS servers")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// When the server opens it writes the leaf port it chose to the options.
	hubOpts := &server.Options{ServerName: "store-1", JetStreamDomain: "store-1", LeafNode: server.LeafNodeOpts{Host: "127.0.0.1", Port: -1}}
	hub := startServer(t, hubOpts)
	leafURL, _ := url.Parse(fmt.Sprintf("nats://127.0.0.1:%d", hubOpts.LeafNode.Port))
	register := startServer(t, &server.Options{ServerName: "register-1", JetStreamDomain: "register-1",
		LeafNode: server.LeafNodeOpts{Remotes: []*server.RemoteLeafOpts{{URLs: []*url.URL{leafURL}}}}})
	deadline := time.Now().Add(10 * time.Second)
	for hub.NumLeafNodes() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("leaf did not connect")
		}
		time.Sleep(50 * time.Millisecond)
	}

	hubJS := jsFor(t, hub)
	if _, err := hubJS.CreateStream(ctx, jetstream.StreamConfig{Name: StreamName, Subjects: []string{"catalog.product.>"}, MaxMsgsPerSubject: 1}); err != nil {
		t.Fatal(err)
	}
	publish := func(code string, data []byte) {
		msg := nats.NewMsg("catalog.product." + code)
		msg.Data = data
		msg.Header.Set("content-type", "application/protobuf")
		if _, err := hubJS.PublishMsg(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	publish("1", state(t, "1", "Ekmek", 1500, false))
	publish("2", state(t, "2", "Süt", 3990, false))
	msg := nats.NewMsg("catalog.product.3")
	msg.Data = []byte("bozuk")
	msg.Header.Set("content-type", "application/protobuf")
	if _, err := hubJS.PublishMsg(ctx, msg); err != nil {
		t.Fatal(err)
	}

	st, err := postgres.Open(ctx, pgdbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := app.NewService(st)
	go New(jsFor(t, register), svc, "store-1", slog.New(slog.NewTextHandler(io.Discard, nil))).Run(ctx)

	waitProduct := func(barcode string, ok func(domain.Product, error) bool) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			p, err := svc.GetByBarcode(ctx, barcode)
			if ok(p, err) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %+v %v", barcode, p, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	waitProduct("8691", func(p domain.Product, err error) bool { return err == nil && p.Name == "Ekmek" && p.UnitPrice == 1500 })
	waitProduct("8692", func(p domain.Product, err error) bool { return err == nil && p.UnitPrice == 3990 })

	// A new price and withdrawal from sale come down to the register.
	publish("1", state(t, "1", "Ekmek", 1600, false))
	waitProduct("8691", func(p domain.Product, err error) bool { return err == nil && p.UnitPrice == 1600 })
	publish("2", state(t, "2", "Süt", 3990, true))
	waitProduct("8692", func(_ domain.Product, err error) bool { return errors.Is(err, app.ErrNotFound) })

	// The store price (the store server's store settings stream) takes precedence over the headquarters
	// price; when it is removed the headquarters price returns.
	if _, err := hubJS.CreateStream(ctx, jetstream.StreamConfig{Name: ConfigStream, Subjects: []string{"config.>"}, MaxMsgsPerSubject: 1}); err != nil {
		t.Fatal(err)
	}
	storePrices := func(prices ...*catalogv1.StorePriceChange) {
		data, _ := proto.Marshal(&catalogv1.StorePrices{StoreId: "store-1", ProductId: "1", Prices: prices})
		msg := nats.NewMsg("config.price.1")
		msg.Data = data
		msg.Header.Set("content-type", "application/protobuf")
		if _, err := hubJS.PublishMsg(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	storePrices(&catalogv1.StorePriceChange{UnitPriceKurus: 1750})
	waitProduct("8691", func(p domain.Product, err error) bool { return err == nil && p.UnitPrice == 1750 })
	storePrices()
	waitProduct("8691", func(p domain.Product, err error) bool { return err == nil && p.UnitPrice == 1600 })
}
