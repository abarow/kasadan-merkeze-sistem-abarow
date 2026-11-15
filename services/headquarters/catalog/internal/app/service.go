// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package app is the application layer of headquarters' catalog management: adding
// and changing products, the price schedule, withdrawing from sale. Every change is recorded
// with who made it and the product's latest state is written to the outbox in the same
// transaction to go to the registers.
package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"abarow.com/pkg/menu"
	"abarow.com/pkg/money"
	"abarow.com/pkg/outbox"
	"abarow.com/services/headquarters/catalog/internal/domain"
)

// Change is the record of a change made to the catalog (who, when, what).
type Change struct {
	Code   string
	At     time.Time
	By     string
	Action string
	// Detail is the readable summary of the change.
	Detail string
}

// Change kinds.
const (
	ActionCreated        = "created"
	ActionUpdated        = "updated"
	ActionPriceScheduled = "price_scheduled"
	ActionPriceCancelled = "price_cancelled"
	ActionDiscontinued   = "discontinued"
	ActionReactivated    = "reactivated"
	ActionImported       = "imported"
	ActionImageSet       = "image_set"
	ActionImageRemoved   = "image_removed"
)

// Repository is headquarters' catalog database.
type Repository interface {
	// Get returns the product; domain.ErrNotFound if none.
	Get(ctx context.Context, code string) (domain.Product, error)
	// List returns the products that have the query in their name, code or barcode
	// (all if the query is empty), sorted by name.
	List(ctx context.Context, query string, limit int) ([]domain.Product, error)
	// Create writes the new product; domain.ErrExists if the code exists, domain.ErrBarcodeTaken
	// if the barcode belongs to another product.
	Create(ctx context.Context, p domain.Product, change Change, msg outbox.Message) error
	// Update updates the product from version p.Version-1 to p.Version; domain.ErrConflict
	// if someone changed it in the meantime.
	Update(ctx context.Context, p domain.Product, change Change, msg outbox.Message) error
	// History returns the product's changes from newest to oldest.
	History(ctx context.Context, code string, limit int) ([]Change, error)
	// SetImage updates the product from version p.Version-1 (the image's digest and
	// type) and writes the image (deletes it if Data is empty); the change record and
	// the messages are in the same transaction. domain.ErrConflict if someone changed it
	// in the meantime.
	SetImage(ctx context.Context, p domain.Product, img domain.Image, change Change, msgs []outbox.Message) error
	// Image is the product's image; domain.ErrNotFound if none.
	Image(ctx context.Context, code string) (domain.Image, error)
}

// Encoder turns the product's latest state and image into the messages that go
// to the registers.
type Encoder interface {
	Encode(p domain.Product, at time.Time) (outbox.Message, error)
	EncodeImage(img domain.Image, at time.Time) (outbox.Message, error)
}

// Service holds the use cases of catalog management.
type Service struct {
	repo Repository
	enc  Encoder
	now  func() time.Time
	loc  *time.Location
}

// NewService returns a service that writes future dates in loc time.
func NewService(repo Repository, enc Encoder, loc *time.Location) *Service {
	return &Service{repo: repo, enc: enc, now: time.Now, loc: loc}
}

// MaxResults is the largest number of products returned in a list.
const MaxResults = 200

// Info is the product's details other than the price.
type Info struct {
	Name     string
	Unit     domain.Unit
	TaxRate  money.Rate
	Barcodes []string
	QuickKey bool
	// Restaurant menu.
	Category  string
	MenuOrder int
	Station   string
	Options   []menu.Group
}

func (in Info) apply(p domain.Product) domain.Product {
	p.Name, p.Unit, p.TaxRate, p.Barcodes, p.QuickKey = in.Name, in.Unit, in.TaxRate, in.Barcodes, in.QuickKey
	p.Category, p.MenuOrder, p.Station, p.Options = in.Category, in.MenuOrder, in.Station, in.Options
	return p
}

// Get returns the product.
func (s *Service) Get(ctx context.Context, code string) (domain.Product, error) {
	return s.repo.Get(ctx, code)
}

// List searches the products.
func (s *Service) List(ctx context.Context, query string, limit int) ([]domain.Product, error) {
	if limit <= 0 || limit > MaxResults {
		limit = MaxResults
	}
	return s.repo.List(ctx, query, limit)
}

// History returns the product's changes.
func (s *Service) History(ctx context.Context, code string) ([]Change, error) {
	if _, err := s.repo.Get(ctx, code); err != nil {
		return nil, err
	}
	return s.repo.History(ctx, code, 100)
}

