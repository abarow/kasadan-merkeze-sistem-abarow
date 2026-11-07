// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package domain holds the rules of the Beko fiscal printer adapter: turning abarow's receipt into
// the basket JSON of the Token Integration Hub and reading the device's sale status.
// The field names and payment types are from Token's publicly available
// developer documentation (docs/decisions/beko-x30tr-prep.md).
package domain

import (
	"encoding/json"
	"errors"
	"fmt"

	"abarow.com/pkg/calc"
	"abarow.com/pkg/money"
	"abarow.com/pkg/paymethod"
)

var (
	// ErrInvalidReceipt is a receipt too incomplete or inconsistent to be sent to
	// the device.
	ErrInvalidReceipt = errors.New("geçersiz fiş")
	// ErrUnmappedTax means the VAT rate on the receipt has no section defined on the fiscal printer.
	ErrUnmappedTax = errors.New("KDV oranının ÖKC bölümü tanımlı değil")
	// ErrCardNotCollected means a card or meal card payment came without being marked
	// as one the device will collect (Collect). The device collects the card from its own POS; if the card
	// was already charged elsewhere it can only be written on the receipt (the customer would have
	// paid twice), so it is refused.
	ErrCardNotCollected = errors.New("kart ve yemek kartı ödemesini ÖKC çeker (collect); başka yerden çekilmiş kart ÖKC'ye yazılamaz")
)

// Token's payment types.
const (
	typeCash     = 1
	typeCard     = 3
	typeMealCard = 7
)

// Line is an item on the receipt. The price includes VAT.
type Line struct {
	Name      string
	Qty       money.Qty
	UnitPrice money.Money
	Total     money.Money
	TaxRate   money.Rate
}

// Payment is a payment on the receipt. Method is the paymethod name (cash, card,
// meal_card). Collect says that the payment has not been taken yet and the device will take
// it from its own POS (it is required for card and meal card).
type Payment struct {
	Method  string
	Amount  money.Money
	Collect bool
}

// Receipt is abarow's receipt.
type Receipt struct {
	SaleID   string
	Lines    []Line
	Total    money.Money
	Payments []Payment
	Change   money.Money
}

// Config holds the install settings of the basket for the device.
type Config struct {
	// Sections is the mapping from the VAT rate (basis points) to the fiscal printer section number.
	// The sections are defined on the device (getFiscalInfo); at install they are
	// matched with the ones on the device.
	Sections map[money.Rate]int
	// CashNet says that the cash payment is sent as the amount after the change is deducted
	// (the sum of the payments equals the receipt total). Otherwise the received
	// amount is sent.
	CashNet bool
	// DocumentType is Token's document type; 0 is a sales receipt.
	DocumentType int
}

// Basket is the basket JSON of the Token Integration Hub.
type Basket struct {
	BasketID      string       `json:"basketID"`
	DocumentType  int          `json:"documentType"`
	CreateInvoice bool         `json:"createInvoice"`
	IsVoid        bool         `json:"isVoid"`
	Items         []BasketItem `json:"items"`
	PaymentItems  []BasketPay  `json:"paymentItems"`
	CustomerInfo  *struct{}    `json:"customerInfo"`
	Adjust        *struct{}    `json:"adjust"`
}

// BasketItem is a product in the basket. The price is in kuruş, the quantity is in thousandths,
// the VAT rate is in basis points (the same units as in abarow).
type BasketItem struct {
	Name       string `json:"name"`
	Price      int64  `json:"price"`
	Quantity   int64  `json:"quantity"`
	SectionNo  int    `json:"sectionNo"`
	TaxPercent int64  `json:"taxPercent"`
	Type       int    `json:"type"`
}

// BasketPay is a payment in the basket.
type BasketPay struct {
	Amount      int64  `json:"amount"`
	Description string `json:"description"`
	Type        int    `json:"type"`
}

