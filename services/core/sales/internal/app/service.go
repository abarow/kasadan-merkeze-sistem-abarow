// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package app holds the sales service's use cases and the ports that open to the outside
// world.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"abarow.com/pkg/paymethod"
	"abarow.com/services/core/sales/internal/domain"
)

const eventTypePrefix = "com.abarow."

// settleTimeout is the time allowed for writing the result to the database after an operation
// that cannot be undone in the outside world (taking a payment, issuing a receipt, refunding).
const settleTimeout = 30 * time.Second

// settle gives the context to use when writing the result of the outside operation. The request
// context may have expired or been cancelled by then (the cashier closed the page, the gate
// timed out): if the result of a sale whose card was charged or whose fiscal receipt was issued
// cannot be written because the "context was cancelled", the sale looks open although the receipt
// was issued. So the write is separated from the cancellation and bounded with its own time.
func settle(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
}

// Config is the service's install-specific settings.
type Config struct {
	StoreID    string
	RegisterID string
	// Now returns the current time; time.Now is used if empty.
	Now func() time.Time
	// NewID produces a unique id for sales and events.
	NewID func() string
	// Encoder encodes the events for the message path; JSON if empty (for tests).
	// At the register protobuf is used (eventproto).
	Encoder EventEncoder
	// FiscalCollects says the fiscal device takes the card and meal card payment
	// together with the receipt from its own POS (e.g. Beko X30TR). In this
	// install a card sale's payment does not go through the payment service: if the device
	// cannot take it no receipt is issued, the sale stays open and no money has been taken.
	// Cash sales do not change.
	FiscalCollects bool
}

// Service, sales use case'leridir.
type Service struct {
	repo     Repository
	payments PaymentGateway
	fiscal   FiscalDevice
	cfg      Config
}

