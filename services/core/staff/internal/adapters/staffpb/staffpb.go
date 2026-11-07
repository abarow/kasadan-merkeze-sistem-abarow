// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package staffpb holds the contract (staff.v1) counterparts of staff's domain types;
// the gRPC server and the event encoder (eventproto) use it.
package staffpb

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	staffv1 "abarow.com/gen/com/abarow/staff/v1"

	"abarow.com/pkg/paymethod"
	"abarow.com/services/core/staff/internal/domain"
)

// Shift is the shift's form in the contract.
func Shift(sh domain.Shift) *staffv1.Shift {
	return &staffv1.Shift{
		Id:               sh.ID,
		CashierId:        sh.CashierID,
		CashierName:      sh.CashierName,
		OpenedAt:         Timestamp(sh.OpenedAt),
		ClosedAt:         Timestamp(sh.ClosedAt),
		OpeningCashKurus: int64(sh.OpeningCash),
		CountedCashKurus: int64(sh.CountedCash),
		ClosedBy:         sh.ClosedBy,
	}
}

// ShiftReport is the shift report's form in the contract.
func ShiftReport(r domain.ShiftReport) *staffv1.ShiftReport {
	return &staffv1.ShiftReport{
		Shift:             Shift(r.Shift),
		ReceiptCount:      r.ReceiptCount,
		TotalKurus:        int64(r.Total),
		Payments:          Payments(r.Payments),
		VoidedCount:       r.VoidedCount,
		VoidedTotalKurus:  int64(r.VoidedTotal),
		ExpectedCashKurus: int64(r.ExpectedCash),
		DifferenceKurus:   int64(r.Difference),
		CashInKurus:       int64(r.CashIn),
		CashOutKurus:      int64(r.CashOut),
		DrawerOpens:       r.DrawerOpens,
	}
}

// Payments is the payment type totals' form in the contract.
func Payments(payments []domain.PaymentTotal) []*staffv1.PaymentTotal {
	out := make([]*staffv1.PaymentTotal, 0, len(payments))
	for _, p := range payments {
		method := staffv1.PaymentMethod(paymethod.Number(string(p.Method)))
		out = append(out, &staffv1.PaymentTotal{Method: method, AmountKurus: int64(p.Amount)})
	}
	return out
}

// Timestamp returns the time; the zero time (e.g. the closing of an open shift) is empty.
func Timestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}
