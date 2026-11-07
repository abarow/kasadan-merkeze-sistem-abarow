// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Setup with headquarters: market register → store server → headquarters. The record flows completely,
// does not flow at all (the store server is disconnected), flows partially (headquarters is down or headquarters'
// PostgreSQL is down) and the two-stage delivery receipt: an event at the register is not deleted until it is proven by a receipt that it was processed into the store server's database, and an event at the store server not until it is proven that it was processed into headquarters' database.
// 

import { test, expect } from '@playwright/test'
import { Stack, until, stays } from '../lib/stack.mjs'
import { Register, Store, Headquarters, today } from '../lib/clients.mjs'
import { registerOpen, marketSale, storeOpen, headquartersOpen, registerCard, walkTabs } from '../lib/ui.mjs'

const stack = new Stack({ ad: 'headquarters', headquarters: true })
const ID = 'register-1'
const STORE = 'store-1'
let register, store, headquarters

const bhReceipts = async () => (await store.register(ID))?.receipt_count ?? 0
const headquartersSales = async () => (await headquarters.store(STORE))?.sale_count ?? 0

// storeReceives: the store server sees the register as connected, with no pending events and n receipts.
const storeReceives = (n) =>
  until(`operasyon ${n} fiş görmeli`, async () => {
    const r = await store.register(ID)
    return r && r.link === 'connected' && r.pending_events === 0 && r.receipt_count === n
  })

// headquartersFlows: headquarters must have processed the store's n sales into PostgreSQL.
const headquartersReceives = (n) => until(`merkez ${n} satış görmeli`, async () => (await headquartersSales()) === n)

// settled: returns when a counter is the same in two successive reads (8 s apart); waits for
// the cleanup rounds to finish.
const settled = async (what, read) => {
  let prev = -1
  return until(what, async () => {
    const n = await read()
    const ok = n === prev
    prev = n
    return ok ? { n } : false
  }, { every: 8_000 }).then((v) => v.n)
}
const bhStore = () => stack.bhCount('ABAROW_STORE')

// headquartersEvents: the number of store events headquarters processed (wrote to PostgreSQL); for each
// one headquarters gives a receipt.
const headquartersEvents = async () => (await headquarters.store(STORE))?.event_count ?? 0

// receiptsDeleted: all the receipts headquarters gave must have been written to the store server's
// database (delivery_received); before that the store stream's count
// has not settled.
const receiptsDeleted = () =>
  until('merkezin makbuzları operasyon\'a inmeli', async () => {
    const n = Number(await stack.sql('store', 'store-db', 'select count(*) from delivery_received'))
    return n > 0 && n >= (await headquartersEvents())
  }, { timeout: 180_000 })

// settled: the receipts came down and the cleanup rounds finished; the store stream's count is constant.
const oturdu = async () => {
  await receiptsDeleted()
  return settled('mağaza stream\'i sakinleşmeli', bhStore)
}

