// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package token produces and verifies the image pull tokens.
//
// The chain of trust has three steps:
//
//	publisher root ──signs──▶ headquarters certificate ──signs──▶ unit token
//
// The publisher's root key is known in advance (pinned) to every mirror and agent.
// When headquarters registers with the publisher it gets a headquarters certificate; this certificate
// binds headquarters' token signing key. Headquarters gives its own units (store server and register)
// tokens with this key. A mirror can verify the token only with the root, without reaching
// the publisher; it works when the internet is down too.
//
// Rules: the publisher root can issue tokens for every tier; a headquarters certificate
// can issue only store server and register tokens (it cannot issue a headquarters token). The token
// is bound to the device key's fingerprint (cnf); whoever steals it cannot renew the token
// without the device's private key.
package token

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// Tier is the unit's tier.
type Tier string

// Katmanlar.
const (
	Register     Tier = "register"
	Store        Tier = "store"
	Headquarters Tier = "headquarters"
)

// Level is the unit's update level (which channel it is fed from).
type Level string

// Update levels.
const (
	Test  Level = "test"
	Pilot Level = "pilot"
	Prod  Level = "prod"
)

// ScopePull is the permission to pull images.
const ScopePull = "pull"

// Error values.
var (
	ErrInvalid    = errors.New("jeton geçersiz")
	ErrExpired    = errors.New("jetonun süresi dolmuş")
	ErrNotAllowed = errors.New("jeton bu işleme yetkili değil")
)

// Claims is the content of a verified token.
type Claims struct {
	Device   string    // device id
	Tier     Tier      // katman
	Level    Level     // update level
	Store    string    // store id (for register and store server)
	Register string    // register id (for register)
	Scopes   []string  // yetkiler
	Key      string    // fingerprint of the device key (cnf)
	Issuer   string    // who issued the token: the headquarters device id or "publisher"
	Expiry   time.Time // son kullanma
}

// Allows says the token's permission.
func (c Claims) Allows(scope string) bool { return slices.Contains(c.Scopes, scope) }

// Thumbprint is the fingerprint of a public key (cnf).
func Thumbprint(pub ed25519.PublicKey) string {
	s := sha256.Sum256(pub)
	return base64.RawURLEncoding.EncodeToString(s[:])
}

// The Issuer value is used in the tokens issued by the publisher root.
const PublisherIssuer = "publisher"

type wire struct {
	jwt.Claims
	Use      string   `json:"use"`
	Tier     string   `json:"tier,omitempty"`
	Level    string   `json:"level,omitempty"`
	Store    string   `json:"store,omitempty"`
	Register string   `json:"register,omitempty"`
	Scopes   []string `json:"scp,omitempty"`
	Cnf      string   `json:"cnf,omitempty"`
	Chain    string   `json:"chain,omitempty"` // the headquarters certificate
	PK       string   `json:"pk,omitempty"`    // the token signing key in the certificate
}

const (
	useToken = "token"
	useCert  = "cert"
)

func signer(priv ed25519.PrivateKey) (jose.Signer, error) {
	return jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: priv}, (&jose.SignerOptions{}).WithType("JWT"))
}

func serialize(priv ed25519.PrivateKey, w wire) (string, error) {
	sg, err := signer(priv)
	if err != nil {
		return "", err
	}
	return jwt.Signed(sg).Claims(w).Serialize()
}

// CertInput is the input for the headquarters certificate.
type CertInput struct {
	HeadquartersDevice string            // headquarters' device id
	SigningKey         ed25519.PublicKey // headquarters' token signing key
	NotBefore          time.Time
	Expiry             time.Time
}

// IssueCert produces a headquarters certificate on behalf of the publisher root.
func IssueCert(root ed25519.PrivateKey, in CertInput) (string, error) {
	if in.HeadquartersDevice == "" || len(in.SigningKey) != ed25519.PublicKeySize || !in.Expiry.After(in.NotBefore) {
		return "", errors.New("sertifika girdisi geçersiz")
	}
	return serialize(root, wire{
		Claims: jwt.Claims{
			Issuer: PublisherIssuer, Subject: in.HeadquartersDevice,
			NotBefore: jwt.NewNumericDate(in.NotBefore), Expiry: jwt.NewNumericDate(in.Expiry),
		},
		Use: useCert,
		PK:  base64.RawURLEncoding.EncodeToString(in.SigningKey),
	})
}

// IssueToken produces a token for a unit. If chain is empty the token was issued by the publisher root
// (priv = the root key), otherwise by headquarters (priv = headquarters' signing key,
// chain = the headquarters certificate).
func IssueToken(priv ed25519.PrivateKey, issuer string, c Claims, chain string, now time.Time) (string, error) {
	if c.Device == "" || c.Key == "" || len(c.Scopes) == 0 || !c.Expiry.After(now) {
		return "", errors.New("jeton girdisi geçersiz")
	}
	return serialize(priv, wire{
		Claims: jwt.Claims{
			Issuer: issuer, Subject: c.Device,
			IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(c.Expiry),
		},
		Use: useToken, Tier: string(c.Tier), Level: string(c.Level),
		Store: c.Store, Register: c.Register, Scopes: c.Scopes, Cnf: c.Key, Chain: chain,
	})
}

