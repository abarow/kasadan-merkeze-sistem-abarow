// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package httpapi is the HTTP driving adapter of the stock service. The service
// that serves the screen (report at the store server, reporting at headquarters) checks the
// session and forwards the /api/stock requests here with the person (the pkg/login headers); the
// service's port is not opened to the outside.
//
// In the headquarters scope everyone can see (a report user only their own
// stores); goods receipt and counting at the headquarters warehouse are entered by the admin and the
// catalog user, who also send and receive the headquarters warehouse's transfers.
// In the store scope (a manager who signs in to the store server) can do everything.
package httpapi

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"slices"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/pkg/money"
	"abarow.com/pkg/stock"
	"abarow.com/services/inventory/internal/app"
)

type handler struct {
	svc *app.Service
}

// person is the person making the request.
type person struct {
	login.Person
	headquarters bool
}

// SeesStore says whether the person can see the store.
func (p person) SeesStore(id string) bool {
	return p.AllStores || slices.Contains(p.Stores, id)
}

// CanEdit says whether the person can enter stock for their own place: the admin and the
// catalog role at the headquarters warehouse; everyone who signs in to the store server at a store.
func (p person) CanEdit() bool { return !p.headquarters || p.Role == "admin" || p.Role == "catalog" }

// New returns the HTTP API.
//
// The session is checked by reporting; the person (name, role, the stores they can see)
// comes in headers (pkg/login). The service's port is not opened to the outside.
func New(svc *app.Service) http.Handler {
	h := &handler{svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stock", h.auth(h.overview))
	mux.HandleFunc("GET /api/stock/barcode/{code}", h.auth(h.byBarcode))
	mux.HandleFunc("GET /api/stock/products/{id}/movements", h.auth(h.movements))
	mux.HandleFunc("POST /api/stock/receipts", h.auth(h.receive))
	mux.HandleFunc("POST /api/stock/counts", h.auth(h.count))
	mux.HandleFunc("GET /api/stock/transfers", h.auth(h.transfers))
	mux.HandleFunc("GET /api/stock/locations", h.auth(h.locations))
	mux.HandleFunc("POST /api/stock/transfers", h.auth(h.send))
	mux.HandleFunc("POST /api/stock/transfers/{id}/receive", h.auth(h.receiveTransfer))
	return mux
}

// auth reads the person reporting forwarded; write requests need headquarters warehouse
// authority and JSON (a form on another site cannot trigger them).
func (h *handler) auth(next func(http.ResponseWriter, *http.Request, person)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gp, ok := login.PersonFrom(r.Header)
		if !ok {
			writeError(w, http.StatusUnauthorized, "giriş gerekli")
			return
		}
		p := person{Person: gp, headquarters: h.svc.Headquarters()}
		if r.Method != http.MethodGet {
			if !p.CanEdit() {
				writeError(w, http.StatusForbidden, "merkez deposuna giriş yetkiniz yok (yönetici ya da katalog rolü gerekir)")
				return
			}
			if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "istek JSON olmalı")
				return
			}
		}
		next(w, r, p)
	}
}

type rowJSON struct {
	ProductID    string           `json:"product_id"`
	Name         string           `json:"name"`
	Unit         string           `json:"unit"`
	Barcodes     []string         `json:"barcodes"`
	Discontinued bool             `json:"discontinued"`
	Qty          map[string]int64 `json:"qty_milli"`
}

func (h *handler) overview(w http.ResponseWriter, r *http.Request, p person) {
	locs, rows, err := h.svc.Overview(r.Context(), r.URL.Query().Get("q"), p.SeesStore)
	if err != nil {
		reply(w, err)
		return
	}
	items := make([]rowJSON, 0, len(rows))
	for _, row := range rows {
		b := row.Barcodes
		if b == nil {
			b = []string{}
		}
		q := map[string]int64{}
		for l, v := range row.Qty {
			q[l] = int64(v)
		}
		items = append(items, rowJSON{ProductID: row.ID, Name: row.Name, Unit: string(row.Unit), Barcodes: b, Discontinued: row.Discontinued, Qty: q})
	}
	writeJSON(w, http.StatusOK, map[string]any{"here": h.svc.Here(), "locations": locs, "items": items, "can_edit": p.CanEdit()})
}

