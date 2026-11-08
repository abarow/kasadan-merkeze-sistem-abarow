// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package ledger

import (
	"cmp"
	"slices"
	"time"

	"abarow.com/pkg/money"
)

// Filter is the report's scope.
type Filter struct {
	// Start and End are the period's bounds ([Start, End)).
	Start, End time.Time
	// If Stores is nil it is all the stores; if empty, none.
	Stores []string
	// Location is the time zone in which the hours and days are computed (store
	// time).
	Location *time.Location
	// MaxProducts is the largest number of products in the report; all if 0.
	MaxProducts int
}

// Report is the period's sales report: the store server's day report (one store,
// one day) and headquarters' summary (selected stores, a period) come from the same
// calculation. It has no fiscal value; the fiscal reports (Z) are taken at the registers.
type Report struct {
	Receipts int64
	// Total is the VAT-included sales total; TaxTotal is the VAT in it.
	Total, TaxTotal money.Money
	Voided          int64
	VoidedTotal     money.Money
	// Payments: cash (change deducted), card, then the others.
	Payments []PaymentTotal
	// Taxes are in ascending order of VAT rate.
	Taxes []Tax
	// Stores, Registers and Cashiers are sorted by store, then by id.
	// The cashier id of a sale with no cashier is empty.
	Stores, Registers, Cashiers []GroupTotal
	// Hours are the 24 hours of the day (in store time; all the days of the period
	// together).
	Hours []HourTotal
	// Days are all the days of the period (days with no sales come with zero).
	Days []DayTotal
	// Products are sorted by amount descending, then by name and id.
	Products []ProductTotal
}

// PaymentTotal is the total of a payment type.
type PaymentTotal struct {
	Method string
	Amount money.Money
}

// GroupTotal is the total of a store, register or cashier. The register and
// cashier ids are unique within a store; in the store's own total ID
// is the store id.
type GroupTotal struct {
	StoreID, ID string
	Receipts    int64
	Total       money.Money
	Voided      int64
	VoidedTotal money.Money
}

// HourTotal is the total of an hour (Hour 0–23).
type HourTotal struct {
	Hour     int
	Receipts int64
	Total    money.Money
}

// DayTotal is the total of a day (Date 2006-01-02).
type DayTotal struct {
	Date     string
	Receipts int64
	Total    money.Money
}

// ProductTotal is the quantity sold and the amount of a product. Lines with no id
// (e.g. entered by hand) are grouped by their name.
type ProductTotal struct {
	ProductID, Name string
	Qty             money.Qty
	Total           money.Money
}

// Bucket is the time slice of the sales: hours and days are summed from
// 15-minute slices. Since the differences between all time zones are multiples of 15 minutes
// each slice falls in a single hour in store time.
const Bucket = 15 * time.Minute

// Builder builds the report from aggregated pieces: the database groups the sales
// (SQL), the rules are here in one place (change from cash, ordering, empty
// days). Its zero value cannot be used; it is built with NewBuilder.
type Builder struct {
	f        Filter
	r        Report
	change   money.Money
	stores   map[string]*GroupTotal
	regs     map[[2]string]*GroupTotal
	cashiers map[[2]string]*GroupTotal
	days     map[string]*DayTotal
	payments map[string]money.Money
	taxes    map[money.Rate]*Tax
	products map[string]*ProductTotal
}

// NewBuilder builds an empty report for f's period.
func NewBuilder(f Filter) *Builder {
	if f.Location == nil {
		f.Location = time.UTC
	}
	b := &Builder{f: f, stores: map[string]*GroupTotal{}, regs: map[[2]string]*GroupTotal{},
		cashiers: map[[2]string]*GroupTotal{}, days: map[string]*DayTotal{}, payments: map[string]money.Money{},
		taxes: map[money.Rate]*Tax{}, products: map[string]*ProductTotal{}}
	b.r.Hours = make([]HourTotal, 24)
	for h := range b.r.Hours {
		b.r.Hours[h].Hour = h
	}
	for d := f.Start.In(f.Location); d.Before(f.End); d = d.AddDate(0, 0, 1) {
		date := d.Format(time.DateOnly)
		b.r.Days = append(b.r.Days, DayTotal{Date: date})
	}
	for i := range b.r.Days {
		b.days[b.r.Days[i].Date] = &b.r.Days[i]
	}
	return b
}

func group(m map[[2]string]*GroupTotal, store, id string) *GroupTotal {
	g := m[[2]string{store, id}]
	if g == nil {
		g = &GroupTotal{StoreID: store, ID: id}
		m[[2]string{store, id}] = g
	}
	return g
}

func (b *Builder) store(id string) *GroupTotal {
	g := b.stores[id]
	if g == nil {
		g = &GroupTotal{StoreID: id, ID: id}
		b.stores[id] = g
	}
	return g
}

// AddSales adds the sales of a store, register, cashier and time slice:
// the receipt count, the VAT-included total, the VAT and the change. at is a moment
// inside the slice.
func (b *Builder) AddSales(store, register, cashier string, at time.Time, receipts int64, total, tax, change money.Money) {
	b.AddSalesTotals(store, register, cashier, receipts, total, tax, change)
	b.AddSalesSlice(at, receipts, total)
}

