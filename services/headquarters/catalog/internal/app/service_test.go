// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"

	"abarow.com/pkg/menu"
	"abarow.com/pkg/outbox"
	"abarow.com/services/headquarters/catalog/internal/adapters/out/eventproto"
	"abarow.com/services/headquarters/catalog/internal/app"
	"abarow.com/services/headquarters/catalog/internal/domain"
)

// memRepo is the app.Repository that keeps the catalog in memory.
type memRepo struct {
	products map[string]domain.Product
	changes  []app.Change
	outbox   []outbox.Message
	images   map[string]domain.Image
}

func (m *memRepo) Get(_ context.Context, code string) (domain.Product, error) {
	p, ok := m.products[code]
	if !ok {
		return domain.Product{}, domain.ErrNotFound
	}
	return p, nil
}

func (m *memRepo) List(context.Context, string, int) ([]domain.Product, error) { return nil, nil }

func (m *memRepo) Create(_ context.Context, p domain.Product, c app.Change, msg outbox.Message) error {
	if _, ok := m.products[p.Code]; ok {
		return domain.ErrExists
	}
	m.products[p.Code] = p
	m.changes = append(m.changes, c)
	m.outbox = append(m.outbox, msg)
	return nil
}

func (m *memRepo) Update(_ context.Context, p domain.Product, c app.Change, msg outbox.Message) error {
	if old, ok := m.products[p.Code]; !ok || old.Version != p.Version-1 {
		return domain.ErrConflict
	}
	m.products[p.Code] = p
	m.changes = append(m.changes, c)
	m.outbox = append(m.outbox, msg)
	return nil
}

func (m *memRepo) History(context.Context, string, int) ([]app.Change, error) { return m.changes, nil }

func newService(t *testing.T, now *time.Time) (*app.Service, *memRepo) {
	t.Helper()
	repo := &memRepo{products: map[string]domain.Product{}}
	n := 0
	enc := eventproto.Encoder{NewID: func() string { n++; return fmt.Sprintf("e%d", n) }}
	ist, _ := time.LoadLocation("Europe/Istanbul")
	svc := app.NewService(repo, enc, ist)
	app.SetClock(svc, func() time.Time { return *now })
	return svc, repo
}

