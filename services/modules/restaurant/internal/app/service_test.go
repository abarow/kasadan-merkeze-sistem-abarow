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

	"abarow.com/pkg/menu"
	"abarow.com/pkg/pgdb/pgdbtest"
	"abarow.com/services/modules/restaurant/internal/adapters/out/postgres"
	"abarow.com/services/modules/restaurant/internal/app"
	"abarow.com/services/modules/restaurant/internal/domain"
)

type fakeMenu map[string]domain.Product

func (m fakeMenu) Menu(context.Context) ([]app.MenuItem, error) { return nil, nil }
func (m fakeMenu) Product(_ context.Context, id string) (domain.Product, error) {
	if p, ok := m[id]; ok {
		return p, nil
	}
	return domain.Product{}, app.ErrProductNotFound
}
func (m fakeMenu) Image(context.Context, string) (app.Image, error) {
	return app.Image{}, domain.ErrNotFound
}

type fakeSales struct {
	n      int
	lines  map[string][]app.SaleLine
	status map[string]app.SaleStatus
}

func (s *fakeSales) CreateSale(_ context.Context, _ string, lines []app.SaleLine) (string, error) {
	s.n++
	id := fmt.Sprintf("s%d", s.n)
	s.lines[id], s.status[id] = lines, app.SaleOpen
	return id, nil
}
func (s *fakeSales) Status(_ context.Context, id string) (app.SaleStatus, error) {
	return s.status[id], nil
}

type printed struct{ station, title, text string }

type fakePrinter struct {
	docs []printed
	// broken are the stations that cannot print.
	broken map[string]bool
}

func (p *fakePrinter) Columns(context.Context) (int, error) { return 32, nil }
func (p *fakePrinter) Print(_ context.Context, station, title string, lines []string) error {
	if p.broken[station] {
		return errors.New("kağıt bitti")
	}
	p.docs = append(p.docs, printed{station, title, strings.Join(lines, "\n")})
	return nil
}

// fakeApprover: "mudur" is a manager; the "onay" key is a valid approval.
type fakeApprover struct{}

func (fakeApprover) check(cashier, token string) (string, error) {
	if cashier == "mudur" {
		return "mudur", nil
	}
	if token == "onay" {
		return "mudur", nil
	}
	return "", app.ErrNotAuthorized
}
func (a fakeApprover) VoidLine(_ context.Context, c, t string) (string, error)  { return a.check(c, t) }
func (a fakeApprover) EditFloor(_ context.Context, c, t string) (string, error) { return a.check(c, t) }

var (
	ali     = app.Person{ID: "ali", Name: "Ali"}
	manager = app.Person{ID: "mudur", Name: "Müdür"}
	floor   = []domain.Area{
		{ID: "ic", Name: "İç salon", Tables: []domain.Table{{ID: "1", Name: "Masa 1"}, {ID: "2", Name: "Masa 2"}}},
		{ID: "bahce", Name: "Bahçe", Tables: []domain.Table{{ID: "b1", Name: "Bahçe 1"}}},
	}
	products = fakeMenu{
		"kofte": {ID: "kofte", Name: "Köfte", UnitPrice: 32000, TaxRate: 1000, Options: []menu.Group{
			{ID: "pisirme", Name: "Pişirme", Min: 1, Max: 1, Options: []menu.Option{{ID: "az", Name: "az pişmiş"}, {ID: "iyi", Name: "iyi pişmiş"}}}}},
		"ayran": {ID: "ayran", Name: "Ayran", UnitPrice: 4000, TaxRate: 1000, Station: menu.Bar},
	}
)