func parse(raw string) (*jwt.JSONWebToken, error) {
	// Only EdDSA is accepted; "none" and symmetric algorithms are rejected.
	t, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return t, nil
}

func at(err error) error {
	if errors.Is(err, jwt.ErrExpired) {
		return ErrExpired
	}
	return fmt.Errorf("%w: %v", ErrInvalid, err)
}

// Verify verifies the token with the publisher root and the time and returns its content.
func Verify(root ed25519.PublicKey, raw string, now time.Time) (Claims, error) {
	t, err := parse(raw)
	if err != nil {
		return Claims{}, err
	}
	var anonymous wire
	if err := t.UnsafeClaimsWithoutVerification(&anonymous); err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if anonymous.Use != useToken {
		return Claims{}, fmt.Errorf("%w: bu bir jeton değil", ErrInvalid)
	}

	key := root
	issuer := PublisherIssuer
	if anonymous.Chain != "" {
		cert, err := parse(anonymous.Chain)
		if err != nil {
			return Claims{}, err
		}
		var cw wire
		if err := cert.Claims(root, &cw); err != nil {
			return Claims{}, fmt.Errorf("%w: sertifika yayıncı kökü tarafından imzalanmamış", ErrInvalid)
		}
		if cw.Use != useCert || cw.Issuer != PublisherIssuer {
			return Claims{}, fmt.Errorf("%w: sertifika geçersiz", ErrInvalid)
		}
		if err := cw.Claims.ValidateWithLeeway(jwt.Expected{Time: now}, 0); err != nil {
			return Claims{}, at(err)
		}
		pk, err := base64.RawURLEncoding.DecodeString(cw.PK)
		if err != nil || len(pk) != ed25519.PublicKeySize {
			return Claims{}, fmt.Errorf("%w: sertifikadaki anahtar bozuk", ErrInvalid)
		}
		key = ed25519.PublicKey(pk)
		issuer = cw.Subject
		// Headquarters can issue tokens only for its own units.
		if anonymous.Issuer != issuer {
			return Claims{}, fmt.Errorf("%w: jetonu veren sertifika sahibi değil", ErrInvalid)
		}
		if Tier(anonymous.Tier) != Store && Tier(anonymous.Tier) != Register {
			return Claims{}, fmt.Errorf("%w: merkez bu katman için jeton veremez", ErrNotAllowed)
		}
	}

	var w wire
	if err := t.Claims(key, &w); err != nil {
		return Claims{}, fmt.Errorf("%w: imza doğrulanamadı", ErrInvalid)
	}
	if err := w.Claims.ValidateWithLeeway(jwt.Expected{Time: now}, 0); err != nil {
		return Claims{}, at(err)
	}
	if w.Claims.Expiry == nil || w.Cnf == "" || w.Subject == "" {
		return Claims{}, fmt.Errorf("%w: eksik alan", ErrInvalid)
	}
	return Claims{
		Device: w.Subject, Tier: Tier(w.Tier), Level: Level(w.Level), Store: w.Store, Register: w.Register,
		Scopes: w.Scopes, Key: w.Cnf, Issuer: issuer, Expiry: w.Claims.Expiry.Time(),
	}, nil
}

// PossessionMessage is the message signed to prove ownership of the token.
// nonce is the single-use value given by the verifier.
func PossessionMessage(device, nonce string) []byte {
	return []byte("abarow-sahiplik-v1\n" + device + "\n" + nonce)
}

// VerifyPossession verifies that the device key matches the fingerprint in the token and signed
// the nonce.
func VerifyPossession(c Claims, pub ed25519.PublicKey, nonce string, sig []byte) error {
	if len(pub) != ed25519.PublicKeySize || Thumbprint(pub) != c.Key {
		return fmt.Errorf("%w: cihaz anahtarı jetonla uyuşmuyor", ErrNotAllowed)
	}
	if !ed25519.Verify(pub, PossessionMessage(c.Device, nonce), sig) {
		return fmt.Errorf("%w: sahiplik imzası geçersiz", ErrNotAllowed)
	}
	return nil
}

// CertSubject reads the owner of a headquarters certificate (the headquarters device id) without
// verifying its signature. It is used only by the headquarters service that reads its own certificate
// file; Verify is used for a certificate that comes from someone else.
func CertSubject(raw string) (string, error) {
	t, err := parse(raw)
	if err != nil {
		return "", err
	}
	var w wire
	if err := t.UnsafeClaimsWithoutVerification(&w); err != nil || w.Use != useCert || w.Subject == "" {
		return "", fmt.Errorf("%w: sertifika okunamadı", ErrInvalid)
	}
	return w.Subject, nil
}