func TestCatalogChanges(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	svc, repo := newService(t, &now)

	p, err := svc.Create(ctx, "ayse", "ekmek", app.Info{Name: "Ekmek 250 g", Unit: domain.Piece, TaxRate: 100, Barcodes: []string{"8690000000012"}, QuickKey: true}, 1500)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != 1 || p.UpdatedBy != "ayse" {
		t.Errorf("new product %+v", p)
	}
	if _, err := svc.Create(ctx, "ayse", "ekmek", app.Info{Name: "X", Unit: domain.Piece}, 1); !errors.Is(err, domain.ErrExists) {
		t.Errorf("same code: %v", err)
	}

	// Future-dated price: Monday 00:00 (Istanbul).
	monday := time.Date(2026, 11, 1, 21, 0, 0, 0, time.UTC)
	p, err = svc.SchedulePrice(ctx, "mehmet", "ekmek", 1, 1650, monday)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != 2 || len(p.Prices) != 2 || p.UpdatedBy != "mehmet" {
		t.Errorf("price schedule %+v", p)
	}
	if c := repo.changes[len(repo.changes)-1]; c.By != "mehmet" || c.Action != app.ActionPriceScheduled || c.Detail != "fiyat 16,50 TL, 02.11.2026 00:00 itibarıyla" {
		t.Errorf("change record %+v", c)
	}

	// A change with an old version is rejected (someone else changed it in the meantime).
	if _, err := svc.Update(ctx, "ayse", "ekmek", 1, app.Info{Name: "Ekmek", Unit: domain.Piece, TaxRate: 100}); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("old version: %v", err)
	}
	p, err = svc.Update(ctx, "ayse", "ekmek", 2, app.Info{Name: "Ekmek 300 g", Unit: domain.Piece, TaxRate: 100, Barcodes: []string{"8690000000012", "8690000000099"}})
	if err != nil {
		t.Fatal(err)
	}
	if d := repo.changes[len(repo.changes)-1].Detail; !strings.Contains(d, `ad "Ekmek 250 g" → "Ekmek 300 g"`) || !strings.Contains(d, "hızlı tuştan çıkarıldı") {
		t.Errorf("update summary %q", d)
	}
	if _, err := svc.Update(ctx, "ayse", "ekmek", 3, app.Info{Name: "", Unit: domain.Piece}); !errors.Is(err, domain.ErrInvalidProduct) {
		t.Errorf("empty name: %v", err)
	}

	// Withdrawal from sale; the latest state sent to the register.
	p, err = svc.SetDiscontinued(ctx, "ayse", "ekmek", 3, true)
	if err != nil || !p.Discontinued || p.Version != 4 {
		t.Fatalf("withdrawal from sale %+v %v", p, err)
	}
	msg := repo.outbox[len(repo.outbox)-1]
	if msg.Subject != "catalog.product.ekmek" || msg.Type != "com.abarow.catalog.product.v1" || msg.ContentType != "application/protobuf" || !msg.OccurredAt.Equal(now) {
		t.Errorf("message %+v", msg)
	}
	var state catalogv1.ProductState
	if err := proto.Unmarshal(msg.Payload, &state); err != nil {
		t.Fatal(err)
	}
	if state.GetEventId() != msg.ID || !state.GetDiscontinued() || state.GetProduct().GetName() != "Ekmek 300 g" || len(state.GetProduct().GetBarcodes()) != 2 ||
		len(state.GetPrices()) != 2 || state.GetPrices()[0].GetFrom() != nil || state.GetPrices()[1].GetUnitPriceKurus() != 1650 ||
		!state.GetPrices()[1].GetFrom().AsTime().Equal(monday) || state.GetUpdatedBy() != "ayse" {
		t.Errorf("latest state of the product %v", &state)
	}
	if len(repo.outbox) != 4 || len(repo.changes) != 4 {
		t.Errorf("%d messages, %d records; should be 4", len(repo.outbox), len(repo.changes))
	}

	// A future-dated price is withdrawn.
	p, err = svc.CancelPrice(ctx, "ayse", "ekmek", 4, monday)
	if err != nil || len(p.Prices) != 1 {
		t.Errorf("undo: %+v %v", p.Prices, err)
	}
	if _, err := svc.SchedulePrice(ctx, "ayse", "yok", 1, 1, time.Time{}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("non-existent product: %v", err)
	}
}

func TestImport(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	svc, repo := newService(t, &now)
	items := []domain.Product{
		{Code: "ekmek", Name: "Ekmek", Unit: domain.Piece, TaxRate: 100, Prices: []domain.Price{{Amount: 1500}}},
		{Code: "domates", Name: "Domates", Unit: domain.Kg, TaxRate: 100, Prices: []domain.Price{{Amount: 4990}, {Amount: 5490, From: now.Add(72 * time.Hour)}}},
	}
	if n, err := svc.Import(ctx, "import", items); err != nil || n != 2 {
		t.Fatalf("load: %d %v", n, err)
	}
	// The second load updates the existing one; it keeps its withdrawn-from-sale state.
	if _, err := svc.SetDiscontinued(ctx, "ayse", "ekmek", 1, true); err != nil {
		t.Fatal(err)
	}
	items[0].Name = "Ekmek 250 g"
	if n, err := svc.Import(ctx, "import", items[:1]); err != nil || n != 1 {
		t.Fatalf("second load: %d %v", n, err)
	}
	if p := repo.products["ekmek"]; p.Name != "Ekmek 250 g" || p.Version != 3 || !p.Discontinued {
		t.Errorf("updated product %+v", p)
	}
	// An invalid product stops the load.
	if n, err := svc.Import(ctx, "import", []domain.Product{{Code: "a.b", Name: "X", Unit: domain.Piece, Prices: []domain.Price{{Amount: 1}}}}); !errors.Is(err, domain.ErrInvalidProduct) || n != 0 {
		t.Errorf("invalid product: %d %v", n, err)
	}
}

