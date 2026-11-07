// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"abarow.com/pkg/money"
	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

// fakeDrawer is a fake drawer that records the reasons for opening.
type fakeDrawer struct{ reasons []string }

func (d *fakeDrawer) Open(_ context.Context, reason string) error {
	d.reasons = append(d.reasons, reason)
	return nil
}

// fixedBacklog is pending events that never decrease (the message path is cut).
type fixedBacklog int64

func (b fixedBacklog) PendingEvents(context.Context) (int64, error) { return int64(b), nil }

// cashSale is the cashier's cash sale at the moment at (no change).
func cashSale(id, cashierID string, receiptNo int64, total money.Money, at time.Time) domain.CompletedSale {
	return domain.CompletedSale{
		SaleID: id, CashierID: cashierID, ReceiptNo: receiptNo, Total: total,
		Payments: []domain.PaymentTotal{{Method: domain.Cash, Amount: total}},
		At:       at,
	}
}

func TestShifts(t *testing.T) {
	ctx := context.Background()
	_, st, now := newAuth(t)
	shifts := app.NewShiftService(st, st, app.Events{})
	shifts.Now = func() time.Time { return *now }
	svc := app.NewService(st, st, st, app.Events{})
	start := *now

	if _, err := shifts.Current(ctx); !errors.Is(err, domain.ErrNoShift) {
		t.Fatalf("başlangıçta açık vardiya var: %v", err)
	}
	sh, err := shifts.Open(ctx, "ayse", 50000)
	if err != nil {
		t.Fatal(err)
	}
	// A single drawer at the register: a second shift is not opened, the day is not closed.
	var open domain.ShiftOpenError
	if _, err := shifts.Open(ctx, "ali", 0); !errors.As(err, &open) || open.CashierName != "Kasiyer Ayşe" {
		t.Errorf("ikinci vardiya açılabildi: %v", err)
	}
	if _, err := svc.CloseDay(ctx, "ali"); !errors.As(err, &open) {
		t.Errorf("açık vardiyayla Z alındı: %v", err)
	}

	// Sales in the shift: after 1 s and 1 ns (the fixed-width time format keeps the
	// trailing zeros, text comparison stays correct).
	for _, s := range []domain.CompletedSale{
		cashSale("s1", "ayse", 1, 1000, start.Add(time.Second)),
		cashSale("s2", "ayse", 2, 2500, start.Add(time.Nanosecond)),
		cashSale("s0", "ayse", 3, 9900, start.Add(-time.Second)), // before the shift
		cashSale("sx", "", 4, 700, start.Add(time.Second)),       // kasiyersiz
		cashSale("sm", "mert", 5, 300, start.Add(time.Second)),   // another cashier
	} {
		if err := svc.RecordCompleted(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.RecordVoided(ctx, domain.VoidedSale{SaleID: "v1", CashierID: "ayse", Total: 400, At: start.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}

	// Cash movements: Ayşe puts in 100.00 of small change; she cannot take money out, the manager
	// takes out 200.00 of interim cash.
	if _, err := shifts.RecordCashMovement(ctx, "ayse", domain.CashIn, 10000, "Bozuk para"); err != nil {
		t.Fatal(err)
	}
	if _, err := shifts.RecordCashMovement(ctx, "ayse", domain.CashOut, 20000, "Ara para"); !errors.Is(err, domain.ErrCashOutNotManager) {
		t.Errorf("kasiyer para alabildi: %v", err)
	}
	m, err := shifts.RecordCashMovement(ctx, "ali", domain.CashOut, 20000, " Ara para ")
	if err != nil || m.ID == 0 || m.Note != "Ara para" || m.By != "ali" {
		t.Fatalf("ara para: %+v, %v", m, err)
	}

	// Opening the drawer without a sale: it is not opened when there is no drawer; the manager opens it, it is recorded.
	if _, err := shifts.OpenDrawer(ctx, "ali", "ayse", "Bozuk para"); !errors.Is(err, app.ErrNoDrawer) {
		t.Errorf("çekmece yokken: %v", err)
	}
	drawer := &fakeDrawer{}
	shifts.Drawer = drawer
	if _, err := shifts.OpenDrawer(ctx, "ayse", "ayse", "Bozuk para"); !errors.Is(err, domain.ErrDrawerNotManager) || len(drawer.reasons) != 0 {
		t.Errorf("kasiyer çekmeceyi açtı: %v", err)
	}
	if d, err := shifts.OpenDrawer(ctx, "ali", "ayse", "Bozuk para"); err != nil || d.ID == 0 || len(drawer.reasons) != 1 {
		t.Fatalf("müdür açılışı: %+v, %v", d, err)
	}

	*now = start.Add(8 * time.Hour)
	// The shift does not close before the events are processed; the report would be incomplete.
	stuck := app.NewShiftService(st, st, app.Events{Sources: []app.EventBacklog{fixedBacklog(1)}})
	var pending domain.EventsPendingError
	if _, err := stuck.Close(ctx, "ayse", 0); !errors.As(err, &pending) || pending.Pending != 1 {
		t.Errorf("bekleyen olaylarla kapanış: %v", err)
	}
	if _, err := shifts.Current(ctx); err != nil {
		t.Fatalf("bekleyen olaylarla vardiya kapandı: %v", err)
	}
	// An unknown cashier cannot close (that another cashier cannot close is in the domain
	// test); a manager can close someone else's shift.
	if _, err := shifts.Close(ctx, "mert", 0); !errors.Is(err, domain.ErrUnknownCashier) {
		t.Errorf("tanımsız kasiyer kapatabildi: %v", err)
	}
	r, err := shifts.Close(ctx, "ali", 43400)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != sh.ID || r.ClosedBy != "ali" || r.ReceiptCount != 2 || r.Total != 3500 || r.VoidedCount != 1 {
		t.Errorf("vardiya raporu yanlış: %+v", r)
	}
	// Expected 500.00 + 35.00 + 100.00 − 200.00 = 435.00; counted 434.00.
	if r.CashIn != 10000 || r.CashOut != 20000 || r.ExpectedCash != 43500 || r.Difference != -100 || r.DrawerOpens != 1 {
		t.Errorf("konan %d, alınan %d, beklenen %d, fark %d; istenen 10000, 20000, 43500, -100",
			r.CashIn, r.CashOut, r.ExpectedCash, r.Difference)
	}
	if _, err := shifts.Close(ctx, "ali", 0); !errors.Is(err, domain.ErrNoShift) {
		t.Errorf("kapalı vardiya ikinci kez kapandı: %v", err)
	}
	// History: the report is recomputed from the records, it is the same as at closing.
	again, err := shifts.Report(ctx, "ali", sh.ID)
	if err != nil || again.ExpectedCash != r.ExpectedCash || again.CountedCash != 43400 || again.ClosedAt.IsZero() || again.CashierName != "Kasiyer Ayşe" {
		t.Errorf("geçmiş rapor: %+v, %v", again, err)
	}
	if _, err := shifts.Report(ctx, "ali", 999); !errors.Is(err, domain.ErrUnknownShift) {
		t.Errorf("olmayan vardiya: %v", err)
	}
	history, err := shifts.History(ctx, "ayse", 10)
	if err != nil || len(history) != 1 || history[0].ID != sh.ID {
		t.Errorf("Ayşe'nin geçmişi: %+v, %v", history, err)
	}

	// No movement is written to a closed shift (the adapter checks too).
	if _, err := st.AddCashMovement(ctx, domain.CashMovement{ShiftID: sh.ID, Kind: domain.CashIn, Amount: 100, Note: "geç", By: "ali", At: *now}); !errors.Is(err, domain.ErrNoShift) {
		t.Errorf("kapalı vardiyaya hareket yazıldı: %v", err)
	}

	// Even if the event comes late the sale goes into the shift it was made in; a sale
	// after the closing does not.
	late := cashSale("s3", "ayse", 6, 100, start.Add(time.Hour))
	after := cashSale("s4", "ayse", 7, 200, start.Add(9*time.Hour))
	for _, s := range []domain.CompletedSale{late, after} {
		if err := svc.RecordCompleted(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	completed, _, err := st.ShiftFacts(ctx, sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(completed) != 3 || completed[2].SaleID != "s3" {
		t.Errorf("vardiyaya giren satışlar yanlış: %+v", completed)
	}

	// When the shift closes the day can be closed (if the events were processed); a new
	// shift can be opened.
	stuckDay := app.NewService(st, st, st, app.Events{Sources: []app.EventBacklog{fixedBacklog(2)}})
	if _, err := stuckDay.CloseDay(ctx, "ali"); !errors.As(err, &pending) || pending.Pending != 2 {
		t.Errorf("bekleyen olaylarla Z: %v", err)
	}
	if _, err := svc.CloseDay(ctx, "ali"); err != nil {
		t.Fatal(err)
	}
	ali, err := shifts.Open(ctx, "ali", 0)
	if err != nil {
		t.Fatal(err)
	}
	// The report of an open shift is not given (blind count); a cashier cannot see
	// someone else's report, and does not see anyone else's in their own history.
	if _, err := shifts.Report(ctx, "ali", ali.ID); !errors.Is(err, domain.ErrShiftNotClosed) {
		t.Errorf("açık vardiyanın raporu: %v", err)
	}
	*now = now.Add(time.Hour)
	if _, err := shifts.Close(ctx, "ali", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := shifts.Report(ctx, "ayse", ali.ID); !errors.Is(err, domain.ErrNotShiftOwner) {
		t.Errorf("kasiyer başkasının raporunu gördü: %v", err)
	}
	if history, _ := shifts.History(ctx, "ayse", 10); len(history) != 1 {
		t.Errorf("Ayşe'nin geçmişinde %d vardiya, istenen 1", len(history))
	}
	if history, _ := shifts.History(ctx, "ali", 10); len(history) != 2 || history[0].ID != ali.ID {
		t.Errorf("müdürün geçmişi yeniden eskiye değil: %+v", history)
	}
}

// paper is a fake printer that collects what is printed.
type paper struct {
	columns int
	titles  []string
	lines   [][]string
}

func (p *paper) Columns(context.Context) (int, error) { return p.columns, nil }
func (p *paper) Print(_ context.Context, title string, lines []string) error {
	p.titles = append(p.titles, title)
	p.lines = append(p.lines, lines)
	return nil
}

func TestPrintReports(t *testing.T) {
	ctx := context.Background()
	_, st, now := newAuth(t)
	shifts := app.NewShiftService(st, st, app.Events{})
	shifts.Now = func() time.Time { return *now }
	svc := app.NewService(st, st, st, app.Events{})
	out := &paper{columns: 32}
	print := app.NewPrintService(svc, shifts, out, time.UTC)
	print.Now = func() time.Time { return *now }

	sh, err := shifts.Open(ctx, "ayse", 50000)
	if err != nil {
		t.Fatal(err)
	}
	// The report of an open shift is not printed (blind count).
	if err := print.PrintShift(ctx, "ali", sh.ID); !errors.Is(err, domain.ErrShiftNotClosed) {
		t.Errorf("açık vardiya basıldı: %v", err)
	}
	*now = now.Add(time.Hour)
	if _, err := shifts.Close(ctx, "ayse", 50000); err != nil {
		t.Fatal(err)
	}
	if err := print.PrintShift(ctx, "ayse", sh.ID); err != nil {
		t.Fatal(err)
	}
	if err := print.PrintDay(ctx, 0); err != nil {
		t.Fatal(err)
	}
	z, err := svc.CloseDay(ctx, "ali")
	if err != nil {
		t.Fatal(err)
	}
	if err := print.PrintDay(ctx, z.ZNo); err != nil {
		t.Fatal(err)
	}
	if err := print.PrintDay(ctx, 99); !errors.Is(err, domain.ErrUnknownPeriod) {
		t.Errorf("olmayan Z: %v", err)
	}
	want := []string{fmt.Sprintf("Vardiya raporu %d", sh.ID), "X raporu (Z no 1)", "Z raporu 1"}
	if !slices.Equal(out.titles, want) {
		t.Errorf("basılanlar %q, istenen %q", out.titles, want)
	}
	if strings.TrimSpace(out.lines[2][0]) != "Z RAPORU" {
		t.Errorf("Z raporunun ilk satırı: %q", out.lines[2][0])
	}
}
