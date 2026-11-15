// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package login

import (
	"net/http"
	"net/url"
	"strings"
)

// Only the service that serves the screen checks the session (report at the store server,
// reporting at headquarters); it passes the person to the services behind (inventory,
// catalog-admin) with these headers. The port of the services behind is not exposed outside:
// only the service that serves the screen may put the header. It also deletes the
// headers of the same name coming from the client on every request.
const (
	// UserHeader is the person's username (headquarters).
	UserHeader = "X-Abarow-User"
	// PersonHeader is the person's name (URL-encoded).
	PersonHeader = "X-Abarow-Person"
	// RoleHeader is the person's role (headquarters: admin, catalog, report).
	RoleHeader = "X-Abarow-Role"
	// StoresHeader is the stores the person can see: "*" all, otherwise comma-separated
	// store ids.
	StoresHeader = "X-Abarow-Stores"
	// UpdateSecretHeader is the shared secret key the screen service puts when
	// passing an approval request to the update agent (ABAROW_UPDATE_APPROVE_SECRET);
	// the agent rejects a write request without the key. It makes it harder for another
	// container on the internal network to send a request directly to the agent.
	UpdateSecretHeader = "X-Abarow-Update-Secret"
)

// Person is the person who opened the session.
type Person struct {
	Username, Name, Role string
	// If AllStores is false they see only the stores in Stores.
	AllStores bool
	Stores    []string
}

// StripHeaders deletes the person headers the client could send.
func StripHeaders(h http.Header) {
	for _, k := range []string{UserHeader, PersonHeader, RoleHeader, StoresHeader} {
		h.Del(k)
	}
}

// SetHeaders writes the person to the headers (deleting the old ones first).
func (p Person) SetHeaders(h http.Header) {
	StripHeaders(h)
	h.Set(UserHeader, p.Username)
	h.Set(PersonHeader, url.QueryEscape(p.Name))
	h.Set(RoleHeader, p.Role)
	stores := strings.Join(p.Stores, ",")
	if p.AllStores {
		stores = "*"
	}
	h.Set(StoresHeader, stores)
}

// PersonFrom reads the person in the headers; false if there is no username (a request
// that came without the session being checked).
func PersonFrom(h http.Header) (Person, bool) {
	p := Person{Username: h.Get(UserHeader), Role: h.Get(RoleHeader)}
	if p.Username == "" {
		return Person{}, false
	}
	p.Name, _ = url.QueryUnescape(h.Get(PersonHeader))
	switch stores := h.Get(StoresHeader); stores {
	case "*":
		p.AllStores = true
	case "":
	default:
		p.Stores = strings.Split(stores, ",")
	}
	return p, true
}
