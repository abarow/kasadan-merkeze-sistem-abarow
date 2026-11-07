// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// ui-smoke uses the register screen in a real browser as a cashier would and
// takes a screenshot at every step. While the Docker simulation is running, from the repository
// root:
//
//   docker compose -f deploy/compose/compose.yaml --profile ui-test run --rm ui-smoke
//
// The images are written under tools/ui-smoke/out/. There must be no open shift
// at the register (register-sim closes it).

import { chromium } from 'playwright'

const url = process.env.ABAROW_UI_URL ?? 'http://gateway:8080'
const out = process.env.ABAROW_UI_OUT ?? 'out'

const browser = await chromium.launch()
// The cashier and customer screens are in the same browser context (BroadcastChannel).
const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, locale: 'tr-TR' })
const page = await context.newPage()
const errors = []
page.on('pageerror', (e) => errors.push(e.message))
// The 404 of the unknown barcode trial is expected; the browser's resource loading
// messages are not counted as errors, the application's own errors are.
page.on('console', (m) => m.type() === 'error' && !m.text().startsWith('Failed to load resource') && errors.push(m.text()))

function check(cond, msg) {
  if (!cond) throw new Error(msg)
  console.log(`✓ ${msg}`)
}

async function pressPIN(pin, shot, submit = 'Giriş') {
  for (const d of pin) await page.getByRole('button', { name: d, exact: true }).click()
  if (shot) await page.screenshot({ path: `${out}/${shot}.png` })
  await page.getByRole('button', { name: submit, exact: true }).click()
}

// added waits for the response of the add-to-basket request. "networkidle" cannot be used:
// the screen always keeps the reader stream (/api/device/scans) open.
const added = () => page.waitForResponse((r) => r.request().method() === 'POST' && /\/api\/module\/baskets\/[^/]+\/items$/.test(r.url()))

async function scan(code) {
  const done = added()
  await page.getByLabel('Barkod').fill(code)
  await page.getByLabel('Barkod').press('Enter')
  await done
}

// readerScan imitates a scan from the barcode reader (the device simulator):
// the code reaches the screen through the stream and is added to the basket wherever the cursor is.
async function readerScan(code) {
  const token = await page.evaluate(() => sessionStorage.getItem('abarow.token'))
  const res = await page.request.post(`${url}/api/device/scans`, { data: { code }, headers: { Authorization: `Bearer ${token}` } })
  if (res.status() !== 204) throw new Error(`okutma taklidi: ${res.status()} ${await res.text()}`)
}

// printerState sets the simulator printer's state (paper out, cover
// open ...); the state reaches the screen through the stream.
async function printerState(state) {
  const token = await page.evaluate(() => sessionStorage.getItem('abarow.token'))
  const res = await page.request.post(`${url}/api/device/printer`, { data: { state }, headers: { Authorization: `Bearer ${token}` } })
  if (res.status() !== 204) throw new Error(`yazıcı durumu taklidi: ${res.status()} ${await res.text()}`)
}

// setWeight sets the simulator scale's weight (grams).
async function setWeight(grams) {
  const token = await page.evaluate(() => sessionStorage.getItem('abarow.token'))
  const res = await page.request.post(`${url}/api/device/weight`, { data: { grams }, headers: { Authorization: `Bearer ${token}` } })
  if (res.status() !== 204) throw new Error(`terazi taklidi: ${res.status()} ${await res.text()}`)
}

