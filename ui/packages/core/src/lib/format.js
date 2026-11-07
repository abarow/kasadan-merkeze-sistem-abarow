// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Amounts are carried everywhere in kuruş (integers); they are turned into TL only
// when displayed.

const tl = new Intl.NumberFormat('tr-TR', { style: 'currency', currency: 'TRY' })
const kg = new Intl.NumberFormat('tr-TR', { minimumFractionDigits: 3, maximumFractionDigits: 3 })

export const money = (kurus) => tl.format(kurus / 100)

// Miktar binde bir birimdir: 1 adet = 1000, 1,250 kg = 1250.
export const quantity = (milli, weighed) => (weighed ? `${kg.format(milli / 1000)} kg` : `${milli / 1000} adet`)

// parseTL turns the amount the cashier typed into kuruş: "200" → 20000,
// "1.250,5" → 125050. NaN if invalid. Floating point is not used.
export function parseTL(text) {
  const m = String(text).trim().replace(/\./g, '').match(/^(\d+)(?:,(\d{1,2}))?$/)
  if (!m) return NaN
  return Number(m[1]) * 100 + Number((m[2] ?? '0').padEnd(2, '0'))
}

// toTL turns kuruş into the text to be written in the input field: 12537 → "125,37".
export const toTL = (kurus) => `${Math.trunc(kurus / 100)},${String(kurus % 100).padStart(2, '0')}`

// methodName is the payment method's name on the screen.
export const methodName = (m) => ({ cash: 'Nakit', card: 'Kredi kartı', meal_card: 'Yemek kartı', other: 'Diğer' })[m] ?? m
