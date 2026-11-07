// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package domain holds the business rules of headquarters' catalog management: products,
// barcodes and the headquarters price schedule.
package domain

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"abarow.com/pkg/menu"
	"abarow.com/pkg/money"
	"abarow.com/pkg/price"
)

// Unit is the product's sales unit.
type Unit string

const (
	Piece Unit = "piece"
	Kg    Unit = "kg"
)

var (
	// ErrInvalidProduct means the product is not fit to be recorded.
	ErrInvalidProduct = errors.New("ürün geçersiz")
	// ErrNotFound means the product does not exist.
	ErrNotFound = errors.New("ürün bulunamadı")
	// ErrExists means a product with the same code exists.
	ErrExists = errors.New("bu kodla ürün zaten var")
	// ErrBarcodeTaken means the barcode belongs to another product.
	ErrBarcodeTaken = errors.New("barkod başka bir ürüne ait")
	// ErrConflict means the product was changed by someone else after it was read.
	ErrConflict = errors.New("ürün bu arada başkası tarafından değiştirildi; yeniden açıp tekrar deneyin")
)

// Product is a product in headquarters' catalog. Prices include VAT;
// for a weighed product it is the price per kilogram.
type Product struct {
	// Code is the product's code (stock code); it does not change. It goes into the subject of
	// the message that goes to the registers (catalog.product.<code>): letters, digits, - and _.
	Code     string
	Name     string
	Unit     Unit
	TaxRate  money.Rate
	Barcodes []string
	QuickKey bool
	// Prices is the headquarters price schedule (sorted by effective moment). The first
	// record's effective moment is zero (from the beginning).
	Prices []Price
	// Discontinued: withdrawn from sale (not searched at the register, not found when scanned).
	Discontinued bool
	// Restaurant menu (see pkg/menu): the category (not on the menu if empty), the order
	// on the menu, the kitchen station and the option groups.
	Category  string
	MenuOrder int
	Station   string
	Options   []menu.Group
	// The digest and type of the current image; empty if there is no image. The image itself is
	// stored separately (Image).
	ImageSHA256 string
	ImageType   string
	UpdatedBy   string
	UpdatedAt   time.Time
	// Version increases with each record: if two people change the same product
	// at the same time, the one who saves later gets ErrConflict, the other's change
	// is not silently overwritten.
	Version int64
}

// Image is the product's image; if Data is empty the image has been removed.
type Image struct {
	Code        string
	SHA256      string
	ContentType string
	Data        []byte
}

// MaxCategoryRunes is the largest number of characters in a category name.
const MaxCategoryRunes = 40

// Price is a record of the headquarters price schedule (pkg/price): it is valid
// from the moment From (from the beginning if From is zero). There is no
// removal (Clear) record in the headquarters schedule.
type Price = price.Point

var (
	codePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	barcodePattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
)

// MaxTaxRate is the upper limit of the VAT rate (100%).
const MaxTaxRate money.Rate = 10000

// Normalize prepares the product for recording: trims the spaces, sorts and
// de-duplicates the barcodes, sorts the price schedule.
func (p Product) Normalize() Product {
	p.Code = strings.TrimSpace(p.Code)
	p.Name = strings.Join(strings.Fields(p.Name), " ")
	p.Category = strings.Join(strings.Fields(p.Category), " ")
	p.Options = slices.Clone(p.Options)
	for i, g := range p.Options {
		g.Name = strings.TrimSpace(g.Name)
		g.Options = slices.Clone(g.Options)
		for j := range g.Options {
			g.Options[j].Name = strings.TrimSpace(g.Options[j].Name)
		}
		p.Options[i] = g
	}
	var barcodes []string
	for _, b := range p.Barcodes {
		if b = strings.TrimSpace(b); b != "" {
			barcodes = append(barcodes, b)
		}
	}
	slices.Sort(barcodes)
	p.Barcodes = slices.Compact(barcodes)
	p.Prices = slices.Clone(p.Prices)
	slices.SortStableFunc(p.Prices, func(a, b Price) int { return a.From.Compare(b.From) })
	return p
}

// Validate checks the product.
func (p Product) Validate() error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w (%s): %s", ErrInvalidProduct, p.Code, fmt.Sprintf(format, args...))
	}
	switch {
	case !codePattern.MatchString(p.Code):
		return fmt.Errorf("%w: ürün kodu %q geçersiz (harf, rakam, - ve _; en çok 64 karakter)", ErrInvalidProduct, p.Code)
	case p.Name == "":
		return invalid("ad boş")
	case len([]rune(p.Name)) > 120:
		return invalid("ad en çok 120 karakter")
	case p.Unit != Piece && p.Unit != Kg:
		return invalid("bilinmeyen birim %q", p.Unit)
	case p.TaxRate < 0 || p.TaxRate > MaxTaxRate:
		return invalid("KDV oranı %%0–%%100 arasında olmalı")
	}
	for _, b := range p.Barcodes {
		if !barcodePattern.MatchString(b) {
			return invalid("barkod %q geçersiz", b)
		}
	}
	if len([]rune(p.Category)) > MaxCategoryRunes {
		return invalid("kategori en çok %d karakter", MaxCategoryRunes)
	}
	if !menu.ValidStation(p.Station) {
		return invalid("bilinmeyen istasyon %q", p.Station)
	}
	if err := menu.ValidateGroups(p.Options); err != nil {
		return fmt.Errorf("%w (%s): %w", ErrInvalidProduct, p.Code, err)
	}
	return ValidatePrices(p.Code, p.Prices)
}

// ValidatePrices checks the headquarters price schedule: there is a price valid from the
// beginning, the prices are not negative, there are not two prices at the same moment.
func ValidatePrices(code string, prices []Price) error {
	if len(prices) == 0 || !prices[0].From.IsZero() {
		return fmt.Errorf("%w (%s): baştan beri geçerli bir fiyat gerekli", ErrInvalidProduct, code)
	}
	for i, pr := range prices {
		if pr.Amount < 0 {
			return fmt.Errorf("%w (%s): fiyat negatif", ErrInvalidProduct, code)
		}
		if i > 0 && !pr.From.After(prices[i-1].From) {
			return fmt.Errorf("%w (%s): aynı anda iki fiyat", ErrInvalidProduct, code)
		}
	}
	return nil
}

// PriceAt is the headquarters price in effect at the moment t.
func (p Product) PriceAt(t time.Time) money.Money {
	amount, _ := price.At(p.Prices, t)
	return amount
}

// SchedulePrice adds the price valid from the moment from to the schedule.
// If from is before now or zero the price is valid now. A price given
// at the same moment is replaced by the new one. Records left in the past are dropped.
func (p Product) SchedulePrice(amount money.Money, from, now time.Time) (Product, error) {
	prices, err := price.Plan(p.Prices, amount, false, from, now)
	if err != nil {
		return p, fmt.Errorf("%w (%s): fiyat negatif", ErrInvalidProduct, p.Code)
	}
	p.Prices = prices
	return p.Normalize(), nil
}

// CancelPrice removes a price that has not taken effect yet from the schedule.
func (p Product) CancelPrice(from, now time.Time) (Product, error) {
	prices, err := price.Cancel(p.Prices, from, now)
	switch {
	case errors.Is(err, price.ErrStarted):
		return p, fmt.Errorf("%w (%s): yürürlüğe girmiş fiyat geri alınamaz; yeni fiyat verin", ErrInvalidProduct, p.Code)
	case err != nil:
		return p, fmt.Errorf("%w: %s anında fiyat yok", ErrNotFound, from.Format(time.RFC3339))
	}
	p.Prices = prices
	return p, nil
}
