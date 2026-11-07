// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package mirror is the authorization service of the registry mirror (Zot).
//
// Docker/OCI clients go to the "token" endpoint when pulling an image from a registry.
// This package serves that endpoint: the client gives the abarow token as the password, the
// service verifies the token with the publisher root (offline) and, according to the token's
// update level, signs a short-lived registry token for the requested repositories.
// Zot verifies this token with the service's public certificate.
//
// Channel access is provided by a prefix in the repository name (it cannot be done with tags,
// since tags are not part of the authorization scope): the images sit in "<channel>/<name>"
// repositories and a level can pull from these channels:
//
//	test  → test/
//	pilot → pilot/ and prod/
//	prod  → prod/
//
// Only pull permission is given.
package mirror

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"abarow.com/pkg/httpjson"
	"abarow.com/services/update/internal/token"
)

// Channels are the channel repository prefixes a update level can pull from.
func Channels(level token.Level) []string {
	switch level {
	case token.Test:
		return []string{"test"}
	case token.Pilot:
		return []string{"pilot", "prod"}
	case token.Prod:
		return []string{"prod"}
	}
	return nil
}

// PullAllowed says that the level can pull from the repository. The repository name is of the form
// "<channel>/<name>"; names that escape the channel prefix (like "..") are rejected.
func PullAllowed(level token.Level, repo string) bool {
	if strings.Contains(repo, "..") || strings.Contains(repo, "//") || strings.HasPrefix(repo, "/") {
		return false
	}
	channel, label, ok := strings.Cut(repo, "/")
	if !ok || label == "" {
		return false
	}
	for _, c := range Channels(level) {
		if channel == c {
			return true
		}
	}
	return false
}

// Writer is the write identity of the publisher's CI for the registry. The password is a long
// random secret (e.g. 32 bytes); only the SHA-256 digest is kept at the server
// (not suitable for low-entropy passwords, which is why it is a token, not a password).
type Writer struct {
	User   string
	SHA256 [sha256.Size]byte
}

// WriteAllowed says which repositories the write identity can push to: only the
// test/, pilot/ and prod/ paths and a safe repository name.
func WriteAllowed(repo string) bool {
	if strings.Contains(repo, "..") || strings.Contains(repo, "//") || strings.HasPrefix(repo, "/") {
		return false
	}
	channel, label, ok := strings.Cut(repo, "/")
	if !ok || label == "" {
		return false
	}
	switch channel {
	case "test", "pilot", "prod":
		return true
	}
	return false
}

// Key is the key that signs the registry tokens.
type Key struct {
	priv *ecdsa.PrivateKey
	// CertPEM is the certificate Zot uses to verify the tokens.
	CertPEM []byte
	kid     string
}

// GenerateKey generates a new ES256 key and its self-signed certificate.
func GenerateKey(now time.Time) (*Key, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(now.UnixNano()),
		Subject:               pkix.Name{CommonName: "abarow mirror token signing"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	k, err := ParseKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), cert)
	return k, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), err
}

