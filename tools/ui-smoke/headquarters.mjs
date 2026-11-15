// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// headquarters opens the headquarters screen (headquarters reports) in a real browser
// and takes screenshots. The three-tier simulation calls it after running register-sim on two
// registers (scripts/store-sim.sh):
//
//   docker compose -f deploy/compose/headquarters.yaml --profile ui-test run --rm ui-smoke
//
// The images are written under tools/ui-smoke/out/.

import { chromium } from 'playwright'

const url = process.env.ABAROW_UI_URL ?? 'http://reporting:8080'
const out = process.env.ABAROW_UI_OUT ?? 'out'
const username = process.env.ABAROW_HEADQUARTERS_USER ?? ''
const password = process.env.ABAROW_HEADQUARTERS_PASSWORD ?? ''

const browser = await chromium.launch()
// localDate gives 00:00 of the day days days from now (before now if negative)
// in datetime-local format. The browser interprets it in its own time zone
// (Europe/Istanbul); even if Node's time zone (UTC in CI) differs
// a two-day difference keeps it in the future (or the past) in every case.
const localDate = (days) => {
  const d = new Date(Date.now() + days * 86400000)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}T00:00`
}
const page = await browser.newPage({ viewport: { width: 1280, height: 900 }, locale: 'tr-TR', timezoneId: 'Europe/Istanbul' })
const errors = []
page.on('pageerror', (e) => errors.push(e.message))
// The 401 on the unauthenticated open and of the wrong password is expected; the browser's resource
// loading messages are not counted as errors. The application's own errors and security
// policy (CSP) violations are counted.
page.on('console', (m) => m.type() === 'error' && !m.text().startsWith('Failed to load resource') && errors.push(m.text()))

function check(cond, msg) {
  if (!cond) throw new Error(msg)
  console.log(`✓ ${msg}`)
}

const tl = new Intl.NumberFormat('tr-TR', { style: 'currency', currency: 'TRY' })
const money = (kurus) => tl.format(kurus / 100)
const api = async (path) => (await page.request.get(`${url}${path}`)).json()

try {
  // The reports ask for a sign-in.
  const anon = await page.request.get(`${url}/api/reports/summary`)
  check(anon.status() === 401, 'girişsiz rapor isteği reddedildi (401)')
  // The corporate sign-in (OIDC) is not installed in this simulation (there is no real IdP):
  // the screen's corporate parts are tried by imitating server responses;
  // the protocol itself is in the pkg/login/oidc tests.
  const real = await (await page.request.get(`${url}/api/auth/methods`)).json()
  const corporateMethod = { id: 'oidc', kind: 'redirect', label: 'Kurumsal girişle gir', url: '/api/auth/oidc/start' }
  await page.route('**/api/auth/methods', (route) => route.fulfill({ json: { methods: [...real.methods, corporateMethod] } }))
  await page.goto(`${url}/?oidc_error=refused`)
  await page.getByRole('alert').filter({ hasText: 'kimliğe bağlı açık bir hesap yok' }).waitFor()
  check(
    (await page.getByRole('link', { name: 'Kurumsal girişle gir' }).getAttribute('href')) === '/api/auth/oidc/start' &&
      !page.url().includes('oidc_error'),
    'kurumsal giriş bağlantısı var; IdP dönüşündeki hata gösterildi ve adresten silindi',
  )
  // Random text written in the address is not shown on the screen; only a known code.
  await page.goto(`${url}/?oidc_error=${encodeURIComponent('Hesabınız ele geçirildi; şuraya girin')}`)
  await page.getByRole('alert').filter({ hasText: 'Kurumsal giriş başarısız.' }).waitFor()
  check((await page.getByText('ele geçirildi').count()) === 0, 'adresteki rastgele metin ekranda gösterilmiyor')
  await page.unroute('**/api/auth/methods')
  await page.goto(url)
  await page.getByLabel('Kullanıcı adı').fill(username)
  await page.getByLabel('Parola').fill('yanlış-parola')
  await page.getByRole('button', { name: 'Giriş' }).click()
  await page.getByRole('alert').filter({ hasText: 'Kullanıcı adı ya da parola yanlış' }).waitFor()
  check(true, 'yanlış parola reddedildi')
  await page.screenshot({ path: `${out}/headquarters-0-login.png` })
  await page.getByLabel('Parola').fill(password)
  await page.getByRole('button', { name: 'Giriş' }).click()
  await page.getByRole('heading', { name: 'Mağazalar', exact: true, level: 2 }).waitFor()
  const me = await api('/api/auth/session')
  await page.getByTestId('user').filter({ hasText: `${me.name} · Yönetici` }).waitFor()
  check(me.role === 'admin', `${me.username} hesabıyla giriş yapıldı (yönetici)`)

  // The stores are shown with their API state and today's sales.
  const { stores, today } = await api('/api/stores')
  check(stores.length > 0, `${stores.length} mağaza listelendi`)
  for (const s of stores) {
    const card = page.getByRole('listitem', { name: s.name })
    await card.getByText('Bağlı', { exact: true }).waitFor()
    await card.getByTestId('today-total').filter({ hasText: money(s.today_total_kurus) }).waitFor()
  }
  check(true, 'mağazalar bağlı ve bugünkü satışlarıyla gösteriliyor')

  // Today's summary is the same as the stores' total today.
  const day = await api('/api/reports/summary')
  const storesTotal = stores.reduce((t, s) => t + s.today_total_kurus, 0)
  check(day.total_kurus === storesTotal && day.receipt_count > 0, `bugünün özeti mağazaların toplamına eşit (${money(storesTotal)})`)
  await page.getByTestId('summary-total').filter({ hasText: money(day.total_kurus) }).waitFor()
  await page.getByTestId('summary-receipts').filter({ hasText: new RegExp(`^${day.receipt_count}$`) }).waitFor()
  for (const name of ['Mağazalar', 'Ödemeler', 'KDV', 'Kasiyerler', 'Ürünler']) {
    await page.getByRole('heading', { name, exact: true, level: 3 }).waitFor()
  }
  const paid = day.payments.reduce((t, p) => t + p.amount_kurus, 0)
  check(paid === day.total_kurus, `ödemeler toplamı satışa eşit; mağaza, ödeme, KDV, kasiyer ve ürün tabloları gösteriliyor`)
  await page.getByText('mali değeri yoktur').waitFor()
  check(true, 'mali değeri olmadığı yazıyor')
  await page.screenshot({ path: `${out}/headquarters-1-bugun.png`, fullPage: true })

  // Last 7 days: day by day, days with no sales too.
  await page.getByRole('button', { name: 'Son 7 gün' }).click()
  await page.getByRole('heading', { name: 'Günler', exact: true, level: 3 }).waitFor()
  const week = await api(`/api/reports/summary?from=${addDays(today, -6)}&to=${today}`)
  check(week.days.length === 7, '7 gün listelendi')
  await page.getByTestId('summary-total').filter({ hasText: money(week.total_kurus) }).waitFor()
  check(week.total_kurus >= day.total_kurus, `son 7 günün toplamı ${money(week.total_kurus)}`)

  // When a single store is selected the store's name in the title.
  const first = stores[0]
  const select = page.getByRole('combobox', { name: 'Mağaza', exact: true })
  await select.selectOption(first.id)
  await page.getByRole('heading', { name: new RegExp(`Satış özeti ${first.name}`) }).waitFor()
  const one = await api(`/api/reports/summary?from=${addDays(today, -6)}&to=${today}&store=${first.id}`)
  await page.getByTestId('summary-total').filter({ hasText: money(one.total_kurus) }).waitFor()
  check(true, `${first.name} seçildi: ${money(one.total_kurus)}`)

  // A past period with no sales.
  await page.getByLabel('Başlangıç').fill('2020-01-01')
  await page.getByLabel('Başlangıç').dispatchEvent('change')
  await page.getByLabel('Bitiş').fill('2020-01-31')
  await page.getByLabel('Bitiş').dispatchEvent('change')
  await page.getByText('Bu dönemde satış yok.').waitFor()
  check(true, 'satışsız geçmiş dönem gösterildi')
  await select.selectOption('')
  await page.getByRole('button', { name: 'Bugün' }).click()
  await page.getByTestId('summary-total').filter({ hasText: money(day.total_kurus) }).waitFor()
  check(true, 'bugüne dönüldü')

  // Catalog management: products.
  const productRows = () => page.getByRole('table', { name: 'Ürün listesi' }).getByRole('row')
  // The different prices the stores reported (the store server test gave a product a store
  // price); the event reaches headquarters in a few seconds.
  let exceptions = []
  for (let i = 0; i < 60 && exceptions.length === 0; i++) {
    exceptions = (await api('/api/reports/store-prices')).items.filter((e) => e.store_price_kurus !== undefined)
    if (exceptions.length === 0) await new Promise((r) => setTimeout(r, 1000))
  }
  await page.getByRole('button', { name: 'Ürünler', exact: true }).click()
  await page.getByRole('heading', { name: 'Ürünler', exact: true, level: 2 }).waitFor()
  if (exceptions.length > 0) {
    const e = exceptions[0]
    const region = page.getByRole('region', { name: 'Mağaza fiyatları' })
    const row = region.getByRole('row').filter({ hasText: e.product_name })
    await row.getByTestId('store-price').filter({ hasText: money(e.store_price_kurus) }).waitFor()
    await row.getByText(e.store_name, { exact: true }).waitFor()
    check(true, `mağaza fiyatları: ${e.store_name} · ${e.product_name} ${money(e.store_price_kurus)} (merkez ${money(e.headquarters_price_kurus)}, fark ${money(e.difference_kurus)})`)
  }
  const catalog = await api('/api/catalog/products')
  check(catalog.products.length > 0, `katalogda ${catalog.products.length} ürün (yüklenen başlangıç dosyası)`)
  const sample = catalog.products[0]
  await page.getByRole('button', { name: sample.name, exact: true }).waitFor()
  await page.getByRole('searchbox').fill(sample.code)
  // The search loads with a delay: wait until the title and a single product are left in the table.
  await waitCount(productRows(), 2)
  await productRows().filter({ hasText: sample.name }).waitFor()
  check(true, `koda göre arama (${sample.code})`)
  await page.getByRole('searchbox').fill('')

  // New product.
  const code = `test-${Date.now()}`
  // The name is different on every run: the test can run again on the same database.
  const name = `Deneme Ürün ${code.slice(-5)}`
  const barcode = `29${code.slice(-11)}`
  await page.getByRole('button', { name: 'Yeni ürün' }).click()
  const form = page.getByRole('region', { name: 'Yeni ürün' })
  await form.getByLabel('Kod', { exact: true }).fill(code)
  await form.getByLabel('Ad', { exact: true }).fill(name)
  await form.getByLabel('Barkodlar', { exact: true }).fill(barcode)
  await form.getByLabel('Fiyat (KDV dahil)', { exact: true }).fill('12,50')
  await form.getByRole('button', { name: 'Ekle', exact: true }).click()
  await page.getByRole('button', { name, exact: true }).waitFor()
  check(true, 'yeni ürün eklendi (12,50 TL)')

  // Future-dated price: 00:00 two days later; it is withdrawn; then an immediately valid price.
  await page.getByRole('button', { name, exact: true }).click()
  const editor = page.getByRole('region', { name: `Ürün: ${name}` })
  await editor.getByTestId('current-price').filter({ hasText: money(1250) }).waitFor()
  await editor.getByLabel('Yeni fiyat', { exact: true }).fill('13,75')
  await editor.getByLabel('İleri tarihte', { exact: true }).check()
  // A past moment is rejected (the price does not change right away by mistake).
  await editor.getByLabel('Tarih ve saat', { exact: true }).fill(localDate(-2))
  await editor.getByRole('button', { name: 'Fiyatı kaydet' }).click()
  await editor.getByRole('alert').filter({ hasText: 'geçmişte' }).waitFor()
  await editor.getByTestId('current-price').filter({ hasText: money(1250) }).waitFor()
  check(true, 'geçmiş tarihli "ileri tarihli fiyat" reddedildi')
  await editor.getByLabel('Tarih ve saat', { exact: true }).fill(localDate(2))
  await editor.getByRole('button', { name: 'Fiyatı kaydet' }).click()
  await editor.getByText('İleri tarihli fiyat kaydedildi.').waitFor()
  await editor.getByText(`${money(1375)} · `).waitFor()
  await editor.getByTestId('current-price').filter({ hasText: money(1250) }).waitFor()
  check(true, 'ileri tarihli fiyat kaydedildi; şu anki fiyat değişmedi')
  await page.screenshot({ path: `${out}/headquarters-3-urun.png`, fullPage: true })
  await editor.getByRole('button', { name: 'Geri al' }).click()
  await editor.getByText('İleri tarihli fiyat geri alındı.').waitFor()
  await editor.getByRole('button', { name: 'Geri al' }).waitFor({ state: 'detached' })
  check(true, 'ileri tarihli fiyat geri alındı')
  await editor.getByLabel('Yeni fiyat', { exact: true }).fill('11,90')
  await editor.getByRole('button', { name: 'Fiyatı kaydet' }).click()
  await editor.getByTestId('current-price').filter({ hasText: money(1190) }).waitFor()
  check(true, 'yeni fiyat hemen geçerli (11,90 TL)')

  // Restaurant menu: category, option group (with a price difference) and image
  // (shrunk in the browser and uploaded).
  await editor.getByLabel('Menü kategorisi', { exact: true }).fill('Ekran Testi')
  await editor.getByRole('button', { name: 'Opsiyon grubu ekle' }).click()
  await editor.getByLabel('Grup 1 adı').fill('Pişirme')
  await editor.getByLabel('Grup 1 seçenek 1', { exact: true }).fill('az')
  await editor.getByRole('button', { name: 'Seçenek ekle' }).click()
  await editor.getByLabel('Grup 1 seçenek 2', { exact: true }).fill('iyi')
  await editor.getByLabel('Grup 1 seçenek 2 fiyat farkı').fill('+5,00')
  await editor.getByRole('button', { name: 'Kaydet', exact: true }).click()
  await editor.getByText('Kaydedildi; kasalara iniyor.').waitFor()
  // 1×1 PNG.
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64')
  await editor.getByLabel('Görsel seç').setInputFiles({ name: 'urun.png', mimeType: 'image/png', buffer: png })
  await editor.getByText('Görsel yüklendi; kasalara iniyor.').waitFor()
  await editor.getByRole('img', { name }).waitFor()
  const menuProduct = (await api(`/api/catalog/products/${code}`)).product
  check(
    menuProduct.category === 'Ekran Testi' &&
      menuProduct.option_groups[0].options[1].price_delta_kurus === 500 &&
      menuProduct.image_sha256.length === 64,
    'menü kategorisi, opsiyon grubu (+5,00 TL) ve görsel kaydedildi',
  )
  await page.screenshot({ path: `${out}/headquarters-3-menu.png`, fullPage: true })

  // If someone else changed it in the meantime a change made with the old version is rejected.
  const stale = await page.request.post(`${url}/api/catalog/products/${code}/prices`, {
    data: { version: 1, unit_price_kurus: 1 },
    headers: { 'Content-Type': 'application/json' },
  })
  check(stale.status() === 409, 'eski sürümle değişiklik reddedildi (409)')

  await editor.getByRole('button', { name: 'Satıştan kaldır' }).click()
  await editor.getByText('Ürün satıştan kaldırıldı.').waitFor()
  await productRows().filter({ hasText: name }).getByText('satışta değil').waitFor()
  const detail = await api(`/api/catalog/products/${code}`)
  check(
    detail.history.length === 7 && detail.history.every((c) => c.by === me.username),
    `değişiklik geçmişi: ${detail.history.length} kayıt, hepsi ${me.username} adına`,
  )
  await editor.getByRole('button', { name: 'Kapat' }).click()
  await page.getByRole('button', { name: 'Raporlar', exact: true }).click()
  await page.getByRole('heading', { name: 'Mağazalar', exact: true, level: 2 }).waitFor()

  // Stock (if the inventory module is installed): the stores' stock with their movements
  // gelir; merkez deposuna mal kabul ekrandan girilir.
  if ((await page.request.get(`${url}/api/stock`)).status() === 200) {
    await page.getByRole('button', { name: 'Stok', exact: true }).click()
    await page.getByRole('heading', { name: 'Stok', exact: true, level: 2 }).waitFor()
    await page.getByRole('columnheader', { name: 'Merkez deposu' }).waitFor()
    // register-sim counted this product as 1 at the store server (see scripts/store-sim.sh).
    await page.getByRole('searchbox', { name: 'Ara' }).fill('Stok Denemesi register-1')
    await page.getByRole('button', { name: /^Stok Denemesi register-1, Mağaza 1: 1 adet$/ }).waitFor()
    check(true, 'mağazanın stoğu merkezde görünüyor (operasyon sayımı: 1 adet)')
    await page.getByRole('button', { name: 'Mal kabul', exact: true }).click()
    const receipt = page.getByRole('region', { name: 'Mal kabul (merkez deposu)' })
    await receipt.getByLabel('Ürün ekle').fill('2990000000995')
    await receipt.getByRole('button', { name: 'Bul' }).click()
    await receipt.getByLabel(/miktarı$/).fill('40')
    await receipt.getByLabel('Tedarikçi').fill('Ekran Testi Tedarik')
    const irs = `HEADQUARTERS-${Date.now()}`
    await receipt.getByLabel('İrsaliye no').fill(irs)
    await receipt.getByRole('button', { name: 'Stoğa gir' }).click()
    await receipt.getByText(`İrsaliye ${irs} stoğa girildi.`).waitFor()
    await receipt.getByRole('button', { name: 'Kapat', exact: true }).click()
    await page.getByRole('searchbox', { name: 'Ara' }).fill('2990000000995')
    await page.getByRole('button', { name: /^Mağaza Fiyatlı Deneme Ürünü, Merkez deposu: / }).click()
    await page.getByRole('region', { name: /^Hareketler: .*Merkez deposu$/ }).getByRole('cell', { name: irs }).waitFor()
    await page.screenshot({ path: `${out}/headquarters-5-stok.png` })
    check(true, `merkez deposuna mal kabul ekrandan girildi (${irs}), hareketlerde görünüyor`)

    // Transfer: the store server test sent 2 units to the headquarters warehouse; the warehouse
    // takes delivery. Then 3 units are sent from the warehouse to the store (store-sim
    // takes delivery at the store server). The events come over NATS.
    let arrived = false
    for (let i = 0; i < 120 && !arrived; i++) {
      const d = await (await page.request.get(`${url}/api/stock/transfers`)).json()
      arrived = d.transfers.some((t) => t.pending && t.from === 'store-1' && t.to === 'headquarters')
      if (!arrived) await page.waitForTimeout(500)
    }
    check(arrived, 'mağazanın gönderdiği transfer merkezde (yolda)')
    await page.getByRole('button', { name: 'Raporlar', exact: true }).click()
    await page.getByRole('button', { name: 'Stok', exact: true }).click()
    await page.getByRole('button', { name: /^Transferler \(\d+ depoya yolda\)$/ }).click()
    const list = page.getByRole('region', { name: 'Transferler', exact: true })
    await list.getByRole('button', { name: 'Teslim al', exact: true }).first().click()
    const form = list.getByRole('form', { name: 'Teslim al' })
    await form.getByLabel(/gelen miktar$/).first().fill('2')
    await form.getByRole('button', { name: 'Teslim al', exact: true }).click()
    await list.getByText('Mağaza 1 transferi teslim alındı; gelen miktar merkez deposuna girdi.').waitFor()
    await page.screenshot({ path: `${out}/headquarters-6-transfer.png` })
    check(true, 'mağazadan gelen transfer merkez deposunda teslim alındı')
    await list.getByRole('button', { name: 'Kapat', exact: true }).click()
    await page.getByRole('button', { name: 'Transfer gönder', exact: true }).click()
    const send = page.getByRole('region', { name: 'Transfer gönder (merkez deposundan)' })
    await send.getByLabel('Gönderilecek mağaza').selectOption({ label: 'Mağaza 1' })
    await send.getByLabel('Ürün ekle').fill('2990000000995')
    await send.getByRole('button', { name: 'Bul' }).click()
    await send.getByLabel(/miktarı$/).fill('3')
    await send.getByRole('button', { name: 'Gönder', exact: true }).click()
    await send.getByText(/^Transfer Mağaza 1 için gönderildi/).waitFor()
    check(true, 'merkez deposundan Mağaza 1\'e 3 adet transfer gönderildi')
    await page.getByRole('button', { name: 'Raporlar', exact: true }).click()
  }

  // Users (administrator only): a report user sees only the stores
  // given to them, cannot change the catalog, cannot see the users.
  const reportUser = `report-${Date.now().toString(36)}`
  const reportName = `Rapor Kullanıcısı ${reportUser.slice(-5)}`
  const reportPassword = 'report-test-1'
  await page.getByRole('button', { name: 'Kullanıcılar', exact: true }).click()
  await page.getByRole('heading', { name: 'Kullanıcılar', exact: true, level: 2 }).waitFor()
  const userRow = (n) => page.getByRole('table', { name: 'Kullanıcı listesi' }).getByRole('row').filter({ has: page.getByRole('button', { name: n, exact: true }) })
  await userRow(me.name).getByRole('button', { name: me.name, exact: true }).click()
  const own = page.getByRole('region', { name: `Kullanıcı: ${me.name}` })
  await own.getByTestId('account').filter({ hasText: 'Kendi hesabınızı kapatamazsınız' }).waitFor()
  check(await own.getByRole('combobox', { name: /^Rol/ }).isDisabled(), 'yönetici kendi hesabını kapatamıyor, kendi rolünü değiştiremiyor')
  await own.getByRole('button', { name: 'Kapat', exact: true }).click()
  await page.getByRole('button', { name: 'Yeni kullanıcı' }).click()
  const newUser = page.getByRole('region', { name: 'Yeni kullanıcı' })
  await newUser.getByLabel('Kullanıcı adı', { exact: true }).fill(reportUser)
  await newUser.getByLabel('Ad', { exact: true }).fill(reportName)
  await newUser.getByRole('combobox', { name: /^Rol/ }).selectOption('report')
  await newUser.getByRole('radio', { name: 'Seçilen mağazalar' }).check()
  await newUser.getByLabel('Parola (en az 10 karakter)', { exact: true }).fill(reportPassword)
  await newUser.getByLabel('Parola (tekrar)', { exact: true }).fill(reportPassword)
  await newUser.getByRole('button', { name: 'Ekle', exact: true }).click()
  await newUser.getByRole('alert').filter({ hasText: 'mağaza seçilmeli' }).waitFor()
  check(true, 'mağaza seçilmeden rapor kullanıcısı eklenmedi')
  const firstName = first.name || first.id
  await newUser.getByRole('group', { name: 'Seçilen mağazalar' }).getByLabel(firstName, { exact: true }).check()
  await newUser.getByRole('button', { name: 'Ekle', exact: true }).click()
  const added = page.getByRole('region', { name: `Kullanıcı: ${reportName}` })
  await added.getByText('Kullanıcı eklendi.').waitFor()
  await userRow(reportName).getByText(firstName, { exact: true }).waitFor()
  check(true, `rapor kullanıcısı eklendi; yalnızca ${firstName}`)
  await page.screenshot({ path: `${out}/headquarters-4-kullanici.png`, fullPage: true })
  await added.getByRole('button', { name: 'Kapat', exact: true }).click()

  // The corporate identity section and the user's email address: since the server has no OIDC
  // the section's endpoints (/api/auth/oidc*) are imitated; the address is
  // written to the real user record.
  let linked = false
  await page.route('**/api/auth/oidc', (route) => route.fulfill({ json: { linked } }))
  await page.route('**/api/auth/oidc/unlink', (route) => {
    linked = false
    return route.fulfill({ status: 204 })
  })
  await page.reload()
  await page.getByRole('button', { name: 'Kurumsal kimlik', exact: true }).click()
  await page.getByTestId('oidc-state').filter({ hasText: 'bağlı değil' }).waitFor()
  check(
    (await page.getByRole('link', { name: 'Kurumsal kimliği bağla' }).getAttribute('href')) === '/api/auth/oidc/link',
    'kurumsal kimlik bölümü: bağlı değil, bağlama bağlantısı var',
  )
  linked = true
  await page.reload()
  await page.getByRole('button', { name: 'Kurumsal kimlik', exact: true }).click()
  await page.getByTestId('oidc-state').filter({ hasText: 'bağlı:' }).waitFor()
  await page.getByRole('button', { name: 'Bağı kaldır' }).click()
  await page.getByTestId('oidc-state').filter({ hasText: 'bağlı değil' }).waitFor()
  check(true, 'kurumsal kimlik bölümü: bağ kaldırıldı')
  await page.getByRole('button', { name: 'Kullanıcılar', exact: true }).click()
  await userRow(reportName).getByRole('button', { name: reportName, exact: true }).click()
  const emailForm = page.getByRole('region', { name: `Kullanıcı: ${reportName}` })
  const email = `${reportUser}@firma.example`
  await emailForm.getByLabel('E-posta (kurumsal giriş)').fill(email)
  await emailForm.getByRole('button', { name: 'Kaydet', exact: true }).click()
  await emailForm.getByText('Kaydedildi; hemen geçerli.').waitFor()
  await emailForm.getByTestId('identity').filter({ hasText: 'ilk kurumsal girişinde e-posta adresiyle tanınır' }).waitFor()
  check((await api(`/api/users/${reportUser}`)).user.email === email, 'kullanıcının e-posta adresi kaydedildi')
  // The browser itself stops an invalid address (the email field); the server rejection
  // Go testlerindedir.
  await emailForm.getByLabel('E-posta (kurumsal giriş)').fill('adres-degil')
  await emailForm.getByRole('button', { name: 'Kaydet', exact: true }).click()
  check(
    (await emailForm.getByLabel('E-posta (kurumsal giriş)').evaluate((el) => !el.validity.valid)) &&
      (await api(`/api/users/${reportUser}`)).user.email === email,
    'geçersiz e-posta adresi gönderilmedi, eskisi kaldı',
  )
  await emailForm.getByLabel('E-posta (kurumsal giriş)').fill('')
  await emailForm.getByRole('button', { name: 'Kaydet', exact: true }).click()
  await emailForm.getByText('Kaydedildi; hemen geçerli.').waitFor()
  check((await api(`/api/users/${reportUser}`)).user.email === '', 'e-posta adresi kaldırıldı')
  await emailForm.getByRole('button', { name: 'Kapat', exact: true }).click()
  await page.unroute('**/api/auth/oidc')
  await page.unroute('**/api/auth/oidc/unlink')
  await page.reload()
  await page.getByRole('button', { name: 'Çıkış' }).click()

  await page.getByLabel('Kullanıcı adı').fill(reportUser)
  await page.getByLabel('Parola').fill(reportPassword)
  await page.getByRole('button', { name: 'Giriş' }).click()
  await page.getByTestId('user').filter({ hasText: `${reportName} · Rapor` }).waitFor()
  check((await page.getByRole('button', { name: 'Kullanıcılar', exact: true }).count()) === 0, 'rapor kullanıcısı girdi; Kullanıcılar bölümü yok')
  const seen = await api('/api/stores')
  check(seen.stores.length === 1 && seen.stores[0].id === first.id, `rapor kullanıcısı yalnızca kendi mağazasını görüyor (${seen.stores.map((s) => s.id)})`)
  const forbidden = [
    await page.request.get(`${url}/api/reports/summary?store=baska-store`),
    await page.request.get(`${url}/api/users`),
    await page.request.post(`${url}/api/catalog/products/${code}/prices`, { data: { version: 99, unit_price_kurus: 1 }, headers: { 'Content-Type': 'application/json' } }),
  ].map((r) => r.status())
  check(forbidden.every((s) => s === 403), `başka mağazanın raporu, kullanıcılar ve fiyat değişikliği reddedildi (${forbidden})`)
  await page.getByRole('button', { name: 'Ürünler', exact: true }).click()
  await page.getByTestId('read-only').waitFor()
  await page.getByRole('button', { name, exact: true }).click()
  const readOnly = page.getByRole('region', { name: `Ürün: ${name}` })
  await readOnly.getByTestId('current-price').waitFor()
  check(
    (await page.getByRole('button', { name: 'Yeni ürün' }).count()) === 0 &&
      (await readOnly.getByRole('button', { name: 'Fiyatı kaydet' }).count()) === 0 &&
      (await readOnly.getByLabel('Ad', { exact: true }).isDisabled()),
    'rapor kullanıcısı kataloğu yalnızca okuyor',
  )
  // The person changes their own password; signs in with the new password.
  const newPassword = 'report-test-2'
  await page.getByRole('button', { name: 'Parolam', exact: true }).click()
  await page.getByLabel('Şu anki parola', { exact: true }).fill('yanlış-parola')
  await page.getByLabel('Yeni parola', { exact: true }).fill(newPassword)
  await page.getByLabel('Yeni parola (tekrar)', { exact: true }).fill(newPassword)
  await page.getByRole('button', { name: 'Parolamı değiştir' }).click()
  await page.getByRole('alert').filter({ hasText: 'Şu anki parola yanlış' }).waitFor()
  await page.getByLabel('Şu anki parola', { exact: true }).fill(reportPassword)
  await page.getByRole('button', { name: 'Parolamı değiştir' }).click()
  await page.getByText('Parolanız değişti').waitFor()
  check((await page.request.get(`${url}/api/auth/session`)).status() === 200, 'kendi parolasını değiştirdi (yanlış şu anki parola reddedildi); oturumu açık kaldı')
  await page.getByRole('button', { name: 'Çıkış' }).click()
  await page.getByLabel('Kullanıcı adı').fill(reportUser)
  await page.getByLabel('Parola').fill(newPassword)
  await page.getByRole('button', { name: 'Giriş' }).click()
  await page.getByTestId('user').filter({ hasText: reportName }).waitFor()
  check(true, 'yeni parolayla giriş yapıldı')
  await page.getByRole('button', { name: 'Çıkış' }).click()

  // The administrator closes the report user's account; the administrator's name in the record.
  await page.getByLabel('Kullanıcı adı').fill(username)
  await page.getByLabel('Parola').fill(password)
  await page.getByRole('button', { name: 'Giriş' }).click()
  await page.getByRole('button', { name: 'Kullanıcılar', exact: true }).click()
  await userRow(reportName).getByRole('button', { name: reportName, exact: true }).click()
  await added.getByRole('button', { name: 'Hesabı kapat' }).click()
  await added.getByTestId('account').filter({ hasText: 'Hesap kapalı' }).waitFor()
  const reportDetail = await api(`/api/users/${reportUser}`)
  check(
    reportDetail.user.disabled && reportDetail.history.length === 5 && reportDetail.history[0].by === me.name &&
      reportDetail.history[1].by === reportName && reportDetail.history[1].detail === 'parola değişti (kendisi)',
    `rapor kullanıcısının hesabı kapatıldı; kayıtlar: ${reportDetail.history.map((c) => `${c.detail} (${c.by})`).join(', ')}`,
  )
  await added.getByRole('button', { name: 'Kapat', exact: true }).click()
  await page.getByRole('button', { name: 'Raporlar', exact: true }).click()
  await page.getByRole('heading', { name: 'Mağazalar', exact: true, level: 2 }).waitFor()

  await page.setViewportSize({ width: 390, height: 844 })
  const scroll = await page.evaluate(() => document.documentElement.scrollWidth)
  check(scroll <= 390, `telefon genişliğinde yatay kaydırma yok (${scroll} px)`)
  await page.screenshot({ path: `${out}/headquarters-2-telefon.png`, fullPage: true })
  await page.setViewportSize({ width: 1280, height: 900 })

  await page.getByRole('button', { name: 'Çıkış' }).click()
  await page.getByLabel('Parola').waitFor()
  const after = await page.request.get(`${url}/api/stores`)
  check(after.status() === 401, 'çıkıştan sonra giriş ekranı geldi, API kapandı')
  check(errors.length === 0, `tarayıcıda hata yok${errors.length ? ': ' + errors.join('; ') : ''}`)
} catch (e) {
  await page.screenshot({ path: `${out}/headquarters-bug.png`, fullPage: true }).catch(() => {})
  console.error(`✗ ${e.message}`)
  process.exitCode = 1
} finally {
  await browser.close()
}

function addDays(date, n) {
  const d = new Date(`${date}T12:00:00Z`)
  d.setUTCDate(d.getUTCDate() + n)
  return d.toISOString().slice(0, 10)
}

// waitCount waits until the number of elements the locator matches is n.
async function waitCount(locator, n, timeout = 10000) {
  const deadline = Date.now() + timeout
  while ((await locator.count()) !== n) {
    if (Date.now() > deadline) throw new Error(`${n} öğe beklendi, ${await locator.count()} var`)
    await new Promise((r) => setTimeout(r, 100))
  }
}