func setup(t *testing.T) (*app.Service, *fakeSales, *fakePrinter) {
	t.Helper()
	ctx := context.Background()
	store, err := postgres.Open(ctx, pgdbtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	sales := &fakeSales{lines: map[string][]app.SaleLine{}, status: map[string]app.SaleStatus{}}
	printer := &fakePrinter{broken: map[string]bool{}}
	n := 0
	svc := app.NewService(store, products, sales, printer, fakeApprover{}, func() string { n++; return fmt.Sprintf("id-%d", n) })
	svc.Now = func() time.Time { return time.Date(2026, 10, 30, 19, 0, 0, 0, time.UTC) }
	if err := svc.SetFloor(ctx, manager, "", floor); err != nil {
		t.Fatal(err)
	}
	return svc, sales, printer
}

// Even if opening a table comes twice there is one check; even if a line comes again there is one line;
// kitchen and bar are printed on separate tickets; payment and bill splitting; when the check closes
// the table is freed.
func TestOrderFlow(t *testing.T) {
	ctx := context.Background()
	svc, sales, printer := setup(t)
	o, err := svc.OpenTable(ctx, "1", ali)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := svc.OpenTable(ctx, "1", manager); again.ID != o.ID {
		t.Errorf("the second opening opened a new check")
	}
	if _, err := svc.OpenTable(ctx, "99", ali); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("olmayan masa: %v", err)
	}
	meatball := app.AddInput{ID: "l-1", ProductID: "kofte", Qty: 2, Choices: []menu.Choice{{Group: "pisirme", Options: []string{"az"}}}, Note: "soğansız"}
	for range 2 {
		if o, err = svc.AddLine(ctx, o.ID, meatball, ali); err != nil {
			t.Fatal(err)
		}
	}
	if o, err = svc.AddLine(ctx, o.ID, app.AddInput{ID: "l-2", ProductID: "ayran", Qty: 3}, ali); err != nil || len(o.Lines) != 2 {
		t.Fatalf("lines: %+v %v", o, err)
	}
	if _, err := svc.AddLine(ctx, o.ID, app.AddInput{ID: "l-3", ProductID: "yok", Qty: 1}, ali); !errors.Is(err, app.ErrProductNotFound) {
		t.Errorf("product not on the menu: %v", err)
	}

	if o, err = svc.Send(ctx, o.ID, ali); err != nil {
		t.Fatal(err)
	}
	if len(printer.docs) != 2 || printer.docs[0].station != menu.Bar || printer.docs[1].station != menu.Kitchen ||
		!strings.Contains(printer.docs[1].text, "NOT: soğansız") || !strings.Contains(printer.docs[1].title, "Masa 1") {
		t.Fatalf("station tickets: %+v", printer.docs)
	}
	if o, err = svc.Send(ctx, o.ID, ali); err != nil || len(printer.docs) != 2 {
		t.Errorf("the second send printed again: %d tickets, %v", len(printer.docs), err)
	}

	// Bill: the check printout goes to the receipt printer, it has no fiscal value.
	if o, err = svc.Bill(ctx, o.ID, ali); err != nil {
		t.Fatal(err)
	}
	if last := printer.docs[len(printer.docs)-1]; last.station != "" || !strings.Contains(last.text, domain.NotFiscal) || o.BillAt.IsZero() {
		t.Errorf("adisyon: %+v", last)
	}

	// Bill splitting: first 1 meatball; there is no second payment while one is in progress.
	o, saleID, err := svc.Pay(ctx, o.ID, o.Version, []domain.Part{{LineID: "l-1", Qty: 1}}, ali)
	if err != nil {
		t.Fatal(err)
	}
	if l := sales.lines[saleID]; len(l) != 1 || l[0].Name != "Köfte (az pişmiş)" || l[0].Qty != 1 || l[0].UnitPrice != 32000 {
		t.Errorf("sale lines %+v", l)
	}
	if _, _, err := svc.Pay(ctx, o.ID, 0, nil, ali); !errors.Is(err, domain.ErrPaymentPending) {
		t.Errorf("second payment with a payment in progress: %v", err)
	}
	// The sale has not been paid yet: no change; once paid the quantities are applied.
	if o, _ = svc.Settle(ctx, o.ID); o.Payment == nil {
		t.Error("an open sale counted as paid")
	}
	sales.status[saleID] = app.SalePaid
	if o, err = svc.Order(ctx, o.ID); err != nil || o.Payment != nil || o.Lines[0].Paid != 1 || o.Closed {
		t.Fatalf("partial payment: %+v %v", o, err)
	}
	// A payment with an old version is rejected.
	if _, _, err := svc.Pay(ctx, o.ID, 1, nil, ali); !errors.Is(err, domain.ErrStale) {
		t.Errorf("old version: %v", err)
	}
	o, rest, err := svc.Pay(ctx, o.ID, o.Version, nil, ali)
	if err != nil || len(sales.lines[rest]) != 2 {
		t.Fatalf("kalan: %+v %v", sales.lines[rest], err)
	}
	sales.status[rest] = app.SalePaid
	areas, err := svc.Floor(ctx)
	if err != nil || areas[0].Tables[0].Order != nil {
		t.Errorf("all paid but the table is occupied: %+v %v", areas, err)
	}
}

