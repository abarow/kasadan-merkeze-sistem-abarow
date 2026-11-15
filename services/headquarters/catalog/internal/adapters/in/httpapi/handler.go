// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package httpapi is catalog-admin's HTTP driving adapter: the catalog management API
// of the headquarters screen. The screen is served from reporting; reporting forwards the
// /api/catalog/ requests here. The session is the one reporting opened (the same cookie, the same
// database).
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/pkg/menu"
	"abarow.com/pkg/money"
	"abarow.com/services/headquarters/catalog/internal/app"
	"abarow.com/services/headquarters/catalog/internal/domain"
)

// New returns the catalog API. reporting checks the session; the person comes in
// headers (pkg/login). The service's port is not exposed outside.
func New(svc *app.Service, writers ...string) http.Handler {
	if len(writers) == 0 {
		writers = DefaultWriters
	}
	h := &handler{svc: svc, writers: writers}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/catalog/products", h.auth(h.list))
	mux.HandleFunc("POST /api/catalog/products", h.auth(h.create))
	mux.HandleFunc("GET /api/catalog/products/{code}", h.auth(h.get))
	mux.HandleFunc("PUT /api/catalog/products/{code}", h.auth(h.update))
	mux.HandleFunc("POST /api/catalog/products/{code}/prices", h.auth(h.schedule))
	mux.HandleFunc("POST /api/catalog/products/{code}/prices/cancel", h.auth(h.cancel))
	mux.HandleFunc("POST /api/catalog/products/{code}/discontinue", h.auth(h.discontinue))
	mux.HandleFunc("GET /api/catalog/products/{code}/image", h.auth(h.image))
	mux.HandleFunc("PUT /api/catalog/products/{code}/image", h.auth(h.setImage))
	mux.HandleFunc("POST /api/catalog/products/{code}/image/remove", h.auth(h.removeImage))
	return mux
}

// DefaultWriters are the roles that can change products and prices (at headquarters): administrator
// and the catalog user. On the store server the store manager writes (manager).
var DefaultWriters = []string{"admin", "catalog"}

type handler struct {
	svc     *app.Service
	writers []string
}

// auth reads the person reporting passed on. Everyone reads the catalog; only the administrator and the
// catalog user change products and prices. Write requests accept only
// JSON (a form on another site cannot trigger them).
func (h *handler) auth(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := login.PersonFrom(r.Header)
		if !ok {
			writeError(w, http.StatusUnauthorized, "giriş gerekli")
			return
		}
		user, role := p.Username, p.Role
		if r.Method != http.MethodGet {
			if !slices.Contains(h.writers, role) {
				writeError(w, http.StatusForbidden, "ürün ve fiyat değiştirme yetkiniz yok ("+strings.Join(h.writers, " ya da ")+" rolü gerekir)")
				return
			}
			if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "istek JSON olmalı")
				return
			}
		}
		next(w, r, user)
	}
}

type priceJSON struct {
	UnitPriceKurus int64      `json:"unit_price_kurus"`
	From           *time.Time `json:"from,omitempty"`
}

