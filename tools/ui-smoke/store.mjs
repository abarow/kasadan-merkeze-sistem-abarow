// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// store opens the store server screen (store reports) in a real browser
// and takes screenshots. The store simulation calls it after running register-sim on two
// registers (scripts/store-sim.sh):
//
//   docker compose -f deploy/compose/store.yaml --profile ui-test run --rm ui-smoke
//
// The images are written under tools/ui-smoke/out/.

import { chromium } from 'playwright'

const url = process.env.ABAROW_UI_URL ?? 'http://report:8080'
const out = process.env.ABAROW_UI_OUT ?? 'out'
const password = process.env.ABAROW_REPORT_PASSWORD ?? ''

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
// loading messages are not counted as errors, the application's own errors are.
page.on('console', (m) => m.type() === 'error' && !m.text().startsWith('Failed to load resource') && errors.push(m.text()))

function check(cond, msg) {
  if (!cond) throw new Error(msg)
  console.log(`✓ ${msg}`)
}

const tl = new Intl.NumberFormat('tr-TR', { style: 'currency', currency: 'TRY' })

// The trial manager (code:PIN; see scripts/store-sim.sh). The test adds them to the back
// house; since the store password is off while there is a manager with an open account,
// when the simulation runs again in the same environment this manager is used to sign in.
const [simID, simPIN] = (process.env.ABAROW_REPORT_MANAGER ?? 'sim-manager:86420').split(':')
const simName = 'Simülasyon Müdürü'

async function loginWithPassword(pw) {
  await page.getByLabel('Parola', { exact: true }).fill(pw)
  await page.getByRole('button', { name: 'Giriş' }).click()
}

// loginManager signs in with the sign-in screen's manager form; the manager's name
// must show at the top.
async function loginManager(id, pin, name) {
  const form = page.getByRole('form', { name: 'Müdür girişi' })
  await form.getByLabel('Müdür kodu').fill(id)
  await form.getByLabel('PIN', { exact: true }).fill(pin)
  await form.getByRole('button', { name: 'Giriş' }).click()
  if (name) await page.getByTestId('who').filter({ hasText: name }).waitFor()
}

async function logout() {
  await page.getByRole('button', { name: 'Çıkış' }).click()
  await page.getByRole('button', { name: 'Giriş' }).waitFor()
}

// stockQty is the quantity in the product's row of the stock list (while searching filters
// the list may show all the products for a moment).
const stockQty = (name) =>
  page.getByRole('row').filter({ has: page.getByRole('button', { name, exact: true }) }).getByTestId('stock-qty')

// waitFor tries every half second until the condition is met (at most 60 s);
// the events come over NATS.
async function waitFor(cond) {
  for (let i = 0; i < 120; i++) {
    if (await cond()) return true
    await page.waitForTimeout(500)
  }
  return false
}