// Cancelling a sent line needs the manager's approval; a cancellation ticket goes to the station.
func TestVoidNeedsManager(t *testing.T) {
	ctx := context.Background()
	svc, _, printer := setup(t)
	o, _ := svc.OpenTable(ctx, "2", ali)
	o, _ = svc.AddLine(ctx, o.ID, app.AddInput{ID: "l-1", ProductID: "ayran", Qty: 2}, ali)
	o, _ = svc.Send(ctx, o.ID, ali)
	if _, err := svc.Void(ctx, o.ID, "l-1", 1, "döküldü", ali, ""); !errors.Is(err, app.ErrNotAuthorized) {
		t.Errorf("cancellation without approval: %v", err)
	}
	o, err := svc.Void(ctx, o.ID, "l-1", 1, "döküldü", ali, "onay")
	if err != nil || o.Lines[0].Qty != 1 || o.Lines[1].Status != domain.Voided || !strings.Contains(o.Lines[1].VoidedBy, "onay: mudur") {
		t.Fatalf("approved cancellation: %+v %v", o, err)
	}
	if last := printer.docs[len(printer.docs)-1]; last.station != menu.Bar || !strings.HasPrefix(last.title, "İPTAL") {
		t.Errorf("cancellation ticket %+v", last)
	}
}

// If one station's printer cannot print, its lines wait while the other
// station's ticket is printed; when sent again only the waiting ones are printed.
func TestSendWithBrokenPrinter(t *testing.T) {
	ctx := context.Background()
	svc, _, printer := setup(t)
	o, _ := svc.OpenTable(ctx, "1", ali)
	o, _ = svc.AddLine(ctx, o.ID, app.AddInput{ID: "l-1", ProductID: "ayran", Qty: 1}, ali)
	o, _ = svc.AddLine(ctx, o.ID, app.AddInput{ID: "l-2", ProductID: "kofte", Qty: 1, Choices: []menu.Choice{{Group: "pisirme", Options: []string{"iyi"}}}}, ali)
	printer.broken[menu.Kitchen] = true
	o, err := svc.Send(ctx, o.ID, ali)
	if err == nil || !strings.Contains(err.Error(), "Mutfak fişi basılamadı") || o.Lines[0].Status != domain.Sent || o.Lines[1].Status != domain.Pending {
		t.Fatalf("partial send: %+v %v", o, err)
	}
	printer.broken[menu.Kitchen] = false
	if o, err = svc.Send(ctx, o.ID, ali); err != nil || o.Lines[1].Status != domain.Sent || len(printer.docs) != 2 {
		t.Errorf("sending again: %d tickets %v", len(printer.docs), err)
	}
}

// Moving is to an empty table; in a merge the other table is freed; a table
// with an open check cannot be removed from the floor; the floor layout needs the manager's approval.
func TestMoveMergeAndFloor(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := setup(t)
	a, _ := svc.OpenTable(ctx, "1", ali)
	a, _ = svc.AddLine(ctx, a.ID, app.AddInput{ID: "l-1", ProductID: "ayran", Qty: 1}, ali)
	b, _ := svc.OpenTable(ctx, "2", ali)
	b, _ = svc.AddLine(ctx, b.ID, app.AddInput{ID: "l-2", ProductID: "ayran", Qty: 2}, ali)
	if _, err := svc.Move(ctx, a.ID, a.Version, "2"); !errors.Is(err, domain.ErrTableBusy) {
		t.Errorf("moving to an occupied table: %v", err)
	}
	a, err := svc.Move(ctx, a.ID, a.Version, "b1")
	if err != nil || a.TableID != "b1" {
		t.Fatalf("moving: %+v %v", a, err)
	}
	a, err = svc.Merge(ctx, a.ID, a.Version, b.ID, b.Version)
	if err != nil || len(a.Lines) != 2 {
		t.Fatalf("merging: %+v %v", a, err)
	}
	areas, _ := svc.Floor(ctx)
	if areas[0].Tables[1].Order != nil || areas[1].Tables[0].Order == nil {
		t.Errorf("salon: %+v", areas)
	}
	if err := svc.SetFloor(ctx, ali, "", floor); !errors.Is(err, app.ErrNotAuthorized) {
		t.Errorf("floor layout without approval: %v", err)
	}
	if err := svc.SetFloor(ctx, manager, "", floor[:1]); !errors.Is(err, domain.ErrTableBusy) {
		t.Errorf("removing an occupied table: %v", err)
	}
}

// Changes are announced with the event stream.
func TestEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc, _, _ := setup(t)
	events := svc.Subscribe(ctx)
	o, _ := svc.OpenTable(ctx, "1", ali)
	select {
	case e := <-events:
		if e.Type != "order" || e.OrderID != o.ID || e.TableID != "1" {
			t.Errorf("olay %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("the event did not come")
	}
}
