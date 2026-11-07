// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"abarow.com/pkg/money"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

type pricesHandler struct {
	prices *app.Prices
}

type pricePointJSON struct {
	UnitPriceKurus int64      `json:"unit_price_kurus"`
	From           *time.Time `json:"from,omitempty"`
	Clear          bool       `json:"clear,omitempty"`
}

type productPricesJSON struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Unit         string   `json:"unit"`
	Barcodes     []string `json:"barcodes"`
	Discontinued bool     `json:"discontinued"`
	// HeadquartersPriceKurus is the headquarters price in effect right now; StorePriceKurus is
	// the store price (empty if there is no exception).
	HeadquartersPriceKurus int64            `json:"headquarters_price_kurus"`
	StorePriceKurus        *int64           `json:"store_price_kurus,omitempty"`
	StorePrices            []pricePointJSON `json:"store_prices"`
	StoreUpcoming          []pricePointJSON `json:"store_upcoming"`
	HeadquartersUpcoming   []pricePointJSON `json:"headquarters_upcoming"`
}

func points(list []domain.PricePoint) []pricePointJSON {
	out := []pricePointJSON{}
	for _, p := range list {
		pj := pricePointJSON{UnitPriceKurus: int64(p.Amount), Clear: p.Clear}
		if !p.From.IsZero() {
			from := p.From
			pj.From = &from
		}
		out = append(out, pj)
	}
	return out
}

func productPricesOut(pp app.ProductPrices) productPricesJSON {
	out := productPricesJSON{
		ID: pp.Product.ID, Name: pp.Product.Name, Unit: pp.Product.Unit, Barcodes: pp.Product.Barcodes, Discontinued: pp.Product.Discontinued,
		HeadquartersPriceKurus: int64(pp.Headquarters), StorePrices: points(pp.StorePoints), StoreUpcoming: points(pp.StoreUpcoming), HeadquartersUpcoming: points(pp.HeadquartersUpcoming),
	}
	if out.Barcodes == nil {
		out.Barcodes = []string{}
	}
	if pp.HasStore {
		v := int64(pp.Store)
		out.StorePriceKurus = &v
	}
	return out
}

func (h *pricesHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.prices.List(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "ürünler okunamadı: "+err.Error())
		return
	}
	out := struct {
		Products []productPricesJSON `json:"products"`
	}{Products: []productPricesJSON{}}
	for _, pp := range list {
		out.Products = append(out.Products, productPricesOut(pp))
	}
	writeJSON(w, http.StatusOK, out)
}

type priceChangeJSON struct {
	At     time.Time `json:"at"`
	By     string    `json:"by"`
	Detail string    `json:"detail"`
}

func (h *pricesHandler) get(w http.ResponseWriter, r *http.Request) {
	pp, history, err := h.prices.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	out := struct {
		Product productPricesJSON `json:"product"`
		History []priceChangeJSON `json:"history"`
	}{Product: productPricesOut(pp), History: []priceChangeJSON{}}
	for _, c := range history {
		out.History = append(out.History, priceChangeJSON{At: c.At, By: c.By, Detail: c.Detail})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *pricesHandler) set(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UnitPriceKurus int64      `json:"unit_price_kurus"`
		From           *time.Time `json:"from"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	pp, err := h.prices.SetPrice(r.Context(), by(r), r.PathValue("id"), money.Money(in.UnitPriceKurus), deref(in.From))
	h.reply(w, pp, err)
}

func (h *pricesHandler) clear(w http.ResponseWriter, r *http.Request) {
	var in struct {
		From *time.Time `json:"from"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	pp, err := h.prices.Clear(r.Context(), by(r), r.PathValue("id"), deref(in.From))
	h.reply(w, pp, err)
}

func (h *pricesHandler) cancel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		From time.Time `json:"from"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	pp, err := h.prices.Cancel(r.Context(), by(r), r.PathValue("id"), in.From)
	h.reply(w, pp, err)
}

func (h *pricesHandler) reply(w http.ResponseWriter, pp app.ProductPrices, err error) {
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, productPricesOut(pp))
}

func (h *pricesHandler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrProductNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, domain.ErrInvalidPrice):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusServiceUnavailable, err.Error())
	}
}

// noPrices answers the price requests while the store server is not connected to headquarters.
func noPrices(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "mağaza fiyatları için operasyon merkeze bağlı olmalı")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "istek okunamadı: "+err.Error())
		return false
	}
	return true
}

func deref(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
