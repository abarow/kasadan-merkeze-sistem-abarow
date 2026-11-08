// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package domain holds the restaurant's business rules: the floor and tables, the check
// (order), sending to the kitchen, cancellation, payment and bill splitting, moving and
// merging tables. It depends on no outside package (only the money and menu rules).
package domain

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"abarow.com/pkg/calc"
	"abarow.com/pkg/menu"
	"abarow.com/pkg/money"
)

// Errors the waiter or the cashier can correct.
var (
	ErrNotFound       = errors.New("bulunamadı")
	ErrTableBusy      = errors.New("masa dolu")
	ErrTableFree      = errors.New("masada açık adisyon yok")
	ErrClosed         = errors.New("adisyon kapandı")
	ErrInvalidLine    = errors.New("satır geçersiz")
	ErrLineSent       = errors.New("satır mutfağa gönderildi; değiştirmek için iptal edin (müdür onayı)")
	ErrLineNotSent    = errors.New("satır henüz mutfağa gönderilmedi; silebilirsiniz")
	ErrNotSent        = errors.New("ödenecek satırlar önce mutfağa gönderilmeli")
	ErrQtyTooLarge    = errors.New("adet açık adetten fazla")
	ErrNothingToPay   = errors.New("ödenecek satır yok")
	ErrPaymentPending = errors.New("bu adisyonun bitmemiş bir ödemesi var; önce onu tamamlayın ya da iptal edin")
	ErrStale          = errors.New("adisyon bu arada değişti; ekran yenilendi, tekrar deneyin")
	ErrVoidReason     = errors.New("iptal nedenini yazın")
	ErrFloor          = errors.New("salon ve masa bilgisi geçersiz")
)

// MaxQty is the largest quantity of a line (so that 1000 is not typed by mistake).
const MaxQty = 99

// MaxNoteRunes is the most characters of a line note (so that it fits on the kitchen ticket).
const MaxNoteRunes = 60

// Area is a floor area (e.g. "Inside", "Garden").
type Area struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Tables []Table `json:"tables"`
}

// Table is a table.
type Table struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

// ValidateFloor checks the list of areas and tables: the ids are valid and
// unique across all areas, the names are filled in.
func ValidateFloor(areas []Area) error {
	areaIDs, tableIDs := map[string]bool{}, map[string]bool{}
	for _, a := range areas {
		if !idPattern.MatchString(a.ID) || areaIDs[a.ID] {
			return fmt.Errorf("%w: salon kimliği %q geçersiz ya da iki kez var", ErrFloor, a.ID)
		}
		areaIDs[a.ID] = true
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("%w: salonun adı boş", ErrFloor)
		}
		for _, t := range a.Tables {
			if !idPattern.MatchString(t.ID) || tableIDs[t.ID] {
				return fmt.Errorf("%w: masa kimliği %q geçersiz ya da iki kez var", ErrFloor, t.ID)
			}
			tableIDs[t.ID] = true
			if strings.TrimSpace(t.Name) == "" {
				return fmt.Errorf("%w: %s salonunda adı boş masa", ErrFloor, a.Name)
			}
		}
	}
	return nil
}

// LineStatus is the state of a line.
type LineStatus string

const (
	// Pending: not sent to the kitchen; it can be changed and deleted freely.
	Pending LineStatus = "pending"
	// Sent: sent to the kitchen; it can be cancelled only with the manager's approval.
	Sent LineStatus = "sent"
	// Voided: cancelled after being sent (it is not paid, it stays in the record).
	Voided LineStatus = "voided"
)

// ChosenOption is an option chosen on a line.
type ChosenOption struct {
	Group      string      `json:"group"`
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	PriceDelta money.Money `json:"price_delta_kurus"`
}