test.describe.serial('Merkezli kurulum: kasa → operasyon → merkez', () => {
  test.beforeAll(async () => {
    test.setTimeout(30 * 60_000)
    await stack.up()
    register = new Register(ID, stack.registerURL(ID))
    await register.login()
    await register.ensureShift()
    store = new Store()
    await store.login()
    headquarters = new Headquarters()
    await headquarters.login()
  })

  test.afterAll(async () => {
    test.setTimeout(10 * 60_000)
    await stack.down()
  })

  test('full flow: the sales reach the store server and headquarters without loss, the figures are the same', async () => {
    const [b0, h0] = [await bhReceipts(), await headquartersSales()]
    await register.sellMany(3)
    await storeReceives(b0 + 3)
    await headquartersReceives(h0 + 3)
    const s = await headquarters.store(STORE)
    expect(s.link).toBe('connected')
    expect(s.pending_events).toBe(0)
    // The sales ledger gives the same figures in two tiers.
    const day = await store.summary()
    await until('merkezin özeti operasyon ile aynı olmalı', async () => {
      const c = await headquarters.summary(STORE, day.from)
      return c.receipt_count === (await store.summary()).receipt_count && c.total_kurus === (await store.summary()).total_kurus
    })
    const r = await store.register(ID)
    expect(r.missing_receipts).toHaveLength(0)
  })

  test('receipt chain: an event at the register is deleted with the store server receipt, an event at the store server with headquarters receipt', async () => {
    // Stage 1: the store server processed → receipt to the register → the events at the register are deleted.
    await until('kasadaki satış olayları silinmeli', async () => (await stack.registerCount(ID, 'ABAROW_SALES')) === 0)
    await until('kasadaki vardiya olayları silinmeli', async () => (await stack.registerCount(ID, 'ABAROW_STAFF')) === 0)
    // Stage 2: headquarters processed → receipt to the store server → the events in the store stream are deleted.
    const n = await oturdu()
    // The events whose receipt came have been deleted; what remains is only the queue tail the stream's own named
    // consumers have not passed yet.
    const total = await headquartersEvents()
    expect(total, 'headquarters must have processed the store events').toBeGreaterThan(3)
    expect(n, `${n} events in the store stream; fewer than the ${total} events headquarters processed should remain`).toBeLessThan(total)
  })

  test('partial flow: the store server works while headquarters is down, an event with no receipt is not deleted; it flows and is deleted once headquarters opens', async () => {
    await oturdu()
    const [b0, h0] = [await bhReceipts(), await headquartersSales()]
    await stack.stop('headquarters', 'headquarters')
    await register.sellMany(2)
    // The store server processes into its own report; it does not go to headquarters.
    await storeReceives(b0 + 2)
    // If the earlier events have receipts they are legitimately deleted when the consumer floor advances;
    // when the count settles what remains includes the two sale events with no receipt.
    const n1 = await settled('mağaza stream\'i sakinleşmeli', bhStore)
    expect(n1).toBeGreaterThanOrEqual(2)
    await stays(
      'merkez makbuzu yokken mağaza stream\'inden olay silinmez',
      async () => {
        const n = await bhStore()
        return { ok: n >= n1, detail: `${n1} → ${n}` }
      },
      { duration: 20_000 },
    )
    await stack.start('headquarters', 'headquarters')
    await headquartersReceives(h0 + 2)
    // Headquarters processed: the receipts come down to the store server, the events are deleted when the wait time is up.
    await until('merkez makbuzuyla mağaza stream\'i küçülmeli', async () => (await bhStore()) <= n1 - 2)
    expect(await bhReceipts()).toBe(b0 + 2)
  })

  test('partial flow: while headquarters database is down the event reaches headquarters stream but is not processed; no receipt, not deleted', async () => {
    await oturdu()
    const [b0, h0] = [await bhReceipts(), await headquartersSales()]
    const headquartersStream0 = await stack.headquartersCount('ABAROW_HEADQUARTERS')
    await stack.stop('headquarters', 'headquarters-db')
    await register.sellMany(2)
    await storeReceives(b0 + 2)
    // The event flows to headquarters' NATS stream (flowed partially) but cannot be written to PostgreSQL.
    await until('olaylar merkez stream\'ine gelmeli', async () => (await stack.headquartersCount('ABAROW_HEADQUARTERS')) >= headquartersStream0 + 2)
    const n1 = await settled('mağaza stream\'i sakinleşmeli', bhStore)
    expect(n1).toBeGreaterThanOrEqual(2)
    await stays(
      'PostgreSQL\'e işlenmeyen olay için makbuz yok, operasyon silmez',
      async () => {
        const n = await bhStore()
        return { ok: n >= n1, detail: `${n1} → ${n}` }
      },
      { duration: 20_000 },
    )
    await stack.start('headquarters', 'headquarters-db')
    await headquartersReceives(h0 + 2)
    await until('PostgreSQL\'e işlenince makbuz gelir, mağaza stream\'i küçülür', async () => (await bhStore()) <= n1 - 2)
  })

  test('no flow: the store server is disconnected, the register keeps selling; when it opens it flows from the register to headquarters', async ({ page }) => {
    const [b0, h0] = [await bhReceipts(), await headquartersSales()]
    await stack.stop('store', 'store')
    await register.sellMany(2)
    // Headquarters sees the store as disconnected; there are no new sales.
    await until('merkez mağazayı bağlantısız görmeli', async () => (await headquarters.store(STORE))?.link !== 'connected', { timeout: 120_000 })
    expect(await headquartersSales()).toBe(h0)
    // Kasada olaylar makbuzsuz birikir.
    await stays('makbuzsuz olay kasada kalır', async () => {
      const n = await stack.registerCount(ID, 'ABAROW_SALES')
      return { ok: n >= 2, detail: `kasada ${n}` }
    }, { duration: 15_000 })
    await stack.start('store', 'store')
    await storeReceives(b0 + 2)
    await headquartersReceives(h0 + 2)
    await until('kasadaki olaylar silinmeli', async () => (await stack.registerCount(ID, 'ABAROW_SALES')) === 0)
    await headquartersOpen(page)
    await expect(page.getByRole('listitem', { name: 'Mağaza 1' }).getByText('Bağlı', { exact: true })).toBeVisible({ timeout: 90_000 })
  })

  test('screen: a sale at the register shows on the store server and headquarters screens; while headquarters is down the store server screen works', async ({ page }) => {
    const [b0, h0] = [await bhReceipts(), await headquartersSales()]
    await marketSale(page, stack.registerURL(ID))
    await storeReceives(b0 + 1)
    await headquartersReceives(h0 + 1)

    await storeOpen(page)
    await expect(registerCard(page).getByTestId('receipts')).toHaveText(String(b0 + 1))

    await headquartersOpen(page)
    const card = page.getByRole('listitem', { name: 'Mağaza 1' })
    // The screen refreshes the store list every 30 seconds; on a fresh stack the connection state
    // may not be "Bağlı" yet on the first load.
    await expect(card.getByText('Bağlı', { exact: true })).toBeVisible({ timeout: 90_000 })
    const todayTotal = (await headquarters.reporting.call('GET', '/api/stores')).json.stores.find((s) => s.id === STORE).today_total_kurus
    await expect(card.getByTestId('today-total')).toContainText((todayTotal / 100).toFixed(2).replace('.', ','))
    await page.screenshot({ path: 'out/headquarters-headquarters.png', fullPage: true })

    // When headquarters closes the store server screen keeps working.
    await stack.stop('headquarters', 'headquarters')
    await storeOpen(page)
    await expect(registerCard(page).getByText('Bağlı', { exact: true })).toBeVisible()
    await stack.start('headquarters', 'headquarters')
    expect(today()).toMatch(/^\d{4}-\d{2}-\d{2}$/)
  })
  test('screen: all of headquarters tabs open without errors; if update is not installed it says so', async ({ page }) => {
    await headquartersOpen(page)
    await walkTabs(page, ['Raporlar', 'Ürünler', 'Stok', 'Güncelleme', 'Kullanıcılar'])
    await page.getByRole('navigation', { name: 'Bölümler' }).getByRole('button', { name: 'Güncelleme', exact: true }).click()
    await expect(page.getByTestId('update-missing')).toContainText('kurulu değil')
    await expect(page.getByText('kayıt sunucusu bu merkezde kurulu değil')).toBeVisible()
    // The update endpoints give 404 when not installed (while a session is open).
    await headquarters.login()
    expect((await headquarters.reporting.call('GET', '/api/update/registrations', undefined, { allow: [404] })).status).toBe(404)
    expect((await headquarters.reporting.call('GET', '/api/update/status', undefined, { allow: [404] })).status).toBe(404)
  })

  test('screen: the register update window says so if update is not installed', async ({ page }) => {
    await registerOpen(page, stack.registerURL(ID))
    await page.getByRole('button', { name: 'Raporlar', exact: true }).click()
    await page.getByRole('heading', { name: /X raporu/ }).waitFor()
    await page.getByRole('button', { name: 'Güncelleme', exact: true }).click()
    await expect(page.getByTestId('update-missing')).toContainText('kurulu değil')
    await page.getByRole('button', { name: 'Geri' }).click()
    await expect(page.getByRole('heading', { name: /X raporu/ })).toBeVisible()
  })
})
