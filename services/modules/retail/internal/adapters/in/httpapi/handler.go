// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package httpapi is the HTTP driving adapter of the market module: the JSON API the
// screen uses (/api/module/...) and the screen files.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"abarow.com/pkg/money"
	"abarow.com/pkg/service"
	"abarow.com/services/modules/retail/internal/app"
	"abarow.com/services/modules/retail/internal/domain"
)

// New returns a handler that serves the API and the screen files in webDir.
func New(svc *app.Service, webDir string) http.Handler {
	h := &handler{svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/module/baskets", h.newBasket)
	mux.HandleFunc("GET /api/module/baskets/current", h.currentBasket)
	mux.HandleFunc("GET /api/module/baskets/{id}", h.basket)
	mux.HandleFunc("POST /api/module/baskets/{id}/items", h.addItem)
	mux.HandleFunc("PATCH /api/module/baskets/{id}/lines/{n}", h.setQty)
	mux.HandleFunc("DELETE /api/module/baskets/{id}/lines/{n}", h.removeLine)
	mux.HandleFunc("POST /api/module/baskets/{id}/checkout", h.checkout)
	mux.HandleFunc("POST /api/module/baskets/{id}/reopen", h.reopen)
	mux.HandleFunc("GET /api/module/products", h.products)
	mux.Handle("/", service.SPA(webDir, "retail"))
	return mux
}

type handler struct {
	svc *app.Service
}

type lineJSON struct {
	ProductID      string `json:"product_id"`
	Barcode        string `json:"barcode"`
	Name           string `json:"name"`
	QtyMilli       int64  `json:"qty_milli"`
	UnitPriceKurus int64  `json:"unit_price_kurus"`
	TotalKurus     int64  `json:"total_kurus"`
	TaxRateBp      int64  `json:"tax_rate_bp"`
	Weighed        bool   `json:"weighed"`
}

type basketJSON struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	SaleID     string     `json:"sale_id,omitempty"`
	Lines      []lineJSON `json:"lines"`
	TotalKurus int64      `json:"total_kurus"`
}

type productJSON struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Barcode        string `json:"barcode"`
	Unit           string `json:"unit"`
	UnitPriceKurus int64  `json:"unit_price_kurus"`
	TaxRateBp      int64  `json:"tax_rate_bp"`
}

func toJSON(b *domain.Basket) basketJSON {
	out := basketJSON{ID: string(b.ID), Status: string(b.Status), SaleID: b.SaleID, Lines: []lineJSON{}, TotalKurus: int64(b.Total())}
	for _, l := range b.Lines {
		out.Lines = append(out.Lines, lineJSON{
			ProductID:      l.ProductID,
			Barcode:        l.Barcode,
			Name:           l.Name,
			QtyMilli:       int64(l.Qty),
			UnitPriceKurus: int64(l.UnitPrice),
			TotalKurus:     int64(l.Total()),
			TaxRateBp:      int64(l.TaxRate),
			Weighed:        l.Weighed,
		})
	}
	return out
}

func (h *handler) newBasket(w http.ResponseWriter, r *http.Request) {
	b, err := h.svc.NewBasket(r.Context(), r.Header.Get(cashierHeader))
	reply(w, http.StatusCreated, b, err)
}

// currentBasket returns the last basket the cashier can continue; 404 if none.
func (h *handler) currentBasket(w http.ResponseWriter, r *http.Request) {
	b, err := h.svc.CurrentBasket(r.Context(), r.Header.Get(cashierHeader))
	reply(w, http.StatusOK, b, err)
}

func (h *handler) basket(w http.ResponseWriter, r *http.Request) {
	b, err := h.svc.Basket(r.Context(), basketID(r))
	reply(w, http.StatusOK, b, err)
}

func (h *handler) addItem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Barcode string `json:"barcode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Barcode == "" {
		writeError(w, http.StatusBadRequest, "barkod gerekli")
		return
	}
	b, err := h.svc.AddBarcode(r.Context(), basketID(r), req.Barcode)
	reply(w, http.StatusOK, b, err)
}

func (h *handler) setQty(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	var req struct {
		QtyMilli int64 `json:"qty_milli"`
	}
	if err != nil || json.NewDecoder(r.Body).Decode(&req) != nil {
		writeError(w, http.StatusBadRequest, "satır numarası ve miktar gerekli")
		return
	}
	b, err := h.svc.SetQty(r.Context(), basketID(r), n, money.Qty(req.QtyMilli))
	reply(w, http.StatusOK, b, err)
}

func (h *handler) removeLine(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "satır numarası gerekli")
		return
	}
	b, err := h.svc.RemoveLine(r.Context(), basketID(r), n)
	reply(w, http.StatusOK, b, err)
}

// cashierHeader is the identity of the cashier in the session; the gateway sets it after it has
// verified the session (the module is not open to the outside).
const cashierHeader = "X-Abarow-Cashier-Id"

func (h *handler) checkout(w http.ResponseWriter, r *http.Request) {
	b, err := h.svc.Checkout(r.Context(), basketID(r), r.Header.Get(cashierHeader))
	reply(w, http.StatusOK, b, err)
}

func (h *handler) reopen(w http.ResponseWriter, r *http.Request) {
	b, err := h.svc.Reopen(r.Context(), basketID(r))
	reply(w, http.StatusOK, b, err)
}

func (h *handler) products(w http.ResponseWriter, r *http.Request) {
	products, err := h.svc.Search(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	out := make([]productJSON, 0, len(products))
	for _, p := range products {
		out = append(out, productJSON{ID: p.ID, Name: p.Name, Barcode: p.Barcode, Unit: string(p.Unit), UnitPriceKurus: int64(p.UnitPrice), TaxRateBp: int64(p.TaxRate)})
	}
	writeJSON(w, http.StatusOK, out)
}

func basketID(r *http.Request) domain.BasketID { return domain.BasketID(r.PathValue("id")) }

func reply(w http.ResponseWriter, code int, b *domain.Basket, err error) {
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	writeJSON(w, code, toJSON(b))
}

// statusFor turns errors into HTTP status codes.
func statusFor(err error) int {
	switch {
	case errors.Is(err, app.ErrNotFound), errors.Is(err, app.ErrProductNotFound), errors.Is(err, domain.ErrNoLine):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrClosed):
		return http.StatusConflict
	case errors.Is(err, domain.ErrEmpty), errors.Is(err, domain.ErrInvalidQty), errors.Is(err, domain.ErrNeedsWeight),
		errors.Is(err, domain.ErrWeighedQty), errors.Is(err, domain.ErrInvalidScale):
		return http.StatusUnprocessableEntity
	}
	switch status.Code(err) {
	case codes.InvalidArgument, codes.FailedPrecondition:
		return http.StatusUnprocessableEntity
	case codes.Unavailable, codes.DeadlineExceeded:
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
