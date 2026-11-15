// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package registration manages the registration of units (register, store server,
// headquarters) with the update source and their getting an image pull token.
//
// Flow: the unit asks for registration with its device identity (Submit); the registration stays pending.
// An authorized person approves the registration by entering the approval code
// the unit shows on its own screen (Approve). After approval the unit gets a token (Token)
// by signing a single-use challenge (Challenge) with its device key. A unit that is revoked
// (Revoke) cannot get a new token.
//
// The service works as a tier's approver: at headquarters it approves the store server and register
// registrations, in the publisher's update center the headquarters registrations.
// A headquarters registration also reports headquarters' token signing key; the publisher
// gives the approved headquarters a certificate for this key.
package registration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"abarow.com/services/update/internal/identity"
	"abarow.com/services/update/internal/token"
)

// Error values.
var (
	ErrNotFound    = errors.New("kayıt bulunamadı")
	ErrExists      = errors.New("kayıt zaten var")
	ErrConflict    = errors.New("kayıt aynı anda değiştirildi, yeniden deneyin")
	ErrBadRequest  = errors.New("kayıt isteği geçersiz")
	ErrKeyChanged  = errors.New("bu cihaz kimliği başka bir anahtarla zaten kayıtlı")
	ErrTooMany     = errors.New("çok fazla onay bekleyen kayıt var")
	ErrPending     = errors.New("kayıt onay bekliyor")
	ErrRejected    = errors.New("kayıt reddedildi")
	ErrRevoked     = errors.New("kayıt iptal edildi")
	ErrWrongCode   = errors.New("onay kodu yanlış")
	ErrNoChallenge = errors.New("geçerli meydan okuma yok")
)

// Status is the registration's status.
type Status string

// Durumlar.
const (
	Pending  Status = "pending"
	Approved Status = "approved"
	Rejected Status = "rejected"
	Revoked  Status = "revoked"
)

// MaxCodeAttempts is the number of wrong approval codes allowed on a registration; if exceeded the registration is rejected.
const MaxCodeAttempts = 5

// DefaultMaxPending is the limit of registrations waiting for approval (prevents filling the list with anonymous requests).
const DefaultMaxPending = 200

// Request is a unit's registration. The registration id is the device id.
type Request struct {
	Device      string
	Public      ed25519.PublicKey
	SigningKey  ed25519.PublicKey // headquarters only: the token signing key
	Fingerprint string
	Tier        token.Tier
	Level       token.Level
	Store       string
	Register    string
	Name        string
	Status      Status
	Attempts    int
	CreatedAt   time.Time
	DecidedAt   time.Time
	DecidedBy   string
	// Report is the unit's latest status report (nil if it never reported).
	Report *Report
}

// Store is the port where the registrations are kept.
type Store interface {
	Get(ctx context.Context, device string) (Request, error)
	// Create adds a new registration; if the same device exists it returns ErrExists.
	Create(ctx context.Context, r Request) error
	// Update changes the registration only if its status and attempt count are the same as old;
	// otherwise it returns ErrConflict (the race of two approvers is not lost).
	Update(ctx context.Context, old, next Request) error
	List(ctx context.Context, status Status) ([]Request, error)
	// SaveReport writes the unit's latest status report (independent of the registration decision, no compare-and-write needed).
	SaveReport(ctx context.Context, device string, r Report) error
}

// Issuer is the port that produces the token and certificate for an approved unit.
type Issuer interface {
	Issue(c token.Claims, now time.Time) (string, error)
	// Certify gives a certificate for headquarters' signing key; only the publisher root can give it.
	Certify(in token.CertInput) (string, error)
}

// Submission is a registration request. Signature is the device key's signature of SubmitMessage.
type Submission struct {
	Device      string
	Public      ed25519.PublicKey
	SigningKey  ed25519.PublicKey // required only for the headquarters tier
	Fingerprint string
	Tier        token.Tier
	Level       token.Level
	Store       string
	Register    string
	Name        string
	Signature   []byte
}

// SubmitMessage is the message that is signed in the registration request.
func SubmitMessage(device string, tier token.Tier, level token.Level, name string, signingKey ed25519.PublicKey) []byte {
	return []byte(fmt.Sprintf("abarow-registry-v2\n%s\n%s\n%s\n%s\n%s", device, tier, level, name, base64.RawURLEncoding.EncodeToString(signingKey)))
}

// TokenResult is the token given and (for headquarters) the certificate.
type TokenResult struct {
	Token  string
	Expiry time.Time
	// Cert is given only to the headquarters tier: the certificate of headquarters' signing key.
	Cert string
}

// Service is the registration and token service.
type Service struct {
	store      Store
	issuer     Issuer
	allowed    []token.Tier
	ttl        time.Duration
	certTTL    time.Duration
	maxPending int
	now        func() time.Time

	mu         sync.Mutex
	challenges map[string]challenge
}

