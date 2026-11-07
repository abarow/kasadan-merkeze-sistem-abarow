// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package httpapi is the HTTP driving adapter of the ingest service: the stores'
// connection state to headquarters. It is only for headquarters' internal network
// (the install does not expose the port); the reports and the sign-in are on the headquarters screen.
package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"abarow.com/services/headquarters/ingest/internal/app"
)

// New returns the API handler.
func New(svc *app.Service) http.Handler {
	h := &handler{svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/stores", h.stores)
	return mux
}

type handler struct {
	svc *app.Service
}

type storeJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Link: connected, disconnected or never.
	Link     string     `json:"link"`
	LastSeen *time.Time `json:"last_seen,omitempty"`
	// PendingEvents is the number of events waiting at the store (not yet arrived).
	PendingEvents uint64 `json:"pending_events"`
	Error         string `json:"error,omitempty"`
	// The summary of the events that have come.
	EventCount  int64          `json:"event_count"`
	LastEventAt *time.Time     `json:"last_event_at,omitempty"`
	SaleCount   int64          `json:"sale_count"`
	Registers   []registerJSON `json:"registers"`
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

// registerJSON is a register of the store that has sent events.
type registerJSON struct {
	ID          string    `json:"id"`
	EventCount  int64     `json:"event_count"`
	SaleCount   int64     `json:"sale_count"`
	LastEventAt time.Time `json:"last_event_at"`
}

func (h *handler) stores(w http.ResponseWriter, r *http.Request) {
	stores, err := h.svc.Stores(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "mağazaların durumu okunamadı: " + err.Error()})
		return
	}
	out := make([]storeJSON, len(stores))
	for i, st := range stores {
		out[i] = storeJSON{
			ID: st.ID, Name: st.Name, Link: string(st.Link), LastSeen: timePtr(st.LastSeen), PendingEvents: st.Pending, Error: st.Error,
			EventCount: st.Activity.Events, LastEventAt: timePtr(st.Activity.LastEventAt),
			SaleCount: st.Activity.Sales, Registers: []registerJSON{}, MissingEvents: []gapJSON{},
		}
		for _, g := range st.Activity.MissingEvents {
			out[i].MissingEvents = append(out[i].MissingEvents, gapJSON{Register: g.Register, Producer: g.Producer, From: g.From, To: g.To})
		}
		for _, r := range st.Activity.Registers {
			out[i].Registers = append(out[i].Registers, registerJSON{ID: r.ID, EventCount: r.Events, SaleCount: r.Sales, LastEventAt: r.LastEventAt})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