// Line is a line of the check. The price and VAT are taken from the catalog when the line is
// added and do not change afterwards.
type Line struct {
	// ID is the id the client that added the line produced: if the same request
	// comes again a second line is not opened.
	ID        string         `json:"id"`
	ProductID string         `json:"product_id"`
	Name      string         `json:"name"`
	Options   []ChosenOption `json:"options"`
	Note      string         `json:"note"`
	Qty       int            `json:"qty"`
	// UnitPrice is the product price plus the price differences of the chosen options
	// (VAT included).
	UnitPrice money.Money `json:"unit_price_kurus"`
	TaxRate   money.Rate  `json:"tax_rate_bp"`
	Station   string      `json:"station"`
	AddedBy   string      `json:"added_by"`
	AddedAt   time.Time   `json:"added_at"`
	Status    LineStatus  `json:"status"`
	SentAt    time.Time   `json:"sent_at"`
	// Paid is the paid quantity; Held is the quantity whose payment is in progress (in an open sale).
	Paid       int    `json:"paid"`
	Held       int    `json:"held"`
	VoidReason string `json:"void_reason,omitempty"`
	VoidedBy   string `json:"voided_by,omitempty"`
}

// OpenQty is the quantity that is unpaid and not in payment (0 on a cancelled line).
func (l Line) OpenQty() int {
	if l.Status == Voided {
		return 0
	}
	return l.Qty - l.Paid - l.Held
}

// Total is the line's amount (quantity × unit price).
func (l Line) Total() money.Money { return calc.Line(l.UnitPrice, money.Qty(l.Qty)*money.QtyScale) }

// Label is the name of the line shown on the ticket and in the sale: "Meatball (rare, cheese)".
func (l Line) Label() string {
	if len(l.Options) == 0 {
		return l.Name
	}
	names := make([]string, len(l.Options))
	for i, o := range l.Options {
		names[i] = o.Name
	}
	return l.Name + " (" + strings.Join(names, ", ") + ")"
}

// Part is a line to be paid and its quantity (bill splitting).
type Part struct {
	LineID string `json:"line_id"`
	Qty    int    `json:"qty"`
}

// Payment is the check's payment in progress: the selected lines were turned into
// a sale, and those quantities are in payment until the sale is paid or cancelled.
type Payment struct {
	SaleID string    `json:"sale_id"`
	Parts  []Part    `json:"parts"`
	By     string    `json:"by"`
	At     time.Time `json:"at"`
}

// Order is a table's check.
type Order struct {
	ID       string    `json:"id"`
	TableID  string    `json:"table_id"`
	OpenedBy string    `json:"opened_by"`
	OpenedAt time.Time `json:"opened_at"`
	Lines    []Line    `json:"lines"`
	// Payment is the payment in progress (at most one at a time).
	Payment *Payment `json:"payment,omitempty"`
	// BillAt is the moment the bill (check printout) was last requested.
	BillAt time.Time `json:"bill_at"`
	// Closed: all lines are paid or cancelled, or the check was merged with
	// another table's.
	Closed   bool      `json:"closed"`
	ClosedAt time.Time `json:"closed_at"`
	// Version increases with every change: moving, merging and paying with an old
	// version are rejected (ErrStale).
	Version int64 `json:"version"`
}

// NewOrder opens a new check at the table.
func NewOrder(id, tableID, by string, at time.Time) *Order {
	return &Order{ID: id, TableID: tableID, OpenedBy: by, OpenedAt: at, Version: 1}
}

// Total is the amount of the lines that are not cancelled.
func (o *Order) Total() money.Money {
	var items []calc.Item
	for _, l := range o.Lines {
		if l.Status != Voided {
			items = append(items, calc.Item{Total: l.Total()})
		}
	}
	return calc.Sum(items)
}

// Due is the amount not yet paid (including what is in payment).
func (o *Order) Due() money.Money {
	var t money.Money
	for _, l := range o.Lines {
		if l.Status != Voided {
			t += l.UnitPrice * money.Money(l.Qty-l.Paid)
		}
	}
	return t
}

func (o *Order) line(id string) (int, error) {
	i := slices.IndexFunc(o.Lines, func(l Line) bool { return l.ID == id })
	if i < 0 {
		return -1, fmt.Errorf("%w: satır %s", ErrNotFound, id)
	}
	return i, nil
}