func (h *handler) byBarcode(w http.ResponseWriter, r *http.Request, _ person) {
	it, err := h.svc.ByBarcode(r.Context(), r.PathValue("code"))
	if err != nil {
		reply(w, err)
		return
	}
	b := it.Barcodes
	if b == nil {
		b = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"product_id": it.ID, "name": it.Name, "unit": string(it.Unit), "barcodes": b, "qty_milli": int64(it.Qty)})
}

type lineJSON struct {
	ProductID    string `json:"product_id"`
	Name         string `json:"name,omitempty"`
	QtyMilli     int64  `json:"qty_milli"`
	CountedMilli int64  `json:"counted_milli,omitempty"`
	SystemMilli  int64  `json:"system_milli,omitempty"`
}

type movementJSON struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Ref         string     `json:"ref"`
	Supplier    string     `json:"supplier,omitempty"`
	Counterpart string     `json:"counterpart,omitempty"`
	By          string     `json:"by"`
	At          time.Time  `json:"at"`
	Lines       []lineJSON `json:"lines"`
}

func toMovement(m stock.Movement) movementJSON {
	out := movementJSON{ID: m.ID, Kind: string(m.Kind), Ref: m.Ref, Supplier: m.Supplier, Counterpart: m.Counterpart, By: m.By, At: m.At, Lines: []lineJSON{}}
	for _, l := range m.Lines {
		out.Lines = append(out.Lines, lineJSON{ProductID: l.ProductID, Name: l.Name, QtyMilli: int64(l.Qty), CountedMilli: int64(l.Counted), SystemMilli: int64(l.System)})
	}
	return out
}

func (h *handler) movements(w http.ResponseWriter, r *http.Request, p person) {
	loc := r.URL.Query().Get("location")
	if loc == "" {
		loc = h.svc.Here()
	}
	if loc != h.svc.Here() && !p.SeesStore(loc) {
		writeError(w, http.StatusForbidden, "bu mağazayı görme yetkiniz yok")
		return
	}
	ms, err := h.svc.History(r.Context(), loc, r.PathValue("id"))
	if err != nil {
		reply(w, err)
		return
	}
	out := make([]movementJSON, 0, len(ms))
	for _, m := range ms {
		out = append(out, toMovement(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"movements": out})
}

type entryJSON struct {
	Supplier string `json:"supplier"`
	Ref      string `json:"ref"`
	To       string `json:"to"`
	Lines    []struct {
		ProductID    string `json:"product_id"`
		QtyMilli     int64  `json:"qty_milli"`
		CountedMilli int64  `json:"counted_milli"`
	} `json:"lines"`
}

func decode(w http.ResponseWriter, r *http.Request) (entryJSON, bool) {
	var body entryJSON
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "istek okunamadı")
		return body, false
	}
	return body, true
}

func qtyLines(body entryJSON) []stock.Line {
	var lines []stock.Line
	for _, l := range body.Lines {
		lines = append(lines, stock.Line{ProductID: l.ProductID, Qty: money.Qty(l.QtyMilli)})
	}
	return lines
}

