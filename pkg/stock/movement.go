// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package stock holds the stock rules (store and headquarters warehouse): stock is the total of
// the movements. Movements do not change; a wrong entry is corrected with a reverse movement.
// Stock can go negative: sales do not stop, negative stock is corrected with a count.
package stock

import (
	"errors"
	"strings"
	"time"

	"abarow.com/pkg/money"
)

// Kind is the movement's kind.
type Kind string

const (
	Sale        Kind = "sale"         // sale (negative)
	Receipt     Kind = "receipt"      // goods receipt (positive)
	Count       Kind = "count"        // count difference (positive or negative)
	TransferOut Kind = "transfer_out" // transfer out (negative)
	TransferIn  Kind = "transfer_in"  // transfer in (positive)
)

// Unit is the product's sales unit.
type Unit string

const (
	Piece Unit = "piece"
	Kg    Unit = "kg"
)

// Entry errors the cashier or manager can correct.
var (
	ErrNoLines          = errors.New("en az bir ürün girin")
	ErrInvalidQty       = errors.New("miktar sıfırdan büyük olmalı")
	ErrNegativeCount    = errors.New("sayılan miktar eksi olamaz")
	ErrPieceFraction    = errors.New("adetli ürünün miktarı tam sayı olmalı")
	ErrDuplicateProduct = errors.New("aynı ürün iki satırda; miktarları birleştirin")
	ErrNoSupplier       = errors.New("tedarikçiyi yazın")
	ErrNoRef            = errors.New("irsaliye numarasını yazın")
	ErrUnknownProduct   = errors.New("ürün bulunamadı")
	ErrDuplicateReceipt = errors.New("bu irsaliye bu tedarikçiden daha önce girildi")
	ErrNoDestination    = errors.New("gönderilecek yeri seçin")
	ErrUnknownLocation  = errors.New("gönderilecek yer bulunamadı")
	ErrSameLocation     = errors.New("bir yer kendisine transfer gönderemez")
	ErrUnknownTransfer  = errors.New("transfer bulunamadı")
	ErrTransferReceived = errors.New("bu transfer daha önce teslim alındı")
	ErrNotForHere       = errors.New("bu transfer bu yere gönderilmedi")
)

// Product is a product whose stock is kept (from the catalog or from a sale line).
type Product struct {
	ID, Name     string
	Unit         Unit
	Barcodes     []string
	Discontinued bool
}

// Line is a product of the movement. Qty is signed; in a count Counted and System
// are the counted quantity and the system quantity at that moment (Qty = Counted − System).
type Line struct {
	ProductID, Name string
	Qty             money.Qty
	Counted, System money.Qty
}

// Movement is a stock movement: a sale, a goods receipt, a count
// or a transfer.
type Movement struct {
	ID       string
	Kind     Kind
	Ref      string // sale id, delivery note no, count no or transfer
	Supplier string // mal kabulde
	// Counterpart is the other place in a transfer: the one sent to on an outflow, the
	// sending place on an inflow.
	Counterpart string
	By          string // the person who made the movement
	At          time.Time
	Lines       []Line
}

// Item is a product's stock.
type Item struct {
	Product
	Qty money.Qty
}

// checkQty checks that the quantity entered is valid in its unit.
func checkQty(p Product, q money.Qty) error {
	if p.Unit != Kg && q%money.QtyScale != 0 {
		return ErrPieceFraction
	}
	return nil
}

// NewReceipt is a goods receipt movement: goods that came from a supplier with a delivery note.
// products is the catalog state of the products in the lines.
func NewReceipt(id, supplier, ref, by string, at time.Time, lines []Line, products map[string]Product) (Movement, error) {
	supplier, ref = strings.TrimSpace(supplier), strings.TrimSpace(ref)
	if supplier == "" {
		return Movement{}, ErrNoSupplier
	}
	if ref == "" {
		return Movement{}, ErrNoRef
	}
	received, err := positiveLines(lines, products)
	if err != nil {
		return Movement{}, err
	}
	return Movement{ID: id, Kind: Receipt, Ref: ref, Supplier: supplier, By: by, At: at, Lines: received}, nil
}

// NewCount is a count movement: in each line the counted quantity (Counted) and
// the stock at that moment in system; the difference (counted − in the system) is applied to
// the stock. A line with a zero difference is recorded too: it shows the product was counted.
func NewCount(id, ref, by string, at time.Time, counts []Line, system map[string]money.Qty, products map[string]Product) (Movement, error) {
	if len(counts) == 0 {
		return Movement{}, ErrNoLines
	}
	m := Movement{ID: id, Kind: Count, Ref: strings.TrimSpace(ref), By: by, At: at}
	seen := map[string]bool{}
	for _, c := range counts {
		p, ok := products[c.ProductID]
		if !ok {
			return Movement{}, ErrUnknownProduct
		}
		if seen[c.ProductID] {
			return Movement{}, ErrDuplicateProduct
		}
		seen[c.ProductID] = true
		if c.Counted < 0 {
			return Movement{}, ErrNegativeCount
		}
		if err := checkQty(p, c.Counted); err != nil {
			return Movement{}, err
		}
		sys := system[c.ProductID]
		m.Lines = append(m.Lines, Line{ProductID: p.ID, Name: p.Name, Qty: c.Counted - sys, Counted: c.Counted, System: sys})
	}
	return m, nil
}