func (o *Order) touch() { o.Version++ }

func (o *Order) open() error {
	if o.Closed {
		return ErrClosed
	}
	return nil
}

// Check checks that the version the client knows is current (0: not checked).
func (o *Order) Check(version int64) error {
	if version != 0 && version != o.Version {
		return ErrStale
	}
	return nil
}

// Product is a product on the menu (from the catalog, with its current price).
type Product struct {
	ID, Name  string
	UnitPrice money.Money
	TaxRate   money.Rate
	Station   string
	Options   []menu.Group
	// Weighed: a weighed (kg) product; it is not sold by the piece in a restaurant.
	Weighed bool
}

// NewLineInput is the line to add: the product from the catalog, the choice from the waiter.
type NewLineInput struct {
	ID      string
	Product Product
	Choices []menu.Choice
	Note    string
	Qty     int
	By      string
	At      time.Time
}

// AddLine adds the line. If a line with the same id already exists it does nothing
// (a repeated request) and returns false. The option choice is checked
// against the product's groups; the line's price is the product price plus the differences of the chosen ones.
func (o *Order) AddLine(in NewLineInput) (bool, error) {
	if err := o.open(); err != nil {
		return false, err
	}
	if !validLineID(in.ID) {
		return false, fmt.Errorf("%w: satır kimliği %q", ErrInvalidLine, in.ID)
	}
	if slices.ContainsFunc(o.Lines, func(l Line) bool { return l.ID == in.ID }) {
		return false, nil
	}
	if in.Product.Weighed {
		return false, fmt.Errorf("%w: %s tartılı ürün; restoranda adetle satılır", ErrInvalidLine, in.Product.Name)
	}
	if in.Qty < 1 || in.Qty > MaxQty {
		return false, fmt.Errorf("%w: adet 1 ile %d arasında olmalı", ErrInvalidLine, MaxQty)
	}
	note := strings.Join(strings.Fields(in.Note), " ")
	if len([]rune(note)) > MaxNoteRunes {
		return false, fmt.Errorf("%w: not en çok %d karakter", ErrInvalidLine, MaxNoteRunes)
	}
	chosen, err := menu.Choose(in.Product.Options, in.Choices)
	if err != nil {
		return false, err
	}
	price := in.Product.UnitPrice + chosen.Delta
	if price < 0 {
		return false, fmt.Errorf("%w: seçimlerle fiyat eksiye düşüyor", ErrInvalidLine)
	}
	if err := calc.CheckLine(price, money.Qty(in.Qty)*money.QtyScale); err != nil {
		return false, fmt.Errorf("%w: %w", ErrInvalidLine, err)
	}
	if len(o.Lines) >= calc.MaxLines {
		return false, fmt.Errorf("%w: en çok %d satır", ErrInvalidLine, calc.MaxLines)
	}
	l := Line{
		ID: in.ID, ProductID: in.Product.ID, Name: in.Product.Name, Note: note, Qty: in.Qty,
		UnitPrice: price, TaxRate: in.Product.TaxRate, Station: station(in.Product.Station),
		AddedBy: in.By, AddedAt: in.At, Status: Pending, Options: []ChosenOption{},
	}
	for _, p := range chosen.Options {
		l.Options = append(l.Options, ChosenOption{Group: p.Group, ID: p.ID, Name: p.Name, PriceDelta: p.PriceDelta})
	}
	o.Lines = append(o.Lines, l)
	o.touch()
	return true, nil
}

// validLineID checks the line id the client produced (like a UUID:
// letters, digits and -; at most 64 characters).
func validLineID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func station(s string) string {
	if s == "" {
		return menu.Kitchen
	}
	return s
}

