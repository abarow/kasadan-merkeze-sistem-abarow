// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"fmt"
	"strings"
	"time"

	"abarow.com/pkg/layout"
	"abarow.com/pkg/menu"
	"abarow.com/pkg/money"
)

// StationName is the name of the station shown on the ticket and on the screen.
func StationName(s string) string {
	if s == menu.Bar {
		return "Bar"
	}
	return "Mutfak"
}

// KitchenTicket is the station's order ticket (a cancellation ticket if void): the table,
// the waiter, the time and the lines (quantity, name, options, note). The lines fit cols
// columns: a long name goes to the next line word by word (pkg/layout).
func KitchenTicket(cols int, station, table, by string, at time.Time, lines []Line, void bool) (string, []string) {
	name := StationName(station)
	title := fmt.Sprintf("%s: %s", name, table)
	var doc []string
	if void {
		title = "İPTAL " + title
		doc = append(doc, layout.Center("*** İPTAL ***", cols)...)
	}
	doc = append(doc, layout.Center(strings.ToUpper(name), cols)...)
	doc = append(doc, "")
	doc = append(doc, layout.Words("Masa: "+table, cols)...)
	doc = append(doc, layout.Words("Garson: "+by, cols)...)
	doc = append(doc, "Saat: "+at.Format("15:04"), layout.Rule(cols))
	for _, l := range lines {
		doc = append(doc, layout.Words(fmt.Sprintf("%d x %s", l.Qty, l.Name), cols)...)
		for _, o := range l.Options {
			doc = append(doc, layout.Indent("  - ", o.Name, cols)...)
		}
		if l.Note != "" {
			doc = append(doc, layout.Indent("  NOT: ", l.Note, cols)...)
		}
		if void && l.VoidReason != "" {
			doc = append(doc, layout.Indent("  NEDEN: ", l.VoidReason, cols)...)
		}
	}
	doc = append(doc, layout.Rule(cols))
	return title, doc
}

// Warning says that the bill printed before payment is not a fiscal document.
const NotFiscal = "MALİ DEĞERİ YOKTUR"

// BillDocument is the check printout (the bill): the table, the lines and their amounts,
// the total; if there are paid lines the paid and the remaining amounts. It has no fiscal value (it says
// so at the top and the bottom).
func BillDocument(cols int, table, by string, at time.Time, o *Order) (string, []string) {
	doc := layout.Center(NotFiscal, cols)
	doc = append(doc, layout.Center("ADİSYON", cols)...)
	doc = append(doc, "")
	doc = append(doc, layout.Words("Masa: "+table, cols)...)
	doc = append(doc, layout.Words("Garson: "+by, cols)...)
	doc = append(doc, "Tarih: "+at.Format("02.01.2006 15:04"), layout.Rule(cols))
	var paid money.Money
	for _, l := range o.Lines {
		if l.Status == Voided {
			continue
		}
		doc = append(doc, layout.Pair(fmt.Sprintf("%d x %s", l.Qty, l.Label()), l.Total().String(), cols)...)
		if l.Qty > 1 {
			doc = append(doc, layout.Indent("  ", fmt.Sprintf("birim %s", l.UnitPrice), cols)...)
		}
		paid += l.UnitPrice * money.Money(l.Paid)
	}
	doc = append(doc, layout.Rule(cols))
	doc = append(doc, layout.Pair("TOPLAM", o.Total().String(), cols)...)
	if paid > 0 {
		doc = append(doc, layout.Pair("ÖDENEN", paid.String(), cols)...)
		doc = append(doc, layout.Pair("KALAN", (o.Total()-paid).String(), cols)...)
	}
	doc = append(doc, "")
	doc = append(doc, layout.Center(NotFiscal, cols)...)
	return "Adisyon: " + table, doc
}
