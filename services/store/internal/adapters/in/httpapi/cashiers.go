// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"net/http"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/services/store/internal/app"
	"abarow.com/services/store/internal/domain"
)

type cashiersHandler struct {
	cashiers *app.Cashiers
	auth     *app.Auth
}

// A manager cannot lock themselves out: they cannot close their own account or remove their own
// managership; another manager does that.
var (
	errCloseSelf  = errors.New("kendi hesabınızı kapatamazsınız; başka bir müdür kapatabilir")
	errDemoteSelf = errors.New("kendi müdürlüğünüzü kaldıramazsınız; başka bir müdür değiştirebilir")
)

// cashierJSON is a cashier; the PIN and its digest do not go to the screen.
type cashierJSON struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Disabled  bool      `json:"disabled"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
	// Email is the address by which the manager is recognized at the first corporate sign-in;
	// Linked says whether their corporate identity is linked.
	Email  string `json:"email"`
	Linked bool   `json:"linked"`
}

func cashierOut(c domain.Cashier) cashierJSON {
	return cashierJSON{ID: c.ID, Name: c.Name, Role: string(c.Role), Disabled: c.Disabled, UpdatedBy: c.UpdatedBy, UpdatedAt: c.UpdatedAt.UTC(),
		Email: c.Email, Linked: c.Subject != ""}
}

func (h *cashiersHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.cashiers.List(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "kasiyerler okunamadı: "+err.Error())
		return
	}
	out := struct {
		Cashiers []cashierJSON `json:"cashiers"`
	}{Cashiers: []cashierJSON{}}
	for _, c := range list {
		out.Cashiers = append(out.Cashiers, cashierOut(c))
	}
	writeJSON(w, http.StatusOK, out)
}

type cashierChangeJSON struct {
	At     time.Time `json:"at"`
	By     string    `json:"by"`
	Detail string    `json:"detail"`
}

func (h *cashiersHandler) get(w http.ResponseWriter, r *http.Request) {
	c, history, err := h.cashiers.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	out := struct {
		Cashier cashierJSON         `json:"cashier"`
		History []cashierChangeJSON `json:"history"`
	}{Cashier: cashierOut(c), History: []cashierChangeJSON{}}
	for _, ch := range history {
		out.History = append(out.History, cashierChangeJSON{At: ch.At.UTC(), By: ch.By, Detail: ch.Detail})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *cashiersHandler) add(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Role  string `json:"role"`
		PIN   string `json:"pin"`
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := h.cashiers.Add(r.Context(), by(r), in.ID, in.Name, domain.Role(in.Role), in.PIN, in.Email)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, cashierOut(c))
}

func (h *cashiersHandler) update(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name  string `json:"name"`
		Role  string `json:"role"`
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if login.FromRequest(r).Username == r.PathValue("id") && domain.Role(in.Role) != domain.RoleManager {
		writeError(w, http.StatusConflict, errDemoteSelf.Error())
		return
	}
	c, err := h.cashiers.Update(r.Context(), by(r), r.PathValue("id"), in.Name, domain.Role(in.Role), in.Email)
	h.reply(w, c, err)
}

func (h *cashiersHandler) setPIN(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PIN string `json:"pin"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := h.cashiers.SetPIN(r.Context(), by(r), r.PathValue("id"), in.PIN)
	// When the PIN changes the sessions opened with that PIN close; the session of the manager
	// who changed their own PIN stays open.
	if err == nil && login.FromRequest(r).Username == c.ID {
		h.auth.KeepAfterPINChange(sessionCookie.Token(r), c)
	}
	h.reply(w, c, err)
}

func (h *cashiersHandler) setDisabled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Disabled bool `json:"disabled"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if login.FromRequest(r).Username == r.PathValue("id") && in.Disabled {
		writeError(w, http.StatusConflict, errCloseSelf.Error())
		return
	}
	c, err := h.cashiers.SetDisabled(r.Context(), by(r), r.PathValue("id"), in.Disabled)
	h.reply(w, c, err)
}

// unlinkIdentity removes the person's corporate identity link.
func (h *cashiersHandler) unlinkIdentity(w http.ResponseWriter, r *http.Request) {
	c, err := h.cashiers.UnlinkIdentity(r.Context(), by(r), r.PathValue("id"))
	h.reply(w, c, err)
}

func (h *cashiersHandler) reply(w http.ResponseWriter, c domain.Cashier, err error) {
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cashierOut(c))
}

func (h *cashiersHandler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrCashierNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, domain.ErrCashierExists), errors.Is(err, domain.ErrEmailTaken):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, domain.ErrInvalidCashierID), errors.Is(err, domain.ErrInvalidName),
		errors.Is(err, domain.ErrInvalidRole), errors.Is(err, domain.ErrInvalidPIN), errors.Is(err, domain.ErrInvalidEmail):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusServiceUnavailable, err.Error())
	}
}

// noCashiers answers the cashier requests while the store server's store id is
// not defined.
func noCashiers(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "kasiyer yönetimi için operasyonun mağaza kimliği (ABAROW_STORE_ID) tanımlı olmalı; kurulum betiğini yeniden çalıştırın")
}
