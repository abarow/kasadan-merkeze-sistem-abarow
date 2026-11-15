// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package httpapi is the HTTP driving adapter of the registration service (registry.Service).
//
// There are two separate APIs, served on separate ports:
//
//   - The device API (NewDevice): used by the units' agents, open to the network.
//     Authentication is the device signature; there is no session or cookie.
//   - The admin API (NewAdmin): used by the approver's screen. The session is checked
//     by the service that serves the screen (reporting at headquarters), which passes
//     the person on in headers (pkg/login). This port is not exposed outside: only the screen
//     service may put the header.
package httpapi

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"abarow.com/pkg/httpjson"
	"abarow.com/pkg/login"
	"abarow.com/services/update/internal/agent"
	"abarow.com/services/update/internal/registration"
	"abarow.com/services/update/internal/token"
)

// maxBody is the request body limit.
const maxBody = 16 << 10

// AdminRole is the role the admin API requires.
const AdminRole = "admin"

var enc = base64.RawURLEncoding

// SubmitJSON is the body of the registration request (the agent uses it too). Binary fields
// are base64url (unpadded) encoded.
type SubmitJSON struct {
	Device      string `json:"device"`
	PublicKey   string `json:"public_key"`
	SigningKey  string `json:"signing_key,omitempty"`
	Fingerprint string `json:"fingerprint"`
	Tier        string `json:"tier"`
	Level       string `json:"level"`
	Store       string `json:"store,omitempty"`
	Register    string `json:"register,omitempty"`
	Name        string `json:"name,omitempty"`
	Signature   string `json:"signature"`
}

// StatusJSON is the registration status response.
type StatusJSON struct {
	Status string `json:"status"`
}

// ChallengeRequest and ChallengeResponse are the challenge endpoints.
type ChallengeRequest struct {
	Device string `json:"device"`
}

// ChallengeResponse is the challenge response.
type ChallengeResponse struct {
	Nonce string `json:"nonce"`
}

