// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"abarow.com/pkg/money"
	"abarow.com/services/fiscal/beko/internal/domain"
)

// fakeDevice stands in for the bridge: it accepts the basket and gives the result
// as an event.
type fakeDevice struct {
	events chan Event
	mu     sync.Mutex
	calls  []string
	// accept is the value returned for a basket ("1" accepts).
	accept string
	// status is the status reported for the basket; if silent it is never reported.
	status int
	silent bool
	infoOK bool
}

func newDevice() *fakeDevice {
	return &fakeDevice{events: make(chan Event, 16), accept: "1", infoOK: true}
}

func (d *fakeDevice) Events() <-chan Event { return d.events }

func (d *fakeDevice) Call(_ context.Context, op string, payload []byte) ([]byte, error) {
	d.mu.Lock()
	d.calls = append(d.calls, op)
	d.mu.Unlock()
	switch op {
	case "fiscalinfo":
		if !d.infoOK {
			return nil, errors.New("cihaz yanıt vermiyor")
		}
		return []byte(`{"sectionCount":2}`), nil
	case "basket":
		if d.accept == "1" && !d.silent {
			id := between(string(payload), `"basketID":"`, `"`)
			d.events <- Event{Kind: "serial", Ref: "3", Payload: []byte(fmt.Sprintf(
				`{"basketID":%q,"message":"OK","receiptNo":2,"status":%d,"zNo":12}`, id, d.status))}
		}
		return []byte(d.accept), nil
	}
	return nil, errors.New("bilinmeyen işlem")
}

func (d *fakeDevice) count(op string) (n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.calls {
		if c == op {
			n++
		}
	}
	return n
}

func between(s, a, b string) string {
	i := strings.Index(s, a) + len(a)
	return s[i : i+strings.Index(s[i:], b)]
}

func setup(t *testing.T, d *fakeDevice) *Service {
	t.Helper()
	cfg := domain.Config{Sections: map[money.Rate]int{1000: 1}}
	svc := NewService(d, cfg, 300*time.Millisecond, 10*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go svc.Run(ctx)
	return svc
}

func waitReady(t *testing.T, svc *Service) {
	t.Helper()
	for range 200 {
		if svc.Ready() == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("ÖKC did not become ready")
}

func receipt(id string) domain.Receipt {
	return domain.Receipt{SaleID: id, Total: 1200, Payments: []domain.Payment{{Method: "cash", Amount: 1200}},
		Lines: []domain.Line{{Name: "Ekmek", Qty: 1000, UnitPrice: 1200, Total: 1200, TaxRate: 1000}}}
}

func TestReadyGate(t *testing.T) {
	d := newDevice()
	d.infoOK = false
	svc := setup(t, d)
	// A sale is not sent before the bridge opens and the fiscal information is received.
	if _, err := svc.Register(context.Background(), receipt("s0")); !errors.Is(err, ErrNotReady) {
		t.Errorf("when not ready: %v", err)
	}
	d.events <- Event{Kind: "ready"}
	time.Sleep(60 * time.Millisecond)
	if svc.Ready() == nil {
		t.Error("counted as ready while the fiscal information could not be obtained")
	}
	d.mu.Lock()
	d.infoOK = true
	d.mu.Unlock()
	waitReady(t, svc)
	// When the connection drops it closes again; when it comes back it is ready again.
	d.events <- Event{Kind: "state", Ref: "0", Payload: []byte("X30TR")}
	for range 100 {
		if svc.Ready() != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if svc.Ready() == nil {
		t.Error("the connection broke but ready")
	}
	d.events <- Event{Kind: "state", Ref: "1", Payload: []byte("X30TR")}
	waitReady(t, svc)
	if d.count("basket") != 0 {
		t.Error("the basket was sent while not ready")
	}
}

func TestRegister(t *testing.T) {
	d := newDevice()
	svc := setup(t, d)
	d.events <- Event{Kind: "ready"}
	waitReady(t, svc)

	no, err := svc.Register(context.Background(), receipt("s1"))
	if err != nil || no != "Z0012-000002" {
		t.Fatalf("receipt: %q %v", no, err)
	}
	// If the same sale is sent again the receipt is not issued a second time.
	if no2, err := svc.Register(context.Background(), receipt("s1")); err != nil || no2 != no || d.count("basket") != 1 {
		t.Errorf("resend: %q %v, the basket went %d times", no2, err, d.count("basket"))
	}
}

func TestFailures(t *testing.T) {
	d := newDevice()
	svc := setup(t, d)
	d.events <- Event{Kind: "ready"}
	waitReady(t, svc)

	// A failed sale (card declined): an error; on a retry the basket goes again.
	d.status = domain.StatusFailed
	if _, err := svc.Register(context.Background(), receipt("s2")); !errors.Is(err, ErrDeclined) {
		t.Errorf("rejected sale: %v", err)
	}
	d.status = domain.StatusOK
	if _, err := svc.Register(context.Background(), receipt("s2")); err != nil || d.count("basket") != 2 {
		t.Errorf("retry: %v, the basket went %d times", err, d.count("basket"))
	}
	d.status = domain.StatusVoided
	if _, err := svc.Register(context.Background(), receipt("s3")); !errors.Is(err, ErrVoided) {
		t.Errorf("cancelled receipt: %v", err)
	}

	// Cihaz sepeti kabul etmezse.
	d.accept = "0"
	if _, err := svc.Register(context.Background(), receipt("s4")); !errors.Is(err, ErrRejected) {
		t.Errorf("basket not accepted: %v", err)
	}
	d.accept = "1"

	// If no result comes, uncertainty is reported; if the result comes later, sending the same
	// sale again uses it (the receipt is not issued a second time).
	d.silent = true
	before := d.count("basket")
	if _, err := svc.Register(context.Background(), receipt("s5")); !errors.Is(err, ErrNoResult) {
		t.Errorf("sale with no result: %v", err)
	}
	d.events <- Event{Kind: "serial", Ref: "3", Payload: []byte(`{"basketID":"s5","message":"OK","receiptNo":9,"status":0,"zNo":12}`)}
	time.Sleep(30 * time.Millisecond)
	if no, err := svc.Register(context.Background(), receipt("s5")); err != nil || no != "Z0012-000009" || d.count("basket") != before+1 {
		t.Errorf("late result: %q %v, the basket went %d times", no, err, d.count("basket"))
	}

	// An invalid receipt does not go to the device.
	bad := receipt("s6")
	bad.Lines[0].TaxRate = 800
	n := d.count("basket")
	if _, err := svc.Register(context.Background(), bad); !errors.Is(err, domain.ErrUnmappedTax) || d.count("basket") != n {
		t.Errorf("VAT with no section: %v", err)
	}
}
