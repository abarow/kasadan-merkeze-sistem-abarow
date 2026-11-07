// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Translation between the option groups' form on the screen and their form in the API.
// The ids are produced from the names (Turkish letters are simplified); an existing id
// does not change: the selections in the orders match with it.

import { moneyInput, parseMoney } from './format.js'

const TR = { ç: 'c', ğ: 'g', ı: 'i', İ: 'i', ö: 'o', ş: 's', ü: 'u' }

function slug(name, taken) {
  const base =
    name
      .toLocaleLowerCase('tr-TR')
      .replace(/[çğıİöşü]/g, (c) => TR[c])
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 28) || 'x'
  let id = base
  for (let n = 2; taken.has(id); n++) id = `${base}-${n}`
  taken.add(id)
  return id
}

// fromGroups turns the groups in the API into the screen form.
export const fromGroups = (groups) =>
  groups.map((g) => ({
    ...g,
    options: g.options.map((o) => ({ id: o.id, name: o.name, price: o.price_delta_kurus ? signed(o.price_delta_kurus) : '' })),
  }))

const signed = (kurus) => (kurus > 0 ? '+' : '-') + moneyInput(Math.abs(kurus))

// toGroups turns the groups on the screen into the API form; it gives an error if the price
// difference is invalid.
export function toGroups(groups) {
  const groupIDs = new Set(groups.map((g) => g.id).filter(Boolean))
  return groups.map((g) => {
    const optionIDs = new Set(g.options.map((o) => o.id).filter(Boolean))
    return {
      id: g.id || slug(g.name, groupIDs),
      name: g.name.trim(),
      min: Number(g.min),
      max: Number(g.max),
      options: g.options.map((o) => {
        const text = String(o.price ?? '').trim()
        const negative = text.startsWith('-')
        const amount = text === '' ? 0 : parseMoney(text.replace(/^[+-]/, ''))
        if (amount === null) throw new Error(`${g.name || 'Grup'}: "${o.name}" seçeneğinin fiyat farkı geçersiz (ör. +15,00).`)
        return { id: o.id || slug(o.name, optionIDs), name: o.name.trim(), price_delta_kurus: negative ? -amount : amount }
      }),
    }
  })
}
