// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package policy produces Podman's image signature check (containers-policy.json and
// registries.d) according to the update level.
//
// Trust has two layers (docs/UPDATE.md "Promotion"): the channel manifest pins the images'
// digests, and the image itself is signed during promotion with the channel's cosign
// key, and Podman verifies this signature when pulling:
//
//	level    path              accepted signatures
//	test     <mirror>/test     build key
//	pilot    <mirror>/pilot    pilot key or prod key
//	prod     <mirror>/prod     prod key only
//
// Every other path from the mirror is rejected. The signature is bound not to the mirror address but to a fixed
// identity ("abarow.invalid/<image>@<digest>"): the same signature is valid on the mirrors of the
// register, the store server and headquarters (their addresses differ). The identity
// carries the digest so that Podman matches an image pulled by digest; the signature
// itself binds the image's digest too.
//
// Tried with real Podman and Zot (the optional sync between mirrors carried the signatures
// too); an unsigned image, an image signed with the wrong key and a path without a key were rejected.
package policy

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"abarow.com/services/update/internal/channel"
	"abarow.com/services/update/internal/token"
)

// SignedPrefix is the fixed identity prefix used in signatures; during promotion it is written with
// `cosign sign --sign-container-identity abarow.invalid/<image>@<digest>`.
const SignedPrefix = "abarow.invalid"

// Keys are the cosign public keys per role (PEM). A role can have more than one key:
// during key rotation the old and new keys are accepted together.
type Keys map[channel.Role][]string

// ParseKeys reads the keys valid at the moment t from the "cosign" section of the trust file.
// For the keys' validity windows see channel.Entry.
func ParseKeys(raw []byte, t time.Time) (Keys, error) {
	m, err := channel.CosignAt(raw, t)
	if err != nil {
		return nil, err
	}
	return Keys(m), nil
}

// roles are the roles the level accepts for the signature (the same rule as channel.Verify).
func roles(l token.Level) []channel.Role {
	switch l {
	case token.Test:
		return []channel.Role{channel.Build}
	case token.Pilot:
		return []channel.Role{channel.Pilot, channel.Prod}
	default:
		return []channel.Role{channel.Prod}
	}
}

// Files are the generated configuration files.
type Files struct {
	// Policy is the content of policy.json.
	Policy []byte
	// RegistriesD is the content of registries.d/abarow.yaml (reading the signature attachments).
	RegistriesD []byte
}

var mirrorRe = regexp.MustCompile(`^[A-Za-z0-9.-]+(:[0-9]{1,5})?$`)

type requirement struct {
	Type           string          `json:"type"`
	KeyDatas       []string        `json:"keyDatas,omitempty"`
	SignedIdentity *signedIdentity `json:"signedIdentity,omitempty"`
}

type signedIdentity struct {
	Type         string `json:"type"`
	Prefix       string `json:"prefix"`
	SignedPrefix string `json:"signedPrefix"`
}

// Generate produces policy.json and the registries.d file by the level and the mirror.
// The level must have keys for all the roles it accepts: "half verification" is not done
// with a missing key, it returns an error.
func Generate(level token.Level, mirror string, keys Keys) (Files, error) {
	if !mirrorRe.MatchString(mirror) {
		return Files{}, fmt.Errorf("the mirror address is invalid: %q (address or address:port)", mirror)
	}
	var datas []string
	for _, r := range roles(level) {
		if len(keys[r]) == 0 {
			return Files{}, fmt.Errorf("%w: no valid %s cosign key for level %s (it is not in the trust file or it has expired)", ErrNoKey, level, r)
		}
		for _, p := range keys[r] {
			datas = append(datas, base64.StdEncoding.EncodeToString([]byte(p)))
		}
	}
	scope := mirror + "/" + string(level)
	doc := map[string]any{
		"default": []requirement{{Type: "insecureAcceptAnything"}},
		"transports": map[string]any{
			"docker": map[string]any{
				// Every other path from the mirror (another channel, no channel) is rejected; the longest matching scope wins.
				mirror: []requirement{{Type: "reject"}},
				scope: []requirement{{
					Type: "sigstoreSigned", KeyDatas: datas,
					SignedIdentity: &signedIdentity{Type: "remapIdentity", Prefix: scope, SignedPrefix: SignedPrefix},
				}},
			},
		},
	}
	pol, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return Files{}, err
	}
	reg := fmt.Sprintf("# abarow-update writes: signature attachments (cosign) are read on the %s mirror.\ndocker:\n  %s:\n    use-sigstore-attachments: true\n", mirror, mirror)
	return Files{Policy: append(pol, '\n'), RegistriesD: []byte(reg)}, nil
}

// ErrNoKey reports that one of the roles the level accepts has no valid key left.
var ErrNoKey = errors.New("no valid cosign key")

// Locked is the policy written when no valid key is left: nothing can be pulled from the mirror.
// Instead of the key that expired staying in the old policy and continuing to accept signatures,
// it fails closed (pulling updates stops until a real key comes).
func Locked(mirror string) (Files, error) {
	if !mirrorRe.MatchString(mirror) {
		return Files{}, fmt.Errorf("the mirror address is invalid: %q (address or address:port)", mirror)
	}
	doc := map[string]any{
		"default":    []requirement{{Type: "insecureAcceptAnything"}},
		"transports": map[string]any{"docker": map[string]any{mirror: []requirement{{Type: "reject"}}, SignedPrefix: []requirement{{Type: "reject"}}}},
	}
	pol, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return Files{}, err
	}
	reg := fmt.Sprintf("# abarow-update writes: the %s mirror is LOCKED (no valid cosign key).\ndocker:\n  %s:\n    use-sigstore-attachments: true\n", mirror, mirror)
	return Files{Policy: append(pol, '\n'), RegistriesD: []byte(reg)}, nil
}

// Ours says that an existing policy.json was produced by this package (a policy
// written by someone else is not overwritten).
func Ours(existing []byte) bool {
	return strings.Contains(string(existing), `"signedPrefix": "`+SignedPrefix+`"`) || strings.Contains(string(existing), lockedMarker)
}

// lockedMarker identifies the Locked policy: the "abarow.invalid" scope that matches no real image.
const lockedMarker = `"` + SignedPrefix + `": [`

// ErrForeign reports that someone else's policy.json is in the directory.
var ErrForeign = errors.New("policy.json was written by someone else; abarow does not overwrite it (move the file or merge by hand)")