// ParseKey builds the signer from the PEM key and certificate.
func ParseKey(keyPEM, certPEM []byte) (*Key, error) {
	b, _ := pem.Decode(keyPEM)
	if b == nil {
		return nil, errors.New("the mirror key is not PEM")
	}
	pk, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := pk.(*ecdsa.PrivateKey)
	if !ok || priv.Curve != elliptic.P256() {
		return nil, errors.New("the mirror key must be P-256")
	}
	cb, _ := pem.Decode(certPEM)
	if cb == nil {
		return nil, errors.New("the mirror certificate is not PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	if pub, ok := cert.PublicKey.(*ecdsa.PublicKey); !ok || !pub.Equal(&priv.PublicKey) {
		return nil, errors.New("the mirror certificate does not match the key")
	}
	sum := sha256.Sum256(cert.Raw)
	return &Key{priv: priv, CertPEM: certPEM, kid: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

// Service serves the token endpoint.
type Service struct {
	root    ed25519.PublicKey
	key     *Key
	service string
	issuer  string
	ttl     time.Duration
	now     func() time.Time
	writer  *Writer
}

// Config is the service's settings.
type Config struct {
	// Root is the publisher's root public key (the tokens are verified with it).
	Root ed25519.PublicKey
	Key  *Key
	// Service is the registry's "service" name (the same as bearer.service in Zot's configuration).
	Service string
	// Writer: if given, lets CI get a read and write token for the test/, pilot/, prod/ paths with this username and token digest.
	Writer *Writer
	// TTL is the token lifetime; 5 minutes if zero.
	TTL time.Duration
	Now func() time.Time
}

// New, hizmeti kurar.
func New(c Config) (*Service, error) {
	if len(c.Root) != ed25519.PublicKeySize || c.Key == nil || c.Service == "" {
		return nil, errors.New("the mirror service settings are missing")
	}
	ttl := c.TTL
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	return &Service{root: c.Root, key: c.Key, service: c.Service, issuer: "abarow-mirror", ttl: ttl, now: now, writer: c.Writer}, nil
}

type access struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

type claims struct {
	jwt.Claims
	Access []access `json:"access"`
}

// Handler returns the GET /token endpoint.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /token", s.token)
	return mux
}

func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="abarow-mirror"`)
	httpjson.Error(w, http.StatusUnauthorized, msg)
}

func (s *Service) token(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("service") != s.service {
		httpjson.Error(w, http.StatusBadRequest, "invalid service")
		return
	}
	user, pass, ok := r.BasicAuth()
	if !ok || pass == "" {
		unauthorized(w, "identity required: give the abarow token as the password")
		return
	}
	now := s.now()
	// Write identity (CI): if the username matches it is verified only by this route, it does not fall back to the abarow token.
	var (
		c       token.Claims
		isCI    bool
		subject string
	)
	if s.writer != nil && user == s.writer.User {
		sum := sha256.Sum256([]byte(pass))
		if subtle.ConstantTimeCompare(sum[:], s.writer.SHA256[:]) != 1 {
			unauthorized(w, "invalid identity")
			return
		}
		isCI, subject = true, "ci:"+s.writer.User
	} else {
		var err error
		c, err = token.Verify(s.root, pass, now)
		if err != nil || !c.Allows(token.ScopePull) {
			unauthorized(w, "invalid token")
			return
		}
		subject = c.Device
	}

	var granted []access
	for _, scope := range q["scope"] {
		typ, rest, ok := strings.Cut(scope, ":")
		if !ok || typ != "repository" {
			continue
		}
		i := strings.LastIndex(rest, ":")
		if i < 0 {
			continue
		}
		name, actions := rest[:i], strings.Split(rest[i+1:], ",")
		var ok2 []string
		for _, a := range actions {
			switch {
			case isCI && (a == "pull" || a == "push") && WriteAllowed(name):
				ok2 = append(ok2, a)
			case !isCI && a == "pull" && PullAllowed(c.Level, name):
				ok2 = append(ok2, a)
			}
		}
		if len(ok2) > 0 {
			granted = append(granted, access{Type: "repository", Name: name, Actions: ok2})
		}
	}

	sg, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: jose.JSONWebKey{Key: s.key.priv, KeyID: s.key.kid}}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	idb := make([]byte, 12)
	_, _ = rand.Read(idb)
	raw, err := jwt.Signed(sg).Claims(claims{
		Claims: jwt.Claims{
			Issuer: s.issuer, Subject: subject, Audience: jwt.Audience{s.service},
			IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)), Expiry: jwt.NewNumericDate(now.Add(s.ttl)),
			ID: base64.RawURLEncoding.EncodeToString(idb),
		},
		Access: granted,
	}).Serialize()
	if err != nil {
		httpjson.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token": raw, "access_token": raw, "expires_in": int(s.ttl.Seconds()), "issued_at": now.UTC().Format(time.RFC3339),
	})
}

// String is for debugging.
func (a access) String() string {
	return fmt.Sprintf("%s:%s:%s", a.Type, a.Name, strings.Join(a.Actions, ","))
}