// AddSalesTotals adds the sales of a store, register and cashier to the totals and the
// group tables, without their time.
func (b *Builder) AddSalesTotals(store, register, cashier string, receipts int64, total, tax, change money.Money) {
	b.r.Receipts += receipts
	b.r.Total += total
	b.r.TaxTotal += tax
	b.change += change
	for _, g := range []*GroupTotal{b.store(store), group(b.regs, store, register), group(b.cashiers, store, cashier)} {
		g.Receipts += receipts
		g.Total += total
	}
}

// AddSalesSlice adds the sales of a time slice to the hours and the days.
func (b *Builder) AddSalesSlice(at time.Time, receipts int64, total money.Money) {
	local := at.In(b.f.Location)
	h := &b.r.Hours[local.Hour()]
	h.Receipts += receipts
	h.Total += total
	if d := b.days[local.Format(time.DateOnly)]; d != nil {
		d.Receipts += receipts
		d.Total += total
	}
}

// AddVoids adds the cancellations of a store, register and cashier.
func (b *Builder) AddVoids(store, register, cashier string, voided int64, total money.Money) {
	b.r.Voided += voided
	b.r.VoidedTotal += total
	for _, g := range []*GroupTotal{b.store(store), group(b.regs, store, register), group(b.cashiers, store, cashier)} {
		g.Voided += voided
		g.VoidedTotal += total
	}
}

// AddPayment adds the collection of a payment type (the money taken in cash;
// the change comes with AddSales).
func (b *Builder) AddPayment(method string, amount money.Money) { b.payments[method] += amount }

// AddTax adds the total of a VAT rate.
func (b *Builder) AddTax(t Tax) {
	x := b.taxes[t.Rate]
	if x == nil {
		x = &Tax{Rate: t.Rate}
		b.taxes[t.Rate] = x
	}
	x.Gross += t.Gross
	x.Tax += t.Tax
}

// AddProduct adds the sale of a product. If the same product is added more than
// once they are summed; the name is the alphabetically greatest of the names (the same as
// max(name) in SQL).
func (b *Builder) AddProduct(p ProductTotal) {
	key := ProductKey(p.ProductID, p.Name)
	x := b.products[key]
	if x == nil {
		x = &ProductTotal{ProductID: p.ProductID, Name: p.Name}
		b.products[key] = x
	}
	x.Name = max(x.Name, p.Name)
	x.Qty += p.Qty
	x.Total += p.Total
}

// ProductKey is the product's key in the report: its id, or its name if none.
func ProductKey(productID, name string) string {
	if productID == "" {
		return "ad:" + name
	}
	return productID
}

// Report returns the report from the aggregated pieces.
func (b *Builder) Report() Report {
	r := b.r
	// Change is given from cash: the same rule as the X/Z report at the register.
	if r.Receipts > 0 {
		r.Payments = []PaymentTotal{{Method: Cash, Amount: b.payments[Cash] - b.change}, {Method: Card, Amount: b.payments[Card]}}
	}
	var others []string
	for m := range b.payments {
		if m != Cash && m != Card {
			others = append(others, m)
		}
	}
	slices.Sort(others)
	for _, m := range others {
		r.Payments = append(r.Payments, PaymentTotal{Method: m, Amount: b.payments[m]})
	}
	for _, t := range b.taxes {
		r.Taxes = append(r.Taxes, *t)
	}
	slices.SortFunc(r.Taxes, func(a, b Tax) int { return cmp.Compare(a.Rate, b.Rate) })
	for _, g := range b.stores {
		r.Stores = append(r.Stores, *g)
	}
	sortGroups(r.Stores)
	r.Registers = sortedGroups(b.regs)
	r.Cashiers = sortedGroups(b.cashiers)
	for _, p := range b.products {
		r.Products = append(r.Products, *p)
	}
	slices.SortFunc(r.Products, func(a, b ProductTotal) int {
		if c := cmp.Compare(b.Total, a.Total); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return cmp.Compare(a.ProductID, b.ProductID)
	})
	if b.f.MaxProducts > 0 && len(r.Products) > b.f.MaxProducts {
		r.Products = r.Products[:b.f.MaxProducts]
	}
	return r
}

func sortedGroups(m map[[2]string]*GroupTotal) []GroupTotal {
	out := make([]GroupTotal, 0, len(m))
	for _, g := range m {
		out = append(out, *g)
	}
	sortGroups(out)
	return out
}

func sortGroups(gs []GroupTotal) {
	slices.SortFunc(gs, func(a, b GroupTotal) int {
		if c := cmp.Compare(a.StoreID, b.StoreID); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
}

// Build builds the report in memory from the sales and cancellations (without a
// database). The caller ensures that the sales and cancellations are in the period and that the store
// is selected. The report in the database (ledgerdb) gives the same result; the tests
// compare the two.
func Build(f Filter, sales []Sale, voids []Void) Report {
	b := NewBuilder(f)
	for _, s := range sales {
		b.AddSales(s.StoreID, s.RegisterID, s.CashierID, s.PaidAt, 1, s.Total, s.TaxTotal, s.Change)
		for _, p := range s.Payments {
			b.AddPayment(p.Method, p.Amount)
		}
		for _, t := range s.Taxes {
			b.AddTax(t)
		}
		for _, l := range s.Lines {
			b.AddProduct(ProductTotal{ProductID: l.ProductID, Name: l.Name, Qty: l.Qty, Total: l.Total()})
		}
	}
	for _, v := range voids {
		b.AddVoids(v.StoreID, v.RegisterID, v.CashierID, 1, v.Total)
	}
	return b.Report()
}
