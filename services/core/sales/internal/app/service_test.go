// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"

	"abarow.com/pkg/money"
	"abarow.com/services/core/sales/internal/app"
	"abarow.com/services/core/sales/internal/app/apptest"
	"abarow.com/services/core/sales/internal/domain"
)

// memRepo is an in-memory Repository that imitates transaction behavior: if the fn in InTx
// returns an error the changes are discarded.
type memRepo struct {
	sales  map[domain.SaleID]domain.Snapshot
	lastNo int64
	outbox []app.OutboxMessage
}

func newMemRepo() *memRepo { return &memRepo{sales: map[domain.SaleID]domain.Snapshot{}} }

func (r *memRepo) Get(_ context.Context, id domain.SaleID) (*domain.Sale, error) {
	return get(r.sales, id)
}

func (r *memRepo) ByStatus(_ context.Context, status domain.Status, limit int) ([]*domain.Sale, error) {
	var out []*domain.Sale
	for _, snap := range r.sales {
		if snap.Status == status && len(out) < limit {
			out = append(out, domain.Restore(snap))
		}
	}
	return out, nil
}

func (r *memRepo) InTx(ctx context.Context, fn func(app.Tx) error) error {
	// Like a real store: a transaction is not opened with a cancelled context.
	if err := ctx.Err(); err != nil {
		return err
	}
	tx := &memTx{sales: maps.Clone(r.sales), lastNo: r.lastNo}
	if err := fn(tx); err != nil {
		return err
	}
	r.sales, r.lastNo = tx.sales, tx.lastNo
	r.outbox = append(r.outbox, tx.outbox...)
	return nil
}

type memTx struct {
	sales  map[domain.SaleID]domain.Snapshot
	lastNo int64
	outbox []app.OutboxMessage
}

func (t *memTx) Get(_ context.Context, id domain.SaleID) (*domain.Sale, error) {
	return get(t.sales, id)
}

func (t *memTx) Save(_ context.Context, s *domain.Sale) error {
	t.sales[s.ID()] = s.Snapshot()
	return nil
}

func (t *memTx) NextReceiptNo(context.Context) (int64, error) {
	t.lastNo++
	return t.lastNo, nil
}

func (t *memTx) AppendOutbox(_ context.Context, msgs []app.OutboxMessage) error {
	t.outbox = append(t.outbox, msgs...)
	return nil
}

func get(sales map[domain.SaleID]domain.Snapshot, id domain.SaleID) (*domain.Sale, error) {
	snap, ok := sales[id]
	if !ok {
		return nil, app.ErrNotFound
	}
	return domain.Restore(snap), nil
}

type fixture struct {
	svc      *app.Service
	repo     *memRepo
	payments *apptest.Payments
	fiscal   *apptest.Fiscal
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{repo: newMemRepo(), payments: &apptest.Payments{}, fiscal: &apptest.Fiscal{}}
	svc, err := app.NewService(f.repo, f.payments, f.fiscal, apptest.Config())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	f.svc = svc
	return f
}

func (f *fixture) createSale(t *testing.T) *domain.Sale {
	t.Helper()
	lines := []domain.Line{
		{ProductID: "p1", Name: "Deterjan 1 L", Qty: money.Units(1), UnitPrice: 1299, TaxRate: 2000},
		{ProductID: "p2", Name: "Domates", Qty: 1250, UnitPrice: 8990, TaxRate: 100},
	}
	s, err := f.svc.CreateSale(context.Background(), "kasiyer-1", lines)
	if err != nil {
		t.Fatalf("CreateSale: %v", err)
	}
	return s
}

var cash20000 = []domain.Payment{{Method: domain.PaymentCash, Amount: 20000}}

