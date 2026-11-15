// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package httpjson is the JSON response writer of the screen APIs: the same headers
// and the same error format in every service.
package httpjson

import (
	"encoding/json"
	"net/http"
)

// Write writes v as JSON. API responses (sales figures, session)
// are not cached.
func Write(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Error, {"error": msg} yazar.
func Error(w http.ResponseWriter, code int, msg string) {
	Write(w, code, map[string]string{"error": msg})
}
