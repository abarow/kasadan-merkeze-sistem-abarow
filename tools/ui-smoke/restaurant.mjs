// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// restaurant uses the restaurant register's screen in a real browser as a waiter and
// cashier would: floor layout (manager approval), opening a table, adding a product with options
// from the menu with images, sending to the kitchen, cancellation (manager approval),
// the check breakdown, splitting the bill and payment, moving a table. While the restaurant register is
// running, from the repository root:
//
//   docker compose -p abarow-restaurant -f deploy/compose/compose.yaml -f deploy/compose/restaurant.yaml \
//     --profile ui-test run --rm ui-smoke
//
// The images are written under tools/ui-smoke/out/.

import { chromium } from 'playwright'

const url = process.env.ABAROW_UI_URL ?? 'http://gateway:8080'
const out = process.env.ABAROW_UI_OUT ?? 'out'

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 800 }, locale: 'tr-TR' })
const errors = []
page.on('pageerror', (e) => errors.push(e.message))
// The browser's resource loading messages are not counted as errors (e.g. the 404 of an
// image not yet downloaded), the application's own errors are.
page.on('console', (m) => m.type() === 'error' && !m.text().startsWith('Failed to load resource') && errors.push(m.text()))

function check(cond, msg) {
  if (!cond) throw new Error(msg)
  console.log(`✓ ${msg}`)
}

async function pressPIN(pin, submit = 'Giriş') {
  for (const d of pin) await page.getByRole('button', { name: d, exact: true }).click()
  await page.getByRole('button', { name: submit, exact: true }).click()
}

async function approve() {
  await page.getByRole('heading', { name: 'Müdür onayı' }).waitFor()
  await pressPIN('9999', 'Onayla')
}

const order = () => page.getByRole('region', { name: /^Adisyon: / })
const total = (text) => order().locator('.total strong').first().filter({ hasText: text }).waitFor()

async function openTable(name) {
  await page.getByRole('button', { name: new RegExp(`^${name}: `) }).click()
  await page.getByRole('heading', { name, exact: true, level: 1 }).waitFor()
}

async function addSimple(name) {
  await page.getByRole('region', { name: 'Menü' }).getByRole('button', { name, exact: true }).click()
  await order().getByText(name, { exact: true }).first().waitFor()
}

async function category(name) {
  await page.getByRole('tab', { name }).click()
}

async function payAll(method) {
  await order().getByRole('button', { name: /^ÖDE / }).click()
  await page.getByRole('heading', { name: 'Ödeme' }).waitFor()
  await page.getByRole('button', { name: method }).click()
  await page.getByRole('heading', { name: 'Ödeme alındı' }).waitFor()
}

