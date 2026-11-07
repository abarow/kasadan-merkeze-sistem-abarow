// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package natsconsumer is the driving adapter that reads the sales events from JetStream
// and processes them into the end-of-day report.
package natsconsumer

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/money"
	"abarow.com/pkg/natsio"
	"abarow.com/pkg/paymethod"
	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

const (
	// Stream, sales'in olay stream'idir.
	Stream = "ABAROW_SALES"
	// Durable is the name of staff's durable consumer; when staff restarts it
	// continues where it left off.
	Durable = "staff"
)

// New returns the consumer that processes the sale events into the X/Z report and the shifts
// (pkg/natsio). If the stream does not exist yet (sales never published) it waits
// and retries. A message that cannot be decoded does not get better by retrying; it is
// permanently rejected. A message that cannot be processed comes again later.
func New(js jetstream.JetStream, svc *app.Service, log *slog.Logger) *natsio.Consumer {
	return &natsio.Consumer{JS: js, Stream: Stream,
		Config: jetstream.ConsumerConfig{Durable: Durable, FilterSubjects: []string{"sale.completed.v1", "sale.voided.v1"}},
		What:   "satış olayları", Log: log,
		Handle: func(ctx context.Context, msg jetstream.Msg) error {
			switch msg.Subject() {
			case "sale.completed.v1":
				s, err := decodeCompleted(msg)
				if err != nil {
					return natsio.Reject(err)
				}
				return svc.RecordCompleted(ctx, s)
			case "sale.voided.v1":
				v, err := decodeVoided(msg)
				if err != nil {
					return natsio.Reject(err)
				}
				return svc.RecordVoided(ctx, v)
			}
			return nil
		}}
}

// completedEvent is the JSON form of sales' sale.completed.v1.
type completedEvent struct {
	SaleID    string      `json:"sale_id"`
	CashierID string      `json:"cashier_id"`
	ReceiptNo int64       `json:"receipt_no"`
	Total     money.Money `json:"total_kurus"`
	TaxTotal  money.Money `json:"tax_total_kurus"`
	Change    money.Money `json:"change_kurus"`
	Taxes     []struct {
		Rate  money.Rate  `json:"rate_bp"`
		Gross money.Money `json:"gross_kurus"`
		Tax   money.Money `json:"tax_kurus"`
	} `json:"taxes"`
	Payments []struct {
		Method string      `json:"method"`
		Amount money.Money `json:"amount_kurus"`
	} `json:"payments"`
	At time.Time `json:"occurred_at"`
}

func (e completedEvent) toDomain() domain.CompletedSale {
	s := domain.CompletedSale{SaleID: e.SaleID, CashierID: e.CashierID, ReceiptNo: e.ReceiptNo, Total: e.Total, TaxTotal: e.TaxTotal, Change: e.Change, At: e.At}
	for _, t := range e.Taxes {
		s.Taxes = append(s.Taxes, domain.TaxTotal{Rate: t.Rate, Gross: t.Gross, Tax: t.Tax})
	}
	for _, p := range e.Payments {
		s.Payments = append(s.Payments, domain.PaymentTotal{Method: domain.PaymentMethod(p.Method), Amount: p.Amount})
	}
	return s
}

// voidedEvent is the JSON form of sales' sale.voided.v1.
type voidedEvent struct {
	SaleID    string      `json:"sale_id"`
	CashierID string      `json:"cashier_id"`
	Total     money.Money `json:"total_kurus"`
	At        time.Time   `json:"occurred_at"`
}

// isProtobuf says the message is protobuf-encoded. sales publishes protobuf events;
// old events left in the stream are JSON.
func isProtobuf(msg jetstream.Msg) bool {
	return strings.HasPrefix(msg.Headers().Get("content-type"), "application/protobuf")
}

func decodeCompleted(msg jetstream.Msg) (domain.CompletedSale, error) {
	if !isProtobuf(msg) {
		var e completedEvent
		if err := json.Unmarshal(msg.Data(), &e); err != nil {
			return domain.CompletedSale{}, err
		}
		return e.toDomain(), nil
	}
	body, err := ledger.Decode("sale.completed.v1", msg.Headers().Get("content-type"), msg.Data())
	if err != nil {
		return domain.CompletedSale{}, err
	}
	sale := body.(ledger.Sale)
	s := domain.CompletedSale{SaleID: sale.ID, CashierID: sale.CashierID, ReceiptNo: sale.ReceiptNo, Total: sale.Total,
		TaxTotal: sale.TaxTotal, Change: sale.Change, At: sale.PaidAt}
	for _, t := range sale.Taxes {
		s.Taxes = append(s.Taxes, domain.TaxTotal{Rate: t.Rate, Gross: t.Gross, Tax: t.Tax})
	}
	for _, p := range sale.Payments {
		// An unknown kind (from a newer sales service) is stored as "Other"; the amount
		// is not lost.
		method := domain.PaymentMethod(p.Method)
		if paymethod.Number(p.Method) == 0 {
			method = domain.Other
		}
		s.Payments = append(s.Payments, domain.PaymentTotal{Method: method, Amount: p.Amount})
	}
	return s, nil
}

func decodeVoided(msg jetstream.Msg) (domain.VoidedSale, error) {
	if !isProtobuf(msg) {
		var e voidedEvent
		if err := json.Unmarshal(msg.Data(), &e); err != nil {
			return domain.VoidedSale{}, err
		}
		return domain.VoidedSale{SaleID: e.SaleID, CashierID: e.CashierID, Total: e.Total, At: e.At}, nil
	}
	body, err := ledger.Decode("sale.voided.v1", msg.Headers().Get("content-type"), msg.Data())
	if err != nil {
		return domain.VoidedSale{}, err
	}
	v := body.(ledger.Void)
	return domain.VoidedSale{SaleID: v.SaleID, CashierID: v.CashierID, Total: v.Total, At: v.At}, nil
}
