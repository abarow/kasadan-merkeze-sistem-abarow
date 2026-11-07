// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package nodes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	key1 = "UCUHTJURBM4UVRGDECSQQN2NN5LUJ6U7RPDGUQG46E7YM7UPBEBXLMJW"
	key2 = "UCA3DL2HXSISM4S6744AJTVQALSYF2MFYY6GHQQS3HEKTLGYRJGPMKF7"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		regs []Node
		want string
	}{
		{"boş liste", nil, ""},
		{"geçerli", []Node{{ID: "register-1", Name: "Kasa 1", NKey: key1}, {ID: "register_2", NKey: key2}}, ""},
		{"noktalı kimlik", []Node{{ID: "register.1", NKey: key1}}, "invalid id"},
		{"joker kimlik", []Node{{ID: "register*", NKey: key1}}, "invalid id"},
		{"boş kimlik", []Node{{ID: "", NKey: key1}}, "invalid id"},
		{"uzun kimlik", []Node{{ID: strings.Repeat("k", 64), NKey: key1}}, "invalid id"},
		{"makine adı uzunluğunda kimlik", []Node{{ID: strings.Repeat("k", 63), NKey: key1}}, ""},
		{"tekrarlanan kimlik", []Node{{ID: "register-1", NKey: key1}, {ID: "register-1", NKey: key2}}, "iki kez"},
		{"gizli anahtar", []Node{{ID: "register-1", NKey: "S" + key1[1:]}}, "the key is invalid"},
		{"kısa anahtar", []Node{{ID: "register-1", NKey: key1[:20]}}, "the key is invalid"},
		{"küçük harfli anahtar", []Node{{ID: "register-1", NKey: strings.ToLower(key1)}}, "the key is invalid"},
		{"tekrarlanan anahtar", []Node{{ID: "register-1", NKey: key1}, {ID: "register-2", NKey: key1}}, "on another device"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.regs)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("no error was expected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error should contain %q: %v", tt.want, err)
			}
		})
	}
}

func TestSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.json")
	regs, err := Load(path)
	if err != nil || len(regs) != 0 {
		t.Fatalf("an empty list was expected when the file is missing: %v %v", regs, err)
	}

	regs = Put(regs, Node{ID: "register-2", NKey: key2})
	regs = Put(regs, Node{ID: "register-1", Name: "Giriş", NKey: key1})
	if err := Save(path, regs); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// It is written sorted by id.
	if len(got) != 2 || got[0].ID != "register-1" || got[0].DisplayName() != "Giriş" || got[1].DisplayName() != "register-2" {
		t.Fatalf("okunan liste: %+v", got)
	}

	// When the same id is added again its key changes (the register was reinstalled).
	got = Put(got, Node{ID: "register-2", NKey: "UBB" + key2[3:]})
	if len(got) != 2 || got[1].NKey[:3] != "UBB" {
		t.Fatalf("yerine koyma: %+v", got)
	}

	got, ok := Remove(got, "register-1")
	if !ok || len(got) != 1 || got[0].ID != "register-2" {
		t.Fatalf("removal: %v %+v", ok, got)
	}
	if _, ok := Remove(got, "register-9"); ok {
		t.Error("a register not in the list counted as removed")
	}
	if err := Save(path, got); err != nil {
		t.Fatal(err)
	}
	got, err = Load(path)
	if err != nil || len(got) != 1 {
		t.Fatalf("son liste: %+v %v", got, err)
	}

	// An invalid list is not written.
	if err := Save(path, []Node{{ID: "kasa 1", NKey: key1}}); err == nil {
		t.Error("an invalid list was written")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("a temporary file remained: %v", entries)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.json")
	if err := os.WriteFile(path, []byte(`[{"id":"register-1","nkey":"`+key1+`","key":"x"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("an unknown field was accepted (a typo would have been silently ignored)")
	}
}
