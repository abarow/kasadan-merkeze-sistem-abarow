// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package eventproto is the app.EventEncoder adapter that encodes the sale events
// with the sales.v1 event messages (proto/com/abarow/sales/v1/events.proto).
package eventproto

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	salesv1 "abarow.com/gen/com/abarow/sales/v1"

	"abarow.com/pkg/paymethod"
	"abarow.com/services/core/sales/internal/app"
	"abarow.com/services/core/sales/internal/domain"
)

// ContentType is the content type of a protobuf-encoded event.
const ContentType = "application/protobuf"

// Encoder implements app.EventEncoder with protobuf.
type Encoder struct{}

var _ app.EventEncoder = Encoder{}

func (Encoder) Encode(e domain.Event, id, storeID string) ([]byte, string, error) {
	var m proto.Message
	switch e := e.(type) {
	case domain.SaleCompleted:
		m = &salesv1.SaleCompleted{
			EventId:    id,
			StoreId:    storeID,
			RegisterId: e.RegisterID,
			OccurredAt: timestamppb.New(e.At),
			Sale:       completedSale(e),
		}
	case domain.SaleCharged:
		m = &salesv1.SaleCharged{
			EventId:    id,
			StoreId:    storeID,
			RegisterId: e.RegisterID,
			OccurredAt: timestamppb.New(e.At),
			Sale: &salesv1.Sale{
				Id:         string(e.SaleID),
				RegisterId: e.RegisterID,
				CashierId:  e.CashierID,
				Status:     salesv1.SaleStatus_SALE_STATUS_CHARGED,
				TotalKurus: int64(e.Total),
				Lines:      lines(e.Lines),
				Payments:   payments(e.Payments),
			},
			Reason: e.Reason,
		}
	case domain.SaleVoided:
		m = &salesv1.SaleVoided{
			EventId:    id,
			StoreId:    storeID,
			RegisterId: e.RegisterID,
			OccurredAt: timestamppb.New(e.At),
			SaleId:     string(e.SaleID),
			TotalKurus: int64(e.Total),
			CashierId:  e.CashierID,
			Refunded:   e.Refunded,
		}
	default:
		return nil, "", fmt.Errorf("protobuf karşılığı olmayan olay: %s", e.Name())
	}
	b, err := proto.Marshal(m)
	return b, ContentType, err
}

// completedSale is the form in the contract of the sale in the event (its status is paid).
func completedSale(e domain.SaleCompleted) *salesv1.Sale {
	s := &salesv1.Sale{
		Id:            string(e.SaleID),
		RegisterId:    e.RegisterID,
		CashierId:     e.CashierID,
		Status:        salesv1.SaleStatus_SALE_STATUS_PAID,
		TotalKurus:    int64(e.Total),
		TaxTotalKurus: int64(e.TaxTotal),
		ChangeKurus:   int64(e.Change),
		ReceiptNo:     e.ReceiptNo,
		FiscalNo:      e.FiscalNo,
		PaidAt:        timestamppb.New(e.At),
	}
	for _, t := range e.Taxes {
		s.Taxes = append(s.Taxes, &salesv1.TaxLine{RateBp: int64(t.Rate), GrossKurus: int64(t.Gross), TaxKurus: int64(t.Tax)})
	}
	s.Lines = lines(e.Lines)
	s.Payments = payments(e.Payments)
	return s
}

func lines(in []domain.Line) []*salesv1.SaleLine {
	var out []*salesv1.SaleLine
	for _, l := range in {
		out = append(out, &salesv1.SaleLine{
			ProductId: l.ProductID, Barcode: l.Barcode, Name: l.Name,
			QtyMilli: int64(l.Qty), UnitPriceKurus: int64(l.UnitPrice), TaxRateBp: int64(l.TaxRate),
		})
	}
	return out
}

func payments(in []domain.Payment) []*salesv1.Payment {
	var out []*salesv1.Payment
	for _, p := range in {
		method := salesv1.PaymentMethod(paymethod.Number(string(p.Method)))
		out = append(out, &salesv1.Payment{Method: method, AmountKurus: int64(p.Amount), Ref: p.Ref})
	}
	return out
}
