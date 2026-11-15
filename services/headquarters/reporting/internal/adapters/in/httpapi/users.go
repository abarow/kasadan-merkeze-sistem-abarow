// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"abarow.com/services/headquarters/reporting/internal/app"
	"abarow.com/services/headquarters/reporting/internal/domain"
)

// usersHandler is user management on the headquarters screen (administrator
// only; the application layer checks the permission).
// userJSON is a user; the password and its digest do not go to the screen.
type userJSON struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	// Role: admin (administrator), catalog or report.
	Role string `json:"role"`
	// AllStores means seeing all the stores; if not, only Stores.
	AllStores bool     `json:"all_stores"`
	Stores    []string `json:"stores"`
	Disabled  bool     `json:"disabled,omitempty"`
	// Email is the address by which they are recognized at the first corporate sign-in; Linked says
	// whether their corporate identity is linked.
	Email  string `json:"email"`
	Linked bool   `json:"linked"`
}

func userOut(u domain.User) userJSON {
	stores := u.Stores
	if stores == nil {
		stores = []string{}
	}
	return userJSON{Username: u.ID, Name: u.Name, Role: string(u.Role), AllStores: u.SeesAllStores(), Stores: stores, Disabled: u.Disabled, Email: u.Email, Linked: u.Subject != ""}
}

type usersHandler struct {
	auth *app.Auth
}

type userChangeJSON struct {
	At     time.Time `json:"at"`
	By     string    `json:"by"`
	Detail string    `json:"detail"`
}

// userInputJSON is the details of the user being added or changed.
type userInputJSON struct {
	Name      string   `json:"name"`
	Role      string   `json:"role"`
	AllStores bool     `json:"all_stores"`
	Stores    []string `json:"stores"`
	Email     string   `json:"email"`
}

func (in userInputJSON) input() app.UserInput {
	return app.UserInput{Name: in.Name, Role: domain.Role(in.Role), AllStores: in.AllStores, Stores: in.Stores, Email: in.Email}
}

func (h *usersHandler) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.auth.Users(r.Context(), me(r))
	if err != nil {
		userError(w, err)
		return
	}
	out := struct {
		Users []userJSON `json:"users"`
	}{Users: []userJSON{}}
	for _, u := range list {
		out.Users = append(out.Users, userOut(u))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *usersHandler) get(w http.ResponseWriter, r *http.Request) {
	u, history, err := h.auth.User(r.Context(), me(r), r.PathValue("username"))
	if err != nil {
		userError(w, err)
		return
	}
	out := struct {
		User    userJSON         `json:"user"`
		History []userChangeJSON `json:"history"`
	}{User: userOut(u), History: []userChangeJSON{}}
	for _, c := range history {
		out.History = append(out.History, userChangeJSON{At: c.At, By: c.By, Detail: c.Detail})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *usersHandler) add(w http.ResponseWriter, r *http.Request) {
	var in struct {
		userInputJSON
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := h.auth.AddUser(r.Context(), me(r), in.Username, in.Password, in.input())
	if err != nil {
		userError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, userOut(u))
}

func (h *usersHandler) update(w http.ResponseWriter, r *http.Request) {
	var in userInputJSON
	if !decode(w, r, &in) {
		return
	}
	u, err := h.auth.UpdateUser(r.Context(), me(r), r.PathValue("username"), in.input())
	if err != nil {
		userError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, userOut(u))
}

// setPassword changes the user's password; their open sessions close.
func (h *usersHandler) setPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := h.auth.SetPassword(r.Context(), me(r), r.PathValue("username"), in.Password); err != nil {
		userError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *usersHandler) setDisabled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Disabled bool `json:"disabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := h.auth.SetDisabled(r.Context(), me(r), r.PathValue("username"), in.Disabled)
	if err != nil {
		userError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, userOut(u))
}

// unlinkIdentity removes the user's corporate identity link.
func (h *usersHandler) unlinkIdentity(w http.ResponseWriter, r *http.Request) {
	u, err := h.auth.UnlinkIdentity(r.Context(), me(r), r.PathValue("username"))
	if err != nil {
		userError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, userOut(u))
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "istek okunamadı")
		return false
	}
	return true
}

func userError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, http.StatusForbidden, "kullanıcıları yalnızca yönetici yönetir")
	case errors.Is(err, domain.ErrUserNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, domain.ErrUserExists), errors.Is(err, domain.ErrSelf), errors.Is(err, domain.ErrLastAdmin),
		errors.Is(err, domain.ErrEmailTaken):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, domain.ErrInvalidUsername), errors.Is(err, domain.ErrInvalidName), errors.Is(err, domain.ErrInvalidRole),
		errors.Is(err, domain.ErrInvalidStore), errors.Is(err, domain.ErrNoStores), errors.Is(err, domain.ErrPasswordTooShort),
		errors.Is(err, domain.ErrInvalidEmail),
		errors.Is(err, domain.ErrRoleNotManaged):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusServiceUnavailable, err.Error())
	}
}