type challenge struct {
	nonce string
	until time.Time
}

// ChallengeTTL is how long a challenge is valid.
const ChallengeTTL = 2 * time.Minute

// Config is the service's settings.
type Config struct {
	Store  Store
	Issuer Issuer
	// Allowed are the tiers this service can approve (headquarters: store server and register;
	// publisher: headquarters).
	Allowed []token.Tier
	// TTL is the lifetime of the tokens given.
	TTL time.Duration
	// CertTTL is the lifetime of the headquarters certificates (for the service that approves the headquarters tier; 90 days recommended).
	CertTTL time.Duration
	// MaxPending is the limit of registrations waiting for approval; DefaultMaxPending if 0.
	MaxPending int
	// Now gives the time; time.Now if nil.
	Now func() time.Time
}

// New, hizmeti kurar.
func New(c Config) (*Service, error) {
	if c.Store == nil || c.Issuer == nil || len(c.Allowed) == 0 || c.TTL <= 0 {
		return nil, errors.New("kayıt hizmeti ayarları eksik")
	}
	if slices.Contains(c.Allowed, token.Headquarters) && c.CertTTL <= 0 {
		return nil, errors.New("merkez kaydı onaylayan hizmet için CertTTL gerekli")
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	max := c.MaxPending
	if max <= 0 {
		max = DefaultMaxPending
	}
	return &Service{store: c.Store, issuer: c.Issuer, allowed: c.Allowed, ttl: c.TTL, certTTL: c.CertTTL, maxPending: max, now: now, challenges: map[string]challenge{}}, nil
}

// Submit receives the registration request. If the same device comes again with the same key it returns the existing registration.
func (s *Service) Submit(ctx context.Context, in Submission) (Request, error) {
	level := in.Level // the level is sent explicitly (its default is set by the device setting); the signature covers it
	if !slices.Contains(s.allowed, in.Tier) || (level != token.Test && level != token.Pilot && level != token.Prod) {
		return Request{}, fmt.Errorf("%w: katman ya da seviye bu hizmette kabul edilmez", ErrBadRequest)
	}
	if in.Device == "" || len(in.Public) != ed25519.PublicKeySize || in.Fingerprint == "" {
		return Request{}, fmt.Errorf("%w: kimlik alanları eksik", ErrBadRequest)
	}
	headquartersTier := in.Tier == token.Headquarters
	if headquartersTier != (len(in.SigningKey) == ed25519.PublicKeySize) || (!headquartersTier && len(in.SigningKey) != 0) {
		return Request{}, fmt.Errorf("%w: imzalama anahtarı yalnızca merkez katmanı içindir ve onda zorunludur", ErrBadRequest)
	}
	if identity.DeriveID(in.Public, identity.Fingerprint(in.Fingerprint)) != in.Device {
		return Request{}, fmt.Errorf("%w: cihaz kimliği anahtar ve parmak izinden türetilmemiş", ErrBadRequest)
	}
	if !identity.Verify(in.Public, SubmitMessage(in.Device, in.Tier, level, in.Name, in.SigningKey), in.Signature) {
		return Request{}, fmt.Errorf("%w: imza geçersiz", ErrBadRequest)
	}
	existing, err := s.store.Get(ctx, in.Device)
	switch {
	case err == nil:
		return s.same(existing, in)
	case !errors.Is(err, ErrNotFound):
		return Request{}, err
	}
	pending, err := s.store.List(ctx, Pending)
	if err != nil {
		return Request{}, err
	}
	if len(pending) >= s.maxPending {
		return Request{}, ErrTooMany
	}
	r := Request{
		Device: in.Device, Public: slices.Clone(in.Public), SigningKey: slices.Clone(in.SigningKey), Fingerprint: in.Fingerprint,
		Tier: in.Tier, Level: level, Store: in.Store, Register: in.Register, Name: in.Name, Status: Pending, CreatedAt: s.now(),
	}
	if err := s.store.Create(ctx, r); err != nil {
		if errors.Is(err, ErrExists) { // the same request at the same time
			m, gerr := s.store.Get(ctx, in.Device)
			if gerr != nil {
				return Request{}, gerr
			}
			return s.same(m, in)
		}
		return Request{}, err
	}
	return r, nil
}

// same checks that the existing registration matches the same request (a repeat is harmless).
func (s *Service) same(m Request, in Submission) (Request, error) {
	if !m.Public.Equal(in.Public) {
		return Request{}, ErrKeyChanged
	}
	return m, nil
}

// Get returns a registration.
func (s *Service) Get(ctx context.Context, device string) (Request, error) {
	return s.store.Get(ctx, device)
}

// List returns the registrations in the given status.
func (s *Service) List(ctx context.Context, status Status) ([]Request, error) {
	return s.store.List(ctx, status)
}

func (s *Service) decide(ctx context.Context, approver, device string, from Status) (Request, error) {
	if approver == "" {
		return Request{}, fmt.Errorf("%w: onaylayan belirtilmemiş", ErrBadRequest)
	}
	r, err := s.store.Get(ctx, device)
	if err != nil {
		return Request{}, err
	}
	if r.Status != from {
		return Request{}, fmt.Errorf("%w: kayıt %s durumunda", ErrBadRequest, r.Status)
	}
	return r, nil
}

// Approve approves the registration by verifying the approval code on the unit's screen.
// If many wrong codes are entered the registration is rejected.
func (s *Service) Approve(ctx context.Context, approver, device, code string) error {
	r, err := s.decide(ctx, approver, device, Pending)
	if err != nil {
		return err
	}
	next := r
	if subtle.ConstantTimeCompare([]byte(code), []byte(identity.Code(device))) != 1 {
		next.Attempts++
		if next.Attempts >= MaxCodeAttempts {
			next.Status, next.DecidedAt, next.DecidedBy = Rejected, s.now(), "sistem: çok sayıda yanlış onay kodu"
		}
		if err := s.store.Update(ctx, r, next); err != nil {
			return err
		}
		return ErrWrongCode
	}
	next.Status, next.DecidedAt, next.DecidedBy = Approved, s.now(), approver
	return s.store.Update(ctx, r, next)
}

// Reject rejects a pending registration.
func (s *Service) Reject(ctx context.Context, approver, device string) error {
	r, err := s.decide(ctx, approver, device, Pending)
	if err != nil {
		return err
	}
	next := r
	next.Status, next.DecidedAt, next.DecidedBy = Rejected, s.now(), approver
	return s.store.Update(ctx, r, next)
}

// Revoke revokes an approved registration; the unit cannot get a new token.
func (s *Service) Revoke(ctx context.Context, approver, device string) error {
	r, err := s.decide(ctx, approver, device, Approved)
	if err != nil {
		return err
	}
	next := r
	next.Status, next.DecidedAt, next.DecidedBy = Revoked, s.now(), approver
	if err := s.store.Update(ctx, r, next); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.challenges, device)
	s.mu.Unlock()
	return nil
}

