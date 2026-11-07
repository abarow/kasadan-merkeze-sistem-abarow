// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"abarow.com/pkg/money"
	"abarow.com/services/core/payment/internal/app"
	"abarow.com/services/core/payment/internal/domain"
)

type memRepo struct {
	charges map[string]domain.Charge
	saveErr error
}

func (r *memRepo) Get(_ context.Context, saleID string) (*domain.Charge, error) {
	c, ok := r.charges[saleID]
	if !ok {
		return nil, app.ErrNotFound
	}
	return &c, nil
}

func (r *memRepo) Update(_ context.Context, c domain.Charge) error {
	if _, ok := r.charges[c.SaleID]; !ok {
		return app.ErrNotFound
	}
	r.charges[c.SaleID] = c
	return nil
}

func (r *memRepo) Save(_ context.Context, c domain.Charge) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.charges[c.SaleID] = c
	return nil
}

// terminal is a fake POS that declines from the declineFrom-th charge on;
// if reverseErr is given the reversals fail.
type terminal struct {
	charges     int
	declineFrom int
	reversed    []string
	reverseErr  error
}

func (t *terminal) Charge(context.Context, string, money.Money) (string, error) {
	t.charges++
	if t.declineFrom > 0 && t.charges >= t.declineFrom {
		return "", fmt.Errorf("yetersiz bakiye: %w", app.ErrDeclined)
	}
	return fmt.Sprintf("POS-%d", t.charges), nil
}

func (t *terminal) Reverse(_ context.Context, ref string) error {
	if t.reverseErr != nil {
		return t.reverseErr
	}
	t.reversed = append(t.reversed, ref)
	return nil
}

func setup() (*app.Service, *memRepo, *terminal) {
	repo := &memRepo{charges: map[string]domain.Charge{}}
	term := &terminal{}
	return app.NewService(repo, term, nil), repo, term
}

func TestChargeCashDoesNotUseTerminal(t *testing.T) {
	svc, repo, term := setup()
	got, err := svc.Charge(context.Background(), "s1", []domain.Payment{{Method: domain.Cash, Amount: 20000}})
	if err != nil {
		t.Fatal(err)
	}
	if term.charges != 0 || len(got) != 1 || got[0].Ref != "" {
		t.Errorf("cash payment is wrong: %+v (POS calls %d)", got, term.charges)
	}
	if _, ok := repo.charges["s1"]; !ok {
		t.Error("the payment was not recorded")
	}
}

// A meal card is charged from the POS like a card and reversed on refund; if declined
// the card charged first is reversed, and the message names the method.
func TestMealCard(t *testing.T) {
	svc, _, term := setup()
	ctx := context.Background()
	got, err := svc.Charge(ctx, "s1", []domain.Payment{{Method: domain.Cash, Amount: 1000}, {Method: domain.MealCard, Amount: 4990}})
	if err != nil || term.charges != 1 || got[1].Ref != "POS-1" {
		t.Fatalf("meal card: %+v %v (POS calls %d)", got, err, term.charges)
	}
	if _, err := svc.Refund(ctx, "s1"); err != nil || len(term.reversed) != 1 || term.reversed[0] != "POS-1" {
		t.Errorf("iade: %v %v", term.reversed, err)
	}

	svc, _, term = setup()
	term.declineFrom = 2
	_, err = svc.Charge(ctx, "s2", []domain.Payment{{Method: domain.Card, Amount: 1000}, {Method: domain.MealCard, Amount: 2000}})
	if !errors.Is(err, app.ErrDeclined) || !strings.Contains(err.Error(), "yemek kartı ödemesi alınamadı") || len(term.reversed) != 1 {
		t.Errorf("declined meal card: %v (reversed %v)", err, term.reversed)
	}
}

func TestChargeIsIdempotentPerSale(t *testing.T) {
	svc, _, term := setup()
	ctx := context.Background()
	req := []domain.Payment{{Method: domain.Card, Amount: 10000}, {Method: domain.Cash, Amount: 3000}}

	first, err := svc.Charge(ctx, "s1", req)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Payment{{Method: domain.Card, Amount: 10000, Ref: "POS-1"}, {Method: domain.Cash, Amount: 3000}}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("first charge %+v, want %+v", first, want)
	}

	// The answer was lost and it was retried: no new charge is made, the same result returns.
	again, err := svc.Charge(ctx, "s1", req)
	if err != nil {
		t.Fatal(err)
	}
	if term.charges != 1 || !reflect.DeepEqual(again, first) {
		t.Errorf("the repeated request made a new charge: %+v (POS calls %d)", again, term.charges)
	}
}