// The transfer headquarters sent (the headquarters test sends it; store-sim
// then runs this step separately): when it comes it is received, enters stock.
if (process.env.ABAROW_SMOKE_STEP === 'transfer') {
  try {
    await page.goto(url)
    const methods = await (await page.request.get(`${url}/api/auth/methods`)).json()
    if (methods.methods[0].id === 'password') await loginWithPassword(password)
    else await loginManager(simID, simPIN, simName)
    await page.getByRole('heading', { name: 'Kasa durumu', level: 2 }).waitFor()
    const arrived = await waitFor(async () =>
      (await (await page.request.get(`${url}/api/stock/transfers`)).json()).transfers.some((t) => t.pending && t.from === 'headquarters'),
    )
    check(arrived, 'merkezin gönderdiği transfer mağazaya indi (yolda)')
    await page.getByRole('button', { name: 'Stok', exact: true }).click()
    await page.getByRole('button', { name: /^Transferler \(\d+ yolda\)$/ }).click()
    const list = page.getByRole('region', { name: 'Transferler', exact: true })
    await list.getByRole('button', { name: 'Teslim al', exact: true }).first().click()
    const form = list.getByRole('form', { name: 'Teslim al' })
    await form.getByLabel(/gelen miktar$/).first().fill('3')
    await form.getByRole('button', { name: 'Teslim al', exact: true }).click()
    await list.getByText('Merkez deposu transferi teslim alındı; gelen miktar stoğa girdi.').waitFor()
    await page.screenshot({ path: `${out}/store-stok-delivery-al.png` })
    await list.getByRole('button', { name: 'Kapat', exact: true }).click()
    await page.getByRole('searchbox', { name: 'Ara' }).fill('2990000000995')
    await stockQty('Mağaza Fiyatlı Deneme Ürünü').filter({ hasText: /^8 adet$/ }).waitFor()
    check(true, 'merkezden gelen 3 adet teslim alındı; stok 8 adet')
    check(errors.length === 0, `tarayıcıda hata yok${errors.length ? ': ' + errors.join('; ') : ''}`)
  } catch (e) {
    await page.screenshot({ path: `${out}/store-bug.png`, fullPage: true }).catch(() => {})
    console.error(`✗ ${e.message}`)
    process.exitCode = 1
  } finally {
    await browser.close()
  }
  process.exit()
}

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
  await page.unroute('**/api/auth/methods')
  await page.goto(url)
  const methods = await (await page.request.get(`${url}/api/auth/methods`)).json()
  if (methods.methods[0].id === 'password') {
    // While there is no manager (first install) the sign-in is with the store password.
    await loginWithPassword('yanlış-parola')
    await page.getByRole('alert').filter({ hasText: 'parola yanlış' }).waitFor()
    check(true, 'yanlış parola reddedildi')
    await page.screenshot({ path: `${out}/store-0-login.png` })
    await loginWithPassword(password)
    await page.getByTestId('who').filter({ hasText: 'Mağaza parolasıyla girildi' }).waitFor()
    check(true, 'mağaza parolasıyla giriş yapıldı; üstte "Mağaza parolasıyla girildi" yazıyor')
  } else {
    // While there is a manager with an open account the store password is off.
    await page.getByTestId('password-closed').waitFor()
    const closed = await page.request.post(`${url}/api/auth/login`, { data: { password } })
    check(
      closed.status() === 403 && (await page.getByRole('button', { name: 'Mağaza parolasıyla gir' }).count()) === 0,
      'müdür var: mağaza parolası kapalı (403), giriş ekranında yalnızca müdür girişi',
    )
    await loginManager(simID, simPIN, simName)
    check(true, `deneme müdürüyle giriş yapıldı (${simID})`)
  }
  await page.getByRole('heading', { name: 'Kasa durumu', level: 2 }).waitFor()
  const who = await (await page.request.get(`${url}/api/auth/session`)).json()
  // The corporate identity section (endpoints imitated): whoever signs in with the store password has no personal
  // account, cannot link an identity; the manager sees they are not linked.
  await page.route('**/api/auth/oidc', (route) => route.fulfill({ json: { linked: false } }))
  await page.reload()
  await page.getByRole('button', { name: 'Kurumsal kimlik', exact: true }).click()
  if (who.username === '') {
    await page.getByText('Mağaza parolasıyla girdiniz').waitFor()
    check((await page.getByRole('link', { name: 'Kurumsal kimliği bağla' }).count()) === 0, 'mağaza parolasıyla girene kurumsal kimlik bağlama sunulmuyor')
  } else {
    await page.getByTestId('oidc-state').filter({ hasText: 'bağlı değil' }).waitFor()
    check(
      (await page.getByRole('link', { name: 'Kurumsal kimliği bağla' }).getAttribute('href')) === '/api/auth/oidc/link',
      'müdüre kurumsal kimliği bağlama bağlantısı sunuluyor',
    )
  }
  await page.unroute('**/api/auth/oidc')
  await page.reload()
  await page.getByRole('heading', { name: 'Kasa durumu', level: 2 }).waitFor()
  // The registers are shown in the API order and with their states.
  const registers = (await (await page.request.get(`${url}/api/registers`)).json()).registers
  check(registers.length > 0, `${registers.length} kasa listelendi`)
  for (const r of registers) {
    const card = page.getByRole('listitem', { name: r.name })
    await card.getByText('Bağlı', { exact: true }).waitFor()
    await card.getByTestId('receipts').filter({ hasText: new RegExp(`^${r.receipt_count}$`) }).waitFor()
  }
  check(true, 'kasalar bağlı ve gelen fiş sayıları gösteriliyor')
  for (const r of registers.filter((r) => r.last_closed_shift)) {
    await page.getByRole('listitem', { name: r.name }).getByTestId('last-shift').filter({ hasText: r.last_closed_shift.cashier_name }).waitFor()
  }
  check(true, 'son kapanan vardiyalar kasa farkıyla gösteriliyor')
  await page.getByText(/^ · son no \d+$/).first().waitFor()
  check(true, 'son fiş numarası gösteriliyor')
  check((await page.getByText('Eksik fiş').count()) === 0, 'eksik fiş uyarısı yok')

  // The daily report shows the same total as the API.
  const day = await (await page.request.get(`${url}/api/reports/summary`)).json()
  const total = tl.format(day.total_kurus / 100)
  await page.getByTestId('day-total').filter({ hasText: total }).waitFor()
  check(true, `günlük satış ${total}, ${day.receipt_count} fiş`)
  await page.getByRole('heading', { name: 'Kasa durumu', level: 2 }).waitFor()
  for (const name of ['Ödemeler', 'KDV', 'Kasalar', 'Kasiyerler', 'Saatlik', 'Ürünler']) {
    await page.getByRole('heading', { name, exact: true, level: 3 }).waitFor()
  }
  check(true, 'ödeme, KDV, kasa, kasiyer, saat ve ürün tabloları gösteriliyor')
  await page.getByText('mali değeri yoktur').waitFor()
  check(true, 'mali değeri olmadığı yazıyor')
  await page.screenshot({ path: `${out}/store-1-report.png`, fullPage: true })

  // A past day with no sales.
  await page.getByLabel('Gün', { exact: true }).fill('2020-01-01')
  await page.getByLabel('Gün', { exact: true }).dispatchEvent('change')
  await page.getByText('Bu gün satış yok.').waitFor()
  check(true, 'geçmiş gün seçilince o günün raporu (satış yok) gösterildi')
  await page.getByRole('button', { name: 'Bugün' }).click()
  await page.getByTestId('day-total').filter({ hasText: total }).waitFor()
  check(true, 'bugüne dönüldü')

  // Products: read-only while headquarters counts as connected; when the connection is closed
  // they are managed at the store (adding a product and its coming down to the register is tried in register-sim).
  const officeResp = await page.request.get(`${url}/api/settings/headquarters`)
  const office = await officeResp.json()
  await page.getByRole('button', { name: 'Ürünler', exact: true }).click()
  await page.getByTestId('office-state').waitFor()
  const newProduct = page.getByRole('button', { name: 'Yeni ürün', exact: true })
  if (!office.installed) {
    await page.getByTestId('office-state').filter({ hasText: "merkeze bağlı değil" }).waitFor()
    await newProduct.waitFor()
    check((await page.getByTestId('office-toggle').count()) === 0, 'merkezsiz mağaza: ürünler yazılabilir, bağlantı düğmesi yok')
  } else {
    check(office.connected, 'merkezli mağaza başlangıçta merkez bağlı sayılıyor')
    await page.getByTestId('office-state').filter({ hasText: 'bağlı' }).waitFor()
    await page.getByTestId('read-only').filter({ hasText: 'merkezden (merkez) yönetiliyor' }).waitFor()
    check((await newProduct.count()) === 0, 'bağlıyken "Yeni ürün" yok; katalog salt okunur')
    await page.screenshot({ path: `${out}/store-urunler-bagli.png`, fullPage: true })

    // Close the connection: it asks for confirmation, then they are managed at the store.
    await page.getByTestId('office-toggle').click()
    await page.getByTestId('office-warning').filter({ hasText: 'merkezin kataloğu bu mağazaya alınmaz' }).waitFor()
    await page.getByRole('button', { name: 'Vazgeç' }).click()
    check((await page.getByTestId('office-confirm').count()) === 0, 'vazgeçilince ayar değişmedi')
    await page.getByTestId('office-toggle').click()
    await page.getByTestId('office-confirm').click()
    await page.getByTestId('office-state').filter({ hasText: 'kopuk' }).waitFor()
    await newProduct.waitFor()
    check((await (await page.request.get(`${url}/api/settings/headquarters`)).json()).connected === false, 'bağlantı kapatıldı: ürünler mağazada yönetilir')
    await page.screenshot({ path: `${out}/store-urunler-kopuk.png`, fullPage: true })

    // Reconnect: read-only.
    await page.getByTestId('office-toggle').click()
    await page.getByTestId('office-confirm').click()
    await page.getByTestId('office-state').filter({ hasText: 'bağlı' }).waitFor()
    await page.getByTestId('read-only').waitFor()
    check((await newProduct.count()) === 0, 'yeniden bağlanınca katalog yine salt okunur')
  }
  await page.getByRole('button', { name: 'Raporlar', exact: true }).click()

  // Store prices (if the store server is connected to headquarters).
  const pricesResp = await page.request.get(`${url}/api/prices`)
  if (pricesResp.status() === 200) {
    await page.getByRole('button', { name: 'Fiyatlar', exact: true }).click()
    await page.getByRole('heading', { name: 'Fiyatlar', exact: true, level: 2 }).waitFor()
    const products = (await pricesResp.json()).products
    check(products.length > 0, `merkezin kataloğundan ${products.length} ürün`)
    // register-sim gave this product a store price (see scripts/store-sim.sh).
    const p = products.find((x) => x.store_price_kurus !== undefined)
    check(p !== undefined, `mağaza fiyatlı ürün: ${p?.name}`)
    const row = page.getByRole('row').filter({ hasText: p.name })
    await row.getByTestId('store-price').filter({ hasText: tl.format(p.store_price_kurus / 100) }).waitFor()
    check(true, `listede mağaza fiyatı ${tl.format(p.store_price_kurus / 100)}, merkez fiyatı ${tl.format(p.headquarters_price_kurus / 100)}`)

    await row.getByRole('button', { name: p.name, exact: true }).click()
    const editor = page.getByRole('region', { name: `Fiyat: ${p.name}` })
    await editor.getByTestId('current-store-price').filter({ hasText: tl.format(p.store_price_kurus / 100) }).waitFor()
    await editor.getByLabel('Yeni mağaza fiyatı', { exact: true }).fill('4,25')
    await editor.getByLabel('İleri tarihte', { exact: true }).check()
    // A past moment is rejected (the price does not change right away by mistake).
    await editor.getByLabel('Tarih ve saat', { exact: true }).fill(localDate(-2))
    await editor.getByRole('button', { name: 'Fiyatı kaydet' }).click()
    await editor.getByRole('alert').filter({ hasText: 'geçmişte' }).waitFor()
    await editor.getByTestId('current-store-price').filter({ hasText: tl.format(p.store_price_kurus / 100) }).waitFor()
    check(true, 'geçmiş tarihli "ileri tarihli fiyat" reddedildi')
    await editor.getByLabel('Tarih ve saat', { exact: true }).fill(localDate(2))
    await editor.getByRole('button', { name: 'Fiyatı kaydet' }).click()
    await editor.getByText('İleri tarihli mağaza fiyatı kaydedildi.').waitFor()
    await editor.getByText(`${tl.format(4.25)} · `).waitFor()
    check(true, 'ileri tarihli mağaza fiyatı kaydedildi')
    await page.screenshot({ path: `${out}/store-3-price.png`, fullPage: true })
    await editor.getByRole('button', { name: 'Geri al' }).click()
    await editor.getByText('İleri tarihli kayıt geri alındı.').waitFor()
    await editor.getByRole('button', { name: 'Merkez fiyatına dön' }).click()
    await editor.getByText('Mağaza fiyatı kaldırıldı; merkez fiyatı geçerli.').waitFor()
    await editor.getByTestId('current-store-price').filter({ hasText: 'yok' }).waitFor()
    check(true, 'merkez fiyatına dönüldü')
    // The store price is given again: it shows in headquarters' "store prices" report.
    await editor.getByLabel('Yeni mağaza fiyatı', { exact: true }).fill(String(p.store_price_kurus / 100).replace('.', ','))
    await editor.getByRole('button', { name: 'Fiyatı kaydet' }).click()
    await editor.getByTestId('current-store-price').filter({ hasText: tl.format(p.store_price_kurus / 100) }).waitFor()
    const detail = await (await page.request.get(`${url}/api/prices/${p.id}`)).json()
    check(detail.history.length >= 5 && detail.history[0].by === who.name, `değişiklik geçmişi: ${detail.history.length} kayıt; kayıtta "${who.name}"`)
    await editor.getByRole('button', { name: 'Kapat', exact: true }).click()
    await page.getByRole('button', { name: 'Raporlar', exact: true }).click()
    await page.getByRole('heading', { name: 'Kasa durumu', level: 2 }).waitFor()
  }

  // Stock (if the inventory module is installed): goods receipt and count from the screen.
  const stockBarcode = '2990000000995' // merkezin deneme ürünü (bkz. scripts/store-sim.sh)
  if ((await page.request.get(`${url}/api/stock?q=${stockBarcode}`)).status() === 200) {
    await page.getByRole('button', { name: 'Stok', exact: true }).click()
    await page.getByRole('heading', { name: 'Stok', exact: true, level: 2 }).waitFor()
    await page.getByRole('button', { name: 'Mal kabul', exact: true }).click()
    const receipt = page.getByRole('region', { name: 'Mal kabul' })
    await receipt.getByLabel('Ürün ekle').fill(stockBarcode)
    await receipt.getByRole('button', { name: 'Bul' }).click()
    await receipt.getByLabel(/miktarı$/).fill('12')
    await receipt.getByLabel('Tedarikçi').fill('Ekran Testi Tedarik')
    const irs = `EKRAN-${Date.now()}`
    await receipt.getByLabel('İrsaliye no').fill(irs)
    await receipt.getByRole('button', { name: 'Stoğa gir' }).click()
    await receipt.getByText(`İrsaliye ${irs} stoğa girildi.`).waitFor()
    await page.screenshot({ path: `${out}/store-stok-mal-kabul.png` })
    check(true, `mal kabul ekrandan girildi (${irs})`)
    await receipt.getByRole('button', { name: 'Kapat', exact: true }).click()
    await page.getByRole('button', { name: 'Sayım', exact: true }).click()
    const count = page.getByRole('region', { name: 'Sayım' })
    await count.getByLabel('Ürün ekle').fill(stockBarcode)
    await count.getByRole('button', { name: 'Bul' }).click()
    await count.getByLabel(/miktarı$/).fill('7')
    await count.getByRole('button', { name: 'Sayımı işle' }).click()
    await count.getByText('Sayım işlendi; farklar stoğa yazıldı.').waitFor()
    await count.getByRole('button', { name: 'Kapat', exact: true }).click()
    await page.getByRole('searchbox', { name: 'Ara' }).fill(stockBarcode)
    await stockQty('Mağaza Fiyatlı Deneme Ürünü').filter({ hasText: /^7 adet$/ }).waitFor()
    check(true, 'sayım stoğu 7 adede düzeltti')
    await page.getByRole('button', { name: 'Mağaza Fiyatlı Deneme Ürünü', exact: true }).click()
    const history = page.getByRole('region', { name: /^Hareketler: / })
    await history.getByRole('cell', { name: 'Sayım', exact: true }).first().waitFor()
    await history.getByRole('cell', { name: 'Mal kabul', exact: true }).first().waitFor()
    await page.screenshot({ path: `${out}/store-stok-hareketler.png` })
    check(true, 'ürünün hareketlerinde mal kabul ve sayım görünüyor')
    await history.getByRole('button', { name: 'Kapat', exact: true }).click()

    // Store server connected to headquarters: a transfer to the headquarters warehouse (the headquarters test
    // takes delivery). The place list comes from headquarters.
    if (await waitFor(async () => (await (await page.request.get(`${url}/api/stock/locations`)).json()).locations.some((l) => l.id === 'headquarters'))) {
      await page.getByRole('button', { name: 'Raporlar', exact: true }).click()
      await page.getByRole('button', { name: 'Stok', exact: true }).click()
      await page.getByRole('button', { name: 'Transfer gönder', exact: true }).click()
      const send = page.getByRole('region', { name: 'Transfer gönder' })
      await send.getByLabel('Gönderilecek yer').selectOption({ label: 'Merkez deposu' })
      await send.getByLabel('Ürün ekle').fill(stockBarcode)
      await send.getByRole('button', { name: 'Bul' }).click()
      await send.getByLabel(/miktarı$/).fill('2')
      await send.getByRole('button', { name: 'Gönder', exact: true }).click()
      await send.getByText(/^Transfer Merkez deposu için gönderildi/).waitFor()
      await send.getByRole('button', { name: 'Kapat', exact: true }).click()
      await page.getByRole('searchbox', { name: 'Ara' }).fill(stockBarcode)
      await stockQty('Mağaza Fiyatlı Deneme Ürünü').filter({ hasText: /^5 adet$/ }).waitFor()
      await page.screenshot({ path: `${out}/store-stok-transfer.png` })
      check(true, 'merkez deposuna 2 adet transfer gönderildi; stok 5 adede düştü')
    }
  }

  // Cashiers (if the store server's store id is defined).
  const cashiersResp = await page.request.get(`${url}/api/cashiers`)
  if (cashiersResp.status() === 200) {
    await page.getByRole('button', { name: 'Kasiyerler', exact: true }).click()
    await page.getByRole('heading', { name: 'Kasiyerler', exact: true, level: 2 }).waitFor()
    const rows = () => page.getByRole('table', { name: 'Kasiyer listesi' }).getByRole('row')
    // register-sim adds a cashier and closes their account (see scripts/store-sim.sh);
    // the list must show the state in the API.
    const registerSim = (await cashiersResp.json()).cashiers.find((c) => c.id.startsWith('store-'))
    if (registerSim) {
      const row = rows().filter({ hasText: registerSim.name })
      await row.waitFor()
      if (registerSim.disabled) await row.getByText('hesap kapalı').waitFor()
      check(true, `kasa simülasyonunun kasiyeri listede${registerSim.disabled ? ', hesabı kapalı' : ''}: ${registerSim.name}`)
    }
    const id = `ui-${Date.now().toString(36)}`
    await page.getByRole('button', { name: 'Yeni kasiyer' }).click()
    const form = page.getByRole('region', { name: 'Yeni kasiyer' })
    await form.getByLabel('Kod', { exact: true }).fill(id)
    await form.getByLabel('Ad', { exact: true }).fill(`Ekran Kasiyeri ${id}`)
    await form.getByLabel('PIN (4–8 hane)', { exact: true }).fill('2468')
    await form.getByLabel('PIN (tekrar)', { exact: true }).fill('2469')
    await form.getByRole('button', { name: 'Ekle' }).click()
    await form.getByRole('alert').filter({ hasText: "PIN'ler aynı değil" }).waitFor()
    check(true, 'iki kez yazılan PIN farklıyken kasiyer eklenmedi')
    await form.getByLabel('PIN (tekrar)', { exact: true }).fill('2468')
    await form.getByRole('button', { name: 'Ekle' }).click()
    // The added cashier's page opens; when the name changes the region's name changes too.
    const editor = page.getByRole('region', { name: /^Kasiyer: / })
    await editor.getByText('Kasiyer eklendi; kasalara iniyor.').waitFor()
    await rows().filter({ hasText: `Ekran Kasiyeri ${id}` }).waitFor()
    check(true, `kasiyer eklendi (${id})`)
    // The manager's row is found from the name button: the manager's name also appears in the
    // row (last change) of the cashiers they changed.
    const managerName = `Ekran Müdürü ${id}`
    const managerRow = () => rows().filter({ has: page.getByRole('button', { name: managerName, exact: true }) })
    if (!who.username) {
      await page.getByTestId('first-manager').waitFor()
      check(true, 'mağaza parolasıyla girilince ilk müdürle parolanın kapanacağı yazıyor')
    }
    await editor.getByLabel('Ad', { exact: true }).fill(managerName)
    await editor.getByRole('combobox', { name: /^Rol/ }).selectOption('manager')
    await editor.getByRole('button', { name: 'Kaydet' }).click()
    if (!who.username) {
      // When the first manager is saved the store password is off: the session closes, only the manager
      // sign-in remains on the sign-in screen.
      await page.getByTestId('password-closed').waitFor()
      const closed = await page.request.post(`${url}/api/auth/login`, { data: { password } })
      check(closed.status() === 403, 'ilk müdür kaydedildi: mağaza parolası kapandı, ekran müdür girişine döndü')
    } else {
      await editor.getByText('Kaydedildi; kasalara iniyor.').waitFor()
      await managerRow().getByText('Müdür', { exact: true }).waitFor()
      check(true, 'ad ve rol değişti (müdür)')
      await editor.getByRole('button', { name: 'Kapat', exact: true }).click()
      await logout()
    }

    // The manager signs in to the screen with their own code and PIN; their name is written to the changes.
    await loginManager(id, '24680')
    await page.getByRole('form', { name: 'Müdür girişi' }).getByRole('alert').filter({ hasText: 'Kod ya da PIN yanlış' }).waitFor()
    check(true, 'yanlış PIN reddedildi')
    await page.screenshot({ path: `${out}/store-5-mudur-login.png` })
    await loginManager(id, '2468', managerName)
    check(true, `müdür PIN'iyle giriş yapıldı; üstte adı yazıyor (${managerName})`)
    await page.getByRole('button', { name: 'Kasiyerler', exact: true }).click()
    await managerRow().getByRole('button', { name: managerName, exact: true }).click()
    const own = page.getByRole('region', { name: `Kasiyer: ${managerName}` })
    await own.getByTestId('account').filter({ hasText: 'Kendi hesabınızı kapatamazsınız' }).waitFor()
    check(
      (await own.getByRole('button', { name: 'Hesabı kapat' }).count()) === 0 && (await own.getByRole('combobox', { name: /^Rol/ }).isDisabled()),
      'müdür kendi hesabını kapatamıyor, kendi rolünü değiştiremiyor',
    )
    // The session of the manager who changed their own PIN stays open.
    await own.getByLabel('Yeni PIN', { exact: true }).fill('13579')
    await own.getByLabel('Yeni PIN (tekrar)', { exact: true }).fill('13579')
    await own.getByRole('button', { name: "PIN'i değiştir" }).click()
    await own.getByText('PIN değişti; kasalara iniyor.').waitFor()
    check((await page.request.get(`${url}/api/auth/session`)).status() === 200, "müdür kendi PIN'ini değiştirdi; oturumu açık kaldı")
    await own.getByRole('button', { name: 'Kapat', exact: true }).click()
    await page.getByRole('button', { name: 'Yeni kasiyer' }).click()
    await form.getByLabel('Kod', { exact: true }).fill(`${id}-k`)
    await form.getByLabel('Ad', { exact: true }).fill(`PIN Kasiyeri ${id}`)
    await form.getByLabel('PIN (4–8 hane)', { exact: true }).fill('1357')
    await form.getByLabel('PIN (tekrar)', { exact: true }).fill('1357')
    await form.getByRole('button', { name: 'Ekle' }).click()
    const added = page.getByRole('region', { name: `Kasiyer: PIN Kasiyeri ${id}` })
    await added.getByRole('listitem').filter({ hasText: `${managerName} · eklendi` }).waitFor()
    check(true, `müdürün eklediği kasiyerin kaydında müdürün adı: ${managerName}`)
    await added.getByRole('button', { name: 'Hesabı kapat' }).click()
    await added.getByTestId('account').filter({ hasText: 'Hesap kapalı' }).waitFor()
    await added.getByRole('button', { name: 'Kapat', exact: true }).click()
    // The trial manager (added on the first run; signed in with on later ones).
    const now = (await (await page.request.get(`${url}/api/cashiers`)).json()).cashiers
    if (!now.some((c) => c.id === simID)) {
      await page.getByRole('button', { name: 'Yeni kasiyer' }).click()
      await form.getByLabel('Kod', { exact: true }).fill(simID)
      await form.getByLabel('Ad', { exact: true }).fill(simName)
      await form.getByRole('combobox', { name: /^Rol/ }).selectOption('manager')
      await form.getByLabel('PIN (4–8 hane)', { exact: true }).fill(simPIN)
      await form.getByLabel('PIN (tekrar)', { exact: true }).fill(simPIN)
      await form.getByRole('button', { name: 'Ekle' }).click()
      const simEditor = page.getByRole('region', { name: `Kasiyer: ${simName}` })
      await simEditor.getByText('Kasiyer eklendi; kasalara iniyor.').waitFor()
      await simEditor.getByRole('button', { name: 'Kapat', exact: true }).click()
      check(true, `deneme müdürü eklendi (${simID})`)
    }
    await logout()

    // Another manager closes the account: the manager can no longer sign in.
    await loginManager(simID, simPIN, simName)
    await page.getByRole('button', { name: 'Kasiyerler', exact: true }).click()
    await managerRow().getByRole('button', { name: managerName, exact: true }).click()
    await editor.getByRole('button', { name: 'Hesabı kapat' }).click()
    await editor.getByTestId('account').filter({ hasText: 'Hesap kapalı' }).waitFor()
    await managerRow().getByText('hesap kapalı').waitFor()
    check(true, `başka müdür (${simName}) hesabı kapattı`)
    const detail = await (await page.request.get(`${url}/api/cashiers/${id}`)).json()
    const raw = JSON.stringify(detail).toLowerCase()
    const by = detail.history.map((c) => c.by)
    check(
      detail.history.length === 4 && by[0] === simName && by[1] === managerName && by[3] === who.name && !raw.includes('13579') && !raw.includes('pin_hash'),
      `değişiklik geçmişi: ${detail.history.length} kayıt (${by.reverse().join(' → ')}); PIN ekrana gelmiyor`,
    )
    const closed = await page.request.post(`${url}/api/auth/login`, { data: { cashier_id: id, pin: '13579' } })
    check(closed.status() === 403, `hesabı kapalı müdürün girişi reddedildi (${closed.status()})`)
    await page.screenshot({ path: `${out}/store-4-kasiyer.png`, fullPage: true })
    await editor.getByRole('button', { name: 'Kapat', exact: true }).click()
    await page.getByRole('button', { name: 'Raporlar', exact: true }).click()
    await page.getByRole('heading', { name: 'Kasa durumu', level: 2 }).waitFor()
  }

  await page.setViewportSize({ width: 390, height: 844 })
  await page.screenshot({ path: `${out}/store-2-telefon.png`, fullPage: true })
  await page.setViewportSize({ width: 1280, height: 900 })

  await logout()
  const after = await page.request.get(`${url}/api/registers`)
  check(after.status() === 401, 'çıkıştan sonra giriş ekranı geldi, API kapandı')
  check(errors.length === 0, `tarayıcıda hata yok${errors.length ? ': ' + errors.join('; ') : ''}`)
} catch (e) {
  await page.screenshot({ path: `${out}/store-bug.png`, fullPage: true }).catch(() => {})
  console.error(`✗ ${e.message}`)
  process.exitCode = 1
} finally {
  await browser.close()
}