func TestCheckoutCompletesSale(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := f.createSale(t)

	got, err := f.svc.Checkout(ctx, s.ID(), cash20000)
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if got.Status() != domain.StatusPaid || got.ReceiptNo() != 1 || got.FiscalNo() != "SIM-000001" || got.Change() != 7463 {
		t.Errorf("sale is wrong: %+v (change %d)", got.Snapshot(), got.Change())
	}
	stored, err := f.svc.GetSale(ctx, s.ID())
	if err != nil || stored.Status() != domain.StatusPaid {
		t.Errorf("saved sale: %v, %v", stored, err)
	}

	if len(f.repo.outbox) != 1 {
		t.Fatalf("outbox'ta %d mesaj, istenen 1", len(f.repo.outbox))
	}
	msg := f.repo.outbox[0]
	if msg.Type != "com.abarow.sale.completed.v1" || msg.Subject != "sale.completed.v1" ||
		msg.StoreID != "store-1" || msg.RegisterID != "register-1" || msg.ID == "" || msg.ID == string(s.ID()) {
		t.Errorf("outbox message is wrong: %+v", msg)
	}
	var ev domain.SaleCompleted
	if err := json.Unmarshal(msg.Payload, &ev); err != nil {
		t.Fatalf("payload could not be decoded: %v", err)
	}
	if ev.SaleID != s.ID() || ev.CashierID != "kasiyer-1" || ev.ReceiptNo != 1 || ev.Total != 12537 || ev.TaxTotal != 328 || len(ev.Lines) != 2 {
		t.Errorf("payload is wrong: %+v", ev)
	}
}

func TestCheckoutReceiptNumbersAreSequential(t *testing.T) {
	f := newFixture(t)
	for want := int64(1); want <= 3; want++ {
		got, err := f.svc.Checkout(context.Background(), f.createSale(t).ID(), cash20000)
		if err != nil {
			t.Fatal(err)
		}
		if got.ReceiptNo() != want {
			t.Errorf("receipt no = %d, want %d", got.ReceiptNo(), want)
		}
	}
}

var errOffline = errors.New("bağlantı yok")

func TestCheckoutPaymentFailureLeavesNoTrace(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := f.createSale(t)
	f.payments.Err = errOffline

	if _, err := f.svc.Checkout(ctx, s.ID(), cash20000); !errors.Is(err, errOffline) {
		t.Fatalf("err = %v, want %v", err, errOffline)
	}
	if f.fiscal.Calls != 0 {
		t.Errorf("fiscal record was called %d times, want 0", f.fiscal.Calls)
	}
	if stored, _ := f.svc.GetSale(ctx, s.ID()); stored.Status() != domain.StatusOpen || len(stored.Payments()) != 0 {
		t.Errorf("sale changed: %+v", stored.Snapshot())
	}
	if len(f.repo.outbox) != 0 {
		t.Errorf("outbox'ta %d mesaj, istenen 0", len(f.repo.outbox))
	}

	// A failed attempt must not spend a receipt number.
	f.payments.Err = nil
	got, err := f.svc.Checkout(ctx, s.ID(), cash20000)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReceiptNo() != 1 {
		t.Errorf("receipt no = %d, want 1", got.ReceiptNo())
	}
}