func (s *Service) approved(ctx context.Context, device string) (Request, error) {
	r, err := s.store.Get(ctx, device)
	if err != nil {
		return Request{}, err
	}
	switch r.Status {
	case Approved:
		return r, nil
	case Pending:
		return Request{}, ErrPending
	case Rejected:
		return Request{}, ErrRejected
	default:
		return Request{}, ErrRevoked
	}
}

// Challenge gives an approved unit a single-use challenge.
func (s *Service) Challenge(ctx context.Context, device string) (string, error) {
	if _, err := s.approved(ctx, device); err != nil {
		return "", err
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	s.challenges[device] = challenge{nonce: nonce, until: s.now().Add(ChallengeTTL)}
	s.mu.Unlock()
	return nonce, nil
}

// Token gives a token to the approved unit that signs the challenge with its device key.
// At the headquarters tier it also returns the certificate for headquarters' signing key.
func (s *Service) Token(ctx context.Context, device, nonce string, sig []byte) (TokenResult, error) {
	r, err := s.approved(ctx, device)
	if err != nil {
		return TokenResult{}, err
	}
	s.mu.Lock()
	c, ok := s.challenges[device]
	delete(s.challenges, device) // single-use
	s.mu.Unlock()
	if !ok || !s.now().Before(c.until) || subtle.ConstantTimeCompare([]byte(c.nonce), []byte(nonce)) != 1 {
		return TokenResult{}, ErrNoChallenge
	}
	if !identity.Verify(r.Public, token.PossessionMessage(device, nonce), sig) {
		return TokenResult{}, fmt.Errorf("%w: sahiplik imzası geçersiz", ErrBadRequest)
	}
	now := s.now()
	exp := now.Add(s.ttl)
	raw, err := s.issuer.Issue(token.Claims{
		Device: r.Device, Tier: r.Tier, Level: r.Level, Store: r.Store, Register: r.Register,
		Scopes: []string{token.ScopePull}, Key: token.Thumbprint(r.Public), Expiry: exp,
	}, now)
	if err != nil {
		return TokenResult{}, err
	}
	res := TokenResult{Token: raw, Expiry: exp}
	if r.Tier == token.Headquarters {
		res.Cert, err = s.issuer.Certify(token.CertInput{HeadquartersDevice: r.Device, SigningKey: r.SigningKey, NotBefore: now.Add(-time.Minute), Expiry: now.Add(s.certTTL)})
		if err != nil {
			return TokenResult{}, err
		}
	}
	return res, nil
}
