// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package channel

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Entry is a key in the trust file; the optional validity window allows the
// transition period of key rotation: the new key is added with NotBefore,
// the old key is left with a time limit by NotAfter; both are accepted in the window.
// A key outside the window is ignored (an expired key drops out by itself).
//
// Three forms are accepted in JSON: a plain string ("key"), an object
// ({"key": "...", "not_before": "2026-12-01T00:00:00Z", "not_after": "..."})
// and an array of these.
type Entry struct {
	Key       string    `json:"key"`
	NotBefore time.Time `json:"not_before,omitzero"`
	NotAfter  time.Time `json:"not_after,omitzero"`
}

// ValidAt says whether the key is valid at the moment t (NotAfter is not included).
func (e Entry) ValidAt(t time.Time) bool {
	return (e.NotBefore.IsZero() || !t.Before(e.NotBefore)) && (e.NotAfter.IsZero() || t.Before(e.NotAfter))
}

// UnmarshalJSON reads the string or object form.
func (e *Entry) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		*e = Entry{}
		return json.Unmarshal(b, &e.Key)
	}
	type plain Entry
	var p plain
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return err
	}
	*e = Entry(p)
	return nil
}

// Entries are a role's keys; it can be a single key, a single object or an array.
type Entries []Entry

// UnmarshalJSON reads the string, object or array form.
func (es *Entries) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '[' {
		var list []Entry
		if err := json.Unmarshal(b, &list); err != nil {
			return err
		}
		*es = list
		return nil
	}
	var one Entry
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*es = Entries{one}
	return nil
}

// KeyState is the state of a key.
type KeyState string

// Key states.
const (
	KeyValid   KeyState = "geçerli"
	KeyPending KeyState = "henüz geçerli değil"
	KeyExpired KeyState = "süresi dolmuş"
)

// KeyInfo is the summary of a trust key (the check-trust output).
type KeyInfo struct {
	Kind      string // "manifest" or "cosign"
	Role      Role
	ID        string // the key's short id
	State     KeyState
	NotBefore time.Time
	NotAfter  time.Time
}

// state is the key's state at the moment t.
func (e Entry) state(t time.Time) KeyState {
	switch {
	case !e.NotBefore.IsZero() && t.Before(e.NotBefore):
		return KeyPending
	case !e.NotAfter.IsZero() && !t.Before(e.NotAfter):
		return KeyExpired
	}
	return KeyValid
}

func (e Entry) validate() error {
	if !e.NotBefore.IsZero() && !e.NotAfter.IsZero() && !e.NotAfter.After(e.NotBefore) {
		return errors.New("not_after, not_before'dan sonra olmalı")
	}
	return nil
}

func decodeEd25519(k string) (ed25519.PublicKey, error) {
	b, err := base64.RawURLEncoding.DecodeString(k)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("base64url Ed25519 genel anahtarı değil")
	}
	return b, nil
}

// CosignID is the short id of a cosign public key (PEM); it returns an error if invalid.
func CosignID(pemKey string) (string, error) {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil || block.Type != "PUBLIC KEY" {
		return "", errors.New("PEM \"PUBLIC KEY\" değil")
	}
	if _, err := x509.ParsePKIXPublicKey(block.Bytes); err != nil {
		return "", fmt.Errorf("okunamadı: %w", err)
	}
	sum := sha256.Sum256(block.Bytes)
	return base64.RawURLEncoding.EncodeToString(sum[:8]), nil
}

// CosignAt gives, per role, the cosign keys in the trust file that are valid at the moment t.
// Every key is verified (even if not valid): a misspelled key does not drop silently.
func CosignAt(raw []byte, t time.Time) (map[Role][]string, error) {
	f, err := parseFile(raw)
	if err != nil {
		return nil, err
	}
	out := map[Role][]string{}
	for name, entries := range f.Cosign {
		role := Role(name)
		if role != Build && role != Pilot && role != Prod {
			return nil, fmt.Errorf("cosign anahtarı için bilinmeyen rol %q", name)
		}
		for _, e := range entries {
			if _, err := CosignID(e.Key); err != nil {
				return nil, fmt.Errorf("%s cosign anahtarı: %w", name, err)
			}
			if err := e.validate(); err != nil {
				return nil, fmt.Errorf("%s cosign anahtarı: %w", name, err)
			}
			if e.ValidAt(t) {
				out[role] = append(out[role], e.Key)
			}
		}
	}
	return out, nil
}

// Inspect lists the state at the moment t of all the keys in the trust file.
func Inspect(raw []byte, t time.Time) ([]KeyInfo, error) {
	f, err := parseFile(raw)
	if err != nil {
		return nil, err
	}
	var out []KeyInfo
	add := func(kind string, role Role, id string, e Entry) {
		out = append(out, KeyInfo{Kind: kind, Role: role, ID: id, State: e.state(t), NotBefore: e.NotBefore, NotAfter: e.NotAfter})
	}
	for role, entries := range map[Role]Entries{Build: f.Build, Pilot: f.Pilot, Prod: f.Prod} {
		for _, e := range entries {
			pub, err := decodeEd25519(e.Key)
			if err != nil {
				return nil, fmt.Errorf("%s anahtarı: %w", role, err)
			}
			if err := e.validate(); err != nil {
				return nil, fmt.Errorf("%s anahtarı: %w", role, err)
			}
			add("manifest", role, KeyID(pub), e)
		}
	}
	for name, entries := range f.Cosign {
		for _, e := range entries {
			id, err := CosignID(e.Key)
			if err != nil {
				return nil, fmt.Errorf("%s cosign anahtarı: %w", name, err)
			}
			if err := e.validate(); err != nil {
				return nil, fmt.Errorf("%s cosign anahtarı: %w", name, err)
			}
			add("cosign", Role(name), id, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Role != b.Role {
			return a.Role < b.Role
		}
		return a.ID < b.ID
	})
	return out, nil
}