func (m *memRepo) SetImage(_ context.Context, p domain.Product, img domain.Image, c app.Change, msgs []outbox.Message) error {
	if old, ok := m.products[p.Code]; !ok || old.Version != p.Version-1 {
		return domain.ErrConflict
	}
	m.products[p.Code] = p
	if m.images == nil {
		m.images = map[string]domain.Image{}
	}
	m.images[p.Code] = img
	m.changes = append(m.changes, c)
	m.outbox = append(m.outbox, msgs...)
	return nil
}

func (m *memRepo) Image(_ context.Context, code string) (domain.Image, error) {
	img, ok := m.images[code]
	if !ok || len(img.Data) == 0 {
		return domain.Image{}, domain.ErrNotFound
	}
	return img, nil
}

// The menu details go to the registers with the product; the image is checked from its content,
// the image's message is written to the outbox first and then the product's; loading from a file
// does not delete an uploaded image.
func TestMenuAndImage(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	svc, repo := newService(t, &now)
	groups := []menu.Group{{ID: "pisirme", Name: "Pişirme", Min: 1, Max: 1, Options: []menu.Option{{ID: "az", Name: "az"}, {ID: "iyi", Name: "iyi"}}}}
	p, err := svc.Create(ctx, "ayse", "kofte", app.Info{Name: "Köfte", Unit: domain.Piece, TaxRate: 1000,
		Category: "  Ana   yemekler ", MenuOrder: 20, Station: menu.Kitchen, Options: groups}, 25000)
	if err != nil || p.Category != "Ana yemekler" {
		t.Fatalf("menu product: %+v %v", p, err)
	}
	if _, err := svc.Update(ctx, "ayse", "kofte", 1, app.Info{Name: "Köfte", Unit: domain.Piece, Station: "salon"}); !errors.Is(err, domain.ErrInvalidProduct) {
		t.Errorf("unknown station: %v", err)
	}
	if _, err := svc.SetImage(ctx, "ayse", "kofte", 1, []byte("<svg/>")); !errors.Is(err, domain.ErrInvalidProduct) {
		t.Errorf("SVG: %v", err)
	}
	png := []byte("\x89PNG\r\n\x1a\nveri")
	p, err = svc.SetImage(ctx, "ayse", "kofte", 1, png)
	if err != nil || p.Version != 2 || p.ImageType != "image/png" || len(p.ImageSHA256) != 64 {
		t.Fatalf("image: %+v %v", p, err)
	}
	n := len(repo.outbox)
	if repo.outbox[n-2].Subject != "catalog.image.kofte" || repo.outbox[n-1].Subject != "catalog.product.kofte" {
		t.Errorf("message order: %s, %s", repo.outbox[n-2].Subject, repo.outbox[n-1].Subject)
	}
	var state catalogv1.ProductState
	if err := proto.Unmarshal(repo.outbox[n-1].Payload, &state); err != nil {
		t.Fatal(err)
	}
	if pp := state.GetProduct(); pp.GetCategory() != "Ana yemekler" || pp.GetImageSha256() != p.ImageSHA256 || len(pp.GetOptionGroups()) != 1 {
		t.Errorf("product message %v", pp)
	}
	if _, err := svc.Import(ctx, "dosya", []domain.Product{{Code: "kofte", Name: "Köfte", Unit: domain.Piece, Prices: []domain.Price{{Amount: 26000}}}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Get(ctx, "kofte"); got.ImageSHA256 != p.ImageSHA256 {
		t.Errorf("loading from a file deleted the image: %+v", got)
	}
	got, _ := svc.Get(ctx, "kofte")
	if p, err = svc.RemoveImage(ctx, "ayse", "kofte", got.Version); err != nil || p.ImageSHA256 != "" {
		t.Errorf("image removal: %+v %v", p, err)
	}
	if _, err := svc.Image(ctx, "kofte"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("removed image: %v", err)
	}
}
