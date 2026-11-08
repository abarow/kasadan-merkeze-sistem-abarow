// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"abarow.com/pkg/money"
	"abarow.com/services/modules/retail/internal/adapters/out/memory"
	"abarow.com/services/modules/retail/internal/app"
	"abarow.com/services/modules/retail/internal/domain"
)

type products map[string]domain.Product

func (p products) ByBarcode(_ context.Context, barcode string) (domain.Product, error) {
	if prod, ok := p[barcode]; ok {
		return prod, nil
	}
	return domain.Product{}, app.ErrProductNotFound
}

func (p products) Search(context.Context, string, int) ([]domain.Product, error) { return nil, nil }

type sales struct {
	calls   int
	cashier string
}

func (s *sales) CreateSale(_ context.Context, cashierID string, lines []domain.Line) (string, error) {
	s.cashier = cashierID
	s.calls++
	return fmt.Sprintf("sale-%d", s.calls), nil
}

func setup() (*app.Service, *sales) {
	catalog := products{
		"8690000000012": {ID: "ekmek", Name: "Ekmek", Barcode: "8690000000012", Unit: domain.Piece, UnitPrice: 1500, TaxRate: 100},
		"2700001":       {ID: "domates", Name: "Domates", Barcode: "2700001", Unit: domain.Kg, UnitPrice: 4990, TaxRate: 100},
	}
	s := &sales{}
	n := 0
	return app.NewService(memory.NewBaskets(), catalog, s, func() string { n++; return fmt.Sprintf("b%d", n) }), s
}

func TestScanAndCheckout(t *testing.T) {
	svc, sales := setup()
	ctx := context.Background()
	b, err := svc.NewBasket(ctx, "ayse")
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"8690000000012", " 8690000000012 ", "2700001012504"} {
		if b, err = svc.AddBarcode(ctx, b.ID, code); err != nil {
			t.Fatalf("%s: %v", code, err)
		}
	}
	if len(b.Lines) != 2 || b.Lines[0].Qty != 2000 || b.Lines[1].Qty != 1250 || !b.Lines[1].Weighed {
		t.Fatalf("lines are wrong: %+v", b.Lines)
	}

	b, err = svc.Checkout(ctx, b.ID, "ayse")
	if err != nil || b.SaleID != "sale-1" || b.Status != domain.CheckedOut || sales.cashier != "ayse" {
		t.Fatalf("Checkout: %+v, %v (cashier %q)", b, err, sales.cashier)
	}
	// A repeated request does not create a second sale.
	if b, err = svc.Checkout(ctx, b.ID, "ayse"); err != nil || b.SaleID != "sale-1" || sales.calls != 1 {
		t.Errorf("second Checkout: %+v, %v (CreateSale %d times)", b, err, sales.calls)
	}
}

func TestScanErrors(t *testing.T) {
	svc, _ := setup()
	ctx := context.Background()
	b, _ := svc.NewBasket(ctx, "ayse")
	tests := map[string]error{
		"0000000000000": app.ErrProductNotFound,
		"2700001012505": domain.ErrInvalidScale, // the check digit is wrong
		"2700001":       domain.ErrNeedsWeight,  // a weighed product without a scale
	}
	for code, want := range tests {
		if _, err := svc.AddBarcode(ctx, b.ID, code); !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", code, err, want)
		}
	}
	if _, err := svc.AddBarcode(ctx, "yok", "8690000000012"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("non-existent basket: err = %v, want %v", err, app.ErrNotFound)
	}
	if _, err := svc.Checkout(ctx, b.ID, "ayse"); !errors.Is(err, domain.ErrEmpty) {
		t.Errorf("empty basket: err = %v, want %v", err, domain.ErrEmpty)
	}
}

// scale, kasadaki teraziyi taklit eder.
type scale struct {
	grams money.Qty
	err   error
	calls int
}

func (s *scale) Weigh(context.Context) (money.Qty, error) {
	s.calls++
	return s.grams, s.err
}

