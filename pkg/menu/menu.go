// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package menu holds the restaurant menu's rules in the catalog: option
// groups and their selection, kitchen stations, product images. Headquarters'
// catalog, the register's catalog and the restaurant module use the same rules.
package menu

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"abarow.com/pkg/money"
)

// Kitchen stations: the order ticket is printed at the product's station.
const (
	Kitchen = "kitchen"
	Bar     = "bar"
)

// ValidStation says the station is known (empty: kitchen).
func ValidStation(s string) bool { return s == "" || s == Kitchen || s == Bar }

// Option is an option; when selected PriceDelta (VAT included, can be
// negative) is added to the line's price.
type Option struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	PriceDelta money.Money `json:"price_delta_kurus"`
}

// Group is a product's option group: in the order at least Min and at most Max
// options are selected (optional if Min is 0).
type Group struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Min     int      `json:"min"`
	Max     int      `json:"max"`
	Options []Option `json:"options"`
}

var (
	// ErrInvalid means the menu information (option groups, station, category)
	// is not fit to be recorded.
	ErrInvalid = errors.New("menü bilgisi geçersiz")
	// ErrChoice means the option selection in the order is invalid (an error the
	// waiter can correct).
	ErrChoice = errors.New("opsiyon seçimi geçersiz")
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

// Limits: so that they fit the menu screen and the ticket.
const (
	MaxGroups   = 10
	MaxOptions  = 30
	MaxNameRune = 40
)

// ValidateGroups checks the option groups: the ids are valid and unique,
// the names filled, 0 ≤ Min ≤ Max ≤ the number of options, Max at least 1.
func ValidateGroups(groups []Group) error {
	if len(groups) > MaxGroups {
		return fmt.Errorf("%w: en çok %d opsiyon grubu", ErrInvalid, MaxGroups)
	}
	seen := map[string]bool{}
	for _, g := range groups {
		if !idPattern.MatchString(g.ID) || seen[g.ID] {
			return fmt.Errorf("%w: opsiyon grubu kimliği %q geçersiz ya da iki kez var", ErrInvalid, g.ID)
		}
		seen[g.ID] = true
		if err := checkName(g.Name); err != nil {
			return fmt.Errorf("%w: opsiyon grubu %s: %s", ErrInvalid, g.ID, err)
		}
		if len(g.Options) == 0 || len(g.Options) > MaxOptions {
			return fmt.Errorf("%w: %s: 1 ile %d arasında seçenek olmalı", ErrInvalid, g.Name, MaxOptions)
		}
		if g.Min < 0 || g.Max < 1 || g.Min > g.Max || g.Max > len(g.Options) {
			return fmt.Errorf("%w: %s: en az %d, en çok %d seçim olamaz (%d seçenek var)", ErrInvalid, g.Name, g.Min, g.Max, len(g.Options))
		}
		ids := map[string]bool{}
		for _, o := range g.Options {
			if !idPattern.MatchString(o.ID) || ids[o.ID] {
				return fmt.Errorf("%w: %s: seçenek kimliği %q geçersiz ya da iki kez var", ErrInvalid, g.Name, o.ID)
			}
			ids[o.ID] = true
			if err := checkName(o.Name); err != nil {
				return fmt.Errorf("%w: %s: seçenek %s: %s", ErrInvalid, g.Name, o.ID, err)
			}
		}
	}
	return nil
}

func checkName(name string) error {
	switch n := len([]rune(strings.TrimSpace(name))); {
	case n == 0:
		return errors.New("ad boş")
	case n > MaxNameRune:
		return fmt.Errorf("ad en çok %d karakter", MaxNameRune)
	}
	return nil
}

// Choice is the options selected in a group.
type Choice struct {
	Group   string   `json:"group"`
	Options []string `json:"options"`
}

// Picked is a selected option (with its group).
type Picked struct {
	Group string
	Option
}

// Chosen is the result of the selection: the selected options in group and option order,
// the total of the price differences.
type Chosen struct {
	Options []Picked
	Delta   money.Money
}

// Label gives the selected ones in the form to be written on the ticket and the screen:
// "medium rare, cheddar".
func (c Chosen) Label() string {
	names := make([]string, len(c.Options))
	for i, o := range c.Options {
		names[i] = o.Name
	}
	return strings.Join(names, ", ")
}

// Choose checks the selection in the order: in each group between Min and Max
// options, only known groups and options, the same option once.
func Choose(groups []Group, choices []Choice) (Chosen, error) {
	byGroup := map[string][]string{}
	for _, c := range choices {
		if !slices.ContainsFunc(groups, func(g Group) bool { return g.ID == c.Group }) {
			return Chosen{}, fmt.Errorf("%w: bilinmeyen grup %q", ErrChoice, c.Group)
		}
		if _, dup := byGroup[c.Group]; dup {
			return Chosen{}, fmt.Errorf("%w: grup %q iki kez", ErrChoice, c.Group)
		}
		byGroup[c.Group] = c.Options
	}
	var out Chosen
	for _, g := range groups {
		picked := byGroup[g.ID]
		if len(picked) < g.Min {
			if g.Min == 1 {
				return Chosen{}, fmt.Errorf("%w: %s seçin", ErrChoice, g.Name)
			}
			return Chosen{}, fmt.Errorf("%w: %s için en az %d seçin", ErrChoice, g.Name, g.Min)
		}
		if len(picked) > g.Max {
			return Chosen{}, fmt.Errorf("%w: %s için en çok %d seçilebilir", ErrChoice, g.Name, g.Max)
		}
		seen := map[string]bool{}
		for _, id := range picked {
			if seen[id] {
				return Chosen{}, fmt.Errorf("%w: %s: aynı seçenek iki kez", ErrChoice, g.Name)
			}
			seen[id] = true
		}
		// The options are written in menu order.
		for _, o := range g.Options {
			if seen[o.ID] {
				out.Options = append(out.Options, Picked{Group: g.ID, Option: o})
				out.Delta += o.PriceDelta
				delete(seen, o.ID)
			}
		}
		if len(seen) > 0 {
			return Chosen{}, fmt.Errorf("%w: %s: bilinmeyen seçenek", ErrChoice, g.Name)
		}
	}
	return out, nil
}

// MaxImageBytes is the largest size of a product image. The screen shrinks the image
// before uploading (at most 640 px); this limit keeps the catalog message below NATS's
// message limit (1 MB).
const MaxImageBytes = 512 << 10

// ErrImage means the image was not accepted.
var ErrImage = errors.New("görsel kabul edilmedi")

// CheckImage works out the image's type from its content (JPEG, PNG or WebP;
// the extension or what the client says is not looked at) and checks its size.
// It returns the type and the SHA-256 digest (hexadecimal).
func CheckImage(data []byte) (contentType, sum string, err error) {
	if len(data) == 0 {
		return "", "", fmt.Errorf("%w: dosya boş", ErrImage)
	}
	if len(data) > MaxImageBytes {
		return "", "", fmt.Errorf("%w: en çok %d KB olmalı (%d KB)", ErrImage, MaxImageBytes>>10, (len(data)+1023)>>10)
	}
	switch t := http.DetectContentType(data); t {
	case "image/jpeg", "image/png", "image/webp":
		h := sha256.Sum256(data)
		return t, hex.EncodeToString(h[:]), nil
	default:
		return "", "", fmt.Errorf("%w: JPEG, PNG ya da WebP olmalı (%s)", ErrImage, t)
	}
}
