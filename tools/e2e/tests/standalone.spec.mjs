// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Setup without headquarters: market register + store server, no headquarters. The record flows
// completely, does not flow at all (the register is disconnected), flows partially (what was done
// while the store server was down comes later) and the delivery receipt rule: an event at the register is deleted only when it is proven by a receipt that it was processed into the store server's database; since there is no
// headquarters no event is deleted from the store stream at the store server.

import { test, expect } from '@playwright/test'
import { Stack, until, stays } from '../lib/stack.mjs'
import { Register, Store } from '../lib/clients.mjs'
import { marketSale, storeOpen, registerCard, walkTabs } from '../lib/ui.mjs'

const stack = new Stack({ ad: 'standalone', headquarters: false })
const ID = 'register-1'
let register, store

const receipts = async () => (await store.register(ID))?.receipt_count ?? 0

// waits for flows: the store server sees the register as connected, with no pending events and n receipts.
const akar = (n) =>
  until(`operasyon ${n} fiş görmeli`, async () => {
    const r = await store.register(ID)
    return r && r.link === 'connected' && r.pending_events === 0 && r.receipt_count === n
  })

// registerCleaned: all the events in the register's sales stream have been deleted with receipts.
const registerCleaned = () => until('kasadaki satış olayları makbuzla silinmeli', async () => (await stack.registerCount(ID, 'ABAROW_SALES')) === 0)

test.describe.serial('Merkezsiz kurulum: kasa + operasyon', () => {
  test.beforeAll(async () => {
    test.setTimeout(30 * 60_000)
    await stack.up()
    register = new Register(ID, stack.registerURL(ID))
    await register.login()
    await register.ensureShift()
    store = new Store()
    await store.login()
  })

  test.afterAll(async () => {
    test.setTimeout(10 * 60_000)
    await stack.down()
  })

  test('full flow: the sales reach the store server report without loss or repetition', async () => {
    const base = await receipts()
    await register.sellMany(3)
    await akar(base + 3)
    const r = await store.register(ID)
    expect(r.missing_receipts, 'there must be no missing receipt').toHaveLength(0)
    const day = await store.summary()
    // The register's breakdown is consistent with the daily total.
    expect(day.registers.reduce((t, x) => t + x.total_kurus, 0)).toBe(day.total_kurus)
    expect(day.receipt_count).toBeGreaterThanOrEqual(3)
    // No double counting: the receipt count does not change after waiting a bit.
    await stays('fiş sayısı sabit', async () => ({ ok: (await receipts()) === base + 3 }), { duration: 6_000 })
  })

  test('receipt came: the event at the register is deleted after the store server processes it', async () => {
    // The earlier sales' events were processed into the store server, their receipts came down to the register and
    // they were deleted from the register when the wait time (6 s) was up.
    await registerCleaned()
    // The register gets a receipt for its own shift events too.
    await until('vardiya olayı makbuzla silinmeli', async () => (await stack.registerCount(ID, 'ABAROW_STAFF')) === 0)
  })

  test('partial flow: while the store server is down the sales accumulate at the register, they come when it opens; an event with no receipt is not deleted', async ({ page }) => {
    const base = await receipts()
    await register.sellMany(2)
    await akar(base + 2)
    await registerCleaned()

    // The store server's NATS closes; the register keeps selling.
    await stack.stop('store', 'store')
    await register.sellMany(2)
    // The events are written from the outbox to the register's stream in half-second rounds; let both come first.
    await until('iki satış olayı kasanın akışına yazılmalı', async () => (await stack.registerCount(ID, 'ABAROW_SALES')) === 2, { timeout: 30_000, every: 500 })
    // The two events at the register have no receipt: they stay even after the delete rounds pass.
    await stays(
      'makbuzsuz olay silinmez',
      async () => {
        const n = await stack.registerCount(ID, 'ABAROW_SALES')
        return { ok: n === 2, detail: `kasada ${n} olay` }
      },
      { duration: 20_000 },
    )
    // Screen: the register is disconnected or the state cannot be read.
    await storeOpen(page)
    await expect(registerCard(page).getByText('Bağlantı yok').or(page.getByText('Kasaların durumu okunamadı'))).toBeVisible({ timeout: 60_000 })

    await stack.start('store', 'store')
    await akar(base + 4)
    // When all are processed the receipts come down to the register and the events are deleted.
    await registerCleaned()
    await stays('fiş sayısı sabit', async () => ({ ok: (await receipts()) === base + 4 }), { duration: 8_000 })
    await page.reload()
    await expect(registerCard(page).getByText('Bağlı', { exact: true })).toBeVisible({ timeout: 60_000 })
    await expect(registerCard(page).getByTestId('receipts')).toHaveText(String(base + 4))
  })

  test('no flow: the register NATS connection is cut, the sales wait in the outbox, they flow once the connection comes', async ({ page }) => {
    const base = await receipts()
    await stack.stop(ID, 'nats')
    // The register and store server are running, there is no connection between them: sales continue.
    await register.sellMany(2)
    await storeOpen(page)
    await expect(registerCard(page).getByText('Bağlantı yok')).toBeVisible({ timeout: 90_000 })
    await expect(registerCard(page).getByText('satışlar kasada birikir')).toBeVisible()
    expect(await receipts(), 'with no connection the store server must not see a new receipt').toBe(base)

    await stack.start(ID, 'nats')
    await akar(base + 2)
    await page.reload()
    await expect(registerCard(page).getByText('Bağlı', { exact: true })).toBeVisible({ timeout: 60_000 })
    await registerCleaned()
  })

  test('no headquarters: no event is deleted from the store stream at the store server', async () => {
    // The events at the register were deleted with receipts (above); at the store server all of them remain.
    await registerCleaned()
    const n = await stack.bhCount('ABAROW_STORE')
    expect(n, 'the sale and shift events must stay in the store stream').toBeGreaterThanOrEqual(9)
    await stays(
      'mağaza stream\'i olay kaybetmez',
      async () => {
        const now = await stack.bhCount('ABAROW_STORE')
        return { ok: now >= n, detail: `${n} → ${now}` }
      },
      { duration: 20_000 },
    )
    // Since no headquarters is defined there is no receipt table either (delivery_received is empty).
    const registerReceipt = await stack.bhCount('ABAROW_RECEIPT')
    expect(registerReceipt, 'kasalara verilen makbuzlar makbuz stream\'inde').toBeGreaterThanOrEqual(9)
  })

  test('screen: the cashier sells on the market screen, it shows on the store server screen', async ({ page }) => {
    const base = await receipts()
    await marketSale(page, stack.registerURL(ID))
    await akar(base + 1)
    await storeOpen(page)
    await expect(registerCard(page).getByText('Bağlı', { exact: true })).toBeVisible()
    await expect(registerCard(page).getByTestId('receipts')).toHaveText(String(base + 1))
    await page.screenshot({ path: 'out/standalone-store.png', fullPage: true })
  })

  test('screen: all of the store server tabs open without errors; if update is not installed it says so', async ({ page }) => {
    await storeOpen(page)
    await walkTabs(page, ['Raporlar', 'Ürünler', 'Fiyatlar', 'Kasiyerler', 'Stok', 'Güncelleme'])
    await expect(page.getByTestId('update-missing')).toContainText('kurulu değil')
    // Sign-out closes the session: the protected endpoint now gives 401.
    await page.getByRole('button', { name: 'Çıkış' }).click()
    await expect(page.getByLabel('Parola', { exact: true })).toBeVisible()
    expect((await fetch('http://127.0.0.1:8090/api/registers')).status).toBe(401)
  })
})