// TokenRequest is the token request.
type TokenRequest struct {
	Device    string `json:"device"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
}

// TokenResponse is the token response.
type TokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	Cert      string    `json:"cert,omitempty"`
}

// ErrorJSON is the error response; Code is machine-readable.
type ErrorJSON struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// Codes are the ErrorJSON.Code values.
const (
	CodeBadRequest = "bad_request"
	CodeNotFound   = "not_found"
	CodePending    = "pending"
	CodeRejected   = "rejected"
	CodeRevoked    = "revoked"
	CodeKeyChanged = "key_changed"
	CodeTooMany    = "too_many"
	CodeWrongCode  = "wrong_code"
	CodeChallenge  = "no_challenge"
	CodeConflict   = "conflict"
	CodeForbidden  = "forbidden"
	CodeInternal   = "internal"
)

// fail turns a registration error into an HTTP status and a code. The details of an
// unexpected error do not go to the client.
func fail(w http.ResponseWriter, err error) {
	code, status := CodeInternal, http.StatusInternalServerError
	switch {
	case errors.Is(err, registration.ErrBadRequest):
		code, status = CodeBadRequest, http.StatusBadRequest
	case errors.Is(err, registration.ErrNotFound):
		code, status = CodeNotFound, http.StatusNotFound
	case errors.Is(err, registration.ErrPending):
		code, status = CodePending, http.StatusConflict
	case errors.Is(err, registration.ErrRejected):
		code, status = CodeRejected, http.StatusForbidden
	case errors.Is(err, registration.ErrRevoked):
		code, status = CodeRevoked, http.StatusForbidden
	case errors.Is(err, registration.ErrKeyChanged):
		code, status = CodeKeyChanged, http.StatusConflict
	case errors.Is(err, registration.ErrTooMany):
		code, status = CodeTooMany, http.StatusTooManyRequests
	case errors.Is(err, registration.ErrWrongCode):
		code, status = CodeWrongCode, http.StatusForbidden
	case errors.Is(err, registration.ErrNoChallenge):
		code, status = CodeChallenge, http.StatusBadRequest
	case errors.Is(err, registration.ErrConflict):
		code, status = CodeConflict, http.StatusConflict
	}
	msg := err.Error()
	if code == CodeInternal {
		msg = "iç hata"
	}
	httpjson.Write(w, status, ErrorJSON{Error: msg, Code: code})
}

func bad(w http.ResponseWriter, msg string) {
	httpjson.Write(w, http.StatusBadRequest, ErrorJSON{Error: msg, Code: CodeBadRequest})
}

// decode reads the JSON body: only application/json, limited size, no unknown fields.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if !login.JSONRequest(r) {
		httpjson.Write(w, http.StatusUnsupportedMediaType, ErrorJSON{Error: "içerik türü application/json olmalı", Code: CodeBadRequest})
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		bad(w, "istek gövdesi geçersiz")
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		bad(w, "istek gövdesinde fazladan veri var")
		return false
	}
	return true
}

func decodeKey(s string) (ed25519.PublicKey, bool) {
	if s == "" {
		return nil, true
	}
	b, err := enc.DecodeString(s)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, false
	}
	return b, true
}

type device struct{ svc *registration.Service }

// NewDevice returns the device API.
func NewDevice(svc *registration.Service, opts ...DeviceOption) http.Handler {
	h := &device{svc: svc}
	mux := http.NewServeMux()
	for _, o := range opts {
		o(mux)
	}
	mux.HandleFunc("POST /v1/registrations", h.submit)
	mux.HandleFunc("GET /v1/registrations/{device}", h.status)
	mux.HandleFunc("POST /v1/challenge", h.challenge)
	mux.HandleFunc("POST /v1/token", h.token)
	mux.HandleFunc("POST /v1/report", h.report)
	return mux
}

func (h *device) submit(w http.ResponseWriter, r *http.Request) {
	var in SubmitJSON
	if !decode(w, r, &in) {
		return
	}
	pub, ok1 := decodeKey(in.PublicKey)
	sign, ok2 := decodeKey(in.SigningKey)
	sig, err := enc.DecodeString(in.Signature)
	if !ok1 || !ok2 || err != nil {
		bad(w, "anahtar ya da imza biçimi geçersiz")
		return
	}
	res, err := h.svc.Submit(r.Context(), registration.Submission{
		Device: in.Device, Public: pub, SigningKey: sign, Fingerprint: in.Fingerprint,
		Tier: token.Tier(in.Tier), Level: token.Level(in.Level), Store: in.Store, Register: in.Register, Name: in.Name, Signature: sig,
	})
	if err != nil {
		fail(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, StatusJSON{Status: string(res.Status)})
}

func (h *device) status(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Get(r.Context(), r.PathValue("device"))
	if err != nil {
		fail(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, StatusJSON{Status: string(res.Status)})
}

func (h *device) challenge(w http.ResponseWriter, r *http.Request) {
	var in ChallengeRequest
	if !decode(w, r, &in) {
		return
	}
	nonce, err := h.svc.Challenge(r.Context(), in.Device)
	if err != nil {
		fail(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, ChallengeResponse{Nonce: nonce})
}

// ReportRequest is the status report. Payload is the base64url encoding of the JSON form of
// registry.Report; the signature is over registry.ReportMessage.
type ReportRequest struct {
	Device    string    `json:"device"`
	Timestamp time.Time `json:"timestamp"`
	Payload   string    `json:"payload"`
	Signature string    `json:"signature"`
}

func (h *device) report(w http.ResponseWriter, r *http.Request) {
	var in ReportRequest
	if !decode(w, r, &in) {
		return
	}
	payload, err1 := enc.DecodeString(in.Payload)
	sig, err2 := enc.DecodeString(in.Signature)
	if err1 != nil || err2 != nil {
		bad(w, "bildirim biçimi geçersiz")
		return
	}
	if err := h.svc.SubmitReport(r.Context(), registration.ReportInput{Device: in.Device, Time: in.Timestamp, Payload: payload, Signature: sig}); err != nil {
		fail(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, StatusJSON{Status: "ok"})
}

func (h *device) token(w http.ResponseWriter, r *http.Request) {
	var in TokenRequest
	if !decode(w, r, &in) {
		return
	}
	sig, err := enc.DecodeString(in.Signature)
	if err != nil {
		bad(w, "imza biçimi geçersiz")
		return
	}
	res, err := h.svc.Token(r.Context(), in.Device, in.Nonce, sig)
	if err != nil {
		fail(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, TokenResponse{Token: res.Token, ExpiresAt: res.Expiry.UTC(), Cert: res.Cert})
}

// RegistrationJSON is a registration in the admin list (keys and the approval code are not exposed).
type RegistrationJSON struct {
	Device    string     `json:"device"`
	Tier      string     `json:"tier"`
	Level     string     `json:"level"`
	Store     string     `json:"store,omitempty"`
	Register  string     `json:"register,omitempty"`
	Name      string     `json:"name,omitempty"`
	Status    string     `json:"status"`
	Attempts  int        `json:"attempts"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
	DecidedBy string     `json:"decided_by,omitempty"`
	// Report is the unit's latest status report (the running version and digests); absent if it never reported.
	Report *registration.Report `json:"report,omitempty"`
}