// SaleID is the sale movement's id: even if the same sale comes twice it is deducted
// once.
func SaleID(saleID string) string { return "sale:" + saleID }

// NewSale is the stock movement of a completed sale at the register: every line is negative.
// If the same product is on more than one line they are combined.
func NewSale(saleID, cashierID string, at time.Time, lines []Line) Movement {
	m := Movement{ID: SaleID(saleID), Kind: Sale, Ref: saleID, By: cashierID, At: at}
	index := map[string]int{}
	for _, l := range lines {
		if i, ok := index[l.ProductID]; ok {
			m.Lines[i].Qty -= l.Qty
			continue
		}
		index[l.ProductID] = len(m.Lines)
		m.Lines = append(m.Lines, Line{ProductID: l.ProductID, Name: l.Name, Qty: -l.Qty})
	}
	return m
}

// Transfer is goods sent from one place to another. The sending place does the transfer
// out; the receiving place does the transfer in when it takes delivery of the goods.
// In between the transfer is in transit.
type Transfer struct {
	ID, From, To, By string
	At               time.Time
	// Lines are the quantities sent (positive).
	Lines []Line
	// If received: who, when and the quantities received.
	ReceivedBy string
	ReceivedAt time.Time
	Received   []Line
}

// Location is a place a transfer can be sent to (the headquarters warehouse or a
// store).
type Location struct{ ID, Name string }

// Pending says the transfer is in transit.
func (t Transfer) Pending() bool { return t.ReceivedAt.IsZero() }

// NewTransferOut is the outflow movement of the goods sent from from to to; the basis is the
// transfer id. The quantities are entered greater than zero, they are negative in the movement.
func NewTransferOut(id, transferID, from, to, by string, at time.Time, lines []Line, products map[string]Product) (Movement, Transfer, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return Movement{}, Transfer{}, ErrNoDestination
	}
	if to == from {
		return Movement{}, Transfer{}, ErrSameLocation
	}
	sent, err := positiveLines(lines, products)
	if err != nil {
		return Movement{}, Transfer{}, err
	}
	m := Movement{ID: id, Kind: TransferOut, Ref: transferID, Counterpart: to, By: by, At: at}
	for _, l := range sent {
		m.Lines = append(m.Lines, Line{ProductID: l.ProductID, Name: l.Name, Qty: -l.Qty})
	}
	return m, Transfer{ID: transferID, From: from, To: to, By: by, At: at, Lines: sent}, nil
}

// NewTransferIn is the inflow movement of the transfer coming to here: received
// are the quantities taken delivery of (a product that came short is entered as less, one that
// never came as zero or not written at all; the difference from what was sent shows in the
// transfer's record). If received is empty all of what was sent counts as received.
// In the movement there is a line for every product sent, in the order sent.
func NewTransferIn(id, here, by string, at time.Time, t Transfer, received []Line, products map[string]Product) (Movement, error) {
	if t.To != here {
		return Movement{}, ErrNotForHere
	}
	if !t.Pending() {
		return Movement{}, ErrTransferReceived
	}
	got := map[string]money.Qty{}
	if len(received) == 0 {
		for _, l := range t.Lines {
			got[l.ProductID] = l.Qty
		}
	}
	sent := map[string]bool{}
	for _, l := range t.Lines {
		sent[l.ProductID] = true
	}
	seen := map[string]bool{}
	for _, l := range received {
		p, ok := products[l.ProductID]
		if !ok || !sent[l.ProductID] {
			return Movement{}, ErrUnknownProduct
		}
		if seen[l.ProductID] {
			return Movement{}, ErrDuplicateProduct
		}
		seen[l.ProductID] = true
		if l.Qty < 0 {
			return Movement{}, ErrInvalidQty
		}
		if err := checkQty(p, l.Qty); err != nil {
			return Movement{}, err
		}
		got[l.ProductID] = l.Qty
	}
	m := Movement{ID: id, Kind: TransferIn, Ref: t.ID, Counterpart: t.From, By: by, At: at}
	for _, l := range t.Lines {
		m.Lines = append(m.Lines, Line{ProductID: l.ProductID, Name: l.Name, Qty: got[l.ProductID]})
	}
	return m, nil
}

// positiveLines checks the lines entered: a known product, greater than zero, a quantity
// fit for its unit, each product once.
func positiveLines(lines []Line, products map[string]Product) ([]Line, error) {
	if len(lines) == 0 {
		return nil, ErrNoLines
	}
	var out []Line
	seen := map[string]bool{}
	for _, l := range lines {
		p, ok := products[l.ProductID]
		if !ok {
			return nil, ErrUnknownProduct
		}
		if seen[l.ProductID] {
			return nil, ErrDuplicateProduct
		}
		seen[l.ProductID] = true
		if l.Qty <= 0 {
			return nil, ErrInvalidQty
		}
		if err := checkQty(p, l.Qty); err != nil {
			return nil, err
		}
		out = append(out, Line{ProductID: p.ID, Name: p.Name, Qty: l.Qty})
	}
	return out, nil
}
