// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package report

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"

	"abarow.com/pkg/httpjson"
	"abarow.com/pkg/ledger"
	"abarow.com/pkg/login"
)

// CashierJSON is a cashier's period total.
type CashierJSON struct {
	StoreID   string `json:"store_id"`
	CashierID string `json:"cashier_id"`
	// CashierName is the name of the cashier managed at the store server (empty if none).
	CashierName      string `json:"cashier_name,omitempty"`
	ReceiptCount     int64  `json:"receipt_count"`
	TotalKurus       int64  `json:"total_kurus"`
	VoidedCount      int64  `json:"voided_count"`
	VoidedTotalKurus int64  `json:"voided_total_kurus"`
}

// SummaryJSON is the form of the summary that goes to the screen (GET /api/reports/summary).
type SummaryJSON struct {
	From             string               `json:"from"`
	To               string               `json:"to"`
	Store            string               `json:"store"`
	ReceiptCount     int64                `json:"receipt_count"`
	TotalKurus       int64                `json:"total_kurus"`
	TaxTotalKurus    int64                `json:"tax_total_kurus"`
	VoidedCount      int64                `json:"voided_count"`
	VoidedTotalKurus int64                `json:"voided_total_kurus"`
	Payments         []ledger.PaymentJSON `json:"payments"`
	Taxes            []ledger.TaxJSON     `json:"taxes"`
	// Stores is the breakdown by store (a single store at the store server).
	Stores []ledger.GroupJSON `json:"stores"`
	// Registers is the breakdown by register; it is filled only in the service that names
	// the registers (the store server).
	Registers []ledger.GroupJSON   `json:"registers"`
	Cashiers  []CashierJSON        `json:"cashiers"`
	Days      []ledger.DayJSON     `json:"days"`
	Hours     []ledger.HourJSON    `json:"hours"`
	Products  []ledger.ProductJSON `json:"products"`
}

// JSON is the form of the summary that goes to the screen.
func (s Summary) JSON() SummaryJSON {
	out := SummaryJSON{
		From: s.From, To: s.To, Store: s.StoreID, ReceiptCount: s.Receipts, TotalKurus: int64(s.Total),
		TaxTotalKurus: int64(s.TaxTotal), VoidedCount: s.Voided, VoidedTotalKurus: int64(s.VoidedTotal),
		Payments: s.PaymentsJSON(), Taxes: s.TaxesJSON(), Days: s.DaysJSON(), Hours: s.HoursJSON(), Products: s.ProductsJSON(),
		Stores:    ledger.GroupsJSON(s.Report.Stores, func(g ledger.GroupTotal) string { return s.StoreName(g.ID) }),
		Registers: []ledger.GroupJSON{},
		Cashiers:  []CashierJSON{},
	}
	if s.registerName != nil {
		out.Registers = ledger.GroupsJSON(s.Report.Registers, func(g ledger.GroupTotal) string { return s.registerName(g.ID) })
	}
	for _, c := range s.Report.Cashiers {
		out.Cashiers = append(out.Cashiers, CashierJSON{
			StoreID: c.StoreID, CashierID: c.ID, CashierName: s.CashierName(c.StoreID, c.ID), ReceiptCount: c.Receipts,
			TotalKurus: int64(c.Total), VoidedCount: c.Voided, VoidedTotalKurus: int64(c.VoidedTotal),
		})
	}
	return out
}

// Handler is the handler of GET /api/reports/summary:
// ?from=2026-10-01&to=2026-10-31&store=store-1 (all optional; today if from is
// absent; all the stores the person sees if store is absent). login.Web.Require
// puts the person.
func (s *Service) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		sum, err := s.Summary(r.Context(), Query{From: q.Get("from"), To: q.Get("to"), Store: q.Get("store")}, login.FromRequest(r))
		switch {
		case errors.Is(err, ErrInvalidPeriod):
			httpjson.Error(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, ErrForbidden):
			httpjson.Error(w, http.StatusForbidden, err.Error())
		case err != nil:
			httpjson.Error(w, http.StatusServiceUnavailable, err.Error())
		default:
			httpjson.Write(w, http.StatusOK, sum.JSON())
		}
	}
}

// Secure adds the browser security headers to every response: the service that serves the screen
// may be reachable from the internet (behind a reverse proxy with TLS).
func Secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// Approver passes on operations done by an authorized person, such as update approval:
// GET requests are open to everyone in the session (the status and the approval code); write
// requests come only from a person allow accepts (the administrator at headquarters,
// the manager at the store server), otherwise it is 403. The shared secret key
// (login.UpdateSecretHeader) is added to a write request; a header of the same name put by the client
// is always deleted. If target is nil it gives 404 for "not installed".
func Approver(mux *http.ServeMux, web login.Web, target *url.URL, missing string, allow func(login.Person) bool, secret string, patterns ...string) {
	var h http.HandlerFunc
	if target != nil {
		proxy := httputil.NewSingleHostReverseProxy(target)
		h = web.Forward(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Del(login.UpdateSecretHeader)
			if r.Method != http.MethodGet {
				p, _ := login.PersonFrom(r.Header)
				if !allow(p) {
					httpjson.Error(w, http.StatusForbidden, "bu işlem için yetkiniz yok")
					return
				}
				r.Header.Set(login.UpdateSecretHeader, secret)
			}
			proxy.ServeHTTP(w, r)
		}))
	} else {
		h = web.Require(func(w http.ResponseWriter, _ *http.Request) {
			httpjson.Error(w, http.StatusNotFound, missing)
		})
	}
	for _, p := range patterns {
		mux.HandleFunc(p, h)
	}
}

// Backend passes the requests in patterns to the service behind (inventory,
// catalog-admin) with the person in the session: the session is checked here, the person goes in
// headers (login.Web.Forward); that service checks the role and the store permission.
// If target is nil (the module is not installed) the requests get 404 with the
// missing message.
func Backend(mux *http.ServeMux, web login.Web, target *url.URL, missing string, patterns ...string) {
	var h http.HandlerFunc
	if target != nil {
		h = web.Forward(httputil.NewSingleHostReverseProxy(target))
	} else {
		h = web.Require(func(w http.ResponseWriter, _ *http.Request) {
			httpjson.Error(w, http.StatusNotFound, missing)
		})
	}
	for _, p := range patterns {
		mux.HandleFunc(p, h)
	}
}
