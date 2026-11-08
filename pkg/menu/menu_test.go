// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package menu

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"
)

var groups = []Group{
	{ID: "pisirme", Name: "Pişirme", Min: 1, Max: 1, Options: []Option{{ID: "az", Name: "az pişmiş"}, {ID: "orta", Name: "orta"}, {ID: "iyi", Name: "iyi pişmiş"}}},
	{ID: "ekstra", Name: "Ekstralar", Min: 0, Max: 2, Options: []Option{{ID: "kasar", Name: "kaşar", PriceDelta: 1500}, {ID: "sogan", Name: "soğan", PriceDelta: 500}, {ID: "jalapeno", Name: "jalapeno", PriceDelta: 700}}},
}

func TestValidateGroups(t *testing.T) {
	if err := ValidateGroups(groups); err != nil {
		t.Fatal(err)
	}
	bad := []struct {
		name string
		g    []Group
	}{
		{"seçeneksiz", []Group{{ID: "a", Name: "A", Max: 1}}},
		{"max seçenekten çok", []Group{{ID: "a", Name: "A", Max: 2, Options: []Option{{ID: "x", Name: "X"}}}}},
		{"min max'tan büyük", []Group{{ID: "a", Name: "A", Min: 2, Max: 1, Options: []Option{{ID: "x", Name: "X"}, {ID: "y", Name: "Y"}}}}},
		{"aynı grup", []Group{groups[0], groups[0]}},
		{"aynı seçenek", []Group{{ID: "a", Name: "A", Max: 1, Options: []Option{{ID: "x", Name: "X"}, {ID: "x", Name: "Y"}}}}},
		{"adsız", []Group{{ID: "a", Name: " ", Max: 1, Options: []Option{{ID: "x", Name: "X"}}}}},
		{"kimlikte nokta", []Group{{ID: "a.b", Name: "A", Max: 1, Options: []Option{{ID: "x", Name: "X"}}}}},
	}
	for _, c := range bad {
		if err := ValidateGroups(c.g); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// Selection: the required group must be selected, the maximum must not be exceeded; the price differences
// are summed, the options are written in menu order.
func TestChoose(t *testing.T) {
	c, err := Choose(groups, []Choice{{Group: "ekstra", Options: []string{"sogan", "kasar"}}, {Group: "pisirme", Options: []string{"az"}}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Delta != 2000 || c.Label() != "az pişmiş, kaşar, soğan" {
		t.Errorf("%+v %q", c, c.Label())
	}
	for name, choices := range map[string][]Choice{
		"zorunlu seçilmedi":    nil,
		"çok seçim":            {{Group: "pisirme", Options: []string{"az", "orta"}}},
		"bilinmeyen seçenek":   {{Group: "pisirme", Options: []string{"yanik"}}},
		"bilinmeyen grup":      {{Group: "pisirme", Options: []string{"az"}}, {Group: "sos", Options: []string{"x"}}},
		"aynı seçenek iki kez": {{Group: "pisirme", Options: []string{"az"}}, {Group: "ekstra", Options: []string{"kasar", "kasar"}}},
	} {
		if _, err := Choose(groups, choices); !errors.Is(err, ErrChoice) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := Choose(groups, nil); err == nil || !strings.Contains(err.Error(), "Pişirme seçin") {
		t.Errorf("error message: %v", err)
	}
}

func TestCheckImage(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	ct, sum, err := CheckImage(buf.Bytes())
	if err != nil || ct != "image/png" || len(sum) != 64 {
		t.Fatalf("%s %s %v", ct, sum, err)
	}
	if _, _, err := CheckImage([]byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>")); !errors.Is(err, ErrImage) {
		t.Errorf("SVG was accepted: %v", err)
	}
	if _, _, err := CheckImage(make([]byte, MaxImageBytes+1)); !errors.Is(err, ErrImage) {
		t.Errorf("large file: %v", err)
	}
}
