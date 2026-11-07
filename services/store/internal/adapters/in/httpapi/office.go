// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"

	"abarow.com/pkg/login"
	"abarow.com/pkg/report"
	"abarow.com/services/store/internal/app"
)

// Option gives New an optional dependency.
type Option func(*options)

type options struct {
	catalog *url.URL
	office  *app.Headquarters
	update  *url.URL
	secret  string
}

// WithUpdate connects this store server's update agent: /api/update/status (the status and
// the code the approver will enter at headquarters) and the approval of the new version: only a manager gives
// the approval. secret is the secret key shared with the agent. If agent is nil
// "not installed".
func WithUpdate(agent *url.URL, secret string) Option {
	return func(o *options) { o.update, o.secret = agent, secret }
}

// WithCatalog connects product and price management at the store (catalog-admin): the
// /api/catalog/ requests are forwarded to it, and while headquarters is connected the write
// requests are rejected. office is the "headquarters connected" setting; the setting endpoints
// (/api/settings/headquarters) open with it too. If admin is nil (catalog-admin is
// not installed) the catalog requests answer "not installed".
func WithCatalog(admin *url.URL, office *app.Headquarters) Option {
	return func(o *options) { o.catalog, o.office = admin, office }
}

// catalogRoutes adds the catalog proxy and the setting endpoints to the mux.
func catalogRoutes(mux *http.ServeMux, web login.Web, o options) {
	if o.office != nil {
		s := &officeHandler{office: o.office}
		mux.HandleFunc("GET /api/settings/headquarters", web.Require(s.get))
		mux.HandleFunc("POST /api/settings/headquarters", web.Require(s.set))
	}
	if o.catalog == nil {
		report.Backend(mux, web, nil, "katalog yönetimi kurulu değil", "/api/catalog/")
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(o.catalog)
	var h http.Handler = proxy
	if o.office != nil {
		h = readOnlyWhileConnected(o.office, proxy)
	}
	mux.HandleFunc("/api/catalog/", web.Forward(h))
}

// readOnlyWhileConnected rejects the catalog writes (product, barcode, price, image) while
// headquarters counts as connected: the catalog comes from headquarters, it is read at the store.
// When the setting is turned off it is managed at the store.
func readOnlyWhileConnected(office *app.Headquarters, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			connected, err := office.Connected(r.Context())
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "ayar okunamadı: "+err.Error())
				return
			}
			if connected {
				writeError(w, http.StatusForbidden, "ürünler ve fiyatlar merkezden (merkez) yönetiliyor; mağazada değiştirmek için ayarlardan merkez bağlantısını kapatın")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type officeHandler struct{ office *app.Headquarters }

type officeJSON struct {
	// Installed: the store is installed to headquarters (--headquarters was given at install).
	Installed bool `json:"installed"`
	// Connected: headquarters counts as connected; the catalog comes from headquarters.
	Connected bool `json:"connected"`
	// CatalogWritable: the products can be changed at the store (if not connected).
	CatalogWritable bool `json:"catalog_writable"`
	// Applied: (only when the setting changes) the catalog stream was applied right away;
	// if false it is applied once NATS comes.
	Applied *bool `json:"applied,omitempty"`
}

func (h *officeHandler) state(r *http.Request) (officeJSON, error) {
	connected, err := h.office.Connected(r.Context())
	return officeJSON{Installed: h.office.Installed(), Connected: connected, CatalogWritable: !connected}, err
}

func (h *officeHandler) get(w http.ResponseWriter, r *http.Request) {
	out, err := h.state(r)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "ayar okunamadı: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *officeHandler) set(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Connected *bool `json:"connected"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Connected == nil {
		writeError(w, http.StatusBadRequest, "connected (true ya da false) gerekli")
		return
	}
	applied, err := h.office.Set(r.Context(), *in.Connected)
	switch {
	case errors.Is(err, app.ErrNoHeadquarters):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "ayar kaydedilemedi: "+err.Error())
		return
	}
	out, err := h.state(r)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "ayar okunamadı: "+err.Error())
		return
	}
	out.Applied = &applied
	writeJSON(w, http.StatusOK, out)
}