// SetLine changes the quantity and note of an unsent line; if the quantity is 0
// the line is deleted.
func (o *Order) SetLine(id string, qty int, note string) error {
	if err := o.open(); err != nil {
		return err
	}
	i, err := o.line(id)
	if err != nil {
		return err
	}
	if o.Lines[i].Status != Pending {
		return ErrLineSent
	}
	if qty == 0 {
		o.Lines = slices.Delete(o.Lines, i, i+1)
		o.touch()
		return nil
	}
	if qty < 1 || qty > MaxQty {
		return fmt.Errorf("%w: adet 1 ile %d arasında olmalı", ErrInvalidLine, MaxQty)
	}
	note = strings.Join(strings.Fields(note), " ")
	if len([]rune(note)) > MaxNoteRunes {
		return fmt.Errorf("%w: not en çok %d karakter", ErrInvalidLine, MaxNoteRunes)
	}
	o.Lines[i].Qty, o.Lines[i].Note = qty, note
	o.touch()
	return nil
}

// Stations are the stations of the waiting lines (in order).
func (o *Order) Stations() []string {
	var out []string
	for _, l := range o.Lines {
		if l.Status == Pending && !slices.Contains(out, l.Station) {
			out = append(out, l.Station)
		}
	}
	slices.Sort(out)
	return out
}

// Send counts the waiting lines of the station (all if empty) as sent to the kitchen
// and returns them (the station ticket is printed from them). If there are no waiting lines
// it returns empty; the same line is not sent twice.
func (o *Order) Send(at time.Time, station string) ([]Line, error) {
	if err := o.open(); err != nil {
		return nil, err
	}
	var sent []Line
	for i := range o.Lines {
		if o.Lines[i].Status == Pending && (station == "" || o.Lines[i].Station == station) {
			o.Lines[i].Status, o.Lines[i].SentAt = Sent, at
			sent = append(sent, o.Lines[i])
		}
	}
	if len(sent) > 0 {
		o.touch()
	}
	return sent, nil
}

// Void cancels qty of a sent line (with the manager's approval; the approval
// is the caller's job). If the whole line is not cancelled the cancelled quantity becomes a separate
// cancellation line (its id is voidID). The cancelled line is returned (a cancellation
// ticket is printed at the station).
func (o *Order) Void(lineID, voidID string, qty int, reason, by string) (Line, error) {
	if err := o.open(); err != nil {
		return Line{}, err
	}
	i, err := o.line(lineID)
	if err != nil {
		return Line{}, err
	}
	l := o.Lines[i]
	switch {
	case l.Status == Pending:
		return Line{}, ErrLineNotSent
	case l.Status == Voided:
		return Line{}, fmt.Errorf("%w: satır zaten iptal", ErrInvalidLine)
	case strings.TrimSpace(reason) == "":
		return Line{}, ErrVoidReason
	case qty < 1 || qty > l.OpenQty():
		return Line{}, fmt.Errorf("%w: iptal edilecek adet 1 ile %d arasında olmalı", ErrQtyTooLarge, l.OpenQty())
	}
	reason = strings.Join(strings.Fields(reason), " ")
	if qty == l.Qty {
		o.Lines[i].Status, o.Lines[i].VoidReason, o.Lines[i].VoidedBy = Voided, reason, by
		o.touch()
		return o.Lines[i], nil
	}
	if !validLineID(voidID) {
		return Line{}, fmt.Errorf("%w: iptal satırının kimliği %q", ErrInvalidLine, voidID)
	}
	o.Lines[i].Qty -= qty
	v := l
	v.ID, v.Qty, v.Paid, v.Held, v.Status, v.VoidReason, v.VoidedBy = voidID, qty, 0, 0, Voided, reason, by
	o.Lines = slices.Insert(o.Lines, i+1, v)
	o.touch()
	return v, nil
}

// RequestBill records that the bill was requested (the check printout is printed).
func (o *Order) RequestBill(at time.Time) error {
	if err := o.open(); err != nil {
		return err
	}
	o.BillAt = at
	o.touch()
	return nil
}

