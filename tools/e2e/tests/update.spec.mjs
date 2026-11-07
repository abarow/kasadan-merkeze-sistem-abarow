// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Update center: the registration and approval of units for image pull permission.
// The chain is set up with real processes (scripts/e2e.sh up update): publisher →
// headquarters → store server → register. The publisher approves headquarters at install; the tests approve the store server's and
// register's registration from real screens: the code on the unit's screen is
// read to the headquarters administrator, a wrong code is rejected, the right code approves.
// The register does not know headquarters' address; the registration is carried only by the store server's proxy.

import { test, expect } from '@playwright/test'
import { Stack, until } from '../lib/stack.mjs'
import { Register, Headquarters } from '../lib/clients.mjs'
import { registerOpen, storeOpen, headquartersOpen, pressPIN } from '../lib/ui.mjs'
import { manifest, publish, unpublish, BAD_DIGEST, generateKey, editTrust, restoreTrust } from '../lib/channel.mjs'
import { REGISTRY, CI_CREDS, skopeo, pushAlpine, writeDigest, unitToken, signImages, generatePolicy, podmanPull, cosignKeyPair, trustVariant } from '../lib/registry.mjs'

const stack = new Stack({ ad: 'update', headquarters: true, update: true })
const ID = 'register-1'
const CODE = /^[A-Z0-9]{4}-[A-Z0-9]{4}$/
const DEVICE = /^ABRW-[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}$/

const registerURL = () => stack.registerURL(ID)

/** Merkez ekranının Güncelleme bölümünü açar ve kayıt sekmesini seçer. */
async function headquartersUpdate(page, tab) {
  await headquartersOpen(page)
  await page.getByRole('navigation', { name: 'Bölümler' }).getByRole('button', { name: 'Güncelleme', exact: true }).click()
  await page.getByRole('heading', { name: 'Birim kayıtları', level: 2 }).waitFor()
  if (tab) await page.getByRole('navigation', { name: 'Kayıt durumu' }).getByRole('button', { name: tab, exact: true }).click()
}

const tab = (page, name) => page.getByRole('navigation', { name: 'Kayıt durumu' }).getByRole('button', { name, exact: true }).click()

/** Operasyon ekranında Güncelleme sekmesini açar. */
async function storeUpdate(page) {
  await storeOpen(page)
  await page.getByRole('navigation', { name: 'Bölümler' }).getByRole('button', { name: 'Güncelleme', exact: true }).click()
  await page.getByTestId('update-status').waitFor()
}

/** Kasa ekranında Raporlar > Güncelleme penceresini açar. */
async function registerUpdate(page) {
  await registerOpen(page, registerURL())
  await page.getByRole('button', { name: 'Raporlar', exact: true }).click()
  await page.getByRole('heading', { name: /X raporu/ }).waitFor()
  await page.getByRole('button', { name: 'Güncelleme', exact: true }).click()
  await page.getByTestId('update-status').waitFor()
}

/** Yönetici, bekleyen kaydı (aria-label = cihaz kimliği) kodla onaylar. */
async function approve(page, device, code) {
  const card = page.getByRole('listitem', { name: device })
  await card.getByLabel('Birimin ekranındaki kod').fill(code)
  await card.getByRole('button', { name: 'Onayla', exact: true }).click()
}