try {
  await page.goto(url)
  await page.getByText('SİMÜLATÖR MODU').waitFor()
  check(true, 'simülatör uyarı şeridi görünüyor')

  // Sign-in: first a wrong PIN is rejected, then the register opens with the right PIN.
  await page.getByRole('button', { name: /Kasiyer Ayşe/ }).click()
  await pressPIN('0000')
  await page.getByRole('alert').filter({ hasText: 'deneme hakkı' }).waitFor()
  check(true, 'yanlış PIN reddedildi, kalan hak gösterildi')
  await pressPIN('1111', '0-login')
  await page.getByRole('button', { name: 'Vardiyayı aç' }).waitFor()
  check(true, 'kasiyer girişi yapıldı, vardiya açma ekranı geldi')

  // The shift is opened with 500.00 TL opening cash.
  await page.getByLabel('Açılış nakdi').fill('500')
  await page.screenshot({ path: `${out}/0-vardiya-ac.png` })
  await page.getByRole('button', { name: 'Vardiyayı aç' }).click()
  await page.locator('.who').filter({ hasText: 'Kasiyer Ayşe' }).waitFor()
  check(true, 'vardiya açıldı, satış ekranı geldi')
  // Customer screen (second screen): a second window in the same browser.
  const shopper = await context.newPage()
  shopper.on('pageerror', (e) => errors.push(`müşteri ekranı: ${e.message}`))
  await shopper.setViewportSize({ width: 1024, height: 600 })
  await shopper.goto(`${url}/customer.html`)
  await shopper.getByText('Hoş geldiniz').waitFor()
  check(true, 'müşteri ekranı açıldı: hoş geldiniz')
  await page.getByRole('button', { name: /Ekmek 250 g/ }).waitFor()
  check(true, 'hızlı tuşlar yüklendi')

  await scan('8690000000012')
  await page.locator('.total strong').filter({ hasText: '15,00' }).waitFor()
  // The reader reads from device: even while the cursor is in the search box the code goes to the basket.
  await page.getByLabel('Ürün ara').focus()
  await readerScan('8690000000012')
  await page.locator('.total strong').filter({ hasText: '30,00' }).waitFor()
  check(true, 'okuyucudan gelen barkod sepete eklendi (imleç arama kutusundayken)')
  // If the printer runs out of paper a warning appears at the top; it goes away when the paper is loaded.
  await printerState('paper_out')
  await page.getByRole('alert').filter({ hasText: 'Fiş yazıcısı: Kağıt bitti: fiş basılamaz.' }).waitFor()
  await page.screenshot({ path: `${out}/1-yazici-kagit-bitti.png` })
  check(true, 'yazıcının kağıdı bitince uyarı şeridi göründü')
  await printerState('ready')
  await page.getByText('Fiş yazıcısı:').waitFor({ state: 'detached' })
  check(true, 'kağıt takılınca uyarı kalktı')
  // The scale at the register: when a weighed product is selected the weight is read from the scale.
  await setWeight(0)
  await page.getByRole('button', { name: /Domates/ }).click()
  await page.getByRole('alert').filter({ hasText: 'terazide ürün yok' }).waitFor()
  check(true, 'boş terazide tartılı ürün eklenmedi, uyarı gösterildi')
  await setWeight(1250)
  await page.getByRole('button', { name: /Domates/ }).click()
  const tomato = page.locator('.lines li').filter({ hasText: 'Domates' })
  await tomato.filter({ hasText: '1,250 kg' }).waitFor()
  await page.screenshot({ path: `${out}/1-terazi.png` })
  check(true, 'tartılı ürün teraziden okunan 1,250 kg ile eklendi')
  await tomato.getByRole('button', { name: 'Satırı sil' }).click()
  await tomato.waitFor({ state: 'detached' })
  await setWeight(0)
  await scan('2700001012504')
  const milk = added()
  await page.getByRole('button', { name: /Süt 1 L/ }).click()
  await milk
  // 2 × 15,00 + 39,90 × 1,250 (49,88) + 39,90 = 119,78
  await page.locator('.total strong').filter({ hasText: '119,78' }).waitFor()
  check(true, 'sepet toplamı 119,78')
  await shopper.locator('.customer .total strong').filter({ hasText: '119,78' }).waitFor()
  await shopper.screenshot({ path: `${out}/1-musteri-sepet.png` })
  check(true, 'müşteri ekranında sepet ve toplam 119,78')
  // When the screen is reopened the cashier continues from their basket.
  await page.reload()
  await page.locator('.total strong').filter({ hasText: '119,78' }).waitFor()
  check(true, 'sayfa yenilenince sepet korundu')
  await scan('0000000000000')
  await page.getByRole('alert').filter({ hasText: 'ürün bulunamadı' }).waitFor()
  check(true, 'bilinmeyen barkod uyarısı gösterildi')
  await page.screenshot({ path: `${out}/1-sepet.png` })

  await page.getByRole('button', { name: 'ÖDE' }).click()
  await page.getByRole('heading', { name: 'Ödeme' }).waitFor()
  // If the screen is reopened during payment the payment window comes back.
  await page.reload()
  await page.getByRole('heading', { name: 'Ödeme' }).waitFor()
  await page.getByText('Ödenecek ₺119,78').waitFor()
  check(true, 'ödeme sırasında yenilenince ödeme penceresi geri geldi')
  await page.getByLabel('Alınan nakit').fill('200')
  await page.getByText('Para üstü').waitFor()
  check(true, 'para üstü hesaplandı')
  await page.screenshot({ path: `${out}/2-odeme.png` })

  await page.getByRole('button', { name: 'Nakit al' }).click()
  await page.getByRole('heading', { name: 'Satış tamamlandı' }).waitFor()
  const change = await page.locator('.done .change strong').innerText()
  check(change.includes('80,22'), `satış tamamlandı, para üstü ${change}`)
  await shopper.getByText('Teşekkür ederiz').waitFor()
  const shown = await shopper.locator('.done .change strong').textContent()
  await shopper.screenshot({ path: `${out}/2-musteri-para-ustu.png` })
  check(shown.includes('80,22'), `müşteri ekranında para üstü ${shown}`)
  await page.screenshot({ path: `${out}/3-tamamlandi.png` })

  await page.getByRole('button', { name: 'Yeni satış' }).click()
  await page.getByText('Barkod okutun ya da sağdan ürün seçin.').waitFor()
  check(true, 'yeni sepet açıldı')

  // Mixed payment: in a 15.00 sale 10.00 cash, the remaining 5.00 by card.
  await scan('8690000000012')
  await page.locator('.total strong').filter({ hasText: '15,00' }).waitFor()
  await page.getByRole('button', { name: 'ÖDE' }).click()
  await page.getByRole('heading', { name: 'Ödeme' }).waitFor()
  await page.getByLabel('Alınan nakit').fill('10')
  await page.getByRole('button', { name: 'Kalan ₺5,00 kartla öde' }).click()
  await page.getByRole('heading', { name: 'Satış tamamlandı' }).waitFor()
  const paid = await page.locator('.done dl').innerText()
  check(/Nakit\s+₺10,00/.test(paid) && /Kredi kartı\s+₺5,00/.test(paid), 'karışık ödeme: 10,00 nakit + 5,00 kart')
  check(!(await page.locator('.done .change').isVisible()), 'karışık ödemede para üstü yok')
  await page.screenshot({ path: `${out}/3-karisik-odeme.png` })
  await page.getByRole('button', { name: 'Yeni satış' }).click()
  await page.getByText('Barkod okutun ya da sağdan ürün seçin.').waitFor()

  // Meal card: all of a 15.00 sale with the meal card (cash is not touched).
  await scan('8690000000012')
  await page.locator('.total strong').filter({ hasText: '15,00' }).waitFor()
  await page.getByRole('button', { name: 'ÖDE' }).click()
  await page.getByRole('heading', { name: 'Ödeme' }).waitFor()
  await page.screenshot({ path: `${out}/3-odeme-yontemleri.png` })
  await page.getByRole('button', { name: 'Yemek kartı ile öde' }).click()
  await page.getByRole('heading', { name: 'Satış tamamlandı' }).waitFor()
  const meal = await page.locator('.done dl').innerText()
  check(/Yemek kartı\s+₺15,00/.test(meal), 'yemek kartıyla ödendi: 15,00')
  await page.getByRole('button', { name: 'Yeni satış' }).click()
  await page.getByText('Barkod okutun ya da sağdan ürün seçin.').waitFor()

  // Reports: the X report opens; Z is not taken (it closes the day).
  await page.getByRole('button', { name: 'Raporlar' }).click()
  await page.getByRole('heading', { name: /X raporu/ }).waitFor()
  await page.locator('.report tfoot').getByText('TOPLAM', { exact: true }).waitFor()
  check(true, 'X raporu açıldı')
  await page.getByRole('button', { name: 'Gün sonu (Z) al (müdür onayı)' }).waitFor()
  check(true, 'kasiyer Z için müdür onayı ister')
  await page.screenshot({ path: `${out}/4-x-raporu.png` })
  await page.getByRole('button', { name: 'Kapat', exact: true }).click()

  // Register operations: the cashier puts money into the register; takes it out with manager approval.
  await page.getByRole('button', { name: 'Kasa', exact: true }).click()
  await page.getByRole('heading', { name: /Kasa işlemleri/ }).waitFor()
  await page.getByRole('button', { name: 'Kasaya para koy' }).click()
  await page.getByLabel('Tutar').fill('50')
  await page.getByLabel('Açıklama').fill('Bozuk para')
  await page.getByRole('button', { name: 'Kasaya koy', exact: true }).click()
  await page.getByRole('status').filter({ hasText: 'Kasaya ₺50,00 kondu.' }).waitFor()
  check(true, 'kasaya 50,00 kondu')
  await page.getByRole('button', { name: 'Kasadan para al (müdür onayı)' }).click()
  await page.getByLabel('Tutar').fill('20')
  await page.getByLabel('Açıklama').fill('Ara para')
  await page.getByRole('button', { name: 'Kasadan al', exact: true }).click()
  await page.getByRole('heading', { name: 'Müdür onayı' }).waitFor()
  await pressPIN('9999', '5-mudur-onayi', 'Onayla')
  await page.getByRole('status').filter({ hasText: 'Kasadan ₺20,00 alındı.' }).waitFor()
  check(true, 'müdür onayıyla kasadan 20,00 alındı')
  await page.getByRole('button', { name: 'Çekmeceyi aç (satışsız) (müdür onayı)' }).click()
  await page.getByLabel('Neden').fill('Bozuk para bozdurma')
  await page.getByRole('button', { name: 'Çekmeceyi aç', exact: true }).click()
  await page.getByRole('heading', { name: 'Müdür onayı' }).waitFor()
  await pressPIN('9999', undefined, 'Onayla')
  await page.getByRole('status').filter({ hasText: 'Çekmece açıldı' }).waitFor()
  check(true, 'müdür onayıyla çekmece satışsız açıldı')
  await page.screenshot({ path: `${out}/5-register.png` })

  // Shift closing: the count is blind, the expected amount shows after the count.
  // The drawer should hold 500.00 + 119.78 + 10.00 (the cash of the mixed payment) + 50.00
  // − 20.00 = 659.78.
  await page.getByRole('button', { name: /Vardiyayı kapat/ }).click()
  await page.getByRole('heading', { name: /Vardiya kapanışı/ }).waitFor()
  check(!(await page.getByText('Beklenen nakit').isVisible()), 'sayım kör: beklenen tutar gösterilmiyor')
  await page.getByLabel('Sayılan nakit').fill('659,78')
  await page.getByRole('button', { name: 'Vardiyayı kapat', exact: true }).click()
  await page.getByRole('heading', { name: /Vardiya raporu/ }).waitFor()
  const expected = await page.locator('.cashup dt:text-is("Beklenen nakit") + dd').innerText()
  check(expected.includes('659,78'), `vardiya raporu: beklenen nakit ${expected}`)
  await page.locator('.cashup').getByText('Fark', { exact: true }).waitFor()
  check(true, 'kasa farkı yok')
  const opens = await page.locator('.cashup dt:text-is("Satışsız çekmece açılışı") + dd').innerText()
  check(opens === '1', `raporda satışsız çekmece açılışı ${opens}`)
  await page.getByRole('button', { name: 'Yazdır' }).click()
  await page.getByRole('status').filter({ hasText: 'Yazıcıya gönderildi.' }).waitFor()
  check(true, 'vardiya raporu yazıcıya gönderildi')
  await page.screenshot({ path: `${out}/6-vardiya-raporu.png` })
  await page.getByRole('button', { name: 'Tamam' }).click()
  await page.getByText('Kasiyer seçin').waitFor()
  check(true, 'vardiyasını kapatan kasiyerin oturumu kapandı')

  // Past shift report: sign in again, Reports → Shift reports.
  // The cashier sees only their own shifts; the newest is the one just closed.
  await page.getByRole('button', { name: /Kasiyer Ayşe/ }).click()
  await pressPIN('1111')
  await page.getByRole('button', { name: 'Raporlar' }).click()
  await page.getByRole('button', { name: 'Vardiya raporları' }).click()
  await page.locator('.shifts button').first().click()
  const past = await page.locator('.cashup dt:text-is("Beklenen nakit") + dd').innerText()
  check(past.includes('659,78'), `geçmiş vardiya raporu açıldı: beklenen nakit ${past}`)
  await page.screenshot({ path: `${out}/7-vardiya-gecmisi.png` })
  await page.getByRole('button', { name: 'Listeye dön' }).click()
  await page.getByRole('button', { name: 'Geri' }).click()

  // End of day: the cashier takes Z with manager approval (the shift is closed).
  await page.getByRole('button', { name: 'Gün sonu (Z) al (müdür onayı)' }).click()
  await page.getByRole('button', { name: 'Evet, Z raporu al' }).click()
  await page.getByRole('heading', { name: 'Müdür onayı' }).waitFor()
  await pressPIN('9999', undefined, 'Onayla')
  await page.getByRole('heading', { name: /Z raporu/ }).waitFor()
  check(true, 'müdür onayıyla Z alındı')
  await page.getByRole('button', { name: 'Yazdır' }).click()
  await page.getByRole('status').filter({ hasText: 'Yazıcıya gönderildi.' }).waitFor()
  check(true, 'Z raporu yazıcıya gönderildi')
  await page.screenshot({ path: `${out}/8-z-raporu.png` })
  await page.getByRole('button', { name: 'Kapat', exact: true }).click()
  await page.getByRole('button', { name: 'Çıkış' }).click()
  await page.getByText('Kasiyer seçin').waitFor()
  check(true, 'çıkış yapıldı')
  await shopper.getByText('Bu kasa kapalı').waitFor()
  check(true, 'çıkışta müşteri ekranı: kasa kapalı')
  check(errors.length === 0, `tarayıcıda hata yok${errors.length ? ': ' + errors.join('; ') : ''}`)
} catch (e) {
  await page.screenshot({ path: `${out}/bug.png` }).catch(() => {})
  console.error(`✗ ${e.message}`)
  if (errors.length) console.error(`tarayıcı hataları: ${errors.join('; ')}`)
  process.exitCode = 1
} finally {
  await browser.close()
}