// NewBasket turns the receipt into the device's basket. The basket id is the sale id:
// if the same sale is sent again it is the same basket.
func NewBasket(r Receipt, cfg Config) (Basket, error) {
	if r.SaleID == "" || len(r.Lines) == 0 || len(r.Payments) == 0 {
		return Basket{}, fmt.Errorf("%w: satış kimliği, kalem ve ödeme gerekli", ErrInvalidReceipt)
	}
	b := Basket{BasketID: r.SaleID, DocumentType: cfg.DocumentType}
	h := calc.Receipt{Total: r.Total, Change: r.Change}
	for _, l := range r.Lines {
		section, ok := cfg.Sections[l.TaxRate]
		if !ok {
			return Basket{}, fmt.Errorf("%w: %%%s (%q)", ErrUnmappedTax, rate(l.TaxRate), l.Name)
		}
		h.Items = append(h.Items, calc.Item{Total: l.Total, Rate: l.TaxRate})
		b.Items = append(b.Items, BasketItem{
			Name: l.Name, Price: int64(l.UnitPrice), Quantity: int64(l.Qty), SectionNo: section, TaxPercent: int64(l.TaxRate),
		})
	}
	for _, p := range r.Payments {
		pay := BasketPay{Amount: int64(p.Amount)}
		switch p.Method {
		case paymethod.Cash:
			pay.Type, pay.Description = typeCash, "Nakit"
		case paymethod.Card:
			pay.Type, pay.Description = typeCard, "Kredi Kartı"
		case paymethod.MealCard:
			pay.Type, pay.Description = typeMealCard, "Yemek Kartı"
		default:
			return Basket{}, fmt.Errorf("%w: ödeme türü %q ÖKC'ye gönderilemez", ErrInvalidReceipt, p.Method)
		}
		if pay.Type != typeCash && !p.Collect {
			return Basket{}, ErrCardNotCollected
		}
		h.Payments = append(h.Payments, calc.Payment{Method: p.Method, Amount: p.Amount})
		b.PaymentItems = append(b.PaymentItems, pay)
	}
	// The items and payments must add up to the receipt total (the rule is in pkg/calc; the fiscal printer
	// computes the VAT breakdown itself).
	if err := h.Validate(); err != nil {
		return Basket{}, fmt.Errorf("%w: %v", ErrInvalidReceipt, err)
	}
	if cfg.CashNet && r.Change > 0 {
		// The change is given from cash: it is deducted from a cash payment.
		for i := range b.PaymentItems {
			if b.PaymentItems[i].Type == typeCash && b.PaymentItems[i].Amount >= int64(r.Change) {
				b.PaymentItems[i].Amount -= int64(r.Change)
				break
			}
		}
	}
	return b, nil
}

// JSON, sepetin cihaza gidecek halidir.
func (b Basket) JSON() ([]byte, error) { return json.Marshal(b) }

func rate(r money.Rate) string { return fmt.Sprint(float64(r) / 100) }

// Sale statuses (the status field in Token's sale status).
const (
	StatusOK     = 0
	StatusFailed = -1
	StatusVoided = 99
)

// SaleStatus is the result the device reports for a sale (tag 3).
type SaleStatus struct {
	BasketID     string `json:"basketID"`
	DocumentType int    `json:"documentType"`
	Message      string `json:"message"`
	PaymentCount int    `json:"paymentCount"`
	ReceiptNo    int64  `json:"receiptNo"`
	Status       int    `json:"status"`
	ZNo          int64  `json:"zNo"`
}

// ParseSaleStatus reads the device's sale status JSON.
func ParseSaleStatus(data []byte) (SaleStatus, error) {
	var s SaleStatus
	if err := json.Unmarshal(data, &s); err != nil {
		return SaleStatus{}, fmt.Errorf("satış durumu okunamadı: %w", err)
	}
	if s.BasketID == "" {
		return SaleStatus{}, errors.New("satış durumunda sepet kimliği yok")
	}
	return s, nil
}

// FiscalNo is the fiscal number abarow keeps on the receipt record: the Z number and
// the receipt number (Z0007-000002).
func (s SaleStatus) FiscalNo() string { return fmt.Sprintf("Z%04d-%06d", s.ZNo, s.ReceiptNo) }
