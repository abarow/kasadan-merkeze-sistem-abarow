// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package channel defines, signs and verifies the signed channel manifest (which version has been
// promoted to a channel with which image digests).
//
// The channel manifest is the source of trust of the update flow: the digests in the manifest are
// applied, not the tags in the registry. The channel's key signs the manifest; each machine
// trusts only the keys accepted by its own update level:
//
//	level    channel   accepted signatures
//	test     test      build key
//	pilot    pilot     pilot key or prod key
//	prod     prod      prod key only
//
// The manifest cannot be rolled back to an older version (increasing sequence number) and cannot
// go stale (expiry moment); so a captured mirror cannot force the agent onto an old
// version with a vulnerability, or hide new versions forever. This document gives no
// security guarantee.
package channel

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"abarow.com/services/update/internal/token"
)

// Role is the role of the signing key.
type Role string

// Roller.
const (
	Build Role = "build" // the images built by CI (the test channel)
	Pilot Role = "pilot"
	Prod  Role = "prod"
)

// Urgency values. Urgency is only information; it does not change behavior.
const (
	UrgencyNormal   = "normal"
	UrgencySecurity = "security"
)

// UnitPrefix is the mandatory prefix of the units the agent can touch.
const UnitPrefix = "abarow-"

// Error values.
var (
	ErrInvalid   = errors.New("kanal bildirimi geçersiz")
	ErrSignature = errors.New("kanal bildiriminin imzası bu seviyenin güvendiği anahtarlardan gelmiyor")
	ErrChannel   = errors.New("bildirim bu seviyenin kanalına ait değil")
	ErrExpired   = errors.New("kanal bildiriminin geçerlilik süresi dolmuş")
	ErrRollback  = errors.New("bildirim bilinen en yeni sürümden eski")
)

// Image is the new image of a service.
type Image struct {
	// Unit is the systemd/Quadlet unit name (without extension): abarow-sales.
	Unit string `json:"unit"`
	// Repo is the image's repository: ghcr.io/abarow/abarow-sales (without a tag).
	Repo string `json:"repo"`
	// Digest is the image's content digest: sha256:...
	Digest string `json:"digest"`
}

// Ref is the image's name pinned with the digest.
func (i Image) Ref() string { return i.Repo + "@" + i.Digest }

// Manifest is the version promoted to a channel.
type Manifest struct {
	Channel string `json:"channel"`
	// Version is the human-readable version label (0.4.0).
	Version string `json:"version"`
	// Seq is the sequence number that increases per channel; the basis of the rollback protection.
	Seq     uint64    `json:"seq"`
	Issued  time.Time `json:"issued"`
	Expires time.Time `json:"expires"`
	Urgency string    `json:"urgency"`
	// Notes is the summary of changes (shown on the screen).
	Notes  string  `json:"notes"`
	Images []Image `json:"images"`
}