// When a weighed product is chosen without a scale barcode the weight is read from the scale at the register;
// if there is no scale the scale barcode is asked for; a product sold by the piece is not weighed.
func TestScaleWeighing(t *testing.T) {
	svc, _ := setup()
	ctx := context.Background()
	sc := &scale{grams: 1250}
	svc.WithScale(sc)
	b, _ := svc.NewBasket(ctx, "ayse")
	b, err := svc.AddBarcode(ctx, b.ID, "2700001")
	if err != nil || len(b.Lines) != 1 || b.Lines[0].Qty != 1250 || !b.Lines[0].Weighed {
		t.Fatalf("weighing: %+v %v", b, err)
	}
	if b, err = svc.AddBarcode(ctx, b.ID, "8690000000012"); err != nil || sc.calls != 1 {
		t.Errorf("a product sold by the piece was weighed: %d %v", sc.calls, err)
	}
	if _, err := svc.AddBarcode(ctx, b.ID, "2700001012504"); err != nil || sc.calls != 1 {
		t.Errorf("terazi barkodunda teraziye soruldu: %d %v", sc.calls, err)
	}
	moving := errors.New("terazi sabitlenmedi; ürün durunca yeniden seçin")
	for weighErr, want := range map[error]error{app.ErrNoScale: domain.ErrNeedsWeight, moving: moving} {
		sc.err = weighErr
		before, _ := svc.Basket(ctx, b.ID)
		if _, err := svc.AddBarcode(ctx, b.ID, "2700001"); !errors.Is(err, want) {
			t.Errorf("%v: %v", weighErr, err)
		}
		if after, _ := svc.Basket(ctx, b.ID); len(after.Lines) != len(before.Lines) {
			t.Errorf("%v: a line was added", weighErr)
		}
	}
}

