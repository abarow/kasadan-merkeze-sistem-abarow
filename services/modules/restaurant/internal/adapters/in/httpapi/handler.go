// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package httpapi is the HTTP driving adapter of the restaurant module
// (/api/module/...). The register screen connects through the gateway; the gateway
// forwards the person in the session with headers (CashierHeader, CashierNameHeader).
// In the future the waiter's phone will use the same API too (DESIGN section 20):
// a line is added with an id the client produced (safe to repeat), structural
// operations are done with the check's version, changes are announced
// with the /events stream.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"abarow.com/pkg/menu"
	"abarow.com/pkg/service"
	"abarow.com/services/modules/restaurant/internal/app"
	"abarow.com/services/modules/restaurant/internal/domain"
)

// The headers the gateway sets.
const (
	CashierHeader     = "X-Abarow-Cashier-Id"
	CashierNameHeader = "X-Abarow-Cashier-Name"
	// ApprovalHeader is the manager approval key (POST /api/auth/approve).
	ApprovalHeader = "X-Abarow-Approval"
)

type handler struct{ svc *app.Service }

// New returns the HTTP API and the screen (webDir).
func New(svc *app.Service, webDir string) http.Handler {
	h := &handler{svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/module/menu", h.menu)
	mux.HandleFunc("GET /api/module/images/{id}", h.image)
	mux.HandleFunc("GET /api/module/floor", h.floor)
	mux.HandleFunc("PUT /api/module/floor", h.setFloor)
	mux.HandleFunc("POST /api/module/tables/{id}/open", h.openTable)
	mux.HandleFunc("GET /api/module/orders/{id}", h.order)
	mux.HandleFunc("POST /api/module/orders/{id}/lines", h.addLine)
	mux.HandleFunc("PATCH /api/module/orders/{id}/lines/{line}", h.setLine)
	mux.HandleFunc("POST /api/module/orders/{id}/lines/{line}/void", h.void)
	mux.HandleFunc("POST /api/module/orders/{id}/send", h.send)
	mux.HandleFunc("POST /api/module/orders/{id}/bill", h.bill)
	mux.HandleFunc("POST /api/module/orders/{id}/pay", h.pay)
	mux.HandleFunc("POST /api/module/orders/{id}/settle", h.settle)
	mux.HandleFunc("POST /api/module/orders/{id}/move", h.move)
	mux.HandleFunc("POST /api/module/orders/{id}/merge", h.merge)
	mux.HandleFunc("POST /api/module/orders/{id}/close", h.closeEmpty)
	mux.HandleFunc("GET /api/module/events", h.events)
	mux.Handle("/", service.SPA(webDir, "restaurant"))
	return mux
}

func person(r *http.Request) app.Person {
	name, err := url.QueryUnescape(r.Header.Get(CashierNameHeader))
	if err != nil || name == "" {
		name = r.Header.Get(CashierHeader)
	}
	return app.Person{ID: r.Header.Get(CashierHeader), Name: name}
}

// --- menu ---

type optionJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PriceDelta int64  `json:"price_delta_kurus"`
}

type groupJSON struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Min     int          `json:"min"`
	Max     int          `json:"max"`
	Options []optionJSON `json:"options"`
}

type itemJSON struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	UnitPriceKurus int64       `json:"unit_price_kurus"`
	Station        string      `json:"station"`
	OptionGroups   []groupJSON `json:"option_groups"`
	// Image is the address of the picture (by digest; the address changes when it changes); empty if none.
	Image string `json:"image"`
}

type categoryJSON struct {
	Name  string     `json:"name"`
	Items []itemJSON `json:"items"`
}

