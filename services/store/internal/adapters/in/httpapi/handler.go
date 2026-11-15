// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package httpapi is the HTTP driving adapter of the report service: the JSON API and the
// screen files used by the store server screen.
package httpapi

import (
	"net/http"
	"net/url"
	"time"

	"abarow.com/pkg/httpjson"
	"abarow.com/pkg/login"
	"abarow.com/pkg/login/oidc"
	"abarow.com/pkg/report"
	"abarow.com/pkg/service"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

// New returns a handler that serves the API and the screen files in webDir (ui/apps/store).
// The reports, the registers' state, the store prices and the cashiers ask for a sign-in
// (manager PIN or store password); the screen files are open since there is no data in them.
// If prices is nil (the store server is not connected to headquarters) the price requests, if cashiers
// is nil (no store id) the cashier requests are rejected with an explanation.
// If stock is given the /api/stock requests are forwarded to the inventory service
// in the name of the person in the session; if not, "the stock module is not installed". With
// WithCatalog product and price management at the store (catalog-admin) is connected.
func New(svc *app.Service, auth *app.Auth, corp *oidc.Corporate, webDir string, prices *app.Prices, cashiers *app.Cashiers, stock *url.URL, opts ...Option) http.Handler {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	h := &handler{svc: svc}
	a := newWeb(auth, corp)
	mux := http.NewServeMux()
	a.Handle(mux)
	corp.Handle(mux, a)
	mux.HandleFunc("GET /api/registers", a.Require(h.registers))
	mux.HandleFunc("GET /api/reports/summary", a.Require(svc.Summaries(cashiers).Handler()))
	if prices != nil {
		p := &pricesHandler{prices: prices}
		mux.HandleFunc("GET /api/prices", a.Require(p.list))
		mux.HandleFunc("GET /api/prices/{id}", a.Require(p.get))
		mux.HandleFunc("POST /api/prices/{id}", a.Require(p.set))
		mux.HandleFunc("POST /api/prices/{id}/clear", a.Require(p.clear))
		mux.HandleFunc("POST /api/prices/{id}/cancel", a.Require(p.cancel))
	} else {
		mux.HandleFunc("/api/prices", a.Require(noPrices))
		mux.HandleFunc("/api/prices/", a.Require(noPrices))
	}
	if cashiers != nil {
		c := &cashiersHandler{cashiers: cashiers, auth: auth}
		mux.HandleFunc("GET /api/cashiers", a.Require(c.list))
		mux.HandleFunc("POST /api/cashiers", a.Require(c.add))
		mux.HandleFunc("GET /api/cashiers/{id}", a.Require(c.get))
		mux.HandleFunc("POST /api/cashiers/{id}", a.Require(c.update))
		mux.HandleFunc("POST /api/cashiers/{id}/pin", a.Require(c.setPIN))
		mux.HandleFunc("POST /api/cashiers/{id}/disabled", a.Require(c.setDisabled))
		mux.HandleFunc("POST /api/cashiers/{id}/identity/unlink", a.Require(c.unlinkIdentity))
	} else {
		mux.HandleFunc("/api/cashiers", a.Require(noCashiers))
		mux.HandleFunc("/api/cashiers/", a.Require(noCashiers))
	}
	report.Backend(mux, a, stock, "stok modülü kurulu değil", "/api/stock", "/api/stock/")
	catalogRoutes(mux, a, o)
	report.Approver(mux, a, o.update, "güncelleme kurulu değil", func(p login.Person) bool { return p.Role == string(domain.RoleManager) }, o.secret,
		"/api/update/status", "/api/update/apply", "/api/update/skip", "/api/update/snooze")
	mux.Handle("/", service.SPA(webDir, "store"))
	return report.Secure(mux)
}

type handler struct {
	svc *app.Service
}

// gapJSON is a gap in the sequence numbers of a sender (sales, staff).
type gapJSON struct {
	Producer string `json:"producer"`
	From     int64  `json:"from"`
	To       int64  `json:"to"`
}

type rangeJSON struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type registerJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Link: connected, disconnected or never.
	Link     string     `json:"link"`
	LastSeen *time.Time `json:"last_seen,omitempty"`
	// PendingEvents: the number of events waiting at the register while connected.
	PendingEvents uint64 `json:"pending_events"`
	Error         string `json:"error,omitempty"`
	// The sales that have come to the store server.
	ReceiptCount  int64      `json:"receipt_count"`
	LastReceiptNo int64      `json:"last_receipt_no"`
	LastSaleAt    *time.Time `json:"last_sale_at,omitempty"`
	// MissingReceipts: the gaps between the receipts that have come (missing receipts).
	MissingReceipts []rangeJSON `json:"missing_receipts"`
	// MissingEvents: the gaps in the sequence numbers among the events the register numbered and sent
	// (every kind of event; records that never came).
	MissingEvents []gapJSON `json:"missing_events"`
	// UnfinishedSales: the sales whose payment was taken but whose receipt could not be issued, waiting
	// at the register to be completed or refunded.
	UnfinishedSales []unfinishedJSON `json:"unfinished_sales"`
	// OpenShift: the open shift at the register (empty if none); LastClosedShift: the most recently
	// closed shift, with the cash difference.
	OpenShift       *shiftJSON `json:"open_shift,omitempty"`
	LastClosedShift *shiftJSON `json:"last_closed_shift,omitempty"`
}