// Parts checks the lines to pay and their quantities; if parts is empty everything
// that is open is paid. All lines must have been sent to the kitchen.
func (o *Order) Parts(parts []Part) ([]Part, error) {
	if err := o.open(); err != nil {
		return nil, err
	}
	if o.Payment != nil {
		return nil, ErrPaymentPending
	}
	if len(parts) == 0 {
		for _, l := range o.Lines {
			if l.OpenQty() > 0 {
				parts = append(parts, Part{LineID: l.ID, Qty: l.OpenQty()})
			}
		}
	}
	var out []Part
	seen := map[string]bool{}
	for _, p := range parts {
		if p.Qty == 0 {
			continue
		}
		i, err := o.line(p.LineID)
		if err != nil {
			return nil, err
		}
		l := o.Lines[i]
		if seen[l.ID] {
			return nil, fmt.Errorf("%w: aynı satır iki kez", ErrInvalidLine)
		}
		seen[l.ID] = true
		if l.Status == Pending {
			return nil, ErrNotSent
		}
		if p.Qty < 0 || p.Qty > l.OpenQty() {
			return nil, fmt.Errorf("%w: %s için en çok %d", ErrQtyTooLarge, l.Label(), l.OpenQty())
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, ErrNothingToPay
	}
	return out, nil
}

// Hold ties the selected quantities to the sale: they are in payment until the sale
// is paid or cancelled. parts must have gone through Parts.
func (o *Order) Hold(saleID string, parts []Part, by string, at time.Time) error {
	if o.Payment != nil {
		return ErrPaymentPending
	}
	for _, p := range parts {
		i, err := o.line(p.LineID)
		if err != nil {
			return err
		}
		o.Lines[i].Held += p.Qty
	}
	o.Payment = &Payment{SaleID: saleID, Parts: parts, By: by, At: at}
	o.touch()
	return nil
}

// Settle applies the result of the payment in progress: if paid the quantities become paid,
// otherwise (the sale is cancelled) they return to the check. If all lines are paid or
// cancelled the check closes. It returns true if it closed.
func (o *Order) Settle(paid bool, at time.Time) bool {
	if o.Payment == nil {
		return o.Closed
	}
	for _, p := range o.Payment.Parts {
		if i, err := o.line(p.LineID); err == nil {
			o.Lines[i].Held -= p.Qty
			if paid {
				o.Lines[i].Paid += p.Qty
			}
		}
	}
	o.Payment = nil
	o.touch()
	if paid && o.settled() {
		o.Closed, o.ClosedAt = true, at
	}
	return o.Closed
}

// settled: no open quantity is left (at least one line was paid or cancelled).
func (o *Order) settled() bool {
	if len(o.Lines) == 0 {
		return false
	}
	for _, l := range o.Lines {
		if l.Status == Pending || l.OpenQty() > 0 || l.Held > 0 {
			return false
		}
	}
	return true
}

// CloseEmpty closes a check with no lines (a table opened by mistake).
func (o *Order) CloseEmpty(at time.Time) error {
	if err := o.open(); err != nil {
		return err
	}
	if len(o.Lines) > 0 {
		return fmt.Errorf("%w: adisyonda satır var", ErrInvalidLine)
	}
	o.Closed, o.ClosedAt = true, at
	o.touch()
	return nil
}

// MoveTo moves the check to another (empty) table; the caller checks that the
// table is empty.
func (o *Order) MoveTo(tableID string) error {
	if err := o.open(); err != nil {
		return err
	}
	if o.Payment != nil {
		return ErrPaymentPending
	}
	o.TableID = tableID
	o.touch()
	return nil
}

// Absorb merges another table's check into this one: its lines pass here,
// the other check closes.
func (o *Order) Absorb(other *Order, at time.Time) error {
	if err := o.open(); err != nil {
		return err
	}
	if err := other.open(); err != nil {
		return err
	}
	if o.Payment != nil || other.Payment != nil {
		return ErrPaymentPending
	}
	for _, l := range other.Lines {
		if slices.ContainsFunc(o.Lines, func(x Line) bool { return x.ID == l.ID }) {
			return fmt.Errorf("%w: aynı satır iki adisyonda", ErrInvalidLine)
		}
	}
	o.Lines = append(o.Lines, other.Lines...)
	other.Lines = nil
	other.Closed, other.ClosedAt = true, at
	o.touch()
	other.touch()
	return nil
}
