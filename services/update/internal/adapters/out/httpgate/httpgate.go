// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package httpgate is the HTTP implementation of release.Gate: it asks the unit's screen service
// (gateway at the register) whether the update is at a safe moment. The update is not applied while
// a shift is open at the register, since sales may be going on.
package httpgate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"abarow.com/pkg/login"
)

// Answer is the screen service's response.
type Answer struct {
	Safe   bool   `json:"safe"`
	Reason string `json:"reason,omitempty"`
}

// Gate is the HTTP gate.
type Gate struct {
	URL    string
	Secret string
	Client *http.Client
}

// Unsafe returns the reason if it is not a safe moment. If the screen service does not answer it
// returns an error and the update is not applied (uncertainty is not counted as safe).
func (g Gate) Unsafe(ctx context.Context) (string, error) {
	c := g.Client
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.URL, nil)
	if err != nil {
		return "", err
	}
	if g.Secret != "" {
		req.Header.Set(login.UpdateSecretHeader, g.Secret)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("güvenli an sorusu %d döndü", resp.StatusCode)
	}
	var a Answer
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if a.Safe {
		return "", nil
	}
	if a.Reason == "" {
		return "güvenli an değil", nil
	}
	return a.Reason, nil
}
