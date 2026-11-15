// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"abarow.com/pkg/httpjson"
	"abarow.com/pkg/login"
	"abarow.com/services/update/internal/agent"
	"abarow.com/services/update/internal/release"
)

// Error codes of the new release flow.
const (
	CodeNoOffer = "no_offer"
	CodeStale   = "stale"
	CodeBusy    = "busy"
	CodeBlocked = "blocked"
)

// maxChannel is the channel manifest file limit.
const maxChannel = 64 << 10

// DeviceOption adds optional endpoints to the device API.
type DeviceOption func(*http.ServeMux)

// WithChannels serves the signed channel manifests from the dir/<channel>.json files
// (GET /v1/channels/{channel}). The manifests are public: the channel signature protects
// their integrity, they are not secret; the images themselves are protected in the registry
// with a token. At the publisher the promotion workflow writes the file, at headquarters the
// headquarters agent polls and writes the manifest it verified.
func WithChannels(dir string) DeviceOption {
	return func(mux *http.ServeMux) {
		mux.HandleFunc("GET /v1/channels/{channel}", func(w http.ResponseWriter, r *http.Request) {
			switch c := r.PathValue("channel"); c {
			case "test", "pilot", "prod":
				raw, err := os.ReadFile(filepath.Join(dir, c+".json"))
				if err != nil || len(raw) > maxChannel {
					httpjson.Write(w, http.StatusNotFound, ErrorJSON{Error: "bu kanalda bildirim yok", Code: CodeNotFound})
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				_, _ = w.Write(raw)
			default:
				httpjson.Write(w, http.StatusNotFound, ErrorJSON{Error: "bilinmeyen kanal", Code: CodeNotFound})
			}
		})
	}
}

// Updates is the agent's new release flow (release.Updater).
type Updates interface {
	Status() release.Status
	Apply(ctx context.Context, by string, seq uint64) error
	Skip(by string) error
	Snooze(by string, d time.Duration) error
}

// AgentOption adds the new release endpoints to the agent's local API.
type AgentOption func(*agentConfig)

type agentConfig struct {
	updates Updates
	secret  string
}

// WithUpdates connects the new release endpoints. If secret is set the write requests
// must carry the shared secret key the screen service puts (login.UpdateSecretHeader). A write
// request must also carry the person the screen service verified (the sign-in headers): the
// name of this person is written to the approval record.
func WithUpdates(u Updates, secret string) AgentOption {
	return func(c *agentConfig) { c.updates, c.secret = u, secret }
}

// AgentStatusJSON is the agent's status response: the registration status and (if installed) the new release flow.
type AgentStatusJSON struct {
	agent.Status
	Update *release.Status `json:"update,omitempty"`
}

// ApplyRequest is the approval request; Seq is the version the approver saw on the screen.
type ApplyRequest struct {
	Seq uint64 `json:"seq"`
}

// SnoozeRequest is the request to postpone the reminder.
type SnoozeRequest struct {
	Minutes int `json:"minutes"`
}

func (c agentConfig) mount(mux *http.ServeMux) {
	if c.updates == nil {
		return
	}
	write := func(h func(w http.ResponseWriter, r *http.Request, who string)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if c.secret != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get(login.UpdateSecretHeader)), []byte(c.secret)) != 1 {
				httpjson.Write(w, http.StatusForbidden, ErrorJSON{Error: "onay isteği yalnızca ekran servisinden kabul edilir", Code: CodeForbidden})
				return
			}
			p, ok := login.PersonFrom(r.Header)
			if !ok {
				httpjson.Write(w, http.StatusUnauthorized, ErrorJSON{Error: "onaylayan kişi bilinmiyor", Code: CodeForbidden})
				return
			}
			h(w, r, p.Username)
		}
	}
	mux.HandleFunc("POST /api/update/apply", write(func(w http.ResponseWriter, r *http.Request, who string) {
		var in ApplyRequest
		if !decode(w, r, &in) {
			return
		}
		if err := c.updates.Apply(r.Context(), who, in.Seq); err != nil {
			failUpdate(w, err)
			return
		}
		httpjson.Write(w, http.StatusAccepted, c.updates.Status())
	}))
	mux.HandleFunc("POST /api/update/skip", write(func(w http.ResponseWriter, r *http.Request, who string) {
		var in struct{}
		if !decode(w, r, &in) {
			return
		}
		if err := c.updates.Skip(who); err != nil {
			failUpdate(w, err)
			return
		}
		httpjson.Write(w, http.StatusOK, c.updates.Status())
	}))
	mux.HandleFunc("POST /api/update/snooze", write(func(w http.ResponseWriter, r *http.Request, who string) {
		var in SnoozeRequest
		if !decode(w, r, &in) {
			return
		}
		if in.Minutes == 0 {
			in.Minutes = 60
		}
		if in.Minutes < 5 || in.Minutes > 7*24*60 {
			bad(w, "erteleme 5 dakika ile 7 gün arasında olmalı")
			return
		}
		if err := c.updates.Snooze(who, time.Duration(in.Minutes)*time.Minute); err != nil {
			failUpdate(w, err)
			return
		}
		httpjson.Write(w, http.StatusOK, c.updates.Status())
	}))
}

func failUpdate(w http.ResponseWriter, err error) {
	var be release.BlockedError
	switch {
	case errors.As(err, &be):
		httpjson.Write(w, http.StatusConflict, ErrorJSON{Error: be.Error(), Code: CodeBlocked})
	case errors.Is(err, release.ErrNoOffer):
		httpjson.Write(w, http.StatusNotFound, ErrorJSON{Error: err.Error(), Code: CodeNoOffer})
	case errors.Is(err, release.ErrStale):
		httpjson.Write(w, http.StatusConflict, ErrorJSON{Error: err.Error(), Code: CodeStale})
	case errors.Is(err, release.ErrBusy):
		httpjson.Write(w, http.StatusConflict, ErrorJSON{Error: err.Error(), Code: CodeBusy})
	default:
		httpjson.Write(w, http.StatusInternalServerError, ErrorJSON{Error: err.Error(), Code: CodeInternal})
	}
}
