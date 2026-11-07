// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import "errors"

// MaxGrams is the largest weight accepted (register scales weigh 15–30 kg;
// more than that is a read error).
const MaxGrams = 100_000

// The situations in which the scale gives no weight. They are shown to the cashier.
var (
	ErrNoScale       = errors.New("terazi bağlı değil")
	ErrScaleMotion   = errors.New("terazi sabitlenmedi; ürün durunca yeniden seçin")
	ErrScaleEmpty    = errors.New("terazide ürün yok")
	ErrScaleNegative = errors.New("terazi eksi gösteriyor; boşken sıfırlayın")
	ErrScaleOver     = errors.New("terazinin kapasitesi aşıldı")
	ErrScaleFault    = errors.New("terazi hata bildiriyor")
	ErrScaleUnits    = errors.New("terazi kilogram dışında bir birimde; teraziyi kg'a ayarlayın")
	ErrScaleReply    = errors.New("terazinin yanıtı okunamadı; protokol ayarını denetleyin")
	ErrNoScaleSim    = errors.New("terazi simülatör değil; ağırlık taklit edilemez")
)

// Reading is a reading from the scale.
type Reading struct {
	Grams  int64
	Stable bool
}

// Check verifies that the reading is fit for a sale: stable, greater than zero and
// reasonable.
func (r Reading) Check() error {
	switch {
	case !r.Stable:
		return ErrScaleMotion
	case r.Grams < 0:
		return ErrScaleNegative
	case r.Grams == 0:
		return ErrScaleEmpty
	case r.Grams > MaxGrams:
		return ErrScaleOver
	}
	return nil
}
