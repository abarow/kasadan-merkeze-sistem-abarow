// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// The shared format helpers of the management screens (store server, headquarters).
// Amounts come from the API in kuruş (integers); they are turned into TL only
// when displayed.

const tl = new Intl.NumberFormat('tr-TR', { style: 'currency', currency: 'TRY' })
const qty = new Intl.NumberFormat('tr-TR', { maximumFractionDigits: 3 })

export const money = (kurus) => tl.format(kurus / 100)

// Miktar binde bir birimdir: 1 adet = 1000, 1,250 kg = 1250. Birim
// (piece/kg) is not in the report; the number is shown as it is.
export const quantity = (milli) => qty.format(milli / 1000)

export const rate = (bp) => `%${String(bp / 100).replace('.', ',')}`

// when shows the time briefly: only the hour if today, otherwise date and time.
export function when(iso) {
  if (!iso) return '—'
  const d = new Date(iso)
  const sameDay = d.toDateString() === new Date().toDateString()
  return sameDay
    ? d.toLocaleTimeString('tr-TR')
    : d.toLocaleString('tr-TR', { dateStyle: 'short', timeStyle: 'short' })
}

export const method = (m) => ({ cash: 'Nakit', card: 'Kredi kartı', meal_card: 'Yemek kartı', other: 'Diğer' })[m] ?? m

// Dates in the API are in the form 2026-10-27 (in store time). In day
// calculations noon is taken so that the browser's time zone does not interfere.
const noon = (date) => new Date(`${date}T12:00:00`)
const iso = (d) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`

export function addDays(date, n) {
  const d = noon(date)
  d.setDate(d.getDate() + n)
  return iso(d)
}

export const monthStart = (date) => `${date.slice(0, 8)}01`

export const longDate = (date) => noon(date).toLocaleDateString('tr-TR', { dateStyle: 'long' })

export const shortDate = (date) => noon(date).toLocaleDateString('tr-TR', { day: 'numeric', month: 'short', weekday: 'short' })

// parseMoney turns the amount typed on the screen into kuruş: "16,50", "16,5",
// "1.234,56", "16.50" and "16" are accepted; null if invalid.
export function parseMoney(text) {
  let s = String(text ?? '').replace(/\s|₺|TL/gi, '')
  if (s.includes(',')) s = s.replace(/\./g, '').replace(',', '.')
  if (!/^\d+(\.\d{1,2})?$/.test(s)) return null
  const [lira, kurus = ''] = s.split('.')
  return Number(lira) * 100 + Number(kurus.padEnd(2, '0'))
}

// moneyInput writes kuruş for an edit field: 1650 → "16,50".
export const moneyInput = (kurus) => (kurus / 100).toFixed(2).replace('.', ',')

// dateTime writes the moment briefly in store time: 02.11.2026 00:00.
export const dateTime = (iso) =>
  new Date(iso).toLocaleString('tr-TR', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' })

// parseQty turns the quantity entered into thousandths of a unit ("12" → 12000,
// "1,250" or "1.250" → 1250: both comma and dot are decimal, a thousands
// separator is not written; reading "1.25" kg as 125 kg would be dangerous). If not kg
// it must be an integer. null if invalid.
export function parseQty(text, unit) {
  const t = String(text).trim().replace(',', '.')
  if (!/^\d+(\.\d{1,3})?$/.test(t)) return null
  const milli = Math.round(Number(t) * 1000)
  if (unit !== 'kg' && milli % 1000 !== 0) return null
  return milli
}

// unitName is the unit's abbreviation on the screen.
export const unitName = (u) => ({ piece: 'adet', kg: 'kg' })[u] ?? u

// kindName is the kind of the stock movement.
export const kindName = (k) =>
  ({ sale: 'Satış', receipt: 'Mal kabul', count: 'Sayım', transfer_out: 'Transfer çıkışı', transfer_in: 'Transfer girişi' })[k] ?? k

// capitalize capitalizes the first letter of the server's error message (with the Turkish rule).
export const capitalize = (s) => s.charAt(0).toLocaleUpperCase('tr-TR') + s.slice(1)
