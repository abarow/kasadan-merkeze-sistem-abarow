// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package identity generates and stores the update agent's device identity.
//
// The identity consists of two things: an Ed25519 key pair generated specifically for the device and
// the machine's hardware fingerprint (a digest of values like the machine-id and the motherboard
// serial number). The device id (ID) is derived from both; so if the same key is
// copied to another machine the fingerprint does not match and it is noticed.
// The private key never leaves the device; it only produces signatures.
package identity

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Error values.
var (
	// ErrNoFingerprint is returned when no hardware or operating system source could be read.
	ErrNoFingerprint = errors.New("donanım parmak izi için okunabilir kaynak yok")
	// ErrFingerprintChanged shows that the saved key is being used on another machine
	// (a copied disk or changed hardware).
	ErrFingerprintChanged = errors.New("donanım parmak izi kayıtlı kimlikle uyuşmuyor")
	// ErrNotFound is returned when there is no saved identity.
	ErrNotFound = errors.New("kayıtlı cihaz kimliği yok")
)

// Fingerprint is a digest of the machine's hardware and operating system values.
type Fingerprint string

// NewFingerprint produces a stable digest from named values. Empty values are ignored.
func NewFingerprint(parts map[string]string) Fingerprint {
	keys := make([]string, 0, len(parts))
	for k, v := range parts {
		if strings.TrimSpace(v) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s\n", k, strings.TrimSpace(parts[k]))
	}
	return Fingerprint(hex.EncodeToString(h.Sum(nil)))
}

// sources are the files that go into the fingerprint (relative to the root file system).
var sources = map[string]string{
	"machine-id":     "etc/machine-id",
	"board_serial":   "sys/class/dmi/id/board_serial",
	"product_uuid":   "sys/class/dmi/id/product_uuid",
	"product_serial": "sys/class/dmi/id/product_serial",
	"chassis_serial": "sys/class/dmi/id/chassis_serial",
}

// ReadFingerprint produces a fingerprint from the sources that can be read from the root file
// system (the machine's root in production); it also returns which sources were used.
// Sources that cannot be read (e.g. those that need root permission) are skipped; at least one must be readable.
func ReadFingerprint(root fs.FS) (Fingerprint, []string, error) {
	parts := map[string]string{}
	var used []string
	for label, path := range sources {
		b, err := fs.ReadFile(root, path)
		if err != nil || strings.TrimSpace(string(b)) == "" {
			continue
		}
		parts[label] = string(b)
		used = append(used, label)
	}
	if len(used) == 0 {
		return "", nil, ErrNoFingerprint
	}
	sort.Strings(used)
	return NewFingerprint(parts), used, nil
}

// idEncoding does not include easily confused characters (I, O, 0, 1).
var idEncoding = base32.NewEncoding("ABCDEFGHJKLMNPQRSTUVWXYZ23456789").WithPadding(base32.NoPadding)

// DeriveID derives the device id from the public key and the fingerprint: ABRW-XXXX-XXXX-XXXX-XXXX.
func DeriveID(pub ed25519.PublicKey, fp Fingerprint) string {
	h := sha256.New()
	h.Write(pub)
	h.Write([]byte{0})
	h.Write([]byte(fp))
	s := idEncoding.EncodeToString(h.Sum(nil))[:16]
	return fmt.Sprintf("ABRW-%s-%s-%s-%s", s[0:4], s[4:8], s[8:12], s[12:16])
}

// Code produces from the device id the short code the approver compares with the device's screen
// (XXXX-XXXX). This code is entered when giving approval: it prevents approving the wrong device.
func Code(id string) string {
	sum := sha256.Sum256([]byte("abarow-onay-kodu-v1\n" + id))
	s := idEncoding.EncodeToString(sum[:])[:8]
	return s[:4] + "-" + s[4:]
}

// Identity is this device's identity.
type Identity struct {
	ID          string
	Fingerprint Fingerprint
	Public      ed25519.PublicKey
	private     ed25519.PrivateKey
}

// Generate generates a new device key. crypto/rand is used if r is nil.
func Generate(fp Fingerprint, r io.Reader) (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(r)
	if err != nil {
		return nil, err
	}
	return &Identity{ID: DeriveID(pub, fp), Fingerprint: fp, Public: pub, private: priv}, nil
}

// Sign produces a signature with the device key.
func (i *Identity) Sign(msg []byte) []byte { return ed25519.Sign(i.private, msg) }

// Verify verifies the signature of a public key.
func Verify(pub ed25519.PublicKey, msg, sig []byte) bool {
	return len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, msg, sig)
}

const (
	keyFile         = "cihaz.key"
	fingerprintFile = "cihaz.parmakizi"
)

// Save writes the identity to the directory (the private key can be read only by its owner).
func Save(dir string, i *Identity) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, keyFile), []byte(hex.EncodeToString(i.private.Seed())), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, fingerprintFile), []byte(i.Fingerprint), 0o600)
}

// Load reads the saved identity. fp is this machine's current fingerprint; if it does not
// match the saved one it returns ErrFingerprintChanged (the key may have been copied to another machine).
func Load(dir string, fp Fingerprint) (*Identity, error) {
	raw, err := os.ReadFile(filepath.Join(dir, keyFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	seed, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("cihaz anahtarı dosyası bozuk")
	}
	registered, err := os.ReadFile(filepath.Join(dir, fingerprintFile))
	if err != nil {
		return nil, err
	}
	if Fingerprint(registered) != fp {
		return nil, ErrFingerprintChanged
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	return &Identity{ID: DeriveID(pub, fp), Fingerprint: fp, Public: pub, private: priv}, nil
}
