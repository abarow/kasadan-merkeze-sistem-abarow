// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package httpclient is the HTTP client the agent uses to connect to the registration
// server's device API (it implements agent.Transport). The server certificate is pinned to the given CA;
// plain HTTP is used only if explicitly allowed (a test environment).
package httpclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"abarow.com/services/update/internal/adapters/in/httpapi"
	"abarow.com/services/update/internal/channel"
	"abarow.com/services/update/internal/registration"
)

// maxResponse is the response body limit.
const maxResponse = 64 << 10

// Options are the client's settings.
type Options struct {
	// BaseURL is the registration server's device API address (https://...).
	BaseURL string
	// CAPEM is the CA that signed the server certificate (PEM); the system roots are used if empty.
	CAPEM []byte
	// AllowInsecure allows plain HTTP (a test environment only).
	AllowInsecure bool
	// Timeout is the request timeout; 15 s if zero.
	Timeout time.Duration
}

// Client is the registration server client.
type Client struct {
	base string
	http *http.Client
}

// NewTransport builds a transport with TLS pinned by the settings and returns the cleaned
// base address (shared by the agent client and the proxy on the store server).
func NewTransport(o Options) (*http.Transport, string, error) {
	base := strings.TrimRight(o.BaseURL, "/")
	switch {
	case strings.HasPrefix(base, "https://"):
	case strings.HasPrefix(base, "http://") && o.AllowInsecure:
	default:
		return nil, "", errors.New("kayıt sunucusu adresi https:// ile başlamalı")
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if len(o.CAPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(o.CAPEM) {
			return nil, "", errors.New("CA sertifikası okunamadı")
		}
		tc.RootCAs = pool
	}
	return &http.Transport{TLSClientConfig: tc}, base, nil
}

// New, istemciyi kurar.
func New(o Options) (*Client, error) {
	tr, base, err := NewTransport(o)
	if err != nil {
		return nil, err
	}
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	return &Client{base: base, http: &http.Client{Timeout: timeout, Transport: tr}}, nil
}

// codes maps the server's error codes to registration errors.
var codes = map[string]error{
	httpapi.CodeBadRequest: registration.ErrBadRequest,
	httpapi.CodeNotFound:   registration.ErrNotFound,
	httpapi.CodePending:    registration.ErrPending,
	httpapi.CodeRejected:   registration.ErrRejected,
	httpapi.CodeRevoked:    registration.ErrRevoked,
	httpapi.CodeKeyChanged: registration.ErrKeyChanged,
	httpapi.CodeTooMany:    registration.ErrTooMany,
	httpapi.CodeWrongCode:  registration.ErrWrongCode,
	httpapi.CodeChallenge:  registration.ErrNoChallenge,
	httpapi.CodeConflict:   registration.ErrConflict,
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e httpapi.ErrorJSON
		if json.Unmarshal(raw, &e) == nil {
			if sentinel, ok := codes[e.Code]; ok {
				return fmt.Errorf("%w: %s", sentinel, e.Error)
			}
		}
		return fmt.Errorf("kayıt sunucusu %d döndü", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Submit sends the registration request and returns the state at the server.
func (c *Client) Submit(ctx context.Context, s registration.Submission) (registration.Status, error) {
	var res httpapi.StatusJSON
	err := c.do(ctx, http.MethodPost, "/v1/registrations", httpapi.SubmitJSON{
		Device: s.Device, PublicKey: enc(s.Public), SigningKey: enc(s.SigningKey), Fingerprint: s.Fingerprint,
		Tier: string(s.Tier), Level: string(s.Level), Store: s.Store, Register: s.Register, Name: s.Name, Signature: enc(s.Signature),
	}, &res)
	return registration.Status(res.Status), err
}

// Status returns the registration's status.
func (c *Client) Status(ctx context.Context, device string) (registration.Status, error) {
	var res httpapi.StatusJSON
	err := c.do(ctx, http.MethodGet, "/v1/registrations/"+device, nil, &res)
	return registration.Status(res.Status), err
}

// Challenge, meydan okuma ister.
func (c *Client) Challenge(ctx context.Context, device string) (string, error) {
	var res httpapi.ChallengeResponse
	err := c.do(ctx, http.MethodPost, "/v1/challenge", httpapi.ChallengeRequest{Device: device}, &res)
	return res.Nonce, err
}

// Token asks for a token with a signed challenge.
func (c *Client) Token(ctx context.Context, device, nonce string, sig []byte) (registration.TokenResult, error) {
	var res httpapi.TokenResponse
	err := c.do(ctx, http.MethodPost, "/v1/token", httpapi.TokenRequest{Device: device, Nonce: nonce, Signature: enc(sig)}, &res)
	return registration.TokenResult{Token: res.Token, Expiry: res.ExpiresAt, Cert: res.Cert}, err
}

// Report sends the signed status report.
func (c *Client) Report(ctx context.Context, device string, ts time.Time, payload, sig []byte) error {
	return c.do(ctx, http.MethodPost, "/v1/report", httpapi.ReportRequest{Device: device, Timestamp: ts.UTC(), Payload: enc(payload), Signature: enc(sig)}, nil)
}

// Fetch fetches the channel's signed manifest (release.Source). The caller verifies the
// manifest's signature; this client only carries it.
func (c *Client) Fetch(ctx context.Context, name string) (channel.Signed, error) {
	var s channel.Signed
	err := c.do(ctx, http.MethodGet, "/v1/channels/"+name, nil, &s)
	return s, err
}

// enc writes the binary value as base64url (unpadded); an empty value stays empty.
func enc(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
