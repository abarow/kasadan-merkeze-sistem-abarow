// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Screen (Playwright) steps: selling like a cashier, opening the store server and
// headquarters screens. The selectors are the same as the screen tests in tools/ui-smoke.

import { expect } from '@playwright/test'

export async function pressPIN(page, pin, submit = 'Giriş') {
  for (const d of pin) await page.getByRole('button', { name: d, exact: true }).click()
  await page.getByRole('button', { name: submit, exact: true }).click()
}

/** Kasiyer Ayşe ile girer (PIN 1111) ve vardiya yoksa 500,00 TL ile açar. */
async function cashierLogin(page, url, ready) {
  await page.goto(url)
  await page.getByRole('button', { name: /Kasiyer Ayşe/ }).click()
  await pressPIN(page, '1111')
  const openShift = page.getByRole('button', { name: 'Vardiyayı aç' })
  await openShift.or(ready).waitFor()
  if (await openShift.isVisible()) {
    await page.getByLabel('Açılış nakdi').fill('500')
    await openShift.click()
  }
  await ready.waitFor()
}

/** Kasa ekranını kasiyer Ayşe ile açar (market: barcodeField alanı gelir). */
export const registerOpen = (page, url) => cashierLogin(page, url, page.getByLabel('Barkod'))

/** Market kasa ekranında bir satış yapar: barcodeField okut, öde, tamamlandı ekranı. */
export async function marketSale(page, url, { barcode = '8690000000012', cash = '100' } = {}) {
  const barcodeField = page.getByLabel('Barkod')
  await cashierLogin(page, url, barcodeField)
  const added = page.waitForResponse((r) => r.request().method() === 'POST' && /\/api\/module\/baskets\/[^/]+\/items$/.test(r.url()))
  await barcodeField.fill(barcode)
  await barcodeField.press('Enter')
  await added
  await page.getByRole('button', { name: 'ÖDE' }).click()
  await page.getByRole('heading', { name: 'Ödeme' }).waitFor()
  await page.getByLabel('Alınan nakit').fill(cash)
  await page.getByRole('button', { name: 'Nakit al' }).click()
  await page.getByRole('heading', { name: 'Satış tamamlandı' }).waitFor()
}

/**
 * Restoran kasa ekranında bir masayı açar, bir ayran ekler ve nakit öder
 * (ilk çalıştırmada salon düzenini müdür onayıyla kurar).
 */
export async function restaurantSale(page, url, table = '1') {
  const tablesHeading = page.getByRole('heading', { name: 'Masalar', level: 1 })
  await cashierLogin(page, url, tablesHeading)
  if ((await page.getByText('Henüz salon ve masa tanımlanmadı').count()) > 0) {
    await page.getByRole('button', { name: 'Salon düzeni' }).click()
    await page.getByRole('button', { name: 'Salon ekle' }).click()
    await page.getByLabel('Salon 1 adı').fill('İç salon')
    await page.getByRole('button', { name: '5 masa ekle' }).click()
    await page.getByRole('button', { name: 'Kaydet' }).click()
    await page.getByRole('heading', { name: 'Müdür onayı' }).waitFor()
    await pressPIN(page, '9999', 'Onayla')
  }
  await page.getByRole('button', { name: new RegExp(`^${table}: `) }).click()
  await page.getByRole('heading', { name: table, exact: true, level: 1 }).waitFor()
  await page.getByRole('tab', { name: 'İçecekler' }).click()
  const order = page.getByRole('region', { name: /^Adisyon: / })
  await page.getByRole('region', { name: 'Menü' }).getByRole('button', { name: 'Ayran', exact: true }).click()
  await order.getByText('Ayran', { exact: true }).first().waitFor()
  // For payment the check is sent to the kitchen/bar and the bill is requested.
  await order.getByRole('button', { name: /^Mutfağa gönder/ }).click()
  await order.getByText('Mutfağa gönderildi.').waitFor()
  await order.getByRole('button', { name: 'Hesap', exact: true }).click()
  await order.getByText('Adisyon basıldı.').waitFor()
  await order.getByRole('button', { name: /^ÖDE / }).click()
  await page.getByRole('heading', { name: 'Ödeme' }).waitFor()
  await page.getByLabel('Alınan nakit').fill('400')
  await page.getByRole('button', { name: 'Nakit al' }).click()
  await page.getByRole('heading', { name: 'Ödeme alındı' }).waitFor()
}

/** Operasyon ekranını mağaza parolasıyla açar; "Kasa durumu" gelir. */
export async function storeOpen(page, url = 'http://127.0.0.1:8090', password = 'store-test') {
  await page.goto(url)
  // A session from an earlier sign-in may have stayed open in the same browser.
  const heading = page.getByRole('heading', { name: 'Kasa durumu', level: 2 })
  const pw = page.getByLabel('Parola', { exact: true })
  await pw.or(heading).waitFor()
  if (await pw.isVisible()) {
    await pw.fill(password)
    await page.getByRole('button', { name: 'Giriş' }).click()
  }
  await heading.waitFor()
}

/** Operasyon ekranındaki kasa kartı. */
export const registerCard = (page, name = 'Kasa 1') => page.getByRole('listitem', { name })

/** Merkez ekranını deneme hesabıyla açar; "Mağazalar" gelir. */
export async function headquartersOpen(page, url = 'http://127.0.0.1:9090', user = 'headquarters', password = 'headquarters-test') {
  await page.goto(url)
  const heading = page.getByRole('heading', { name: 'Mağazalar', exact: true, level: 2 })
  const name = page.getByLabel('Kullanıcı adı')
  await name.or(heading).waitFor()
  if (await name.isVisible()) {
    await name.fill(user)
    await page.getByLabel('Parola').fill(password)
    await page.getByRole('button', { name: 'Giriş' }).click()
  }
  await heading.waitFor()
}

export { expect }

/**
 * walkTabs, ekranın "Bölümler" gezinmesindeki her sekmeyi sırayla açar. Her
 * sekmede sayfa hatası (pageerror), konsol hatası ve 5xx yanıtı toplanır; en
 * sonda hiçbiri olmamalı. Açılan sekmelerin adlarını döner.
 */
export async function walkTabs(page, expected) {
  const problems = []
  page.on('pageerror', (e) => problems.push(`sayfa hatası: ${e.message}`))
  page.on('console', (m) => m.type() === 'error' && !/status of 40[0-9]/.test(m.text()) && problems.push(`konsol: ${m.text()}`))
  page.on('response', (r) => r.status() >= 500 && problems.push(`${r.status()} ${r.url()}`))
  const nav = page.getByRole('navigation', { name: 'Bölümler' })
  const names = (await nav.getByRole('button').allTextContents()).map((s) => s.trim())
  expect(names, 'sekmeler').toEqual(expected)
  for (const name of names) {
    await nav.getByRole('button', { name, exact: true }).click()
    await expect(nav.getByRole('button', { name, exact: true })).toHaveAttribute('aria-current', 'page')
    await page.waitForLoadState('networkidle')
    await expect(page.getByRole('main')).toBeVisible()
  }
  expect(problems, 'error during navigation').toEqual([])
  return names
}
