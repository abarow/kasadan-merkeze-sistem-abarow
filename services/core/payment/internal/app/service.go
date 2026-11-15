// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package app holds the payment service's use case and ports.
package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"abarow.com/pkg/money"
	"abarow.com/services/core/payment/internal/domain"
)

var (
	// ErrNotFound is returned when there is no payment record for the sale.
	ErrNotFound = errors.New("ödeme kaydı yok")
	// ErrDeclined is returned when the card transaction is declined by the bank.
	ErrDeclined = errors.New("kart ödemesi reddedildi")
)

// Repository stores the payments taken by sale id.
type Repository interface {
	// Get returns the sale's payment record; ErrNotFound if none.
	Get(ctx context.Context, saleID string) (*domain.Charge, error)
	Save(ctx context.Context, c domain.Charge) error
	// Update updates the payments of an existing record (refund).
	Update(ctx context.Context, c domain.Charge) error
}

// CardTerminal, banka POS'udur.
type CardTerminal interface {
	// Charge takes the amount from the card and returns the transaction reference. If the
	// bank declines it returns an error wrapping ErrDeclined.
	Charge(ctx context.Context, saleID string, amount money.Money) (ref string, err error)
	// Reverse cancels an approved transaction.
	Reverse(ctx context.Context, ref string) error
}

// Service is the payment use case.
type Service struct {
	// mu prevents concurrent requests for the same sale from taking payment twice.
	// It is enough since there is a single payment service at the register.
	mu       sync.Mutex
	repo     Repository
	terminal CardTerminal
	now      func() time.Time
}

// NewService connects the ports. time.Now is used if now is empty.
func NewService(repo Repository, terminal CardTerminal, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, terminal: terminal, now: now}
}

// Charge takes the sale's payments and returns each payment with its reference.
//
// It is idempotent by sale id: if there is a record for the sale no new payment is
// taken, the recorded payments are returned. In a split card payment, if one card is
// declined the card transactions approved earlier are reversed; so the failed request leaves
// no trace and can be safely retried.
func (s *Service) Charge(ctx context.Context, saleID string, payments []domain.Payment) ([]domain.Payment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, err := s.repo.Get(ctx, saleID)
	if err == nil {
		return slices.Clone(existing.Payments), nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if err := domain.ValidateRequest(saleID, payments); err != nil {
		return nil, err
	}

	charged := slices.Clone(payments)
	var approved []string
	for i := range charged {
		if !charged[i].Method.OnTerminal() {
			continue
		}
		ref, err := s.terminal.Charge(ctx, saleID, charged[i].Amount)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("%s ödemesi alınamadı: %w", charged[i].Method.Label(), err), s.reverse(ctx, approved))
		}
		charged[i].Ref = ref
		approved = append(approved, ref)
	}
	if err := s.repo.Save(ctx, domain.Charge{SaleID: saleID, Payments: charged, At: s.now()}); err != nil {
		return nil, errors.Join(fmt.Errorf("ödeme kaydedilemedi: %w", err), s.reverse(ctx, approved))
	}
	return charged, nil
}

// Refund gives back the payments taken for the sale: card transactions are
// reversed at the POS, cash is handed to the customer from the drawer by the cashier.
// It is for a sale whose fiscal record (receipt) could never be made; the refund of a sale
// whose receipt was issued is a separate flow.
//
// It can be called again: the payments given back are marked, if a card transaction cannot
// be reversed the progress up to that point is saved and an error is returned; on retry
// the remaining ones are reversed.
func (s *Service) Refund(ctx context.Context, saleID string) ([]domain.Payment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, err := s.repo.Get(ctx, saleID)
	if err != nil {
		return nil, err
	}
	var failed error
	for i := range c.Payments {
		p := &c.Payments[i]
		if p.Refunded {
			continue
		}
		if p.Method.OnTerminal() {
			if err := s.terminal.Reverse(ctx, p.Ref); err != nil {
				failed = fmt.Errorf("DİKKAT: %s numaralı %s işlemi geri alınamadı; tekrar deneyin ya da bankayla elle iade yapın: %w", p.Ref, p.Method.Label(), err)
				break
			}
		}
		p.Refunded = true
	}
	if err := s.repo.Update(ctx, *c); err != nil {
		return nil, errors.Join(fmt.Errorf("iade kaydedilemedi: %w", err), failed)
	}
	if failed != nil {
		return nil, failed
	}
	return slices.Clone(c.Payments), nil
}

// reverse reverses the approved card transactions. A transaction that cannot be reversed
// means money was taken from the customer but not recorded; the error says this
// clearly.
func (s *Service) reverse(ctx context.Context, refs []string) error {
	var errs []error
	for _, ref := range refs {
		if err := s.terminal.Reverse(context.WithoutCancel(ctx), ref); err != nil {
			errs = append(errs, fmt.Errorf("DİKKAT: %s numaralı kart işlemi geri alınamadı, elle iade gerekir: %w", ref, err))
		}
	}
	return errors.Join(errs...)
}