// Create adds a new product; the price is valid from now.
func (s *Service) Create(ctx context.Context, by, code string, info Info, price money.Money) (domain.Product, error) {
	now := s.now()
	p := info.apply(domain.Product{Code: code, Prices: []domain.Price{{Amount: price}}, UpdatedBy: by, UpdatedAt: now, Version: 1}).Normalize()
	if err := p.Validate(); err != nil {
		return domain.Product{}, err
	}
	detail := fmt.Sprintf("%s eklendi, fiyat %s TL", p.Name, p.Prices[0].Amount)
	return p, s.save(ctx, p, true, ActionCreated, detail)
}

// Update changes the product's details other than the price. version is the version
// the product was at when editing started.
func (s *Service) Update(ctx context.Context, by, code string, version int64, info Info) (domain.Product, error) {
	return s.change(ctx, by, code, version, ActionUpdated, func(p domain.Product) (domain.Product, string, error) {
		before := p
		p = info.apply(p).Normalize()
		return p, describeUpdate(before, p), p.Validate()
	})
}

// SchedulePrice gives the product a price valid from the moment from (from now if
// zero or in the past).
func (s *Service) SchedulePrice(ctx context.Context, by, code string, version int64, price money.Money, from time.Time) (domain.Product, error) {
	return s.change(ctx, by, code, version, ActionPriceScheduled, func(p domain.Product) (domain.Product, string, error) {
		now := s.now()
		old := p.PriceAt(now)
		p, err := p.SchedulePrice(price, from, now)
		if err != nil {
			return p, "", err
		}
		if from.IsZero() || !from.After(now) {
			return p, fmt.Sprintf("fiyat %s → %s TL (hemen)", old, price), nil
		}
		return p, fmt.Sprintf("fiyat %s TL, %s itibarıyla", price, from.In(s.loc).Format("02.01.2006 15:04")), nil
	})
}

// CancelPrice withdraws a price that has not taken effect yet.
func (s *Service) CancelPrice(ctx context.Context, by, code string, version int64, from time.Time) (domain.Product, error) {
	return s.change(ctx, by, code, version, ActionPriceCancelled, func(p domain.Product) (domain.Product, string, error) {
		amount := p.PriceAt(from)
		p, err := p.CancelPrice(from, s.now())
		return p, fmt.Sprintf("%s itibarıyla verilen %s TL fiyat geri alındı", from.In(s.loc).Format("02.01.2006 15:04"), amount), err
	})
}

// SetDiscontinued withdraws the product from sale or puts it back on sale.
func (s *Service) SetDiscontinued(ctx context.Context, by, code string, version int64, discontinued bool) (domain.Product, error) {
	action, detail := ActionReactivated, "yeniden satışa açıldı"
	if discontinued {
		action, detail = ActionDiscontinued, "satıştan kaldırıldı"
	}
	return s.change(ctx, by, code, version, action, func(p domain.Product) (domain.Product, string, error) {
		p.Discontinued = discontinued
		return p, detail, nil
	})
}

// Import loads products in bulk (e.g. the registers' old catalog.json): a new
// product is added, an existing one's details and price schedule change to what is loaded.
// The first error stops it; what was loaded up to then stays. It returns the number
// of products loaded.
func (s *Service) Import(ctx context.Context, by string, products []domain.Product) (int, error) {
	for i, in := range products {
		now := s.now()
		in = in.Normalize()
		in.UpdatedBy, in.UpdatedAt = by, now
		if err := in.Validate(); err != nil {
			return i, err
		}
		existing, err := s.repo.Get(ctx, in.Code)
		switch {
		case err == nil:
			in.Version = existing.Version + 1
			in.Discontinued = existing.Discontinued
			// The image does not come with the file; an uploaded image stays.
			in.ImageSHA256, in.ImageType = existing.ImageSHA256, existing.ImageType
			err = s.save(ctx, in, false, ActionImported, "dosyadan güncellendi")
		case errors.Is(err, domain.ErrNotFound):
			in.Version = 1
			err = s.save(ctx, in, true, ActionImported, "dosyadan eklendi")
		}
		if err != nil {
			return i, fmt.Errorf("%s: %w", in.Code, err)
		}
	}
	return len(products), nil
}

// Image is the product's image.
func (s *Service) Image(ctx context.Context, code string) (domain.Image, error) {
	return s.repo.Image(ctx, code)
}

// SetImage gives or changes the product's image. The image is checked from its content
// (JPEG, PNG, WebP; at most menu.MaxImageBytes). version is the version the product was at
// when editing started.
func (s *Service) SetImage(ctx context.Context, by, code string, version int64, data []byte) (domain.Product, error) {
	ct, sum, err := menu.CheckImage(data)
	if err != nil {
		return domain.Product{}, fmt.Errorf("%w (%s): %w", domain.ErrInvalidProduct, code, err)
	}
	return s.saveImage(ctx, by, code, version, domain.Image{Code: code, SHA256: sum, ContentType: ct, Data: data},
		ActionImageSet, fmt.Sprintf("görsel yüklendi (%d KB)", (len(data)+1023)>>10))
}

