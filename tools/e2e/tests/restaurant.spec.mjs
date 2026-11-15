// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Restaurant register (table, check, cash payment) in two setups: without headquarters
// and with headquarters. The sale follows the same path as the market register (sales service,
// outbox, NATS, store server, headquarters); here it is done through the screen (Playwright) and
// the same flow / disconnection / partial flow / delete-with-receipt rules are tried.

import { test, expect } from '@playwright/test'
import { Stack, until, stays } from '../lib/stack.mjs'
import { Store, Headquarters } from '../lib/clients.mjs'
import { restaurantSale, storeOpen, registerCard } from '../lib/ui.mjs'

const ID = 'register-1'
const STORE = 'store-1'

for (const withHeadquarters of [false, true]) {
  const ad = withHeadquarters ? 'restaurant-headquarters' : 'restaurant-standalone'
  const stack = new Stack({ ad, headquarters: withHeadquarters, restaurant: true })
  let store, headquarters
  // A different table for every sale: the paid table empties but the following tests should not get mixed up.
  let table = 0
  const nextTable = () => String((table++ % 5) + 1)

  const bhReceipts = async () => (await store.register(ID))?.receipt_count ?? 0
  const headquartersSales = async () => (await headquarters.store(STORE))?.sale_count ?? 0
  const storeReceives = (n) =>
    until(`operasyon ${n} fiş görmeli`, async () => {
      const r = await store.register(ID)
      return r && r.link === 'connected' && r.pending_events === 0 && r.receipt_count === n
    })
  const headquartersReceives = (n) => until(`merkez ${n} satış görmeli`, async () => (await headquartersSales()) === n)
  const registerCleaned = () => until('kasadaki satış olayları makbuzla silinmeli', async () => (await stack.registerCount(ID, 'ABAROW_SALES')) === 0)

  test.describe.serial(`Restoran kasası, ${withHeadquarters ? 'with headquarters' : 'standalone'} setup`, () => {
    test.beforeAll(async () => {
      test.setTimeout(30 * 60_000)
      await stack.up()
      store = new Store()
      await store.login()
      if (withHeadquarters) {
        headquarters = new Headquarters()
        await headquarters.login()
      }
    })

    test.afterAll(async () => {
      test.setTimeout(10 * 60_000)
      await stack.down()
    })

    test('full flow: when the check is paid the sale reaches the store server (and headquarters), the event at the register is deleted with a receipt', async ({ page }) => {
      const [b0, h0] = [await bhReceipts(), withHeadquarters ? await headquartersSales() : 0]
      await restaurantSale(page, stack.registerURL(ID), nextTable())
      await storeReceives(b0 + 1)
      if (withHeadquarters) await headquartersReceives(h0 + 1)
      const r = await store.register(ID)
      expect(r.missing_receipts).toHaveLength(0)
      await registerCleaned()
      await storeOpen(page)
      await expect(registerCard(page).getByText('Bağlı', { exact: true })).toBeVisible()
      await expect(registerCard(page).getByTestId('receipts')).toHaveText(String(b0 + 1))
      await page.screenshot({ path: `out/${ad}-store.png`, fullPage: true })
    })

    test('partial flow: while the store server is down the check is paid, the sale accumulates at the register, it comes when it opens', async ({ page }) => {
      const [b0, h0] = [await bhReceipts(), withHeadquarters ? await headquartersSales() : 0]
      await registerCleaned()
      await stack.stop('store', 'store')
      await restaurantSale(page, stack.registerURL(ID), nextTable())
      // The sale's event is written from the outbox to the register's stream and waits without a receipt.
      await until('satış olayı kasanın stream\'ine yazılmalı', async () => (await stack.registerCount(ID, 'ABAROW_SALES')) >= 1)
      await stays('makbuzsuz olay silinmez', async () => {
        const n = await stack.registerCount(ID, 'ABAROW_SALES')
        return { ok: n >= 1, detail: `kasada ${n}` }
      }, { duration: 15_000 })
      await stack.start('store', 'store')
      await storeReceives(b0 + 1)
      if (withHeadquarters) await headquartersReceives(h0 + 1)
      await registerCleaned()
    })

    test('no flow: the register NATS connection is cut, the check is paid; it flows once the connection comes', async ({ page }) => {
      const b0 = await bhReceipts()
      await stack.stop(ID, 'nats')
      await restaurantSale(page, stack.registerURL(ID), nextTable())
      await storeOpen(page)
      await expect(registerCard(page).getByText('Bağlantı yok')).toBeVisible({ timeout: 90_000 })
      expect(await bhReceipts()).toBe(b0)
      await stack.start(ID, 'nats')
      await storeReceives(b0 + 1)
      await registerCleaned()
    })

    if (withHeadquarters) {
      test('while headquarters is down the sale shows at the store server, it flows when headquarters opens', async ({ page }) => {
        const [b0, h0] = [await bhReceipts(), await headquartersSales()]
        await stack.stop('headquarters', 'headquarters')
        await restaurantSale(page, stack.registerURL(ID), nextTable())
        await storeReceives(b0 + 1)
        // While headquarters' NATS is down ingest's status API does not answer either; the number of sales
        // is read directly from headquarters' database.
        expect(Number(await stack.sql('headquarters', 'headquarters-db', 'select count(*) from sales'))).toBe(h0)
        await stack.start('headquarters', 'headquarters')
        await headquartersReceives(h0 + 1)
      })
    } else {
      test('no headquarters: no event is deleted from the store stream at the store server', async () => {
        await registerCleaned()
        const n = await stack.bhCount('ABAROW_STORE')
        expect(n).toBeGreaterThanOrEqual(3)
        await stays('mağaza stream\'i olay kaybetmez', async () => {
          const now = await stack.bhCount('ABAROW_STORE')
          return { ok: now >= n, detail: `${n} → ${now}` }
        }, { duration: 20_000 })
      })
    }
  })
}
