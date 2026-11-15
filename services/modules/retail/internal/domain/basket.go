// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package domain holds the basket rules of the market module.
package domain

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"abarow.com/pkg/calc"
	"abarow.com/pkg/money"
)

// Unit is the product's selling unit.
type Unit string

const (
	Piece Unit = "piece"
	Kg    Unit = "kg"
)

var (
	ErrClosed       = errors.New("sepet kapalı")
	ErrEmpty        = errors.New("sepet boş")
	ErrNoLine       = errors.New("sepette böyle bir satır yok")
	ErrInvalidQty   = errors.New("miktar pozitif olmalı")
	ErrNeedsWeight  = errors.New("tartılı ürün: terazi barkodunu okutun")
	ErrWeighedQty   = errors.New("tartılı ürünün miktarı değiştirilemez; yeniden tartın")
	ErrInvalidScale = errors.New("terazi barkodu okunamadı")
)

// Product is the form in the catalog of the product to add to the basket. The price includes
// VAT; for weighed products it is the price per kilogram.
type Product struct {
	ID        string
	Name      string
	Barcode   string
	Unit      Unit
	UnitPrice money.Money
	TaxRate   money.Rate
}

// Line is a line in the basket.
type Line struct {
	ProductID string
	Barcode   string
	Name      string
	Qty       money.Qty
	UnitPrice money.Money
	TaxRate   money.Rate
	// Weighed shows that the quantity was weighed (it came from the scale barcode or from the
	// scale at the register).
	Weighed bool
}

// Total is the line's amount including VAT.
func (l Line) Total() money.Money { return calc.Line(l.UnitPrice, l.Qty) }

// BasketID is the basket's id.
type BasketID string

// Status, sepetin durumudur.
type Status string

const (
	Open Status = "open"
	// CheckedOut: the list of lines was handed to sales; the payment is taken in sales.
	CheckedOut Status = "checked_out"
)

// Basket is the products the cashier scanned. The fields are read-only;
// changes are made with methods.
type Basket struct {
	ID BasketID
	// CashierID is the cashier who opened the basket; when the screen reopens the cashier
	// continues from their own basket.
	CashierID string
	Status    Status
	Lines     []Line
	SaleID    string
	CreatedAt time.Time
	// UpdatedAt is the moment the basket last changed.
	UpdatedAt time.Time
}

// NewBasket returns the cashier's empty and open basket.
func NewBasket(id BasketID, cashierID string, now time.Time) *Basket {
	return &Basket{ID: id, CashierID: cashierID, Status: Open, CreatedAt: now, UpdatedAt: now}
}

// Add adds the product to the basket. If the same product is scanned again the line's
// quantity increases; weighed products are a separate line at every weighing.
func (b *Basket) Add(p Product, qty money.Qty, weighed bool) error {
	if b.Status != Open {
		return ErrClosed
	}
	if qty <= 0 {
		return ErrInvalidQty
	}
	if p.Unit == Kg && !weighed {
		return ErrNeedsWeight
	}
	if !weighed {
		for i := range b.Lines {
			if !b.Lines[i].Weighed && b.Lines[i].ProductID == p.ID {
				if err := calc.CheckLine(p.UnitPrice, b.Lines[i].Qty+qty); err != nil {
					return err
				}
				b.Lines[i].Qty += qty
				return nil
			}
		}
	}
	if err := calc.CheckLine(p.UnitPrice, qty); err != nil {
		return err
	}
	if len(b.Lines) >= calc.MaxLines {
		return fmt.Errorf("%w: en çok %d satır", calc.ErrTooLarge, calc.MaxLines)
	}
	b.Lines = append(b.Lines, Line{
		ProductID: p.ID,
		Barcode:   p.Barcode,
		Name:      p.Name,
		Qty:       qty,
		UnitPrice: p.UnitPrice,
		TaxRate:   p.TaxRate,
		Weighed:   weighed,
	})
	return nil
}

// SetQty changes the quantity of a line sold by the piece.
func (b *Basket) SetQty(i int, qty money.Qty) error {
	if err := b.checkLine(i); err != nil {
		return err
	}
	if qty <= 0 {
		return ErrInvalidQty
	}
	if b.Lines[i].Weighed {
		return ErrWeighedQty
	}
	if err := calc.CheckLine(b.Lines[i].UnitPrice, qty); err != nil {
		return err
	}
	b.Lines[i].Qty = qty
	return nil
}

// Remove removes a line from the basket.
func (b *Basket) Remove(i int) error {
	if err := b.checkLine(i); err != nil {
		return err
	}
	b.Lines = slices.Delete(b.Lines, i, i+1)
	return nil
}

// Total is the basket's total including VAT.
func (b *Basket) Total() money.Money {
	items := make([]calc.Item, 0, len(b.Lines))
	for _, l := range b.Lines {
		items = append(items, calc.Item{Total: l.Total(), Rate: l.TaxRate})
	}
	return calc.Sum(items)
}

// CheckOut ties the basket to the sale created in sales and closes it.
func (b *Basket) CheckOut(saleID string) error {
	if b.Status != Open {
		return ErrClosed
	}
	if len(b.Lines) == 0 {
		return ErrEmpty
	}
	b.Status = CheckedOut
	b.SaleID = saleID
	return nil
}

// Reopen reopens a basket whose payment was abandoned (whose sale was cancelled).
func (b *Basket) Reopen() {
	b.Status = Open
	b.SaleID = ""
}

// Clone returns an independent copy of the basket.
func (b *Basket) Clone() *Basket {
	c := *b
	c.Lines = slices.Clone(b.Lines)
	return &c
}

func (b *Basket) checkLine(i int) error {
	if b.Status != Open {
		return ErrClosed
	}
	if i < 0 || i >= len(b.Lines) {
		return ErrNoLine
	}
	return nil
}