type shiftJSON struct {
	ShiftID          int64      `json:"shift_id"`
	CashierID        string     `json:"cashier_id"`
	CashierName      string     `json:"cashier_name"`
	OpenedAt         time.Time  `json:"opened_at"`
	OpeningCashKurus int64      `json:"opening_cash_kurus"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	ClosedBy         string     `json:"closed_by,omitempty"`
	ReceiptCount     int64      `json:"receipt_count"`
	TotalKurus       int64      `json:"total_kurus"`
	ExpectedCash     int64      `json:"expected_cash_kurus"`
	CountedCash      int64      `json:"counted_cash_kurus"`
	// DifferenceKurus: counted − expected; minus is a cash shortage, plus a cash surplus.
	DifferenceKurus int64 `json:"difference_kurus"`
}

func shiftOut(sh *domain.Shift) *shiftJSON {
	if sh == nil {
		return nil
	}
	return &shiftJSON{
		ShiftID: sh.ID, CashierID: sh.CashierID, CashierName: sh.CashierName, OpenedAt: sh.OpenedAt.UTC(),
		OpeningCashKurus: int64(sh.OpeningCash), ClosedAt: timePtr(sh.ClosedAt), ClosedBy: sh.ClosedBy,
		ReceiptCount: sh.Receipts, TotalKurus: int64(sh.Total), ExpectedCash: int64(sh.ExpectedCash),
		CountedCash: int64(sh.CountedCash), DifferenceKurus: int64(sh.Difference),
	}
}

type unfinishedJSON struct {
	SaleID      string    `json:"sale_id"`
	CashierID   string    `json:"cashier_id,omitempty"`
	TotalKurus  int64     `json:"total_kurus"`
	Reason      string    `json:"reason"`
	Since       time.Time `json:"since"`
	LastAttempt time.Time `json:"last_attempt"`
}

// registers returns the registers' connection state to the store server and the receipts that have come.
func (h *handler) registers(w http.ResponseWriter, r *http.Request) {
	regs, err := h.svc.Registers(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "kasaların durumu okunamadı: "+err.Error())
		return
	}
	out := make([]registerJSON, len(regs))
	for i, st := range regs {
		out[i] = registerJSON{
			ID: st.ID, Name: st.Name, Link: string(st.Link), PendingEvents: st.Pending, Error: st.Error,
			LastSeen: timePtr(st.LastSeen), ReceiptCount: st.Activity.Receipts, LastReceiptNo: st.Activity.LastReceiptNo,
			LastSaleAt: timePtr(st.Activity.LastSaleAt), MissingReceipts: []rangeJSON{}, MissingEvents: []gapJSON{}, UnfinishedSales: []unfinishedJSON{},
			OpenShift: shiftOut(st.Activity.OpenShift), LastClosedShift: shiftOut(st.Activity.LastClosed),
		}
		for _, m := range st.Activity.Missing {
			out[i].MissingReceipts = append(out[i].MissingReceipts, rangeJSON{From: m.From, To: m.To})
		}
		for _, g := range st.Activity.MissingEvents {
			out[i].MissingEvents = append(out[i].MissingEvents, gapJSON{Producer: g.Producer, From: g.From, To: g.To})
		}
		for _, u := range st.Activity.Unfinished {
			out[i].UnfinishedSales = append(out[i].UnfinishedSales, unfinishedJSON{
				SaleID: u.SaleID, CashierID: u.CashierID, TotalKurus: int64(u.Total), Reason: u.Reason,
				Since: u.Since.UTC(), LastAttempt: u.LastAttempt.UTC(),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"registers": out})
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}

var (
	writeJSON  = httpjson.Write
	writeError = httpjson.Error
)
