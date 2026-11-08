// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"bytes"
	"errors"
	"regexp"
	"testing"
	"testing/fstest"
)

func TestFingerprintIsStableAndIgnoresEmpty(t *testing.T) {
	a := NewFingerprint(map[string]string{"machine-id": "abc\n", "board_serial": "X1", "bos": "  "})
	b := NewFingerprint(map[string]string{"board_serial": "X1", "machine-id": "abc"})
	if a != b {
		t.Errorf("order and spaces changed the fingerprint: %s != %s", a, b)
	}
	if c := NewFingerprint(map[string]string{"machine-id": "abd", "board_serial": "X1"}); c == a {
		t.Error("a different value gave the same fingerprint")
	}
}

func TestReadFingerprint(t *testing.T) {
	root := fstest.MapFS{
		"etc/machine-id":                {Data: []byte("0123456789abcdef\n")},
		"sys/class/dmi/id/board_serial": {Data: []byte("SER123\n")},
	}
	fp, used, err := ReadFingerprint(root)
	if err != nil || fp == "" {
		t.Fatalf("the fingerprint could not be read: %v", err)
	}
	if len(used) != 2 || used[0] != "board_serial" || used[1] != "machine-id" {
		t.Errorf("sources used %v", used)
	}
	if _, _, err := ReadFingerprint(fstest.MapFS{}); !errors.Is(err, ErrNoFingerprint) {
		t.Errorf("with no sources error %v, want ErrNoFingerprint", err)
	}
}

func TestIDDependsOnKeyAndFingerprint(t *testing.T) {
	fp := NewFingerprint(map[string]string{"machine-id": "m1"})
	a, err := Generate(fp, bytes.NewReader(bytes.Repeat([]byte{1}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate(fp, bytes.NewReader(bytes.Repeat([]byte{2}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Error("different keys gave the same identity")
	}
	if !regexp.MustCompile(`^ABRW(-[A-HJ-NP-Z2-9]{4}){4}$`).MatchString(a.ID) {
		t.Errorf("identity format: %q", a.ID)
	}
	if DeriveID(a.Public, fp) != a.ID {
		t.Error("identity derivation is not stable")
	}
	if DeriveID(a.Public, NewFingerprint(map[string]string{"machine-id": "m2"})) == a.ID {
		t.Error("a different fingerprint gave the same identity")
	}
}

func TestCodeIsStableShort(t *testing.T) {
	id := "ABRW-AAAA-BBBB-CCCC-DDDD"
	c := Code(id)
	if c != Code(id) || !regexp.MustCompile(`^[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}$`).MatchString(c) {
		t.Errorf("approval code %q", c)
	}
	if Code("ABRW-AAAA-BBBB-CCCC-DDDE") == c {
		t.Error("a different identity gave the same code")
	}
}

func TestSignAndVerify(t *testing.T) {
	i, err := Generate(NewFingerprint(map[string]string{"machine-id": "m"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	sig := i.Sign([]byte("merhaba"))
	if !Verify(i.Public, []byte("merhaba"), sig) {
		t.Error("a valid signature was not verified")
	}
	if Verify(i.Public, []byte("merhabA"), sig) {
		t.Error("a changed message was verified")
	}
	if Verify(nil, []byte("merhaba"), sig) {
		t.Error("a signature without a key was verified")
	}
}

func TestSaveLoadAndClonedDisk(t *testing.T) {
	dir := t.TempDir()
	fp := NewFingerprint(map[string]string{"machine-id": "m1"})
	if _, err := Load(dir, fp); !errors.Is(err, ErrNotFound) {
		t.Fatalf("in an empty directory error %v, want ErrNotFound", err)
	}
	i, err := Generate(fp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, i); err != nil {
		t.Fatal(err)
	}
	g, err := Load(dir, fp)
	if err != nil {
		t.Fatal(err)
	}
	if g.ID != i.ID || !bytes.Equal(g.Public, i.Public) {
		t.Error("the loaded identity is not the same as the saved one")
	}
	if !Verify(i.Public, []byte("x"), g.Sign([]byte("x"))) {
		t.Error("the loaded key does not produce the same signature")
	}
	// The disk was copied to another machine: the fingerprint does not match.
	if _, err := Load(dir, NewFingerprint(map[string]string{"machine-id": "baska"})); !errors.Is(err, ErrFingerprintChanged) {
		t.Errorf("on a copied disk error %v, want ErrFingerprintChanged", err)
	}
}
