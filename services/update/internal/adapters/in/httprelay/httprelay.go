// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package httprelay is the proxy that the agent on the store server offers to the registers.
//
// A register can talk only to the store server; it cannot reach headquarters' registration
// server directly. The proxy forwards the register's device API requests (registration, status,
// challenge, token) to headquarters. The requests are device-signed and the approval is at
// headquarters; the proxy makes no decision itself, it can only delay or drop.
// The token carries headquarters' signature, the proxy cannot forge one.
//
// The proxy is kept narrow: only six endpoints are forwarded (the admin endpoints are not),
// request headers are dropped (including the person headers), the body and response size is
// limited, and the number of requests forwarded concurrently is limited.
package httprelay

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"

	"abarow.com/pkg/httpjson"
	"abarow.com/services/update/internal/adapters/in/httpapi"
)

const (
	maxRequest  = 16 << 10
	maxResponse = 64 << 10
	// DefaultMaxInFlight is the limit of requests forwarded to headquarters at the same time.
	DefaultMaxInFlight = 16
	// CodeUpstream is the error code that says headquarters could not be reached.
	CodeUpstream = "upstream_unavailable"
)

// Options are the proxy's settings.
type Options struct {
	// Upstream is the base address of headquarters' registration server device API.
	Upstream string
	// Transport is the transport to headquarters (its certificate must be pinned).
	Transport http.RoundTripper
	// MaxInFlight is the limit of requests forwarded at the same time; DefaultMaxInFlight if 0.
	MaxInFlight int
}

// New returns the proxy.
func New(o Options) (http.Handler, error) {
	target, err := url.Parse(o.Upstream)
	if err != nil || target.Host == "" || (target.Scheme != "https" && target.Scheme != "http") {
		return nil, errors.New("vekil: merkez adresi geçersiz")
	}
	if o.Transport == nil {
		return nil, errors.New("vekil: taşıyıcı gerekli")
	}
	n := o.MaxInFlight
	if n <= 0 {
		n = DefaultMaxInFlight
	}
	sem := make(chan struct{}, n)
	rp := &httputil.ReverseProxy{
		Transport: o.Transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// Only the body's content type is forwarded: the person headers, cookies and authorization headers are dropped.
			ct := pr.In.Header.Get("Content-Type")
			pr.Out.Header = http.Header{}
			if ct != "" {
				pr.Out.Header.Set("Content-Type", ct)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Body = http.MaxBytesReader(nil, resp.Body, maxResponse)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			httpjson.Write(w, http.StatusBadGateway, httpapi.ErrorJSON{Error: "merkeze ulaşılamıyor", Code: CodeUpstream})
		},
	}

	forward := func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxRequest {
			httpjson.Write(w, http.StatusRequestEntityTooLarge, httpapi.ErrorJSON{Error: "istek çok büyük", Code: httpapi.CodeBadRequest})
			return
		}
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		default:
			httpjson.Write(w, http.StatusTooManyRequests, httpapi.ErrorJSON{Error: "vekil meşgul", Code: httpapi.CodeTooMany})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRequest)
		rp.ServeHTTP(w, r)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/registrations", forward)
	mux.HandleFunc("GET /v1/registrations/{device}", forward)
	mux.HandleFunc("POST /v1/challenge", forward)
	mux.HandleFunc("POST /v1/token", forward)
	mux.HandleFunc("POST /v1/report", forward)
	// Signed channel manifest: the register learns of the new version through the store server too.
	mux.HandleFunc("GET /v1/channels/{channel}", forward)
	return mux, nil
}