// NewService builds the service by connecting the ports.
func NewService(repo Repository, payments PaymentGateway, fiscal FiscalDevice, cfg Config) (*Service, error) {
	if cfg.StoreID == "" || cfg.RegisterID == "" {
		return nil, errors.New("mağaza ve kasa kimliği boş olamaz")
	}
	if cfg.Encoder == nil {
		cfg.Encoder = jsonEncoder{}
	}
	if cfg.NewID == nil {
		return nil, errors.New("kimlik üreteci (NewID) gerekli")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{repo: repo, payments: payments, fiscal: fiscal, cfg: cfg}, nil
}

// CreateSale creates an open sale from the lines the vertical module collected.
// cashierID is the cashier making the sale; their session was verified at the gateway.
func (s *Service) CreateSale(ctx context.Context, cashierID string, lines []domain.Line) (*domain.Sale, error) {
	sale, err := domain.NewSale(domain.SaleID(s.cfg.NewID()), s.cfg.RegisterID, cashierID, lines, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.repo.InTx(ctx, func(tx Tx) error { return tx.Save(ctx, sale) }); err != nil {
		return nil, err
	}
	return sale, nil
}

// GetSale returns the sale.
func (s *Service) GetSale(ctx context.Context, id domain.SaleID) (*domain.Sale, error) {
	return s.repo.Get(ctx, id)
}

// ListSales returns the sales in the given status from newest to oldest (at most 100).
// For example charged: sales whose payment was taken but whose fiscal record (receipt)
// could not be made, waiting for "retry the receipt".
func (s *Service) ListSales(ctx context.Context, status domain.Status, limit int) ([]*domain.Sale, error) {
	switch status {
	case domain.StatusOpen, domain.StatusCharged, domain.StatusPaid, domain.StatusVoided:
	default:
		return nil, fmt.Errorf("%w: %q", domain.ErrInvalidStatus, status)
	}
	return s.repo.ByStatus(ctx, status, min(max(limit, 1), 100))
}

// Checkout takes the sale's payment, makes its fiscal record and closes the sale.
//
// The payment taken is recorded on the sale before the fiscal record (charged). If the
// fiscal record fails the sale stays in this state; on retry the payments in the request
// are ignored and it continues with the recorded payment, the customer is not charged a
// second time.
//
// The receipt number is allocated after the fiscal record succeeds, in the same
// transaction as the sale and the SaleCompleted event. So failed attempts leave no gaps in
// the receipt numbers; the store server counts gaps as missing receipts.
func (s *Service) Checkout(ctx context.Context, id domain.SaleID, payments []domain.Payment) (*domain.Sale, error) {
	sale, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	switch sale.Status() {
	case domain.StatusOpen:
		if s.collectedByDevice(payments) {
			return s.checkoutCollected(ctx, sale, payments)
		}
		if sale, err = s.charge(ctx, sale, payments); err != nil {
			return nil, err
		}
	case domain.StatusCharged:
		// The payment was taken in the previous attempt, the fiscal record could not be made.
	default:
		return nil, domain.ErrNotOpen
	}

	fiscalNo, err := s.fiscal.Register(ctx, sale)
	ctx, cancel := settle(ctx)
	defer cancel()
	if err != nil {
		cause := err
		err = fmt.Errorf("mali kayıt yapılamadı, ödeme alındı; tekrar denenebilir: %w", cause)
		// The sale stays charged and is announced over the message path: the store server shows the
		// sales waiting to be completed at the register with their actual reason.
		if recErr := s.recordFiscalFailure(ctx, id, cause); recErr != nil {
			err = errors.Join(err, fmt.Errorf("operasyona bildirilemedi: %w", recErr))
		}
		return nil, err
	}

	return s.complete(ctx, id, fiscalNo, nil)
}

// complete closes the sale whose fiscal record has been made: it allocates the receipt number,
// writes the sale and the SaleCompleted event in the same transaction. If charge is given
// (a sale whose payment the device took) the payments are recorded in the same transaction too.
func (s *Service) complete(ctx context.Context, id domain.SaleID, fiscalNo string, charge []domain.Payment) (*domain.Sale, error) {
	var done *domain.Sale
	err := s.repo.InTx(ctx, func(tx Tx) error {
		// The sale may have been closed by another request in the meantime; the current state is
		// read inside the transaction.
		cur, err := tx.Get(ctx, id)
		if err != nil {
			return err
		}
		if charge != nil {
			if err := cur.RecordCharge(charge); err != nil {
				return err
			}
		}
		no, err := tx.NextReceiptNo(ctx)
		if err != nil {
			return err
		}
		if err := cur.MarkPaid(no, fiscalNo, s.now()); err != nil {
			return err
		}
		if err := s.save(ctx, tx, cur); err != nil {
			return err
		}
		done = cur
		return nil
	})
	if err != nil {
		return nil, err
	}
	return done, nil
}

// collectedByDevice says the payment will be taken by the fiscal device: the device charges
// the card from its own POS and the payment includes a card or meal card.
func (s *Service) collectedByDevice(payments []domain.Payment) bool {
	if !s.cfg.FiscalCollects {
		return false
	}
	return slices.ContainsFunc(payments, func(p domain.Payment) bool { return p.Method != paymethod.Cash })
}

// checkoutCollected completes a sale whose payment the fiscal device will take. Card and
// meal card do not go through the payment service; they go to the device with the receipt
// as "you take this payment" (Payment.Collect). The sale does not change until the device
// reports the result:
//   - the device issued the receipt: the payments and the fiscal record are recorded in a single transaction;
//   - the device definitely did not do it (card declined...): the sale is open, no money was taken;
//     the cashier continues with another payment;
//   - the result is uncertain (the connection broke, no result came): the card may have been charged.
//     The payment counts as taken, the sale waits for "complete the receipt"; on retry the same
//     basket is sent.
func (s *Service) checkoutCollected(ctx context.Context, sale *domain.Sale, payments []domain.Payment) (*domain.Sale, error) {
	if err := sale.CheckPayments(payments); err != nil {
		return nil, err
	}
	marked := slices.Clone(payments)
	for i := range marked {
		marked[i].Collect = marked[i].Method != paymethod.Cash
	}
	preview, err := sale.PreviewCharge(marked)
	if err != nil {
		return nil, err
	}
	fiscalNo, err := s.fiscal.Register(ctx, preview)
	ctx, cancel := settle(ctx)
	defer cancel()
	if errors.Is(err, ErrNotProcessed) {
		return nil, fmt.Errorf("ödeme alınmadı, satış açık: %w", err)
	}
	if err != nil {
		cause := err
		err = fmt.Errorf("mali kaydın sonucu belli değil, ödeme alınmış olabilir; tekrar denenebilir: %w", cause)
		if chErr := s.repo.InTx(ctx, func(tx Tx) error {
			cur, err := tx.Get(ctx, sale.ID())
			if err != nil {
				return err
			}
			if err := cur.RecordCharge(marked); err != nil {
				return err
			}
			return tx.Save(ctx, cur)
		}); chErr != nil {
			return nil, errors.Join(err, fmt.Errorf("ödeme kaydedilemedi: %w", chErr))
		}
		if recErr := s.recordFiscalFailure(ctx, sale.ID(), cause); recErr != nil {
			err = errors.Join(err, fmt.Errorf("operasyona bildirilemedi: %w", recErr))
		}
		return nil, err
	}
	done, err := s.complete(ctx, sale.ID(), fiscalNo, marked)
	if err != nil {
		return nil, fmt.Errorf("ÖKC fişi kesti (%s) ama satış kaydedilemedi: %w", fiscalNo, err)
	}
	return done, nil
}

// charge takes the payment and records it on the sale. If recording fails after the payment
// is taken the sale stays open; on retry the payment service returns the earlier result for the
// same sale, so the payment is not taken a second time.
func (s *Service) charge(ctx context.Context, sale *domain.Sale, payments []domain.Payment) (*domain.Sale, error) {
	if err := sale.CheckPayments(payments); err != nil {
		return nil, err
	}
	charged, err := s.payments.Charge(ctx, sale.ID(), payments)
	if err != nil {
		return nil, fmt.Errorf("ödeme alınamadı: %w", err)
	}
	ctx, cancel := settle(ctx)
	defer cancel()
	var cur *domain.Sale
	err = s.repo.InTx(ctx, func(tx Tx) error {
		var err error
		if cur, err = tx.Get(ctx, sale.ID()); err != nil {
			return err
		}
		if err := cur.RecordCharge(charged); err != nil {
			return err
		}
		return tx.Save(ctx, cur)
	})
	if err != nil {
		return nil, fmt.Errorf("ödeme kaydedilemedi: %w", err)
	}
	return cur, nil
}

// VoidSale cancels a sale whose payment has not been taken.
func (s *Service) VoidSale(ctx context.Context, id domain.SaleID) (*domain.Sale, error) {
	var done *domain.Sale
	err := s.repo.InTx(ctx, func(tx Tx) error {
		sale, err := tx.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := sale.Void(s.now()); err != nil {
			return err
		}
		if err := s.save(ctx, tx, sale); err != nil {
			return err
		}
		done = sale
		return nil
	})
	if err != nil {
		return nil, err
	}
	return done, nil
}

// save writes the sale and its accumulated events in the same transaction.
// RefundCharged gives back the payment of a sale whose payment was taken but whose
// fiscal record (receipt) could not be made and cancels the sale (e.g. the ÖKC has been
// out of order for a long time, the customer is not waiting). First the payment is refunded; if it fails
// the sale stays charged and can be retried. Since no receipt was ever issued no fiscal
// document is needed. The caller checks the authority (manager).
func (s *Service) RefundCharged(ctx context.Context, id domain.SaleID) (*domain.Sale, error) {
	sale, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if sale.Status() != domain.StatusCharged {
		return nil, domain.ErrNotCharged
	}
	if slices.ContainsFunc(sale.Payments(), func(p domain.Payment) bool { return p.Collect }) {
		return nil, ErrCollectedByDevice
	}
	if err := s.payments.Refund(ctx, id); err != nil {
		return nil, fmt.Errorf("ödeme geri verilemedi; satış değişmedi, tekrar denenebilir: %w", err)
	}
	ctx, cancel := settle(ctx)
	defer cancel()
	var done *domain.Sale
	err = s.repo.InTx(ctx, func(tx Tx) error {
		sale, err := tx.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := sale.VoidRefunded(s.now()); err != nil {
			return err
		}
		if err := s.save(ctx, tx, sale); err != nil {
			return err
		}
		done = sale
		return nil
	})
	if err != nil {
		return nil, err
	}
	return done, nil
}

// recordFiscalFailure writes the SaleCharged event to the outbox for a sale whose fiscal record
// could not be made. Since the sale was already recorded charged when the payment was taken, it
// shows at the register even if this cannot be written ("Complete the receipt").
func (s *Service) recordFiscalFailure(ctx context.Context, id domain.SaleID, cause error) error {
	return s.repo.InTx(ctx, func(tx Tx) error {
		cur, err := tx.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := cur.RecordFiscalFailure(cause.Error(), s.now()); err != nil {
			return err
		}
		return s.save(ctx, tx, cur)
	})
}

func (s *Service) save(ctx context.Context, tx Tx, sale *domain.Sale) error {
	if err := tx.Save(ctx, sale); err != nil {
		return err
	}
	msgs, err := s.outboxMessages(sale.PullEvents())
	if err != nil {
		return err
	}
	return tx.AppendOutbox(ctx, msgs)
}

func (s *Service) outboxMessages(events []domain.Event) ([]OutboxMessage, error) {
	msgs := make([]OutboxMessage, 0, len(events))
	for _, e := range events {
		id := s.cfg.NewID()
		payload, contentType, err := s.cfg.Encoder.Encode(e, id, s.cfg.StoreID)
		if err != nil {
			return nil, fmt.Errorf("olay kodlanamadı: %w", err)
		}
		msgs = append(msgs, OutboxMessage{
			ID:          id,
			Type:        eventTypePrefix + e.Name(),
			Subject:     e.Name(),
			StoreID:     s.cfg.StoreID,
			RegisterID:  s.cfg.RegisterID,
			Payload:     payload,
			ContentType: contentType,
			OccurredAt:  e.OccurredAt(),
		})
	}
	return msgs, nil
}

func (s *Service) now() time.Time { return s.cfg.Now().UTC() }

// jsonEncoder encodes the event as JSON (if Config.Encoder is empty).
type jsonEncoder struct{}

func (jsonEncoder) Encode(e domain.Event, _, _ string) ([]byte, string, error) {
	b, err := json.Marshal(e)
	return b, "application/json", err
}