// ListJSON is the admin list response.
type ListJSON struct {
	Registrations []RegistrationJSON `json:"registrations"`
}

// ApproveRequest is the approval request; Code is the code on the unit's own screen.
type ApproveRequest struct {
	Code string `json:"code"`
}

type admin struct{ svc *registration.Service }

// NewAdmin returns the admin API.
func NewAdmin(svc *registration.Service) http.Handler {
	h := &admin{svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/update/registrations", h.auth(h.list))
	mux.HandleFunc("POST /api/update/registrations/{device}/approve", h.auth(h.approve))
	mux.HandleFunc("POST /api/update/registrations/{device}/reject", h.auth(h.reject))
	mux.HandleFunc("POST /api/update/registrations/{device}/revoke", h.auth(h.revoke))
	return mux
}

// auth reads the person passed on by the screen service; only the admin role passes.
func (h *admin) auth(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := login.PersonFrom(r.Header)
		if !ok {
			httpjson.Write(w, http.StatusUnauthorized, ErrorJSON{Error: "oturum yok", Code: CodeForbidden})
			return
		}
		if p.Role != AdminRole {
			httpjson.Write(w, http.StatusForbidden, ErrorJSON{Error: "bu işlem için yönetici yetkisi gerekir", Code: CodeForbidden})
			return
		}
		if r.Method != http.MethodGet && !login.JSONRequest(r) {
			httpjson.Write(w, http.StatusUnsupportedMediaType, ErrorJSON{Error: "içerik türü application/json olmalı", Code: CodeBadRequest})
			return
		}
		who := p.Username
		if p.Name != "" {
			who += " (" + p.Name + ")"
		}
		next(w, r, who)
	}
}

func (h *admin) list(w http.ResponseWriter, r *http.Request, _ string) {
	status := registration.Status(r.URL.Query().Get("status"))
	if status == "" {
		status = registration.Pending
	}
	switch status {
	case registration.Pending, registration.Approved, registration.Rejected, registration.Revoked:
	default:
		bad(w, "durum geçersiz")
		return
	}
	list, err := h.svc.List(r.Context(), status)
	if err != nil {
		fail(w, err)
		return
	}
	out := ListJSON{Registrations: []RegistrationJSON{}}
	for _, x := range list {
		j := RegistrationJSON{
			Device: x.Device, Tier: string(x.Tier), Level: string(x.Level), Store: x.Store, Register: x.Register, Name: x.Name,
			Status: string(x.Status), Attempts: x.Attempts, CreatedAt: x.CreatedAt.UTC(), DecidedBy: x.DecidedBy,
		}
		if !x.DecidedAt.IsZero() {
			t := x.DecidedAt.UTC()
			j.DecidedAt = &t
		}
		j.Report = x.Report
		out.Registrations = append(out.Registrations, j)
	}
	httpjson.Write(w, http.StatusOK, out)
}

func (h *admin) approve(w http.ResponseWriter, r *http.Request, who string) {
	var in ApproveRequest
	if !decode(w, r, &in) {
		return
	}
	if err := h.svc.Approve(r.Context(), who, r.PathValue("device"), in.Code); err != nil {
		fail(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, StatusJSON{Status: string(registration.Approved)})
}

func (h *admin) reject(w http.ResponseWriter, r *http.Request, who string) {
	if err := h.svc.Reject(r.Context(), who, r.PathValue("device")); err != nil {
		fail(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, StatusJSON{Status: string(registration.Rejected)})
}

func (h *admin) revoke(w http.ResponseWriter, r *http.Request, who string) {
	if err := h.svc.Revoke(r.Context(), who, r.PathValue("device")); err != nil {
		fail(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, StatusJSON{Status: string(registration.Revoked)})
}

// StatusSource is the port that gives the agent's status.
type StatusSource interface{ Status() agent.Status }

// NewAgent returns the agent's local status API (GET /api/update/status): the unit's
// screen reads the approval code and the status from here; the new release endpoints (apply, skip,
// snooze) are added with WithUpdates (the screen service verifies the person and passes the
// request on; see pkg/report.Approver). It contains no token or key; the port is not exposed outside.
func NewAgent(src StatusSource, opts ...AgentOption) http.Handler {
	var cfg agentConfig
	for _, o := range opts {
		o(&cfg)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/update/status", func(w http.ResponseWriter, _ *http.Request) {
		out := AgentStatusJSON{Status: src.Status()}
		if cfg.updates != nil {
			st := cfg.updates.Status()
			out.Update = &st
		}
		httpjson.Write(w, http.StatusOK, out)
	})
	cfg.mount(mux)
	return mux
}
