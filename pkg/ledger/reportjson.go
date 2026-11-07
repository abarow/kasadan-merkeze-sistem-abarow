// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package ledger

// The report's JSON form: the store server's daily report and headquarters' period summary
// return the same pieces (payment, VAT, group, day, hour, product) with the same field names;
// the screens use shared report components. An empty list goes as [] (not
// null).

// PaymentJSON is the total of a payment type.
type PaymentJSON struct {
	Method      string `json:"method"`
	AmountKurus int64  `json:"amount_kurus"`
}

// TaxJSON is the total of a VAT rate.
type TaxJSON struct {
	RateBp     int64 `json:"rate_bp"`
	GrossKurus int64 `json:"gross_kurus"`
	TaxKurus   int64 `json:"tax_kurus"`
}

// GroupJSON is the total of a store, register or cashier; Name is the name the caller
// gave (the id if none).
type GroupJSON struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	ReceiptCount     int64  `json:"receipt_count"`
	TotalKurus       int64  `json:"total_kurus"`
	VoidedCount      int64  `json:"voided_count"`
	VoidedTotalKurus int64  `json:"voided_total_kurus"`
}

// DayJSON is the total of a day.
type DayJSON struct {
	Date         string `json:"date"`
	ReceiptCount int64  `json:"receipt_count"`
	TotalKurus   int64  `json:"total_kurus"`
}

// HourJSON is the total of an hour of the day.
type HourJSON struct {
	Hour         int   `json:"hour"`
	ReceiptCount int64 `json:"receipt_count"`
	TotalKurus   int64 `json:"total_kurus"`
}

// ProductJSON is the quantity sold and the amount of a product.
type ProductJSON struct {
	ProductID  string `json:"product_id"`
	Name       string `json:"name"`
	QtyMilli   int64  `json:"qty_milli"`
	TotalKurus int64  `json:"total_kurus"`
}

// PaymentsJSON is the report's payments.
func (r Report) PaymentsJSON() []PaymentJSON {
	out := make([]PaymentJSON, 0, len(r.Payments))
	for _, p := range r.Payments {
		out = append(out, PaymentJSON{Method: p.Method, AmountKurus: int64(p.Amount)})
	}
	return out
}

// TaxesJSON is the report's VAT breakdown.
func (r Report) TaxesJSON() []TaxJSON {
	out := make([]TaxJSON, 0, len(r.Taxes))
	for _, t := range r.Taxes {
		out = append(out, TaxJSON{RateBp: int64(t.Rate), GrossKurus: int64(t.Gross), TaxKurus: int64(t.Tax)})
	}
	return out
}

// DaysJSON is the report's days.
func (r Report) DaysJSON() []DayJSON {
	out := make([]DayJSON, 0, len(r.Days))
	for _, d := range r.Days {
		out = append(out, DayJSON{Date: d.Date, ReceiptCount: d.Receipts, TotalKurus: int64(d.Total)})
	}
	return out
}

// HoursJSON, raporun saatleridir.
func (r Report) HoursJSON() []HourJSON {
	out := make([]HourJSON, 0, len(r.Hours))
	for _, h := range r.Hours {
		out = append(out, HourJSON{Hour: h.Hour, ReceiptCount: h.Receipts, TotalKurus: int64(h.Total)})
	}
	return out
}

// ProductsJSON is the report's products.
func (r Report) ProductsJSON() []ProductJSON {
	out := make([]ProductJSON, 0, len(r.Products))
	for _, p := range r.Products {
		out = append(out, ProductJSON{ProductID: p.ProductID, Name: p.Name, QtyMilli: int64(p.Qty), TotalKurus: int64(p.Total)})
	}
	return out
}

// GroupsJSON is the JSON of the groups (store, register or cashier); name gives each
// group's display name (the id is shown if it returns empty).
func GroupsJSON(groups []GroupTotal, name func(GroupTotal) string) []GroupJSON {
	out := make([]GroupJSON, 0, len(groups))
	for _, g := range groups {
		n := ""
		if name != nil {
			n = name(g)
		}
		if n == "" {
			n = g.ID
		}
		out = append(out, GroupJSON{ID: g.ID, Name: n, ReceiptCount: g.Receipts, TotalKurus: int64(g.Total),
			VoidedCount: g.Voided, VoidedTotalKurus: int64(g.VoidedTotal)})
	}
	return out
}