var (
	digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	unitRe   = regexp.MustCompile(`^abarow-[a-z0-9][a-z0-9-]*$`)
	repoRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*[a-z0-9]$`)
)

// Validate checks the manifest's format.
func (m Manifest) Validate() error {
	switch {
	case !slices.Contains([]string{"test", "pilot", "prod"}, m.Channel):
		return fmt.Errorf("%w: kanal %q", ErrInvalid, m.Channel)
	case m.Version == "" || len(m.Version) > 64 || strings.ContainsAny(m.Version, " \t\r\n\"'"):
		return fmt.Errorf("%w: sürüm etiketi", ErrInvalid)
	case m.Seq == 0:
		return fmt.Errorf("%w: sıra numarası", ErrInvalid)
	case m.Expires.IsZero() || !m.Expires.After(m.Issued):
		return fmt.Errorf("%w: geçerlilik süresi", ErrInvalid)
	case m.Urgency != UrgencyNormal && m.Urgency != UrgencySecurity:
		return fmt.Errorf("%w: aciliyet %q", ErrInvalid, m.Urgency)
	case len(m.Notes) > 4096:
		return fmt.Errorf("%w: notlar çok uzun", ErrInvalid)
	case len(m.Images) == 0 || len(m.Images) > 64:
		return fmt.Errorf("%w: imaj listesi", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, im := range m.Images {
		switch {
		case !unitRe.MatchString(im.Unit):
			return fmt.Errorf("%w: birim adı %q (%s ile başlamalı)", ErrInvalid, im.Unit, UnitPrefix)
		case seen[im.Unit]:
			return fmt.Errorf("%w: %s iki kez", ErrInvalid, im.Unit)
		case !repoRe.MatchString(im.Repo) || strings.Contains(im.Repo, "@") || strings.Contains(im.Repo, ":"):
			return fmt.Errorf("%w: depo %q (etiketsiz ve digest'siz olmalı)", ErrInvalid, im.Repo)
		case !digestRe.MatchString(im.Digest):
			return fmt.Errorf("%w: digest %q", ErrInvalid, im.Digest)
		}
		seen[im.Unit] = true
	}
	return nil
}

// Sig is a signature.
type Sig struct {
	Role  Role   `json:"role"`
	KeyID string `json:"key_id"`
	Sig   string `json:"sig"` // base64url
}

// Signed is the signed manifest. Payload is the base64url encoding of the JSON form of the
// Manifest; the signature is over these bytes (no re-serialization problem).
type Signed struct {
	Payload    string `json:"payload"`
	Signatures []Sig  `json:"signatures"`
}

// KeyID is the short id of the public key.
func KeyID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return base64.RawURLEncoding.EncodeToString(h[:8])
}

// message is the bytes that are signed (a domain-separation prefix prevents reuse in another context).
func message(payload string) []byte { return []byte("abarow-channel-v1\n" + payload) }

// Sign validates the manifest's format and signs it; Add is used to add a second
// signature to an existing signed manifest.
func Sign(m Manifest, role Role, key ed25519.PrivateKey) (Signed, error) {
	if err := m.Validate(); err != nil {
		return Signed{}, err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return Signed{}, err
	}
	s := Signed{Payload: base64.RawURLEncoding.EncodeToString(raw)}
	return s.Add(role, key), nil
}

// Add adds a signature to the manifest (the same key's old signature is renewed).
func (s Signed) Add(role Role, key ed25519.PrivateKey) Signed {
	pub := key.Public().(ed25519.PublicKey)
	id := KeyID(pub)
	sig := Sig{Role: role, KeyID: id, Sig: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, message(s.Payload)))}
	out := Signed{Payload: s.Payload}
	for _, o := range s.Signatures {
		if o.KeyID != id {
			out.Signatures = append(out.Signatures, o)
		}
	}
	out.Signatures = append(out.Signatures, sig)
	return out
}

// Trust is the trusted keys.
type Trust map[Role][]ed25519.PublicKey

// TrustFile is the JSON form of the trust file: public keys per role. Manifest
// keys are base64url Ed25519, cosign keys are PEM; each key can be a plain string or an object
// with a validity window (see Entry).
type TrustFile struct {
	Build  Entries            `json:"build,omitempty"`
	Pilot  Entries            `json:"pilot,omitempty"`
	Prod   Entries            `json:"prod,omitempty"`
	Cosign map[string]Entries `json:"cosign,omitempty"`
}

func parseFile(raw []byte) (TrustFile, error) {
	var f TrustFile
	// A misspelled role name ("prodd") must not silently drop the key.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return TrustFile{}, fmt.Errorf("güven dosyası okunamadı: %w", err)
	}
	return f, nil
}

// ParseTrust reads the trust file as of the present time.
func ParseTrust(raw []byte) (Trust, error) { return ParseTrustAt(raw, time.Now()) }

// ParseTrustAt reads the trust file and gives the manifest keys valid at the moment t.
// A key that has expired or not started yet does not enter the list; every key is still verified.
func ParseTrustAt(raw []byte, t time.Time) (Trust, error) {
	f, err := parseFile(raw)
	if err != nil {
		return nil, err
	}
	trust := Trust{}
	for role, entries := range map[Role]Entries{Build: f.Build, Pilot: f.Pilot, Prod: f.Prod} {
		for _, e := range entries {
			pub, err := decodeEd25519(e.Key)
			if err != nil {
				return nil, fmt.Errorf("güven dosyasında geçersiz %s anahtarı: %w", role, err)
			}
			if err := e.validate(); err != nil {
				return nil, fmt.Errorf("%s anahtarı: %w", role, err)
			}
			if e.ValidAt(t) {
				trust[role] = append(trust[role], pub)
			}
		}
	}
	return trust, nil
}

// ChannelOf is the channel the level reads.
func ChannelOf(l token.Level) string { return string(l) }

// accepted are the roles whose signature the level accepts.
func accepted(l token.Level) []Role {
	switch l {
	case token.Test:
		return []Role{Build}
	case token.Pilot:
		return []Role{Pilot, Prod}
	default:
		return []Role{Prod}
	}
}

// Verify verifies the signed manifest according to the level and returns its content.
// lastSeq is the highest sequence number this machine has seen on this channel (0: none).
// The same sequence number is accepted (the same manifest); a smaller one counts as a rollback.
func Verify(s Signed, level token.Level, trust Trust, now time.Time, lastSeq uint64) (Manifest, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s.Payload)
	if err != nil || len(raw) > 128<<10 {
		return Manifest{}, fmt.Errorf("%w: yük okunamadı", ErrInvalid)
	}
	ok := false
	for _, sig := range s.Signatures {
		if !slices.Contains(accepted(level), sig.Role) {
			continue
		}
		b, err := base64.RawURLEncoding.DecodeString(sig.Sig)
		if err != nil {
			continue
		}
		for _, pub := range trust[sig.Role] {
			if KeyID(pub) == sig.KeyID && ed25519.Verify(pub, message(s.Payload), b) {
				ok = true
			}
		}
	}
	if !ok {
		return Manifest{}, ErrSignature
	}
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	if m.Channel != ChannelOf(level) {
		return Manifest{}, ErrChannel
	}
	if now.After(m.Expires) {
		return Manifest{}, ErrExpired
	}
	if m.Seq < lastSeq {
		return Manifest{}, ErrRollback
	}
	return m, nil
}