try {
  await page.goto(url)
  await page.getByText('SİMÜLATÖR MODU').waitFor()
  await page.getByRole('button', { name: /Kasiyer Ayşe/ }).click()
  await pressPIN('1111')
  // If the simulation runs again in the same environment the shift has stayed open.
  const masalar = page.getByRole('heading', { name: 'Masalar', level: 1 })
  const openShift = page.getByRole('button', { name: 'Vardiyayı aç' })
  await masalar.or(openShift).waitFor()
  if (await openShift.isVisible()) {
    await page.getByLabel('Açılış nakdi').fill('500')
    await openShift.click()
  }
  await masalar.waitFor()
  check(true, 'kasiyer girişi ve vardiya: masalar ekranı geldi')

  // Floor layout (on the first run): the cashier saves, the manager approves.
  if ((await page.getByText('Henüz salon ve masa tanımlanmadı').count()) > 0) {
    await page.getByRole('button', { name: 'Salon düzeni' }).click()
    await page.getByRole('button', { name: 'Salon ekle' }).click()
    await page.getByLabel('Salon 1 adı').fill('İç salon')
    await page.getByRole('button', { name: '5 masa ekle' }).click()
    await page.getByRole('button', { name: 'Kaydet' }).click()
    await approve()
    check(true, 'salon düzeni müdür onayıyla kaydedildi (İç salon, 5 masa)')
  }
  await page.getByRole('button', { name: /^1: boş$/ }).waitFor()
  await page.screenshot({ path: `${out}/restaurant-0-masalar.png` })

  // Table 1: the menu has images; the images load (without a session).
  await openTable('1')
  const img = page.getByRole('region', { name: 'Menü' }).locator('img').first()
  await img.waitFor()
  await page.waitForFunction((el) => el.complete && el.naturalWidth > 0, await img.elementHandle())
  check(true, 'menü kategorileri ve ürün görselleri yüklendi')
  await page.screenshot({ path: `${out}/restaurant-1-menu.png` })

  // A product with options: it is not added without making the required selection.
  await category('Ana yemekler')
  await page.getByRole('button', { name: 'Izgara köfte', exact: true }).click()
  const opts = page.getByRole('form', { name: 'Seçim: Izgara köfte' })
  check(await opts.getByRole('button', { name: 'Ekle' }).isDisabled(), 'pişirme seçilmeden köfte eklenemiyor')
  await opts.getByRole('button', { name: 'az pişmiş' }).click()
  await opts.getByRole('button', { name: /^kaşar/ }).click()
  await opts.getByLabel('Not').fill('soğansız')
  await page.screenshot({ path: `${out}/restaurant-2-opsiyon.png` })
  await opts.getByRole('button', { name: 'Ekle' }).click()
  await order().getByText('Izgara köfte (az pişmiş, kaşar)').waitFor()
  await category('İçecekler')
  await addSimple('Ayran')
  await addSimple('Kola')
  await total('445,00')
  check(true, 'köfte (az pişmiş, kaşar +25,00), ayran ve kola eklendi: 445,00')

  await order().getByRole('button', { name: 'Mutfağa gönder (3)' }).click()
  await order().getByText('Mutfağa gönderildi.').waitFor()
  check(true, 'mutfağa ve bara gönderildi')

  // Cancelling a sent ayran asks for manager approval.
  await order().locator('li').filter({ hasText: 'Ayran' }).getByRole('button', { name: 'İptal' }).click()
  await page.getByRole('button', { name: 'Döküldü / bozuk' }).click()
  await page.getByRole('button', { name: 'İptal et' }).click()
  await approve()
  await order().getByText('İptal edildi; istasyona iptal fişi basıldı.').waitFor()
  await total('405,00')
  check(true, 'ayran müdür onayıyla iptal edildi: 405,00')

  await order().getByRole('button', { name: 'Hesap', exact: true }).click()
  await order().getByText('Adisyon basıldı.').waitFor()
  check(true, 'adisyon dökümü basıldı (mali değeri yok)')
  await page.screenshot({ path: `${out}/restaurant-3-adisyon.png` })

  // Splitting the bill: first the köfte in cash, then the remaining cola by card.
  await order().getByRole('button', { name: 'Hesabı böl' }).click()
  const split = page.getByRole('dialog', { name: 'Hesabı böl' })
  await split.getByRole('button', { name: 'Izgara köfte (az pişmiş, kaşar) artır' }).click()
  await split.locator('.total strong').filter({ hasText: '345,00' }).waitFor()
  await split.getByRole('button', { name: 'Seçilenleri öde' }).click()
  await page.getByRole('heading', { name: 'Ödeme' }).waitFor()
  await page.getByLabel('Alınan nakit').fill('400')
  await page.getByRole('button', { name: 'Nakit al' }).click()
  await page.getByRole('heading', { name: 'Ödeme alındı' }).waitFor()
  await page.getByRole('button', { name: 'Adisyona dön' }).click()
  await order().locator('.total.due strong').filter({ hasText: '60,00' }).waitFor()
  check(true, 'hesap bölündü: köfte nakitle ödendi, kalan 60,00')
  await payAll('Kart ile öde')
  await page.getByRole('button', { name: 'Masalara dön' }).click()
  await page.getByRole('button', { name: /^1: boş$/ }).waitFor()
  check(true, 'kalan kartla ödendi; adisyon kapandı, masa boşaldı')

  // Moving: table 2's check to table 3.
  await openTable('2')
  await category('İçecekler')
  await addSimple('Çay')
  await order().getByRole('button', { name: 'Taşı / birleştir' }).click()
  await page.getByRole('dialog', { name: 'Taşı ya da birleştir' }).getByRole('button', { name: '3: taşı' }).click()
  await page.getByRole('heading', { name: '3', exact: true, level: 1 }).waitFor()
  check(true, 'adisyon masa 2\'den masa 3\'e taşındı')
  await order().getByRole('button', { name: 'Mutfağa gönder (1)' }).click()
  await order().getByText('Mutfağa gönderildi.').waitFor()
  await payAll('Kart ile öde')
  await page.getByRole('button', { name: 'Masalara dön' }).click()
  await page.getByRole('button', { name: /^3: boş$/ }).waitFor()
  await page.screenshot({ path: `${out}/restaurant-4-bos.png` })

  await page.getByRole('button', { name: 'Çıkış' }).click()
  await page.getByText('Kasiyer seçin').waitFor()
  check(true, 'çıkış yapıldı')
  check(errors.length === 0, `tarayıcıda hata yok${errors.length ? ': ' + errors.join('; ') : ''}`)
} catch (e) {
  await page.screenshot({ path: `${out}/restaurant-bug.png`, fullPage: true }).catch(() => {})
  console.error(`✗ ${e.message}`)
  if (errors.length) console.error(`tarayıcı hataları: ${errors.join('; ')}`)
  process.exitCode = 1
} finally {
  await browser.close()
}