func TestCheckoutRetryMatchesTheSamePayment(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := f.createSale(t)
	paid := []domain.Payment{{Method: domain.PaymentCard, Amount: 12537}}
	f.fiscal.Err = errOffline

	if _, err := f.svc.Checkout(ctx, s.ID(), paid); !errors.Is(err, errOffline) {
		t.Fatalf("err = %v, want %v", err, errOffline)
	}
	stored, err := f.svc.GetSale(ctx, s.ID())
	if err != nil {
		t.Fatal(err)
	}
	wantPayments := []domain.Payment{{Method: domain.PaymentCard, Amount: 12537, Ref: "REF-1"}}
	if stored.Status() != domain.StatusCharged || !reflect.DeepEqual(stored.Payments(), wantPayments) {
		t.Fatalf("the payment was not recorded on the sale: %+v", stored.Snapshot())
	}
	// The sale whose receipt could not be issued is announced (the store server shows it).
	if len(f.repo.outbox) != 1 || f.repo.outbox[0].Subject != "sale.charged.v1" ||
		!strings.Contains(string(f.repo.outbox[0].Payload), `"reason":"`+errOffline.Error()+`"`) {
		t.Fatalf("outbox when the fiscal record failed: %+v", f.repo.outbox)
	}
	if _, err := f.svc.VoidSale(ctx, s.ID()); !errors.Is(err, domain.ErrNotOpen) {
		t.Errorf("the sale whose payment was taken was cancelled: %v", err)
	}

	// Retry: the recorded payment is used even if a different payment comes in the request.
	f.fiscal.Err = nil
	got, err := f.svc.Checkout(ctx, s.ID(), cash20000)
	if err != nil {
		t.Fatalf("tekrar deneme: %v", err)
	}
	if f.payments.Calls != 1 {
		t.Errorf("payment was taken %d times, want 1", f.payments.Calls)
	}
	if got.Status() != domain.StatusPaid || got.ReceiptNo() != 1 || !reflect.DeepEqual(got.Payments(), wantPayments) {
		t.Errorf("sale is wrong: %+v", got.Snapshot())
	}
	if len(f.repo.outbox) != 2 || f.repo.outbox[1].Subject != "sale.completed.v1" {
		t.Errorf("outbox'ta %d mesaj, istenen 2 (charged, completed)", len(f.repo.outbox))
	}
}

func TestCheckoutRejectsBeforeCharging(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := f.createSale(t)

	short := []domain.Payment{{Method: domain.PaymentCard, Amount: 100}}
	if _, err := f.svc.Checkout(ctx, s.ID(), short); !errors.Is(err, domain.ErrInsufficientPayment) {
		t.Errorf("insufficient payment: err = %v, want %v", err, domain.ErrInsufficientPayment)
	}
	if _, err := f.svc.Checkout(ctx, s.ID(), cash20000); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Checkout(ctx, s.ID(), cash20000); !errors.Is(err, domain.ErrNotOpen) {
		t.Errorf("second checkout: err = %v, want %v", err, domain.ErrNotOpen)
	}
	if f.payments.Calls != 1 {
		t.Errorf("payment was taken %d times, want 1", f.payments.Calls)
	}
}

func TestVoidSale(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := f.createSale(t)

	got, err := f.svc.VoidSale(ctx, s.ID())
	if err != nil {
		t.Fatalf("VoidSale: %v", err)
	}
	if got.Status() != domain.StatusVoided {
		t.Errorf("durum %s, istenen voided", got.Status())
	}
	if len(f.repo.outbox) != 1 || f.repo.outbox[0].Subject != "sale.voided.v1" {
		t.Errorf("outbox is wrong: %+v", f.repo.outbox)
	}
	if _, err := f.svc.Checkout(ctx, s.ID(), cash20000); !errors.Is(err, domain.ErrNotOpen) {
		t.Errorf("checkout on a cancelled sale: err = %v, want %v", err, domain.ErrNotOpen)
	}
}

func TestGetSaleNotFound(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.GetSale(context.Background(), "yok"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("err = %v, want %v", err, app.ErrNotFound)
	}
}

func TestNewServiceValidatesConfig(t *testing.T) {
	cfg := apptest.Config()
	cfg.StoreID = ""
	if _, err := app.NewService(newMemRepo(), &apptest.Payments{}, &apptest.Fiscal{}, cfg); err == nil {
		t.Error("an empty store id was accepted")
	}
	cfg = apptest.Config()
	cfg.NewID = nil
	if _, err := app.NewService(newMemRepo(), &apptest.Payments{}, &apptest.Fiscal{}, cfg); err == nil {
		t.Error("NewID olmadan servis kuruldu")
	}
}