func (h *handler) receive(w http.ResponseWriter, r *http.Request, p person) {
	body, ok := decode(w, r)
	if !ok {
		return
	}
	m, err := h.svc.Receive(r.Context(), body.Supplier, body.Ref, p.Name, qtyLines(body))
	if err != nil {
		reply(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toMovement(m))
}

func (h *handler) count(w http.ResponseWriter, r *http.Request, p person) {
	body, ok := decode(w, r)
	if !ok {
		return
	}
	var lines []stock.Line
	for _, l := range body.Lines {
		lines = append(lines, stock.Line{ProductID: l.ProductID, Counted: money.Qty(l.CountedMilli)})
	}
	m, err := h.svc.Count(r.Context(), body.Ref, p.Name, lines)
	if err != nil {
		reply(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toMovement(m))
}

type transferLineJSON struct {
	ProductID string `json:"product_id"`
	Name      string `json:"name"`
	QtyMilli  int64  `json:"qty_milli"`
	// The received quantity; none if it has not been received.
	ReceivedMilli *int64 `json:"received_milli,omitempty"`
}

type transferJSON struct {
	ID         string             `json:"id"`
	From       string             `json:"from"`
	To         string             `json:"to"`
	By         string             `json:"by"`
	At         time.Time          `json:"at"`
	Pending    bool               `json:"pending"`
	ReceivedBy string             `json:"received_by,omitempty"`
	ReceivedAt *time.Time         `json:"received_at,omitempty"`
	Lines      []transferLineJSON `json:"lines"`
}

func toTransfer(t stock.Transfer) transferJSON {
	out := transferJSON{ID: t.ID, From: t.From, To: t.To, By: t.By, At: t.At, Pending: t.Pending(), ReceivedBy: t.ReceivedBy, Lines: []transferLineJSON{}}
	if !t.Pending() {
		out.ReceivedAt = &t.ReceivedAt
	}
	received := map[string]int64{}
	for _, l := range t.Received {
		received[l.ProductID] = int64(l.Qty)
	}
	for _, l := range t.Lines {
		line := transferLineJSON{ProductID: l.ProductID, Name: l.Name, QtyMilli: int64(l.Qty)}
		if q, ok := received[l.ProductID]; ok {
			line.ReceivedMilli = &q
		}
		out.Lines = append(out.Lines, line)
	}
	return out
}

type locationJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// transfers are the transfers of the places the person sees; those of the headquarters
// warehouse are seen by whoever has authority to enter stock at the warehouse. The list of places comes with their names.
func (h *handler) transfers(w http.ResponseWriter, r *http.Request, p person) {
	visible := func(l string) bool {
		return (l == h.svc.Here() && p.CanEdit()) || (l != h.svc.Here() && p.SeesStore(l))
	}
	ts, err := h.svc.Transfers(r.Context(), visible)
	if err != nil {
		reply(w, err)
		return
	}
	out := make([]transferJSON, 0, len(ts))
	for _, t := range ts {
		out = append(out, toTransfer(t))
	}
	known, err := h.svc.Locations(r.Context())
	if err != nil {
		reply(w, err)
		return
	}
	locs := []locationJSON{}
	for _, l := range known {
		locs = append(locs, locationJSON{ID: l.ID, Name: l.Name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"here": h.svc.Here(), "locations": locs, "transfers": out, "can_edit": p.CanEdit()})
}

// locations are the places a transfer can be sent to (except itself).
func (h *handler) locations(w http.ResponseWriter, r *http.Request, _ person) {
	known, err := h.svc.Locations(r.Context())
	if err != nil {
		reply(w, err)
		return
	}
	out := []locationJSON{}
	for _, l := range known {
		if l.ID != h.svc.Here() {
			out = append(out, locationJSON{ID: l.ID, Name: l.Name})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"here": h.svc.Here(), "locations": out})
}

func (h *handler) send(w http.ResponseWriter, r *http.Request, p person) {
	body, ok := decode(w, r)
	if !ok {
		return
	}
	t, err := h.svc.Send(r.Context(), body.To, p.Name, qtyLines(body))
	if err != nil {
		reply(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toTransfer(t))
}

func (h *handler) receiveTransfer(w http.ResponseWriter, r *http.Request, p person) {
	body, ok := decode(w, r)
	if !ok {
		return
	}
	m, err := h.svc.ReceiveTransfer(r.Context(), r.PathValue("id"), p.Name, qtyLines(body))
	if err != nil {
		reply(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toMovement(m))
}

// inputErrors are input errors the person can correct (422).
var inputErrors = []error{
	stock.ErrNoLines, stock.ErrInvalidQty, stock.ErrNegativeCount, stock.ErrPieceFraction,
	stock.ErrDuplicateProduct, stock.ErrNoSupplier, stock.ErrNoRef, stock.ErrUnknownProduct,
	stock.ErrNoDestination, stock.ErrSameLocation, stock.ErrUnknownLocation, stock.ErrNotForHere,
}

func reply(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrNotFound), errors.Is(err, stock.ErrUnknownTransfer):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, stock.ErrDuplicateReceipt), errors.Is(err, stock.ErrTransferReceived):
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	for _, e := range inputErrors {
		if errors.Is(err, e) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	writeError(w, http.StatusInternalServerError, "stok kaydı yapılamadı")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