type productJSON struct {
	Code         string   `json:"code"`
	Name         string   `json:"name"`
	Unit         string   `json:"unit"`
	TaxRateBp    int64    `json:"tax_rate_bp"`
	Barcodes     []string `json:"barcodes"`
	QuickKey     bool     `json:"quick_key"`
	Discontinued bool     `json:"discontinued"`
	// UnitPriceKurus is the headquarters price in effect right now.
	UnitPriceKurus int64       `json:"unit_price_kurus"`
	Prices         []priceJSON `json:"prices"`
	// Restaurant menu; the image digest if there is an image (the screen asks with ?h=).
	Category    string       `json:"category"`
	MenuOrder   int          `json:"menu_order"`
	Station     string       `json:"station"`
	Options     []menu.Group `json:"option_groups"`
	ImageSHA256 string       `json:"image_sha256"`
	UpdatedBy   string       `json:"updated_by"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Version     int64        `json:"version"`
}

type changeJSON struct {
	At     time.Time `json:"at"`
	By     string    `json:"by"`
	Action string    `json:"action"`
	Detail string    `json:"detail"`
}

func productOut(p domain.Product, now time.Time) productJSON {
	out := productJSON{
		Code: p.Code, Name: p.Name, Unit: string(p.Unit), TaxRateBp: int64(p.TaxRate), Barcodes: p.Barcodes, QuickKey: p.QuickKey,
		Discontinued: p.Discontinued, UnitPriceKurus: int64(p.PriceAt(now)), Prices: []priceJSON{},
		Category: p.Category, MenuOrder: p.MenuOrder, Station: p.Station, Options: p.Options, ImageSHA256: p.ImageSHA256,
		UpdatedBy: p.UpdatedBy, UpdatedAt: p.UpdatedAt, Version: p.Version,
	}
	if out.Options == nil {
		out.Options = []menu.Group{}
	}
	if out.Barcodes == nil {
		out.Barcodes = []string{}
	}
	for _, pr := range p.Prices {
		pj := priceJSON{UnitPriceKurus: int64(pr.Amount)}
		if !pr.From.IsZero() {
			from := pr.From
			pj.From = &from
		}
		out.Prices = append(out.Prices, pj)
	}
	return out
}

func (h *handler) list(w http.ResponseWriter, r *http.Request, _ string) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	products, err := h.svc.List(r.Context(), r.URL.Query().Get("q"), limit)
	if err != nil {
		h.fail(w, err)
		return
	}
	now := time.Now()
	out := struct {
		Products []productJSON `json:"products"`
	}{Products: []productJSON{}}
	for _, p := range products {
		out.Products = append(out.Products, productOut(p, now))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) get(w http.ResponseWriter, r *http.Request, _ string) {
	code := r.PathValue("code")
	p, err := h.svc.Get(r.Context(), code)
	if err != nil {
		h.fail(w, err)
		return
	}
	history, err := h.svc.History(r.Context(), code)
	if err != nil {
		h.fail(w, err)
		return
	}
	out := struct {
		Product productJSON  `json:"product"`
		History []changeJSON `json:"history"`
	}{Product: productOut(p, time.Now()), History: []changeJSON{}}
	for _, c := range history {
		out.History = append(out.History, changeJSON{At: c.At, By: c.By, Action: c.Action, Detail: c.Detail})
	}
	writeJSON(w, http.StatusOK, out)
}

type infoJSON struct {
	Version   int64    `json:"version"`
	Code      string   `json:"code"`
	Name      string   `json:"name"`
	Unit      string   `json:"unit"`
	TaxRateBp int64    `json:"tax_rate_bp"`
	Barcodes  []string `json:"barcodes"`
	QuickKey  bool     `json:"quick_key"`
	// Restaurant menu.
	Category  string       `json:"category"`
	MenuOrder int          `json:"menu_order"`
	Station   string       `json:"station"`
	Options   []menu.Group `json:"option_groups"`
	// Only on a new product: the initial price.
	UnitPriceKurus int64 `json:"unit_price_kurus"`
}

func (in infoJSON) info() app.Info {
	return app.Info{Name: in.Name, Unit: domain.Unit(in.Unit), TaxRate: money.Rate(in.TaxRateBp), Barcodes: in.Barcodes, QuickKey: in.QuickKey,
		Category: in.Category, MenuOrder: in.MenuOrder, Station: in.Station, Options: in.Options}
}

func (h *handler) create(w http.ResponseWriter, r *http.Request, user string) {
	var in infoJSON
	if !decode(w, r, &in) {
		return
	}
	p, err := h.svc.Create(r.Context(), user, in.Code, in.info(), money.Money(in.UnitPriceKurus))
	h.reply(w, http.StatusCreated, p, err)
}

func (h *handler) update(w http.ResponseWriter, r *http.Request, user string) {
	var in infoJSON
	if !decode(w, r, &in) {
		return
	}
	p, err := h.svc.Update(r.Context(), user, r.PathValue("code"), in.Version, in.info())
	h.reply(w, http.StatusOK, p, err)
}

func (h *handler) schedule(w http.ResponseWriter, r *http.Request, user string) {
	var in struct {
		Version        int64      `json:"version"`
		UnitPriceKurus int64      `json:"unit_price_kurus"`
		From           *time.Time `json:"from"`
	}
	if !decode(w, r, &in) {
		return
	}
	var from time.Time
	if in.From != nil {
		from = *in.From
	}
	p, err := h.svc.SchedulePrice(r.Context(), user, r.PathValue("code"), in.Version, money.Money(in.UnitPriceKurus), from)
	h.reply(w, http.StatusOK, p, err)
}

func (h *handler) cancel(w http.ResponseWriter, r *http.Request, user string) {
	var in struct {
		Version int64     `json:"version"`
		From    time.Time `json:"from"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := h.svc.CancelPrice(r.Context(), user, r.PathValue("code"), in.Version, in.From)
	h.reply(w, http.StatusOK, p, err)
}

func (h *handler) discontinue(w http.ResponseWriter, r *http.Request, user string) {
	var in struct {
		Version      int64 `json:"version"`
		Discontinued bool  `json:"discontinued"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := h.svc.SetDiscontinued(r.Context(), user, r.PathValue("code"), in.Version, in.Discontinued)
	h.reply(w, http.StatusOK, p, err)
}

// image is the product's image (previewed on the screen). The type is determined from the content;
// the browser does not guess the type.
func (h *handler) image(w http.ResponseWriter, r *http.Request, _ string) {
	img, err := h.svc.Image(r.Context(), r.PathValue("code"))
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", img.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	_, _ = w.Write(img.Data)
}

// setImage uploads the image: {"version": n, "data": "<base64>"}. Like the write
// requests it is JSON (a form on another site cannot trigger it).
func (h *handler) setImage(w http.ResponseWriter, r *http.Request, user string) {
	var in struct {
		Version int64  `json:"version"`
		Data    []byte `json:"data"`
	}
	// The base64 text is a third larger than the image.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, menu.MaxImageBytes*2)).Decode(&in); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("görsel okunamadı (en çok %d KB): %v", menu.MaxImageBytes>>10, err))
		return
	}
	p, err := h.svc.SetImage(r.Context(), user, r.PathValue("code"), in.Version, in.Data)
	h.reply(w, http.StatusOK, p, err)
}

func (h *handler) removeImage(w http.ResponseWriter, r *http.Request, user string) {
	var in struct {
		Version int64 `json:"version"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := h.svc.RemoveImage(r.Context(), user, r.PathValue("code"), in.Version)
	h.reply(w, http.StatusOK, p, err)
}

func (h *handler) reply(w http.ResponseWriter, code int, p domain.Product, err error) {
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, code, productOut(p, time.Now()))
}

func (h *handler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, domain.ErrExists), errors.Is(err, domain.ErrBarcodeTaken), errors.Is(err, domain.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, domain.ErrInvalidProduct):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusServiceUnavailable, err.Error())
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "istek okunamadı: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