// RemoveImage removes the product's image.
func (s *Service) RemoveImage(ctx context.Context, by, code string, version int64) (domain.Product, error) {
	return s.saveImage(ctx, by, code, version, domain.Image{Code: code}, ActionImageRemoved, "görsel kaldırıldı")
}

func (s *Service) saveImage(ctx context.Context, by, code string, version int64, img domain.Image, action, detail string) (domain.Product, error) {
	p, err := s.repo.Get(ctx, code)
	if err != nil {
		return domain.Product{}, err
	}
	if version != p.Version {
		return domain.Product{}, domain.ErrConflict
	}
	p.ImageSHA256, p.ImageType = img.SHA256, img.ContentType
	p.UpdatedBy, p.UpdatedAt, p.Version = by, s.now(), version+1
	// The image goes before the product's message: by the time the register sees the new digest
	// the image has mostly arrived.
	imgMsg, err := s.enc.EncodeImage(img, p.UpdatedAt)
	if err != nil {
		return domain.Product{}, err
	}
	msg, err := s.enc.Encode(p, p.UpdatedAt)
	if err != nil {
		return domain.Product{}, err
	}
	change := Change{Code: p.Code, At: p.UpdatedAt, By: by, Action: action, Detail: detail}
	return p, s.repo.SetImage(ctx, p, img, change, []outbox.Message{imgMsg, msg})
}

// change reads the product, changes it with fn and saves it with its new version.
func (s *Service) change(ctx context.Context, by, code string, version int64, action string,
	fn func(domain.Product) (domain.Product, string, error)) (domain.Product, error) {
	p, err := s.repo.Get(ctx, code)
	if err != nil {
		return domain.Product{}, err
	}
	if version != p.Version {
		return domain.Product{}, domain.ErrConflict
	}
	p, detail, err := fn(p)
	if err != nil {
		return domain.Product{}, err
	}
	p.UpdatedBy, p.UpdatedAt, p.Version = by, s.now(), version+1
	return p, s.save(ctx, p, false, action, detail)
}

func (s *Service) save(ctx context.Context, p domain.Product, created bool, action, detail string) error {
	msg, err := s.enc.Encode(p, p.UpdatedAt)
	if err != nil {
		return err
	}
	change := Change{Code: p.Code, At: p.UpdatedAt, By: p.UpdatedBy, Action: action, Detail: detail}
	if created {
		return s.repo.Create(ctx, p, change, msg)
	}
	return s.repo.Update(ctx, p, change, msg)
}

// describeUpdate summarizes the details change in a readable form.
func describeUpdate(a, b domain.Product) string {
	var parts []string
	if a.Name != b.Name {
		parts = append(parts, fmt.Sprintf("ad %q → %q", a.Name, b.Name))
	}
	if a.Unit != b.Unit {
		parts = append(parts, fmt.Sprintf("birim %s → %s", a.Unit, b.Unit))
	}
	if a.TaxRate != b.TaxRate {
		parts = append(parts, fmt.Sprintf("KDV %%%s → %%%s", percent(a.TaxRate), percent(b.TaxRate)))
	}
	if fmt.Sprint(a.Barcodes) != fmt.Sprint(b.Barcodes) {
		parts = append(parts, fmt.Sprintf("barkodlar %v → %v", a.Barcodes, b.Barcodes))
	}
	if a.QuickKey != b.QuickKey {
		parts = append(parts, map[bool]string{true: "hızlı tuşa eklendi", false: "hızlı tuştan çıkarıldı"}[b.QuickKey])
	}
	if a.Category != b.Category {
		parts = append(parts, fmt.Sprintf("kategori %q → %q", a.Category, b.Category))
	}
	if a.MenuOrder != b.MenuOrder {
		parts = append(parts, fmt.Sprintf("menü sırası %d → %d", a.MenuOrder, b.MenuOrder))
	}
	if a.Station != b.Station {
		parts = append(parts, fmt.Sprintf("istasyon %s → %s", stationName(a.Station), stationName(b.Station)))
	}
	if !reflect.DeepEqual(a.Options, b.Options) {
		parts = append(parts, fmt.Sprintf("opsiyonlar değişti (%d grup)", len(b.Options)))
	}
	if len(parts) == 0 {
		return "değişiklik yok"
	}
	return strings.Join(parts, "; ")
}

func stationName(s string) string {
	if s == menu.Bar {
		return "bar"
	}
	return "mutfak"
}

// percent writes the base points as a percentage: 2000 → "20", 150 → "1,5".
func percent(r money.Rate) string {
	s := fmt.Sprintf("%d", r/100)
	if f := r % 100; f != 0 {
		s += strings.TrimRight(fmt.Sprintf(",%02d", f), "0")
	}
	return s
}