func TestChargeReversesApprovedCardsOnDecline(t *testing.T) {
	svc, repo, term := setup()
	term.declineFrom = 2
	req := []domain.Payment{{Method: domain.Card, Amount: 6000}, {Method: domain.Card, Amount: 6537}}

	if _, err := svc.Charge(context.Background(), "s1", req); !errors.Is(err, app.ErrDeclined) {
		t.Fatalf("err = %v, want %v", err, app.ErrDeclined)
	}
	if !reflect.DeepEqual(term.reversed, []string{"POS-1"}) {
		t.Errorf("reversed transactions %v, want [POS-1]", term.reversed)
	}
	if len(repo.charges) != 0 {
		t.Error("the declined payment was recorded")
	}
}

func TestChargeReversesWhenSaveFails(t *testing.T) {
	svc, repo, term := setup()
	repo.saveErr = errors.New("disk dolu")
	if _, err := svc.Charge(context.Background(), "s1", []domain.Payment{{Method: domain.Card, Amount: 100}}); err == nil {
		t.Fatal("no record error returned")
	}
	if !reflect.DeepEqual(term.reversed, []string{"POS-1"}) {
		t.Errorf("reversed transactions %v, want [POS-1]", term.reversed)
	}
}

func TestChargeValidatesRequest(t *testing.T) {
	svc, _, term := setup()
	tests := map[string]struct {
		saleID   string
		payments []domain.Payment
	}{
		"satış kimliği boş": {"", []domain.Payment{{Method: domain.Cash, Amount: 1}}},
		"ödeme yok":         {"s1", nil},
		"sıfır tutar":       {"s1", []domain.Payment{{Method: domain.Cash, Amount: 0}}},
		"bilinmeyen yöntem": {"s1", []domain.Payment{{Method: "cheque", Amount: 1}}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.Charge(context.Background(), tt.saleID, tt.payments); !errors.Is(err, domain.ErrInvalidRequest) {
				t.Errorf("err = %v, want %v", err, domain.ErrInvalidRequest)
			}
		})
	}
	if term.charges != 0 {
		t.Errorf("the POS was called %d times on an invalid request", term.charges)
	}
}

func TestRefund(t *testing.T) {
	svc, repo, term := setup()
	ctx := context.Background()
	if _, err := svc.Refund(ctx, "yok"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("sale with no record: %v", err)
	}
	// Nakit 10,00 + kart 5,00 + kart 3,00.
	req := []domain.Payment{{Method: domain.Cash, Amount: 1000}, {Method: domain.Card, Amount: 500}, {Method: domain.Card, Amount: 300}}
	if _, err := svc.Charge(ctx, "s1", req); err != nil {
		t.Fatal(err)
	}

	// If the POS refuses the reversal the progress is saved, an error is returned.
	term.reverseErr = errors.New("POS bağlantısı yok")
	if _, err := svc.Refund(ctx, "s1"); err == nil || !strings.Contains(err.Error(), "POS-1") {
		t.Fatalf("card that cannot be reversed: %v", err)
	}
	if p := repo.charges["s1"].Payments; !p[0].Refunded || p[1].Refunded {
		t.Errorf("partial refund record: %+v", p)
	}

	// On retry the remaining ones are reversed; the one already reversed is not reversed again.
	term.reverseErr = nil
	got, err := svc.Refund(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(term.reversed, []string{"POS-1", "POS-2"}) {
		t.Errorf("reversed transactions %v", term.reversed)
	}
	for _, p := range got {
		if !p.Refunded {
			t.Errorf("payment not refunded: %+v", p)
		}
	}
	if _, err := svc.Refund(ctx, "s1"); err != nil || len(term.reversed) != 2 {
		t.Errorf("third refund: %v, reversed %v", err, term.reversed)
	}
}