func TestCurrentBasket(t *testing.T) {
	svc, _ := setup()
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }

	if _, err := svc.CurrentBasket(ctx, "ayse"); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("cashier without a basket: %v", err)
	}
	old, _ := svc.NewBasket(ctx, "ayse")
	now = now.Add(time.Minute)
	b, _ := svc.NewBasket(ctx, "ayse")
	now = now.Add(time.Minute)
	if _, err := svc.AddBarcode(ctx, b.ID, "8690000000012"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.NewBasket(ctx, "mert"); err != nil {
		t.Fatal(err)
	}

	// A cashier continues from their own last basket; they do not see anyone else's.
	cur, err := svc.CurrentBasket(ctx, "ayse")
	if err != nil || cur.ID != b.ID || len(cur.Lines) != 1 {
		t.Errorf("continued basket: %+v, %v; want %s", cur, err, b.ID)
	}
	// Eski sepete dokunulunca o son sepet olur.
	now = now.Add(time.Minute)
	if _, err := svc.AddBarcode(ctx, old.ID, "8690000000012"); err != nil {
		t.Fatal(err)
	}
	if cur, _ := svc.CurrentBasket(ctx, "ayse"); cur.ID != old.ID {
		t.Errorf("last changed basket %s, want %s", cur.ID, old.ID)
	}

	// A basket older than the session duration is stale; one past the retention period is deleted.
	now = now.Add(app.ResumeWindow + time.Minute)
	if _, err := svc.CurrentBasket(ctx, "ayse"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("a stale basket was continued: %v", err)
	}
	now = now.Add(app.Retention)
	if _, err := svc.NewBasket(ctx, "ayse"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Basket(ctx, b.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("a basket past its retention period was not deleted: %v", err)
	}
}

// slowProducts delays the catalog's answer: it widens the gap between the moment the basket is read
// and the moment it is saved (in reality the catalog is a gRPC call).
type slowProducts struct{ products }

func (p slowProducts) ByBarcode(ctx context.Context, barcode string) (domain.Product, error) {
	time.Sleep(10 * time.Millisecond)
	return p.products.ByBarcode(ctx, barcode)
}

// slowSales delays creating the sale.
type slowSales struct{ sales }

func (s *slowSales) CreateSale(ctx context.Context, cashierID string, lines []domain.Line) (string, error) {
	time.Sleep(30 * time.Millisecond)
	return s.sales.CreateSale(ctx, cashierID, lines)
}

// TestConcurrentScans: with a fast barcode scanner the scans come at the same time;
// none is lost (if one were lost an incomplete amount would be reflected to the customer).
func TestConcurrentScans(t *testing.T) {
	catalog := slowProducts{products{"8690000000012": {ID: "ekmek", Name: "Ekmek", Barcode: "8690000000012", Unit: domain.Piece, UnitPrice: 1500, TaxRate: 100}}}
	svc := app.NewService(memory.NewBaskets(), catalog, &sales{}, func() string { return "b1" })
	ctx := context.Background()
	b, _ := svc.NewBasket(ctx, "ayse")
	const n = 10
	errs := make(chan error, n)
	for range n {
		go func() {
			_, err := svc.AddBarcode(ctx, b.ID, "8690000000012")
			errs <- err
		}()
	}
	for range n {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	b, _ = svc.Basket(ctx, b.ID)
	if len(b.Lines) != 1 || b.Lines[0].Qty != n*1000 {
		t.Fatalf("basket after %d scans: %+v", n, b.Lines)
	}
}

// TestScanDuringCheckout: a scan that comes while moving to payment does not reopen
// a basket whose sale has been created; the scan is rejected.
func TestScanDuringCheckout(t *testing.T) {
	catalog := products{"8690000000012": {ID: "ekmek", Name: "Ekmek", Barcode: "8690000000012", Unit: domain.Piece, UnitPrice: 1500, TaxRate: 100}}
	s := &slowSales{}
	svc := app.NewService(memory.NewBaskets(), catalog, s, func() string { return "b1" })
	ctx := context.Background()
	b, _ := svc.NewBasket(ctx, "ayse")
	if _, err := svc.AddBarcode(ctx, b.ID, "8690000000012"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := svc.Checkout(ctx, b.ID, "ayse")
		done <- err
	}()
	time.Sleep(10 * time.Millisecond) // while the sale is being created
	_, scanErr := svc.AddBarcode(ctx, b.ID, "8690000000012")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	b, _ = svc.Basket(ctx, b.ID)
	if b.Status != domain.CheckedOut || b.SaleID != "sale-1" || len(b.Lines) != 1 || b.Lines[0].Qty != 1000 {
		t.Fatalf("basket: %+v", b)
	}
	if !errors.Is(scanErr, domain.ErrClosed) {
		t.Errorf("scan into a basket in payment: %v", scanErr)
	}
}

func TestSetQtyRemoveReopenSearch(t *testing.T) {
	ctx := context.Background()
	svc, _ := setup()
	b, err := svc.NewBasket(ctx, "ayse")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddBarcode(ctx, b.ID, "8690000000012"); err != nil {
		t.Fatal(err)
	}
	b, err = svc.SetQty(ctx, b.ID, 0, 3000)
	if err != nil || b.Total() != 4500 {
		t.Fatalf("SetQty: %v, total %v", err, b.Total())
	}
	if _, err := svc.SetQty(ctx, b.ID, 5, 1000); !errors.Is(err, domain.ErrNoLine) {
		t.Errorf("non-existent line: %v", err)
	}
	if _, err := svc.SetQty(ctx, b.ID, 0, 0); !errors.Is(err, domain.ErrInvalidQty) {
		t.Errorf("zero quantity: %v", err)
	}
	if _, err := svc.SetQty(ctx, "yok", 0, 1000); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("basket that does not exist: %v", err)
	}
	if _, err := svc.Checkout(ctx, b.ID, "ayse"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveLine(ctx, b.ID, 0); !errors.Is(err, domain.ErrClosed) {
		t.Errorf("deleting a line from a closed basket: %v", err)
	}
	b, err = svc.Reopen(ctx, b.ID)
	if err != nil || b.Status != domain.Open {
		t.Fatalf("Reopen: %v, status %v", err, b.Status)
	}
	b, err = svc.RemoveLine(ctx, b.ID, 0)
	if err != nil || len(b.Lines) != 0 {
		t.Fatalf("RemoveLine: %v, line %d", err, len(b.Lines))
	}
	if got, err := svc.Search(ctx, "ekmek"); err != nil || got != nil {
		t.Errorf("Search: %v %v", got, err)
	}
}