func TestRefundCharged(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := f.createSale(t)
	if _, err := f.svc.RefundCharged(ctx, s.ID()); !errors.Is(err, domain.ErrNotCharged) {
		t.Errorf("refund on a sale whose payment was not taken: %v", err)
	}
	f.fiscal.Err = errOffline
	if _, err := f.svc.Checkout(ctx, s.ID(), []domain.Payment{{Method: domain.PaymentCard, Amount: 12537}}); !errors.Is(err, errOffline) {
		t.Fatalf("err = %v, want %v", err, errOffline)
	}

	// If the payment cannot be refunded the sale does not change, it can be retried.
	f.payments.RefundErr = errors.New("POS bağlantısı yok")
	if _, err := f.svc.RefundCharged(ctx, s.ID()); err == nil {
		t.Fatal("no refund error returned")
	}
	if stored, _ := f.svc.GetSale(ctx, s.ID()); stored.Status() != domain.StatusCharged {
		t.Fatalf("state after the failed refund %s", stored.Status())
	}

	f.payments.RefundErr = nil
	got, err := f.svc.RefundCharged(ctx, s.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status() != domain.StatusVoided || !got.Refunded() || got.VoidedAt().IsZero() || len(f.payments.Refunded) != 1 {
		t.Errorf("refunded sale: %+v", got.Snapshot())
	}
	stored, _ := f.svc.GetSale(ctx, s.ID())
	if !stored.Refunded() {
		t.Error("the refund mark was not stored")
	}
	// First the sale whose receipt could not be issued is announced; the cancellation event carries that
	// the payment was refunded; the receipt number is not allocated.
	if len(f.repo.outbox) != 2 || f.repo.outbox[0].Subject != "sale.charged.v1" || f.repo.outbox[1].Subject != "sale.voided.v1" ||
		!strings.Contains(string(f.repo.outbox[1].Payload), `"refunded":true`) {
		t.Errorf("outbox: %+v", f.repo.outbox)
	}
	if got.ReceiptNo() != 0 {
		t.Errorf("a receipt number was given to the refunded sale: %d", got.ReceiptNo())
	}
}

// collectFixture is the install where the fiscal device charges the card from its own POS.
func collectFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{repo: newMemRepo(), payments: &apptest.Payments{}, fiscal: &apptest.Fiscal{}}
	cfg := apptest.Config()
	cfg.FiscalCollects = true
	svc, err := app.NewService(f.repo, f.payments, f.fiscal, cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	f.svc = svc
	return f
}

var card12537 = []domain.Payment{{Method: domain.PaymentCard, Amount: 12537}}

func TestCollectedCardIsTakenByTheDevice(t *testing.T) {
	f := collectFixture(t)
	ctx := context.Background()
	s := f.createSale(t)

	got, err := f.svc.Checkout(ctx, s.ID(), card12537)
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if f.payments.Calls != 0 {
		t.Errorf("payment service was called %d times, want 0 (the device charges the card)", f.payments.Calls)
	}
	if f.fiscal.Calls != 1 || f.fiscal.Last == nil || len(f.fiscal.Last.Payments()) != 1 || !f.fiscal.Last.Payments()[0].Collect {
		t.Errorf("the payment that went to the device is not Collect: %+v", f.fiscal.Last)
	}
	want := []domain.Payment{{Method: domain.PaymentCard, Amount: 12537, Collect: true}}
	if got.Status() != domain.StatusPaid || got.ReceiptNo() != 1 || got.FiscalNo() != "SIM-000001" || !reflect.DeepEqual(got.Payments(), want) {
		t.Errorf("sale is wrong: %+v", got.Snapshot())
	}
	// The card payment and completion in the same transaction: a single event.
	if len(f.repo.outbox) != 1 || f.repo.outbox[0].Subject != "sale.completed.v1" {
		t.Errorf("outbox: %+v", f.repo.outbox)
	}
}

func TestCollectedCashDoesNotUseTheDevicePath(t *testing.T) {
	f := collectFixture(t)
	s := f.createSale(t)
	if _, err := f.svc.Checkout(context.Background(), s.ID(), cash20000); err != nil {
		t.Fatal(err)
	}
	if f.payments.Calls != 1 || f.fiscal.Last.Payments()[0].Collect {
		t.Errorf("cash should go the old way: payments=%d %+v", f.payments.Calls, f.fiscal.Last.Payments())
	}
}

func TestCollectedMixedPaymentMarksOnlyCard(t *testing.T) {
	f := collectFixture(t)
	s := f.createSale(t)
	mixed := []domain.Payment{{Method: domain.PaymentCash, Amount: 2537}, {Method: domain.PaymentCard, Amount: 10000}}
	if _, err := f.svc.Checkout(context.Background(), s.ID(), mixed); err != nil {
		t.Fatal(err)
	}
	got := f.fiscal.Last.Payments()
	if len(got) != 2 || got[0].Collect || !got[1].Collect {
		t.Errorf("only the card should be Collect: %+v", got)
	}
}

func TestCollectedDeclineKeepsSaleOpen(t *testing.T) {
	f := collectFixture(t)
	ctx := context.Background()
	s := f.createSale(t)
	f.fiscal.Err = app.ErrNotProcessed

	if _, err := f.svc.Checkout(ctx, s.ID(), card12537); !errors.Is(err, app.ErrNotProcessed) {
		t.Fatalf("err = %v, want %v", err, app.ErrNotProcessed)
	}
	stored, _ := f.svc.GetSale(ctx, s.ID())
	if stored.Status() != domain.StatusOpen || len(stored.Payments()) != 0 {
		t.Errorf("the declined card changed the sale: %+v", stored.Snapshot())
	}
	if len(f.repo.outbox) != 0 {
		t.Errorf("outbox'ta %d mesaj, istenen 0", len(f.repo.outbox))
	}

	// The cashier continues with cash; the receipt number was not spent.
	f.fiscal.Err = nil
	got, err := f.svc.Checkout(ctx, s.ID(), cash20000)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status() != domain.StatusPaid || got.ReceiptNo() != 1 {
		t.Errorf("sale is wrong: %+v", got.Snapshot())
	}
}

func TestCollectedUnknownOutcomeKeepsPaymentAndRetries(t *testing.T) {
	f := collectFixture(t)
	ctx := context.Background()
	s := f.createSale(t)
	f.fiscal.Err = errOffline

	if _, err := f.svc.Checkout(ctx, s.ID(), card12537); !errors.Is(err, errOffline) || errors.Is(err, app.ErrNotProcessed) {
		t.Fatalf("err = %v", err)
	}
	stored, _ := f.svc.GetSale(ctx, s.ID())
	want := []domain.Payment{{Method: domain.PaymentCard, Amount: 12537, Collect: true}}
	if stored.Status() != domain.StatusCharged || !reflect.DeepEqual(stored.Payments(), want) {
		t.Fatalf("the payment was not recorded on an uncertain result: %+v", stored.Snapshot())
	}
	if len(f.repo.outbox) != 1 || f.repo.outbox[0].Subject != "sale.charged.v1" {
		t.Fatalf("outbox: %+v", f.repo.outbox)
	}

	// Retry: the payment in the request is ignored, the same basket goes to the device with the
	// recorded (Collect) payment.
	f.fiscal.Err = nil
	got, err := f.svc.Checkout(ctx, s.ID(), cash20000)
	if err != nil {
		t.Fatalf("tekrar deneme: %v", err)
	}
	if f.payments.Calls != 0 || !f.fiscal.Last.Payments()[0].Collect {
		t.Errorf("wrong payment on retry: payments=%d %+v", f.payments.Calls, f.fiscal.Last.Payments())
	}
	if got.Status() != domain.StatusPaid || got.ReceiptNo() != 1 {
		t.Errorf("sale is wrong: %+v", got.Snapshot())
	}
}

func TestCollectedRejectsBadPaymentsBeforeTheDevice(t *testing.T) {
	f := collectFixture(t)
	s := f.createSale(t)
	over := []domain.Payment{{Method: domain.PaymentCard, Amount: 20000}}
	if _, err := f.svc.Checkout(context.Background(), s.ID(), over); !errors.Is(err, domain.ErrExcessNonCash) {
		t.Errorf("excess card: err = %v, want %v", err, domain.ErrExcessNonCash)
	}
	if f.fiscal.Calls != 0 {
		t.Errorf("the device was called %d times, want 0", f.fiscal.Calls)
	}
}

func TestCollectedChargedSaleCannotBeRefundedThroughPayments(t *testing.T) {
	f := collectFixture(t)
	ctx := context.Background()
	s := f.createSale(t)
	f.fiscal.Err = errOffline
	if _, err := f.svc.Checkout(ctx, s.ID(), card12537); err == nil {
		t.Fatal("an error was expected")
	}
	if _, err := f.svc.RefundCharged(ctx, s.ID()); !errors.Is(err, app.ErrCollectedByDevice) {
		t.Errorf("err = %v, want %v", err, app.ErrCollectedByDevice)
	}
	if len(f.payments.Refunded) != 0 {
		t.Errorf("payment servisinden iade istendi: %v", f.payments.Refunded)
	}
}

// Even if the cashier closes the page or the gate times out, if the fiscal receipt was issued
// the result of the sale is written: otherwise the receipt would be issued but the sale would stay open.
func TestCheckoutSettlesAfterRequestContextIsCanceled(t *testing.T) {
	f := newFixture(t)
	s := f.createSale(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.fiscal.During = func(context.Context) { cancel() } // the request is cancelled while the ÖKC issues the receipt

	got, err := f.svc.Checkout(ctx, s.ID(), cash20000)
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if got.Status() != domain.StatusPaid || got.FiscalNo() == "" || got.ReceiptNo() != 1 {
		t.Fatalf("the sale did not close: %+v", got.Snapshot())
	}
	stored, err := f.svc.GetSale(context.Background(), s.ID())
	if err != nil || stored.Status() != domain.StatusPaid {
		t.Fatalf("record: %v %v", stored, err)
	}
	if len(f.repo.outbox) != 1 {
		t.Errorf("outbox'ta %d mesaj, istenen 1", len(f.repo.outbox))
	}
}

// If the request ends without the fiscal device answering, a sale whose payment was taken waits
// for "complete the receipt" and is reported to the store server; the payment record is not lost either.
func TestCheckoutRecordsFiscalFailureAfterRequestContextIsCanceled(t *testing.T) {
	f := newFixture(t)
	s := f.createSale(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.fiscal.During = func(context.Context) { cancel() }
	f.fiscal.Err = context.DeadlineExceeded

	if _, err := f.svc.Checkout(ctx, s.ID(), cash20000); err == nil || strings.Contains(err.Error(), "operasyona bildirilemedi") {
		t.Fatalf("error: %v (the event should have been writable)", err)
	}
	stored, err := f.svc.GetSale(context.Background(), s.ID())
	if err != nil || stored.Status() != domain.StatusCharged {
		t.Fatalf("the sale should be charged: %v %v", stored, err)
	}
	if len(f.repo.outbox) != 1 {
		t.Errorf("the failure event was not written: %d messages", len(f.repo.outbox))
	}
}

// Even if the request is cancelled after the payment is taken the payment is recorded on the sale.
func TestChargeIsRecordedAfterRequestContextIsCanceled(t *testing.T) {
	f := newFixture(t)
	s := f.createSale(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancelOnCharge := &cancelingPayments{Payments: f.payments, cancel: cancel}
	svc, err := app.NewService(f.repo, cancelOnCharge, f.fiscal, apptest.Config())
	if err != nil {
		t.Fatal(err)
	}
	f.fiscal.Err = errOffline // the receipt cannot be issued after the payment; the payment record must stay visible
	if _, err := svc.Checkout(ctx, s.ID(), cash20000); err == nil {
		t.Fatal("an error was expected")
	}
	stored, err := svc.GetSale(context.Background(), s.ID())
	if err != nil || stored.Status() != domain.StatusCharged {
		t.Fatalf("the payment should have been recorded (charged): %v %v", stored, err)
	}
}

type cancelingPayments struct {
	*apptest.Payments
	cancel context.CancelFunc
}

func (c *cancelingPayments) Charge(ctx context.Context, id domain.SaleID, p []domain.Payment) ([]domain.Payment, error) {
	out, err := c.Payments.Charge(ctx, id, p)
	c.cancel()
	return out, err
}
