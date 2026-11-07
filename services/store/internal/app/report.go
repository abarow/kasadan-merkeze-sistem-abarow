// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/report"
)

// ReportSource is the store server adapter of report.Source: the figures come from the store's
// report database, the cashier names from the cashiers managed at the store server.
type ReportSource struct {
	Reports Reports
	// Names is the mapping from cashier ids to names; if nil there are no names
	// (cashier management is off).
	Names func(ctx context.Context) (map[string]string, error)
}

var _ report.Source = ReportSource{}

// Report returns the period's report (there is no store filter: the database
// belongs to a single store).
func (s ReportSource) Report(ctx context.Context, f report.Filter) (report.Result, error) {
	r, err := s.Reports.Report(ctx, ledger.Filter{Start: f.Start, End: f.End, Location: f.Location, MaxProducts: f.MaxProducts})
	if err != nil {
		return report.Result{}, err
	}
	res := report.Result{Report: r}
	if s.Names != nil {
		// If the name cannot be read the report is still shown; the id of a cashier that is not there
		// (defined only in the file at the register) is shown.
		if names, err := s.Names(ctx); err == nil {
			res.CashierNames = map[[2]string]string{}
			for _, c := range r.Cashiers {
				if n := names[c.ID]; n != "" {
					res.CashierNames[[2]string{c.StoreID, c.ID}] = n
				}
			}
		}
	}
	return res, nil
}

// Summaries connects the service's report to the shared report core (pkg/report):
// a single store, the registers from registers.json, the cashier names from cashiers (nil
// means no names).
func (s *Service) Summaries(cashiers *Cashiers) *report.Service {
	src := ReportSource{Reports: s.reports}
	if cashiers != nil {
		src.Names = cashiers.Names
	}
	return report.New(src, s.loc, report.SingleStore(), report.RegisterNames(s.RegisterName))
}
