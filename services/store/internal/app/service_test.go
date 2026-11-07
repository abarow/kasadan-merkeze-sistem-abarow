// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/nodes"
	"abarow.com/services/store/internal/domain"
)

type fakeStore struct {
	failures int
	ensured  [][]string
	sources  map[string]domain.Source
	err      error
}

func (f *fakeStore) Ensure(_ context.Context, ids []string) error {
	if f.failures > 0 {
		f.failures--
		return errors.New("nats: no responders available for request")
	}
	f.ensured = append(f.ensured, ids)
	return nil
}

func (f *fakeStore) Sources(context.Context) (map[string]domain.Source, error) {
	return f.sources, f.err
}

type fakeReports struct {
	sales      []domain.Sale
	voids      []domain.Void
	unfinished []ledger.Unfinished
	shifts     []domain.Shift
	from, to   time.Time
	activity   map[string]domain.Activity
}

func (f *fakeReports) SaveSale(_ context.Context, s domain.Sale) error {
	f.sales = append(f.sales, s)
	return nil
}

func (f *fakeReports) SaveVoid(_ context.Context, v domain.Void) error {
	f.voids = append(f.voids, v)
	return nil
}

func (f *fakeReports) SaveUnfinished(_ context.Context, u ledger.Unfinished) error {
	f.unfinished = append(f.unfinished, u)
	return nil
}

func (f *fakeReports) SaveShift(_ context.Context, sh domain.Shift) error {
	f.shifts = append(f.shifts, sh)
	return nil
}

func (f *fakeReports) Report(_ context.Context, q ledger.Filter) (ledger.Report, error) {
	f.from, f.to = q.Start, q.End
	return ledger.Build(q, f.sales, f.voids), nil
}

func (f *fakeReports) Activity(context.Context) (map[string]domain.Activity, error) {
	return f.activity, nil
}

var (
	discard  = slog.New(slog.NewTextHandler(io.Discard, nil))
	regs     = []nodes.Node{{ID: "register-2", Name: "Giriş"}, {ID: "register-1"}}
	istanbul = mustLoad("Europe/Istanbul")
)

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func TestSetupStoreRetries(t *testing.T) {
	store := &fakeStore{failures: 2}
	svc := NewService(store, &fakeReports{}, regs, istanbul)
	svc.RetryInterval = time.Millisecond
	if err := svc.SetupStore(context.Background(), discard); err != nil {
		t.Fatal(err)
	}
	if len(store.ensured) != 1 || !slices.Equal(store.ensured[0], []string{"register-2", "register-1"}) {
		t.Errorf("kurulum: %v", store.ensured)
	}
}

func TestSetupStoreStopsWithContext(t *testing.T) {
	store := &fakeStore{failures: 1 << 30}
	svc := NewService(store, &fakeReports{}, regs, istanbul)
	svc.RetryInterval = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := svc.SetupStore(ctx, discard); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error %v", err)
	}
}

// The stream is set up even with no register: the store server's own events go
// to headquarters from this stream.
func TestSetupStoreWithoutRegisters(t *testing.T) {
	store := &fakeStore{}
	if err := NewService(store, &fakeReports{}, nil, istanbul).SetupStore(context.Background(), discard); err != nil ||
		len(store.ensured) != 1 || len(store.ensured[0]) != 0 {
		t.Errorf("kasa yokken stream: %v %v", err, store.ensured)
	}
}

func TestRegisters(t *testing.T) {
	now := time.Date(2026, 10, 26, 14, 0, 0, 0, time.UTC)
	store := &fakeStore{sources: map[string]domain.Source{
		"register-1": {Seen: true, Since: time.Second, Lag: 2},
		"register-9": {Seen: true}, // a source not in the list is not shown
	}}
	reports := &fakeReports{activity: map[string]domain.Activity{
		"register-1": {Receipts: 5, LastReceiptNo: 9, Missing: []domain.ReceiptRange{{From: 6, To: 7}}},
	}}
	svc := NewService(store, reports, regs, istanbul)
	svc.now = func() time.Time { return now }
	got, err := svc.Registers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "register-2" || got[0].Name != "Giriş" || got[0].Link != domain.LinkNever || got[0].Activity.Receipts != 0 ||
		got[1].ID != "register-1" || got[1].Name != "register-1" || got[1].Link != domain.LinkConnected || got[1].Pending != 2 ||
		got[1].Activity.LastReceiptNo != 9 || len(got[1].Activity.Missing) != 1 {
		t.Errorf("durumlar %+v", got)
	}

	store.err = errors.New("bağlantı yok")
	if _, err := svc.Registers(context.Background()); err == nil {
		t.Error("the NATS error was kept")
	}
}

func TestRecordRejectsIncompleteEvents(t *testing.T) {
	reports := &fakeReports{}
	svc := NewService(&fakeStore{}, reports, regs, istanbul)
	if err := svc.RecordSale(context.Background(), domain.Sale{ID: "s1", RegisterID: "register-1"}); !errors.Is(err, domain.ErrInvalidEvent) {
		t.Errorf("sale with no receipt number: %v", err)
	}
	if err := svc.RecordVoid(context.Background(), domain.Void{SaleID: "v1"}); !errors.Is(err, domain.ErrInvalidEvent) {
		t.Errorf("cancellation with no register: %v", err)
	}
	if len(reports.sales)+len(reports.voids) != 0 {
		t.Error("an incomplete event was recorded")
	}
	if err := svc.RecordSale(context.Background(), domain.Sale{ID: "s1", RegisterID: "register-1", ReceiptNo: 1, PaidAt: time.Now()}); err != nil || len(reports.sales) != 1 {
		t.Errorf("the sale was not recorded: %v", err)
	}
	if err := svc.RecordUnfinished(context.Background(), ledger.Unfinished{SaleID: "s2", RegisterID: "register-1"}); !errors.Is(err, domain.ErrInvalidEvent) {
		t.Errorf("sale whose receipt could not be issued, with no time: %v", err)
	}
	if err := svc.RecordUnfinished(context.Background(), ledger.Unfinished{SaleID: "s2", RegisterID: "register-1", At: time.Now()}); err != nil || len(reports.unfinished) != 1 {
		t.Errorf("the sale whose receipt could not be issued was not recorded: %v", err)
	}
	if err := svc.RecordShift(context.Background(), domain.Shift{RegisterID: "register-1", OpenedAt: time.Now()}); !errors.Is(err, domain.ErrInvalidEvent) {
		t.Errorf("shift with no number: %v", err)
	}
	if err := svc.RecordShift(context.Background(), domain.Shift{RegisterID: "register-1", ID: 3, OpenedAt: time.Now()}); err != nil || len(reports.shifts) != 1 {
		t.Errorf("vardiya kaydedilmedi: %v", err)
	}
}

func TestRegisterName(t *testing.T) {
	svc := NewService(&fakeStore{}, &fakeReports{}, regs, istanbul)
	if svc.RegisterName("register-2") != "Giriş" || svc.RegisterName("register-9") != "register-9" {
		t.Error("register names")
	}
}
