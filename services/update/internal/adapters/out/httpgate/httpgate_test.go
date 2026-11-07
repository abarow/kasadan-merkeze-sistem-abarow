// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpgate_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"abarow.com/pkg/login"
	"abarow.com/services/update/internal/adapters/out/httpgate"
)

func TestGate(t *testing.T) {
	var secret string
	body := `{"safe":true}`
	code := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret = r.Header.Get(login.UpdateSecretHeader)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	g := httpgate.Gate{URL: srv.URL, Secret: "gizli"}
	ctx := t.Context()

	if r, err := g.Unsafe(ctx); err != nil || r != "" || secret != "gizli" {
		t.Fatalf("güvenli: %q %v %q", r, err, secret)
	}
	body = `{"safe":false,"reason":"açık vardiya var"}`
	if r, err := g.Unsafe(ctx); err != nil || r != "açık vardiya var" {
		t.Fatalf("güvensiz: %q %v", r, err)
	}
	body = `{"safe":false}`
	if r, _ := g.Unsafe(ctx); r == "" {
		t.Fatal("nedensiz güvensiz yanıt güvenli sayıldı")
	}
	// Uncertainty is not counted as safe: it returns an error.
	code = 500
	if _, err := g.Unsafe(ctx); err == nil {
		t.Fatal("sunucu hatası yutuldu")
	}
	code, body = 200, `bozuk`
	if _, err := g.Unsafe(ctx); err == nil {
		t.Fatal("bozuk yanıt yutuldu")
	}
	srv.Close()
	if _, err := g.Unsafe(ctx); err == nil {
		t.Fatal("ulaşılamayan kapı güvenli sayıldı")
	}
}