test.describe.serial('Güncelleme merkezi: kayıt ve onay', () => {
  let ops = {} // operasyonun cihaz kimliği ve kodu
  let register = {} // kasanın cihaz kimliği ve kodu

  test.beforeAll(async () => {
    test.setTimeout(30 * 60_000)
    await stack.up()
  })

  test.afterAll(async () => {
    test.setTimeout(10 * 60_000)
    await stack.down()
  })

  test('headquarters own agent is approved at the publisher; the screen shows the state', async ({ page }) => {
    await headquartersOpen(page)
    await page.getByRole('navigation', { name: 'Bölümler' }).getByRole('button', { name: 'Güncelleme', exact: true }).click()
    const status = page.getByTestId('update-status')
    await expect(status).toContainText('Onaylı')
    await expect(page.getByTestId('update-device')).toHaveText(DEVICE)
    // No code is shown on an approved unit; there is a note that the update is not installed by itself.
    await expect(page.getByTestId('update-code')).toHaveCount(0)
    await expect(status).toContainText('kendiliğinden kurulmaz')
  })

  test('the store server registers, shows the code on its screen; headquarters sees it in the pending list', async ({ browser }) => {
    const opPage = await (await browser.newContext()).newPage()
    await storeUpdate(opPage)
    await expect(opPage.getByTestId('update-status')).toContainText('Onay bekliyor')
    await expect(opPage.getByTestId('update-code')).toHaveText(CODE)
    ops = {
      device: (await opPage.getByTestId('update-device').textContent()).trim(),
      code: (await opPage.getByTestId('update-code').textContent()).trim(),
    }
    expect(ops.device).toMatch(DEVICE)

    const mPage = await (await browser.newContext()).newPage()
    await headquartersUpdate(mPage, 'Bekleyen')
    const card = mPage.getByRole('listitem', { name: ops.device })
    await expect(card).toBeVisible()
    await expect(card).toContainText('Operasyon')
    await expect(card).toContainText('store-1')
    // An approval cannot be sent without entering the code (the field is required).
    await card.getByRole('button', { name: 'Onayla', exact: true }).click()
    await expect(card).toBeVisible()
    await opPage.context().close()
    await mPage.context().close()
  })

  test('a wrong code is rejected; with the right code it is approved and the store server screen becomes Onaylı', async ({ browser }) => {
    const opPage = await (await browser.newContext()).newPage()
    await storeUpdate(opPage)
    const mPage = await (await browser.newContext()).newPage()
    await headquartersUpdate(mPage, 'Bekleyen')

    await approve(mPage, ops.device, 'AAAA-AAAA')
    await expect(mPage.getByRole('alert')).toBeVisible()
    // The registration is still pending, the unit was not approved.
    await expect(mPage.getByRole('listitem', { name: ops.device })).toBeVisible()
    await expect(opPage.getByTestId('update-status')).toContainText('Onay bekliyor')

    await approve(mPage, ops.device, ops.code)
    await expect(mPage.getByRole('listitem', { name: ops.device })).toHaveCount(0)
    await tab(mPage, 'Onaylı')
    const card = mPage.getByRole('listitem', { name: ops.device })
    await expect(card).toBeVisible()
    await expect(card).toContainText('Karar')

    // When the agent gets a token the screen shows Onaylı and removes the code (refreshed every 5 s).
    await expect(opPage.getByTestId('update-status')).toContainText('Onaylı', { timeout: 60_000 })
    await expect(opPage.getByTestId('update-code')).toHaveCount(0)
    await expect(opPage.getByTestId('update-status')).toContainText('Erişim süresi')
    await opPage.context().close()
    await mPage.context().close()
  })

  test('the register registers and is approved without knowing headquarters, only through the store server proxy', async ({ browser }) => {
    const kPage = await (await browser.newContext()).newPage()
    await registerUpdate(kPage)
    await expect(kPage.getByTestId('update-status')).toContainText('Onay bekliyor')
    await expect(kPage.getByTestId('update-code')).toHaveText(CODE)
    register = {
      device: (await kPage.getByTestId('update-device').textContent()).trim(),
      code: (await kPage.getByTestId('update-code').textContent()).trim(),
    }
    expect(register.device).toMatch(DEVICE)
    expect(register.device).not.toBe(ops.device)

    const mPage = await (await browser.newContext()).newPage()
    await headquartersUpdate(mPage, 'Bekleyen')
    const card = mPage.getByRole('listitem', { name: register.device })
    await expect(card).toContainText('Kasa')
    await expect(card).toContainText(ID)
    // Another unit's code does not match this registration.
    await approve(mPage, register.device, ops.code)
    await expect(mPage.getByRole('alert')).toBeVisible()
    await approve(mPage, register.device, register.code)
    await expect(card).toHaveCount(0)

    await expect(kPage.getByTestId('update-status')).toContainText('Onaylı', { timeout: 60_000 })
    await expect(kPage.getByTestId('update-code')).toHaveCount(0)
    await kPage.context().close()
    await mPage.context().close()
  })

  test('with update installed the register sale continues', async () => {
    const k = new Register(ID, registerURL())
    await k.login()
    await k.ensureShift()
    const sale = await k.sell()
    expect(sale.receiptNo).toBeGreaterThan(0)
  })

  // ---- New release flow: signed channel manifest, announcement, approval, rollback ----

  /** Üç katmanın ekranında güncelleme sayfalarını açar. */
  async function openAll(browser) {
    const headquarters = await (await browser.newContext()).newPage()
    await headquartersOpen(headquarters)
    await headquarters.getByRole('navigation', { name: 'Bölümler' }).getByRole('button', { name: 'Güncelleme', exact: true }).click()
    const store = await (await browser.newContext()).newPage()
    await storeUpdate(store)
    const register = await (await browser.newContext()).newPage()
    await registerUpdate(register)
    return { headquarters, store, register, close: () => Promise.all([headquarters, store, register].map((p) => p.context().close())) }
  }

  test('new version: a manifest signed with the wrong key does not show', async ({ browser }) => {
    // Machines at the prod level accept only the prod key; the pilot key is not enough.
    await publish(manifest({ seq: 1, version: '0.4.0', notes: 'Pilot anahtarıyla imzalı' }), { role: 'pilot' })
    const headquarters = await (await browser.newContext()).newPage()
    await headquartersOpen(headquarters)
    await headquarters.getByRole('navigation', { name: 'Bölümler' }).getByRole('button', { name: 'Güncelleme', exact: true }).click()
    await expect(headquarters.getByTestId('update-poll-error')).toContainText('güvendiği anahtarlardan', { timeout: 60_000 })
    await expect(headquarters.getByTestId('update-offer')).toHaveCount(0)
    await headquarters.context().close()
  })

  test('new version: a signed manifest is announced in every tier but is not installed by itself', async ({ browser }) => {
    await publish(manifest({ seq: 1, version: '0.4.0', urgency: 'security', notes: 'Güvenlik yaması: oturum denetimi sıkılaştırıldı' }))
    const all = await openAll(browser)
    for (const [ad, page, units] of [
      ['headquarters', all.headquarters, 'reporting, ingest, headquarters-update'],
      ['store', all.store, 'report, inventory, store-update'],
      ['register', all.register, 'sales, gateway, guncelleme'],
    ]) {
      const offer = page.getByTestId('update-offer')
      await expect(offer, ad).toContainText('Yeni sürüm: 0.4.0', { timeout: 90_000 })
      await expect(page.getByTestId('update-urgency'), ad).toContainText('Güvenlik yaması')
      await expect(page.getByTestId('update-notes'), ad).toContainText('oturum denetimi sıkılaştırıldı')
      // Pre-download is on by default: the images are downloaded before approval.
      await expect(offer, ad).toContainText('imajlar önceden indirildi', { timeout: 60_000 })
      await expect(page.getByTestId('update-phase'), ad).toContainText('Hazır')
      if (ad !== 'register') await expect(offer, ad).toContainText(units)
    }
    // Headquarters sees the approved units' state too: no version applied yet, a new version is ready.
    await tab(all.headquarters, 'Onaylı')
    for (const device of [ops.device, register.device]) {
      const running = all.headquarters.getByRole('listitem', { name: device }).getByTestId('running-version')
      await expect(running).toContainText('bilinmiyor', { timeout: 60_000 })
      await expect(running).toContainText('yeni sürüm 0.4.0 hazır', { timeout: 60_000 })
    }
    // Nothing is applied without approval: after a while it is still in the announcement.
    await new Promise((r) => setTimeout(r, 15_000))
    for (const page of [all.headquarters, all.store, all.register]) {
      await expect(page.getByTestId('update-phase')).toContainText('Hazır')
      await expect(page.getByTestId('update-applied')).toHaveCount(0)
    }
    await all.close()
  })

  test('new version: it cannot be applied while a shift is open at the register', async ({ browser }) => {
    // The earlier tests opened a shift at the register (ensureShift).
    const register = await (await browser.newContext()).newPage()
    await registerUpdate(register)
    await expect(register.getByTestId('update-blocked')).toContainText('açık vardiya var', { timeout: 60_000 })
    await expect(register.getByRole('button', { name: 'Şimdi uygula' })).toBeDisabled()
    await register.context().close()
  })

  test('new version: the headquarters administrator approves, the version is applied and recorded', async ({ page }) => {
    await headquartersUpdate(page)
    await page.getByRole('button', { name: 'Şimdi uygula' }).click()
    await expect(page.getByTestId('update-applied')).toContainText('0.4.0 uygulandı', { timeout: 60_000 })
    await expect(page.getByTestId('update-offer')).toHaveCount(0)
    // The approving person was written to the record.
    await expect(page.getByTestId('update-applied')).toContainText('headquarters')
  })

  test('new version: the store server manager approves', async ({ page }) => {
    await storeUpdate(page)
    await page.getByRole('button', { name: 'Şimdi uygula' }).click()
    await expect(page.getByTestId('update-applied')).toContainText('0.4.0 uygulandı', { timeout: 60_000 })
    await expect(page.getByTestId('update-offer')).toHaveCount(0)
  })

  test('new version: when the shift closes the cashier cannot apply, the manager applies with a PIN', async ({ browser }) => {
    // Entering the register screen opens a shift (cashier sign-in); the shift is closed while the screen is open.
    const page = await (await browser.newContext()).newPage()
    await registerUpdate(page)
    const k = new Register(ID, registerURL())
    await k.login()
    await k.closeShift()
    await expect(page.getByTestId('update-blocked')).toHaveCount(0, { timeout: 60_000 })
    // Cashier Ayşe is not a manager: manager approval is asked for first.
    await page.getByRole('button', { name: 'Şimdi uygula' }).click()
    await page.getByRole('heading', { name: 'Müdür onayı' }).waitFor()
    // Wrong PIN: no approval is given.
    await pressPIN(page, '0000', 'Onayla')
    await expect(page.getByRole('alert').filter({ hasText: /PIN|geçersiz|hatalı/i })).toBeVisible()
    await pressPIN(page, '9999', 'Onayla')
    await expect(page.getByTestId('update-applied')).toContainText('0.4.0 uygulandı', { timeout: 60_000 })
    // The approver is the manager who entered the PIN.
    await expect(page.getByTestId('update-applied')).toContainText('ali')
    await page.context().close()
  })

  test('new version: the headquarters screen shows the units running versions and images', async ({ page }) => {
    await headquartersUpdate(page, 'Onaylı')
    for (const [ad, device] of [['store', ops.device], ['register', register.device]]) {
      const card = page.getByRole('listitem', { name: device })
      // When the application finishes the agent reports the state right away (it does not wait for the 5-minute interval).
      await expect(card.getByTestId('running-version'), ad).toContainText('0.4.0', { timeout: 60_000 })
      await expect(card.getByTestId('running-version'), ad).not.toContainText('yeni sürüm')
      await expect(card, ad).toContainText('Son bildirim')
      await card.getByText(/^İmajlar \(/).click()
      // In the trial runtime the new digest 2222...: its shortened form on the screen.
      await expect(card.getByTestId('report-units'), ad).toContainText('222222222222')
      await expect(card.getByTestId('report-stale'), ad).toHaveCount(0)
    }
  })

  test('new version: a version that does not come up healthy is rolled back and not announced again', async ({ browser }) => {
    // The store server's report unit is published with an image that does not come up healthy; the others are fine.
    await publish(manifest({ seq: 2, version: '0.5.0', notes: 'Bozuk sürüm', images: { 'abarow-report': BAD_DIGEST } }))
    const all = await openAll(browser)
    await expect(all.store.getByTestId('update-offer')).toContainText('Yeni sürüm: 0.5.0', { timeout: 90_000 })
    await all.store.getByRole('button', { name: 'Şimdi uygula' }).click()
    await expect(all.store.getByTestId('update-rolled-back')).toContainText('0.5.0', { timeout: 90_000 })
    await expect(all.store.getByTestId('update-rolled-back')).toContainText('geri alındı')
    await expect(all.store.getByTestId('update-rolled-back')).toContainText('abarow-report')
    await expect(all.store.getByTestId('update-offer')).toHaveCount(0)
    // The rolled-back version is not announced again (even after a few polling rounds).
    await new Promise((r) => setTimeout(r, 15_000))
    await expect(all.store.getByTestId('update-offer')).toHaveCount(0)

    // A newer, fixed version is announced.
    await publish(manifest({ seq: 3, version: '0.5.1', notes: 'Düzeltildi', def: '3' }))
    await expect(all.store.getByTestId('update-offer')).toContainText('Yeni sürüm: 0.5.1', { timeout: 90_000 })
    // Headquarters: after the rollback the store server is still on 0.4.0, 0.5.1 is ready.
    await tab(all.headquarters, 'Onaylı')
    const running = all.headquarters.getByRole('listitem', { name: ops.device }).getByTestId('running-version')
    await expect(running).toContainText('0.4.0', { timeout: 60_000 })
    await expect(running).toContainText('yeni sürüm 0.5.1 hazır', { timeout: 60_000 })
    await all.close()
  })

  test('new version: postpone and skip; a skipped version is not announced again', async ({ browser }) => {
    const all = await openAll(browser)
    await expect(all.headquarters.getByTestId('update-offer')).toContainText('Yeni sürüm: 0.5.1', { timeout: 90_000 })
    // Postpone: the announcement stays, the note that the reminder was postponed appears.
    await all.headquarters.getByRole('button', { name: 'Sonra hatırlat' }).click()
    await expect(all.headquarters.getByText(/Hatırlatma .* ertelendi/)).toBeVisible()
    await expect(all.headquarters.getByTestId('update-offer')).toBeVisible()
    // Atla: bildirim kalkar.
    await all.headquarters.getByRole('button', { name: 'Bu sürümü atla' }).click()
    await expect(all.headquarters.getByTestId('update-offer')).toHaveCount(0)
    await expect(all.headquarters.getByText('Atlanan sürüm: 0.5.1')).toBeVisible()
    await new Promise((r) => setTimeout(r, 15_000))
    await expect(all.headquarters.getByTestId('update-offer')).toHaveCount(0)
    await all.close()
  })

  test('new version: a manifest with an old sequence number and an expired one is rejected', async ({ browser }) => {
    // The newest sequence number we have seen is 3; if manifest number 2 is put back it counts as a rollback.
    await publish(manifest({ seq: 2, version: '0.5.0', notes: 'Eski bildirim' }))
    const headquarters = await (await browser.newContext()).newPage()
    await headquartersOpen(headquarters)
    await headquarters.getByRole('navigation', { name: 'Bölümler' }).getByRole('button', { name: 'Güncelleme', exact: true }).click()
    await expect(headquarters.getByTestId('update-poll-error')).toContainText('bilinen en yeni sürümden eski', { timeout: 60_000 })
    await expect(headquarters.getByTestId('update-offer')).toHaveCount(0)
    // A stale manifest (whose validity period has expired) is rejected too.
    const stale = manifest({ seq: 9, version: '9.9.9' })
    stale.issued = new Date(Date.now() - 3_600_000).toISOString()
    stale.expires = new Date(Date.now() - 60_000).toISOString()
    await publish(stale)
    await expect(headquarters.getByTestId('update-poll-error')).toContainText('süresi dolmuş', { timeout: 60_000 })
    unpublish()
    await headquarters.context().close()
  })

  test('key rotation: the new key becomes valid without a restart, the expired old key drops out', async ({ browser }) => {
    test.setTimeout(5 * 60_000)
    const headquarters = await (await browser.newContext()).newPage()
    await headquartersOpen(headquarters)
    await headquarters.getByRole('navigation', { name: 'Bölümler' }).getByRole('button', { name: 'Güncelleme', exact: true }).click()
    const pollError = headquarters.getByTestId('update-poll-error')
    const original = editTrust((t) => t)
    try {
      // A manifest signed with a new key that is not yet in the trust file is not accepted.
      const fresh = await generateKey('prod-yeni')
      await publish(manifest({ seq: 4, version: '0.5.2', notes: 'Yeni anahtarla imzalı', def: '3' }), { key: 'prod-yeni' })
      await expect(pollError).toContainText('güvendiği anahtarlardan', { timeout: 60_000 })

      // Transition: the new key is added to the trust file (the old one is valid too); the agents are not restarted.
      editTrust((t) => ({ ...t, prod: [...[].concat(t.prod), { key: fresh, not_before: new Date(Date.now() - 60_000).toISOString() }] }))
      await expect(pollError).toHaveCount(0, { timeout: 60_000 })
      await expect(headquarters.getByTestId('update-offer')).toContainText('Yeni sürüm: 0.5.2', { timeout: 60_000 })
      await headquarters.getByRole('button', { name: 'Bu sürümü atla' }).click()
      await expect(headquarters.getByTestId('update-offer')).toHaveCount(0)

      // In the transition window a manifest signed with the old key is still accepted.
      await publish(manifest({ seq: 5, version: '0.5.3', notes: 'Eski anahtarla imzalı', def: '4' }))
      await expect(headquarters.getByTestId('update-offer')).toContainText('Yeni sürüm: 0.5.3', { timeout: 60_000 })
      await headquarters.getByRole('button', { name: 'Bu sürümü atla' }).click()
      await expect(headquarters.getByTestId('update-offer')).toHaveCount(0)

      // The old key expires: a manifest signed with it is now rejected, one signed with the new one is accepted.
      editTrust((t) => ({ ...t, prod: [{ key: [].concat(t.prod)[0].key ?? [].concat(t.prod)[0], not_after: new Date(Date.now() - 1000).toISOString() }, { key: fresh }] }))
      await publish(manifest({ seq: 6, version: '0.5.4', notes: 'Süresi dolan anahtarla', def: '5' }))
      await expect(pollError).toContainText('güvendiği anahtarlardan', { timeout: 60_000 })
      await expect(headquarters.getByTestId('update-offer')).toHaveCount(0)
      await publish(manifest({ seq: 6, version: '0.5.4', notes: 'Yeni anahtarla', def: '5' }), { key: 'prod-yeni' })
      await expect(headquarters.getByTestId('update-offer')).toContainText('Yeni sürüm: 0.5.4', { timeout: 60_000 })
      await headquarters.getByRole('button', { name: 'Bu sürümü atla' }).click()
      await expect(headquarters.getByTestId('update-offer')).toHaveCount(0)
    } finally {
      restoreTrust(original)
      unpublish()
      await headquarters.context().close()
    }
  })

  test('the publisher registry: CI writes to the test path, promotion copies bytes, a unit pulls only its own channel with its token', async () => {
    test.setTimeout(5 * 60_000)
    // CI (the htpasswd user) writes to the test channel's path.
    const digest = await pushAlpine('test/abarow-x', '1')
    expect(digest).toMatch(/^sha256:[0-9a-f]{64}$/)
    writeDigest('promotion', 'abarow-x', digest)

    // The promotion script (the one promotion.yml uses) copies the same digest to the prod path and verifies it.
    const copy = await skopeo(['/scripts/image-copy.sh', `${REGISTRY}/test`, `${REGISTRY}/prod`, '0.4.0', '/w/promotion'], {
      entrypoint: 'bash',
      env: { SRC_CREDS: CI_CREDS, DST_CREDS: CI_CREDS, SKOPEO_TLS_VERIFY: 'false' },
    })
    expect(copy.out).toContain('done: 1 images')
    const prod = await skopeo(['inspect', '--tls-verify=false', '--creds', CI_CREDS, '--format', '{{.Digest}}', `docker://${REGISTRY}/prod/abarow-x:0.4.0`])
    expect(prod.out, 'the digest must not change in promotion').toBe(digest)

    // The unit pulls only the prod path with the token it got from the publisher (headquarters, level prod).
    const creds = `abarow:${await unitToken()}`
    const pull = (path, c) => skopeo(['inspect', '--tls-verify=false', ...(c ? ['--creds', c] : []), `docker://${REGISTRY}/${path}`], { allowFail: true })
    const ok = await pull(`prod/abarow-x@${digest}`, creds)
    expect(ok.ok, ok.out).toBe(true)
    // Another channel's path, a request without credentials and a wrong token are rejected.
    expect((await pull(`test/abarow-x@${digest}`, creds)).ok, 'prod jetonu test yolunu çekmemeli').toBe(false)
    expect((await pull(`prod/abarow-x@${digest}`, null)).ok, 'kimliksiz çekme').toBe(false)
    expect((await pull(`prod/abarow-x@${digest}`, `abarow:${'x'.repeat(40)}`)).ok, 'sahte jeton').toBe(false)
    // The token gives only pull permission: the unit cannot write to the registry.
    const push = await skopeo(['copy', '--dest-tls-verify=false', '--dest-creds', creds, 'docker-archive:/w/alp.tar', `docker://${REGISTRY}/prod/abarow-y:1`], { allowFail: true })
    expect(push.ok, 'birim jetonuyla yazma').toBe(false)
  })

  test('image signature: the Podman policy pulls only an image signed with the level key', async () => {
    test.setTimeout(8 * 60_000)
    const creds = `abarow:${await unitToken()}`

    // abarow-x on the prod path is signed with the prod key during promotion (image-sign.sh).
    const digest = (await skopeo(['inspect', '--tls-verify=false', '--creds', CI_CREDS, '--format', '{{.Digest}}', `docker://${REGISTRY}/prod/abarow-x:0.4.0`])).out
    await signImages(`${REGISTRY}/prod`, '/w/promotion'.replace('/w', 'tools/e2e/out/trust/image'), 'prod')

    // the same bytes: one unsigned (abarow-y), one signed with the wrong key (abarow-w, the pilot key).
    for (const name of ['abarow-y', 'abarow-w']) {
      await skopeo(['copy', '--all', '--preserve-digests', '--src-tls-verify=false', '--dest-tls-verify=false', '--src-creds', CI_CREDS, '--dest-creds', CI_CREDS,
        `docker://${REGISTRY}/prod/abarow-x@${digest}`, `docker://${REGISTRY}/prod/${name}:0.4.0`])
    }
    writeDigest('wrong', 'abarow-w', digest)
    await signImages(`${REGISTRY}/prod`, 'tools/e2e/out/trust/image/wrong', 'pilot')

    // The prod level's policy is produced from the cosign keys in the trust file.
    const policy = await generatePolicy('prod', 'prod')

    const ok = await podmanPull(policy, `${REGISTRY}/prod/abarow-x@${digest}`, creds)
    expect(ok.ok, ok.out).toBe(true)
    const unsigned = await podmanPull(policy, `${REGISTRY}/prod/abarow-y@${digest}`, creds)
    expect(unsigned.ok, 'an unsigned image must not be pulled').toBe(false)
    expect(unsigned.out).toMatch(/signature|rejected/i)
    const wrong = await podmanPull(policy, `${REGISTRY}/prod/abarow-w@${digest}`, creds)
    expect(wrong.ok, 'an image signed with the pilot key must not be pulled at the prod level').toBe(false)
    expect(wrong.out).toMatch(/signature|accepted|rejected/i)
  })

  test('image signature rotation: in the transition window the old and new cosign keys, after expiry only the new one', async () => {
    test.setTimeout(10 * 60_000)
    const creds = `abarow:${await unitToken()}`
    const digest = (await skopeo(['inspect', '--tls-verify=false', '--creds', CI_CREDS, '--format', '{{.Digest}}', `docker://${REGISTRY}/prod/abarow-x:0.4.0`])).out
    const fresh = await cosignKeyPair('prod-yeni')

    // The same bytes under two names: abarow-r1 is signed with the old prod key, abarow-r2 with the new key.
    for (const [name, key] of [['abarow-r1', 'prod'], ['abarow-r2', 'prod-yeni']]) {
      await skopeo(['copy', '--all', '--preserve-digests', '--src-tls-verify=false', '--dest-tls-verify=false', '--src-creds', CI_CREDS, '--dest-creds', CI_CREDS,
        `docker://${REGISTRY}/prod/abarow-x@${digest}`, `docker://${REGISTRY}/prod/${name}:0.4.0`])
      writeDigest(`dondurme-${name}`, name, digest)
      await signImages(`${REGISTRY}/prod`, `tools/e2e/out/trust/image/dondurme-${name}`, key)
    }
    const hour = 3_600_000
    const oldKey = (t) => [].concat(t.cosign.prod)[0]
    const withProd = (t, prod) => ({ ...t, cosign: { ...t.cosign, prod } })

    // Transition window: the old key has not expired, the new one is valid; the signatures of both are accepted.
    const window = trustVariant('gecis', (t) => withProd(t, [
      { key: oldKey(t), not_after: new Date(Date.now() + hour).toISOString() }, { key: fresh, not_before: new Date(Date.now() - hour).toISOString() }]))
    const polWindow = await generatePolicy('prod', 'dondurme-gecis', window)
    for (const name of ['abarow-r1', 'abarow-r2']) {
      const r = await podmanPull(polWindow, `${REGISTRY}/prod/${name}@${digest}`, creds)
      expect(r.ok, `${name} must be pulled in the transition window: ${r.out}`).toBe(true)
    }

    // The old key dropped: one signed with the new key is pulled, one signed with the old key is rejected.
    const after = trustVariant('sonra', (t) => withProd(t, [
      { key: oldKey(t), not_after: new Date(Date.now() - hour).toISOString() }, { key: fresh }]))
    const polAfter = await generatePolicy('prod', 'dondurme-sonra', after)
    const okNew = await podmanPull(polAfter, `${REGISTRY}/prod/abarow-r2@${digest}`, creds)
    expect(okNew.ok, okNew.out).toBe(true)
    const rejected = await podmanPull(polAfter, `${REGISTRY}/prod/abarow-r1@${digest}`, creds)
    expect(rejected.ok, 'an image signed with the expired key must not be pulled').toBe(false)
    expect(rejected.out).toMatch(/signature|rejected/i)

    // No valid key left: no policy is produced (in this case the agent writes the locked policy).
    const none = trustVariant('hic', (t) => withProd(t, [{ key: oldKey(t), not_after: new Date(Date.now() - hour).toISOString() }]))
    await expect(generatePolicy('prod', 'dondurme-hic', none)).rejects.toThrow()
  })

  test('unauthorized requests are rejected', async () => {
    // Without a session: the headquarters, store server and register screens do not give the update endpoints.
    for (const url of [
      'http://127.0.0.1:9090/api/update/registrations',
      'http://127.0.0.1:9090/api/update/status',
      'http://127.0.0.1:8090/api/update/status',
      `${registerURL()}/api/update/status`,
    ]) {
      const r = await fetch(url)
      expect(r.status, url).toBe(401)
    }
    // Approval cannot be done without a session.
    const r = await fetch(`http://127.0.0.1:9090/api/update/registrations/${ops.device}/approve`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ code: ops.code }),
    })
    expect(r.status).toBe(401)
  })

  test('report role: no update and users tabs, the admin endpoints give 403', async ({ page }) => {
    const admin = new Headquarters()
    await admin.login()
    await admin.reporting.call('POST', '/api/users', {
      username: 'rapor1',
      password: 'report-parola-123',
      name: 'Rapor Kişisi',
      role: 'report',
      all_stores: true,
      stores: [],
      email: '',
    })
    const report = new Headquarters()
    await report.login('rapor1', 'report-parola-123')
    for (const [method, path] of [
      ['POST', '/api/update/apply'],
      ['POST', '/api/update/skip'],
      ['POST', '/api/update/snooze'],
      ['GET', '/api/update/registrations'],
      ['POST', `/api/update/registrations/${ops.device}/approve`],
      ['POST', `/api/update/registrations/${ops.device}/revoke`],
    ]) {
      const r = await report.reporting.call(method, path, method === 'POST' ? { code: ops.code } : undefined, { allow: [403] })
      expect(r.status, `${method} ${path}`).toBe(403)
    }
    // A report person can see the agent's status (no code, the status of their own headquarters).
    expect((await report.reporting.call('GET', '/api/update/status')).status).toBe(200)

    await headquartersOpen(page, 'http://127.0.0.1:9090', 'rapor1', 'report-parola-123')
    const nav = page.getByRole('navigation', { name: 'Bölümler' })
    await expect(nav.getByRole('button', { name: 'Güncelleme', exact: true })).toHaveCount(0)
    await expect(nav.getByRole('button', { name: 'Kullanıcılar', exact: true })).toHaveCount(0)
    await page.getByRole('button', { name: 'Çıkış' }).click()
    // The registration the report person approved must be unchanged: the store server is still approved.
    await headquartersUpdate(page, 'Onaylı')
    await expect(page.getByRole('listitem', { name: ops.device })).toBeVisible()
  })

  test('an approved registration is revoked; it goes to the revoked list', async ({ page }) => {
    await headquartersUpdate(page, 'Onaylı')
    const card = page.getByRole('listitem', { name: register.device })
    await expect(card).toBeVisible()
    await card.getByRole('button', { name: 'Erişimi iptal et' }).click()
    await expect(card).toHaveCount(0)
    await tab(page, 'İptal edilen')
    await expect(page.getByRole('listitem', { name: register.device })).toBeVisible()
    // The store server's registration is not affected.
    await tab(page, 'Onaylı')
    await expect(page.getByRole('listitem', { name: ops.device })).toBeVisible()
  })

  test('rejected registration: a register that comes with a new identity is rejected, its screen shows Reddedildi', async ({ browser }) => {
    // The register agent's identity is deleted; it registers again with the new identity.
    await stack.compose(ID, ['stop', 'update'])
    await stack.compose(ID, ['rm', '-f', 'update'])
    await (await import('node:child_process')).execFileSync('docker', ['volume', 'rm', `abarow-${ID}_update-data`], { stdio: 'ignore' })
    await stack.compose(ID, ['up', '--detach', '--wait', 'update'])

    const kPage = await (await browser.newContext()).newPage()
    await registerUpdate(kPage)
    await expect(kPage.getByTestId('update-status')).toContainText('Onay bekliyor')
    const device = (await kPage.getByTestId('update-device').textContent()).trim()
    expect(device).not.toBe(register.device)

    const mPage = await (await browser.newContext()).newPage()
    await headquartersUpdate(mPage, 'Bekleyen')
    const card = mPage.getByRole('listitem', { name: device })
    await card.getByRole('button', { name: 'Reddet' }).click()
    await expect(card).toHaveCount(0)
    await tab(mPage, 'Reddedilen')
    await expect(mPage.getByRole('listitem', { name: device })).toBeVisible()

    await expect(kPage.getByTestId('update-status')).toContainText('Reddedildi', { timeout: 60_000 })
    await expect(kPage.getByText('güncelleme erişimi reddedildi')).toBeVisible()
    await kPage.context().close()
    await mPage.context().close()
  })

  test('when headquarters registration server closes the screen continues, when it opens new registrations show', async ({ page }) => {
    await stack.stop('headquarters', 'headquarters-server')
    await headquartersUpdate(page, 'Bekleyen')
    // There is no registration server: the list gives an error but the headquarters screen works.
    await expect(page.getByRole('alert')).toBeVisible()
    await page.getByRole('navigation', { name: 'Bölümler' }).getByRole('button', { name: 'Raporlar', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Mağazalar', exact: true, level: 2 })).toBeVisible()
    await stack.start('headquarters', 'headquarters-server')
    await until('kayıt sunucusu açılmalı', async () => {
      const r = await fetch('http://127.0.0.1:9090/api/update/status')
      return r.status === 401
    })
  })
})