func (h *handler) menu(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.Menu(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := []categoryJSON{}
	for _, it := range items {
		if it.Weighed {
			continue // a weighed product is not sold by the piece in a restaurant
		}
		if len(out) == 0 || out[len(out)-1].Name != it.Category {
			out = append(out, categoryJSON{Name: it.Category, Items: []itemJSON{}})
		}
		j := itemJSON{ID: it.ID, Name: it.Name, UnitPriceKurus: int64(it.UnitPrice), Station: it.Station, OptionGroups: groups(it.Options)}
		if it.ImageSHA256 != "" {
			j.Image = "/api/module/images/" + url.PathEscape(it.ID) + "?h=" + it.ImageSHA256
		}
		out[len(out)-1].Items = append(out[len(out)-1].Items, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": out})
}

func groups(gs []menu.Group) []groupJSON {
	out := []groupJSON{}
	for _, g := range gs {
		j := groupJSON{ID: g.ID, Name: g.Name, Min: g.Min, Max: g.Max, Options: []optionJSON{}}
		for _, o := range g.Options {
			j.Options = append(j.Options, optionJSON{ID: o.ID, Name: o.Name, PriceDelta: int64(o.PriceDelta)})
		}
		out = append(out, j)
	}
	return out
}

// image is the product's picture. If the requested digest (?h=) is that of the current picture
// it is cached indefinitely; if it does not match (the picture changed, the new one
// has not arrived yet) 404: the screen shows the initials of the name.
func (h *handler) image(w http.ResponseWriter, r *http.Request) {
	img, err := h.svc.Image(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if want := r.URL.Query().Get("h"); want != "" && want != img.SHA256 {
		writeError(w, http.StatusNotFound, "görsel henüz gelmedi")
		return
	}
	w.Header().Set("Content-Type", img.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	_, _ = w.Write(img.Data)
}

// --- salon ---

type tableJSON struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Order *orderJSON `json:"order"`
}

type areaJSON struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Tables []tableJSON `json:"tables"`
}

func (h *handler) floor(w http.ResponseWriter, r *http.Request) {
	areas, err := h.svc.Floor(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := []areaJSON{}
	for _, a := range areas {
		aj := areaJSON{ID: a.ID, Name: a.Name, Tables: []tableJSON{}}
		for _, t := range a.Tables {
			tj := tableJSON{ID: t.ID, Name: t.Name}
			if t.Order != nil {
				o := toOrder(t.Order)
				tj.Order = &o
			}
			aj.Tables = append(aj.Tables, tj)
		}
		out = append(out, aj)
	}
	writeJSON(w, http.StatusOK, map[string]any{"areas": out})
}

func (h *handler) setFloor(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Areas []domain.Area `json:"areas"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := h.svc.SetFloor(r.Context(), person(r), r.Header.Get(ApprovalHeader), in.Areas); err != nil {
		fail(w, err)
		return
	}
	h.floor(w, r)
}

// --- adisyon ---

type lineJSON struct {
	domain.Line
	OpenQty    int    `json:"open_qty"`
	Label      string `json:"label"`
	TotalKurus int64  `json:"total_kurus"`
}

type orderJSON struct {
	ID         string          `json:"id"`
	TableID    string          `json:"table_id"`
	OpenedBy   string          `json:"opened_by"`
	OpenedAt   time.Time       `json:"opened_at"`
	Lines      []lineJSON      `json:"lines"`
	Payment    *domain.Payment `json:"payment"`
	BillAt     *time.Time      `json:"bill_at"`
	Closed     bool            `json:"closed"`
	Version    int64           `json:"version"`
	TotalKurus int64           `json:"total_kurus"`
	DueKurus   int64           `json:"due_kurus"`
	// Pending is the number of lines not yet sent to the kitchen.
	Pending int `json:"pending"`
}

func toOrder(o *domain.Order) orderJSON {
	out := orderJSON{
		ID: o.ID, TableID: o.TableID, OpenedBy: o.OpenedBy, OpenedAt: o.OpenedAt, Lines: []lineJSON{}, Payment: o.Payment,
		Closed: o.Closed, Version: o.Version, TotalKurus: int64(o.Total()), DueKurus: int64(o.Due()),
	}
	if !o.BillAt.IsZero() {
		out.BillAt = &o.BillAt
	}
	for _, l := range o.Lines {
		out.Lines = append(out.Lines, lineJSON{Line: l, OpenQty: l.OpenQty(), Label: l.Label(), TotalKurus: int64(l.Total())})
		if l.Status == domain.Pending {
			out.Pending++
		}
	}
	return out
}

func reply(w http.ResponseWriter, o *domain.Order, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toOrder(o))
}

func (h *handler) openTable(w http.ResponseWriter, r *http.Request) {
	o, err := h.svc.OpenTable(r.Context(), r.PathValue("id"), person(r))
	reply(w, o, err)
}

func (h *handler) order(w http.ResponseWriter, r *http.Request) {
	o, err := h.svc.Order(r.Context(), r.PathValue("id"))
	reply(w, o, err)
}

func (h *handler) addLine(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID        string        `json:"id"`
		ProductID string        `json:"product_id"`
		Choices   []menu.Choice `json:"choices"`
		Note      string        `json:"note"`
		Qty       int           `json:"qty"`
	}
	if !decode(w, r, &in) {
		return
	}
	o, err := h.svc.AddLine(r.Context(), r.PathValue("id"), app.AddInput{ID: in.ID, ProductID: in.ProductID, Choices: in.Choices, Note: in.Note, Qty: in.Qty}, person(r))
	reply(w, o, err)
}

func (h *handler) setLine(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Qty  int    `json:"qty"`
		Note string `json:"note"`
	}
	if !decode(w, r, &in) {
		return
	}
	o, err := h.svc.SetLine(r.Context(), r.PathValue("id"), r.PathValue("line"), in.Qty, in.Note)
	reply(w, o, err)
}

func (h *handler) void(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Qty    int    `json:"qty"`
		Reason string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	o, err := h.svc.Void(r.Context(), r.PathValue("id"), r.PathValue("line"), in.Qty, in.Reason, person(r), r.Header.Get(ApprovalHeader))
	reply(w, o, err)
}

func (h *handler) send(w http.ResponseWriter, r *http.Request) {
	o, err := h.svc.Send(r.Context(), r.PathValue("id"), person(r))
	if err != nil && o != nil {
		// One station could not print, the other did: the error and the latest state of the check.
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "order": toOrder(o)})
		return
	}
	reply(w, o, err)
}

func (h *handler) bill(w http.ResponseWriter, r *http.Request) {
	o, err := h.svc.Bill(r.Context(), r.PathValue("id"), person(r))
	reply(w, o, err)
}

func (h *handler) pay(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version int64         `json:"version"`
		Parts   []domain.Part `json:"parts"`
	}
	if !decode(w, r, &in) {
		return
	}
	o, saleID, err := h.svc.Pay(r.Context(), r.PathValue("id"), in.Version, in.Parts, person(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": toOrder(o), "sale_id": saleID})
}

func (h *handler) settle(w http.ResponseWriter, r *http.Request) {
	o, err := h.svc.Settle(r.Context(), r.PathValue("id"))
	reply(w, o, err)
}

func (h *handler) move(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version int64  `json:"version"`
		TableID string `json:"table_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	o, err := h.svc.Move(r.Context(), r.PathValue("id"), in.Version, in.TableID)
	reply(w, o, err)
}

func (h *handler) merge(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version     int64  `json:"version"`
		FromOrderID string `json:"from_order_id"`
		FromVersion int64  `json:"from_version"`
	}
	if !decode(w, r, &in) {
		return
	}
	o, err := h.svc.Merge(r.Context(), r.PathValue("id"), in.Version, in.FromOrderID, in.FromVersion)
	reply(w, o, err)
}

func (h *handler) closeEmpty(w http.ResponseWriter, r *http.Request) {
	o, err := h.svc.CloseEmpty(r.Context(), r.PathValue("id"))
	reply(w, o, err)
}

// events is the change stream (Server-Sent Events): every event is one JSON
// line ({"type": "order", ...}). A comment line is sent every 20 seconds:
// so that an intermediary does not take the connection for idle and close it.
func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "akış desteklenmiyor")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": abarow restoran\n\n")
	flusher.Flush()
	events := h.svc.Subscribe(r.Context())
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-events:
			if !ok {
				return
			}
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: change\ndata: %s\n\n", data)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		flusher.Flush()
	}
}

// --- helpers ---

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "istek okunamadı: "+err.Error())
		return false
	}
	return true
}

// statusFor turns errors into HTTP status codes.
func statusFor(err error) int {
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, app.ErrProductNotFound):
		return http.StatusNotFound
	case errors.Is(err, app.ErrNotAuthorized):
		return http.StatusForbidden
	case errors.Is(err, domain.ErrStale), errors.Is(err, domain.ErrTableBusy), errors.Is(err, domain.ErrClosed),
		errors.Is(err, domain.ErrPaymentPending), errors.Is(err, domain.ErrLineSent):
		return http.StatusConflict
	case errors.Is(err, domain.ErrInvalidLine), errors.Is(err, domain.ErrLineNotSent), errors.Is(err, domain.ErrNotSent),
		errors.Is(err, domain.ErrQtyTooLarge), errors.Is(err, domain.ErrNothingToPay), errors.Is(err, domain.ErrVoidReason),
		errors.Is(err, domain.ErrFloor), errors.Is(err, domain.ErrTableFree), errors.Is(err, menu.ErrChoice):
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

func fail(w http.ResponseWriter, err error) { writeError(w, statusFor(err), err.Error()) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
