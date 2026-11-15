// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Small HTTP clients for the register, store server and headquarters. The tests make the sale
// through the register's gateway API (the endpoints the cashier's screen uses) and
// read the result from each tier's own API; the screen level (Playwright) is separate
// testlerdedir.

class Client {
  constructor(base) {
    this.base = base
    this.cookies = new Map()
    this.token = ''
  }

  async call(method, path, body, { allow = [] } = {}) {
    const headers = { 'content-type': 'application/json' }
    if (this.token) headers.authorization = `Bearer ${this.token}`
    if (this.cookies.size) headers.cookie = [...this.cookies].map(([k, v]) => `${k}=${v}`).join('; ')
    const res = await fetch(this.base + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
    for (const c of res.headers.getSetCookie?.() ?? []) {
      const [pair] = c.split(';')
      const i = pair.indexOf('=')
      this.cookies.set(pair.slice(0, i), pair.slice(i + 1))
    }
    const text = await res.text()
    if (!res.ok && !allow.includes(res.status)) throw new Error(`${method} ${path}: ${res.status} ${text.slice(0, 300)}`)
    let json
    try {
      json = text ? JSON.parse(text) : undefined
    } catch {
      json = undefined
    }
    return { status: res.status, json, text }
  }
}

/** Kasa: kasiyerin kullandığı gateway uçları. */
export class Register extends Client {
  constructor(id, base) {
    super(base)
    this.id = id
  }

  /** Kasiyerle girer (deneme kasiyeri Ayşe, PIN 1111). */
  async login(cashier = 'ayse', pin = '1111') {
    this.token = (await this.call('POST', '/api/auth/login', { cashier_id: cashier, pin })).json.token
  }

  /** Vardiya açık değilse açar (500,00 TL açılış nakdi). */
  async ensureShift() {
    const cur = (await this.call('GET', '/api/shift')).json
    if (!cur?.shift) await this.call('POST', '/api/shift/open', { opening_cash_kurus: 50000 })
  }

  /** Açık vardiyayı sayılan nakitle kapatır (kasiyer kendi vardiyasını kapatabilir). */
  async closeShift(countedCashKurus = 50000) {
    await this.call('POST', '/api/shift/close', { counted_cash_kurus: countedCashKurus })
  }

  /**
   * sell, market kasasında bir satış yapar: her barkod sepete eklenir, nakit
   * ödenir. Varsayılan: bir Ekmek (15,00 TL). Satış kimliğini döner.
   */
  async sell(barcodes = ['8690000000012'], cashKurus = 5000) {
    const basket = (await this.call('POST', '/api/module/baskets')).json
    let last = basket
    for (const barcode of barcodes) {
      last = (await this.call('POST', `/api/module/baskets/${basket.id}/items`, { barcode })).json
    }
    const checkout = (await this.call('POST', `/api/module/baskets/${basket.id}/checkout`)).json
    const sale = (await this.call('POST', `/api/sales/${checkout.sale_id}/checkout`, { payments: [{ method: 'cash', amount_kurus: cashKurus }] })).json
    if (sale.status !== 'paid') throw new Error(`satış ödenmedi: ${sale.status}`)
    return { id: sale.id, totalKurus: last.total_kurus, receiptNo: sale.receipt_no }
  }

  /** Birden çok satış yapar; satış kimliklerini döner. */
  async sellMany(n) {
    const out = []
    for (let i = 0; i < n; i++) out.push(await this.sell())
    return out
  }
}

/** Operasyon: mağaza raporları (report). */
export class Store extends Client {
  constructor(base = 'http://127.0.0.1:8090') {
    super(base)
  }

  /** Mağaza parolasıyla girer (müdür tanımlıysa deneme müdürüyle). */
  async login(password = 'store-test', manager = 'sim-manager:86420') {
    const r = await this.call('POST', '/api/auth/login', { password }, { allow: [401, 403] })
    if (r.status < 300) return
    const [id, pin] = manager.split(':')
    await this.call('POST', '/api/auth/login', { cashier_id: id, pin })
  }

  async registers() {
    return (await this.call('GET', '/api/registers')).json.registers
  }

  async register(id) {
    return (await this.registers()).find((r) => r.id === id)
  }

  /** Günlük özet: fiş sayısı, toplam, ödemeler, kasa dökümü. */
  async summary() {
    return (await this.call('GET', '/api/reports/summary')).json
  }
}

/** Merkez: ingest durumu ve rapor API'si. */
export class Headquarters extends Client {
  constructor(ingest = 'http://127.0.0.1:8095', reporting = 'http://127.0.0.1:9090') {
    super(ingest)
    this.reportingBase = reporting
    this.reporting = new Client(reporting)
  }

  /** Mağazanın merkezdeki durumu: bağlantı, bekleyen olay, olay ve satış sayısı. */
  async store(id) {
    return (await this.call('GET', '/api/stores')).json.find((s) => s.id === id)
  }

  /** Merkez raporlarına deneme hesabıyla girer (merkez / headquarters-test). */
  async login(user = 'headquarters', password = 'headquarters-test') {
    await this.reporting.call('POST', '/api/auth/login', { username: user, password })
  }

  /** Merkezin özeti (mağaza süzgeciyle). */
  async summary(store, from, to = from) {
    const q = new URLSearchParams({ from, to, store })
    return (await this.reporting.call('GET', `/api/reports/summary?${q}`)).json
  }
}

/** Bugünün tarihi (Europe/Istanbul), YYYY-AA-GG. */
export function today() {
  return new Intl.DateTimeFormat('sv-SE', { timeZone: 'Europe/Istanbul' }).format(new Date())
}
