// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package httpapi is the HTTP driving adapter of the reporting service: the JSON API
// and the screen files used by the headquarters screen.
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
	"abarow.com/services/headquarters/reporting/internal/app"
	"abarow.com/services/headquarters/reporting/internal/domain"
)

// Option gives New an optional dependency.
type Option func(*options)

type options struct {
	updateServer, updateAgent *url.URL
	updateSecret              string
}

// WithUpdate connects the update endpoints if update is installed: server is the registration
// server's admin API (/api/update/registrations: list the pending units,
// approve, reject, revoke; administrator only), agent is this headquarters' own update
// agent (/api/update/status: the status and the approval code). If either is nil
// those endpoints answer "not installed".
func WithUpdate(server, agent *url.URL) Option {
	return func(o *options) { o.updateServer, o.updateAgent = server, agent }
}

// WithUpdateSecret is the shared secret key added to the approval requests passed on to the
// agent (ABAROW_UPDATE_APPROVE_SECRET). Only the administrator approves, postpones or skips
// the new version; everyone in the session sees the status.
func WithUpdateSecret(secret string) Option {
	return func(o *options) { o.updateSecret = secret }
}

// New returns a handler that serves the API and the screen files in webDir (ui/apps/headquarters).
// The reports ask for a sign-in and are filtered by the stores the user sees; user management
// asks for an administrator. The screen files are open since there is no data in them. If catalog is
// given the /api/catalog/ requests are forwarded to catalog management (catalog-admin), if stock
// is given the /api/stock requests to headquarters' inventory: the session is checked here, the person
// (username, name, role, stores) goes in headers (pkg/login); that service checks the role
// and the store permission.
func New(svc *app.Service, rep *report.Service, auth *app.Auth, corp *oidc.Corporate, webDir string, catalog, stock *url.URL, opts ...Option) http.Handler {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	h := &handler{svc: svc}
	a := newAuthHandler(auth, corp)
	u := &usersHandler{auth: auth}
	mux := http.NewServeMux()
	report.Backend(mux, a.Web, catalog, "katalog yönetimi kurulu değil", "/api/catalog/")
	report.Backend(mux, a.Web, stock, "stok modülü kurulu değil", "/api/stock", "/api/stock/")
	report.Backend(mux, a.Web, o.updateServer, "güncelleme kurulu değil", "/api/update/registrations", "/api/update/registrations/")
	report.Approver(mux, a.Web, o.updateAgent, "güncelleme kurulu değil", func(p login.Person) bool { return p.Role == string(domain.RoleAdmin) }, o.updateSecret,
		"/api/update/status", "/api/update/apply", "/api/update/skip", "/api/update/snooze")
	a.Handle(mux)
	corp.Handle(mux, a.Web)
	mux.HandleFunc("POST /api/auth/password", a.Require(a.password))
	mux.HandleFunc("GET /api/stores", a.Require(h.stores))
	mux.HandleFunc("GET /api/reports/summary", a.Require(rep.Handler()))
	mux.HandleFunc("GET /api/reports/store-prices", a.Require(h.storePrices))
	mux.HandleFunc("GET /api/users", a.Require(u.list))
	mux.HandleFunc("POST /api/users", a.Require(u.add))
	mux.HandleFunc("GET /api/users/{username}", a.Require(u.get))
	mux.HandleFunc("POST /api/users/{username}", a.Require(u.update))
	mux.HandleFunc("POST /api/users/{username}/password", a.Require(u.setPassword))
	mux.HandleFunc("POST /api/users/{username}/disabled", a.Require(u.setDisabled))
	mux.HandleFunc("POST /api/users/{username}/identity/unlink", a.Require(u.unlinkIdentity))
	mux.Handle("/", service.SPA(webDir, "headquarters"))
	return report.Secure(mux)
}

type handler struct {
	svc *app.Service
}

type storeJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Link: connected, disconnected, never or unknown (ingest is not writing the
	// state).
	Link          string     `json:"link"`
	LastSeen      *time.Time `json:"last_seen,omitempty"`
	PendingEvents int64      `json:"pending_events"`
	Error         string     `json:"error,omitempty"`
	CheckedAt     time.Time  `json:"checked_at"`
	LastEventAt   *time.Time `json:"last_event_at,omitempty"`
	TodayReceipts int64      `json:"today_receipt_count"`
	TodayTotal    int64      `json:"today_total_kurus"`
	// MissingEvents: the gaps in the sequence numbers among the events the registers numbered and sent;
	// records that never reached headquarters.
	MissingEvents []gapJSON `json:"missing_events"`
}

// gapJSON is a gap in the sequence numbers of a register and its sender (sales, staff).
type gapJSON struct {
	Register string `json:"register"`
	Producer string `json:"producer"`
	From     int64  `json:"from"`
	To       int64  `json:"to"`
}

func (h *handler) stores(w http.ResponseWriter, r *http.Request) {
	stores, err := h.svc.Stores(r.Context(), me(r))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "mağazaların durumu okunamadı: "+err.Error())
		return
	}
	out := struct {
		Today  string      `json:"today"`
		Stores []storeJSON `json:"stores"`
	}{Today: h.svc.Today(), Stores: []storeJSON{}}
	for _, s := range stores {
		j := storeJSON{
			ID: s.ID, Name: s.Name, Link: string(s.Link), LastSeen: timePtr(s.LastSeen), PendingEvents: s.Pending, Error: s.Error,
			CheckedAt: s.CheckedAt, LastEventAt: timePtr(s.LastEventAt), TodayReceipts: s.TodayReceipts, TodayTotal: int64(s.TodayTotal),
			MissingEvents: []gapJSON{},
		}
		for _, g := range s.MissingEvents {
			j.MissingEvents = append(j.MissingEvents, gapJSON{Register: g.Register, Producer: g.Producer, From: g.From, To: g.To})
		}
		out.Stores = append(out.Stores, j)
	}
	writeJSON(w, http.StatusOK, out)
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

var (
	writeJSON  = httpjson.Write
	writeError = httpjson.Error
)

type storePriceJSON struct {
	StoreID     string `json:"store_id"`
	StoreName   string `json:"store_name"`
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	// HeadquartersPriceKurus is the current headquarters price; StorePriceKurus is the current store
	// price (empty if the headquarters price is in effect at the store now).
	HeadquartersPriceKurus int64             `json:"headquarters_price_kurus"`
	StorePriceKurus        *int64            `json:"store_price_kurus,omitempty"`
	DifferenceKurus        *int64            `json:"difference_kurus,omitempty"`
	Upcoming               []storePricePoint `json:"upcoming"`
	UpdatedBy              string            `json:"updated_by"`
	UpdatedAt              time.Time         `json:"updated_at"`
}

type storePricePoint struct {
	UnitPriceKurus int64     `json:"unit_price_kurus"`
	From           time.Time `json:"from"`
	Clear          bool      `json:"clear,omitempty"`
}

// storePrices are the products priced differently from the headquarters price at the stores:
// ?product=<code> (optional).
func (h *handler) storePrices(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.StorePrices(r.Context(), r.URL.Query().Get("product"), me(r))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "mağaza fiyatları okunamadı: "+err.Error())
		return
	}
	out := struct {
		Items []storePriceJSON `json:"items"`
	}{Items: []storePriceJSON{}}
	for _, e := range list {
		item := storePriceJSON{StoreID: e.StoreID, StoreName: e.StoreName, ProductID: e.ProductID, ProductName: e.ProductName,
			HeadquartersPriceKurus: int64(e.Headquarters), Upcoming: []storePricePoint{}, UpdatedBy: e.UpdatedBy, UpdatedAt: e.UpdatedAt}
		if e.HasStore {
			store, diff := int64(e.Store), int64(e.Difference())
			item.StorePriceKurus, item.DifferenceKurus = &store, &diff
		}
		for _, p := range e.Upcoming {
			item.Upcoming = append(item.Upcoming, storePricePoint{UnitPriceKurus: int64(p.Amount), From: p.From, Clear: p.Clear})
		}
		out.Items = append(out.Items, item)
	}
	writeJSON(w, http.StatusOK, out)
}
