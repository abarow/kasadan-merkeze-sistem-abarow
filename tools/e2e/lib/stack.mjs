// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Stack opens and closes a scenario's Docker stack (register, store server, optionally
// headquarters, the update center) and stops and starts its containers. The stack is
// set up by scripts/e2e.sh (the identities, keys, connections in the same order as scripts/store-sim.sh);
// the tests run on top of the stack.

import { execFile, spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { promisify } from 'node:util'

const run = promisify(execFile)
export const root = fileURLToPath(new URL('../../..', import.meta.url))

// Durations shortened in the test (see docs/DESIGN.md "Delivery receipt"): an event is kept at least
// 6 s, the cleanup runs every 2 s. In a real install it is 30/90 days and
// 1 minute.
export const cleanEnv = { ABAROW_CLEAN_MAX_AGE: '6s', ABAROW_CLEAN_INTERVAL: '2s' }

export class Stack {
  /**
   * @param {{ad: string, merkezli: boolean, restoran?: boolean, guncelleme?: boolean, registers?: string[]}} o
   */
  constructor({ ad, headquarters, restaurant = false, update = false, registers = ['register-1'] }) {
    this.ad = ad
    this.headquarters = headquarters
    this.restaurant = restaurant
    this.update = update
    this.registers = registers
  }

  /** Kasanın gateway adresi (register-1 8091, register-2 8092). */
  registerURL(id) {
    return `http://127.0.0.1:${8090 + Number(id.replace('register-', ''))}`
  }

  // compose runs a tier's Compose command. layer: "headquarters",
  // "store" or a register id ("register-1").
  async compose(layer, args, opts = {}) {
    let files, env = { ...process.env, ...cleanEnv, MSYS_NO_PATHCONV: '1' }
    let project = []
    if (layer === 'headquarters') {
      files = ['deploy/compose/headquarters.yaml', ...(this.update ? ['deploy/compose/update-headquarters.yaml'] : [])]
      if (this.update) env.COMPOSE_PROFILES = 'update-server'
    } else if (layer === 'store') {
      files = [
        'deploy/compose/store.yaml',
        ...(this.headquarters ? ['deploy/compose/store-headquarters.yaml'] : []),
        ...(this.update ? ['deploy/compose/update-store.yaml'] : []),
      ]
    } else {
      const n = Number(layer.replace('register-', ''))
      files = [
        'deploy/compose/compose.yaml',
        'deploy/compose/register-store.yaml',
        ...(this.restaurant ? ['deploy/compose/restaurant.yaml'] : []),
        ...(this.update ? ['deploy/compose/update-register.yaml'] : []),
      ]
      project = ['-p', `abarow-${layer}`]
      Object.assign(env, {
        ABAROW_REGISTER_ID: layer,
        ABAROW_UI_PORT: String(8090 + n),
        ABAROW_SALES_PORT: String(9010 + n),
        ABAROW_NATS_PORT: String(4230 + n),
      })
    }
    const argv = ['compose', ...project, ...files.flatMap((f) => ['-f', f]), ...args]
    return run('docker', argv, { cwd: root, env, maxBuffer: 64 * 1024 * 1024, ...opts })
  }

  /** scripts/e2e.sh up|down: yığını kurar ya da siler (çıktı terminale akar). */
  script(command) {
    const bash = process.env.ABAROW_BASH ?? 'bash'
    return new Promise((resolve, reject) => {
      const p = spawn(bash, ['scripts/e2e.sh', command, this.ad], { cwd: root, stdio: 'inherit', env: process.env })
      p.on('exit', (code) => (code === 0 ? resolve() : reject(new Error(`scripts/e2e.sh ${command} ${this.ad}: çıkış ${code}`))))
      p.on('error', reject)
    })
  }

  /** Yığını kurar; ABAROW_E2E_REUSE=1 ise var olan yığın kullanılır. */
  async up() {
    if (process.env.ABAROW_E2E_REUSE) return
    await this.script('up')
  }

  async down() {
    if (process.env.ABAROW_E2E_KEEP || process.env.ABAROW_E2E_REUSE) return
    await this.script('down')
  }

  /** Bir katmandaki servisi durdurur (container'ı silmez, verisi kalır). */
  stop(layer, service) {
    return this.compose(layer, ['stop', service])
  }

  /** Durdurulmuş servisi başlatır. */
  start(layer, service) {
    return this.compose(layer, ['start', service])
  }

  /**
   * streams, bir NATS container'ındaki JetStream stream'lerinin durumunu verir
   * ({ABAROW_SALES: {messages, first_seq, last_seq, ...}}); NATS'ın izleme
   * ucundan (/jsz) okunur.
   */
  async streams(layer, service) {
    const { stdout } = await this.compose(layer, ['exec', '-T', service, 'wget', '-qO-', 'http://127.0.0.1:8222/jsz?accounts=true&streams=true'])
    const out = {}
    for (const a of JSON.parse(stdout).account_details ?? []) {
      for (const s of a.stream_detail ?? []) out[s.name] = s.state
    }
    return out
  }

  /**
   * sql, bir katmanın PostgreSQL container'ında sorgu çalıştırır ve ilk
   * hücreyi metin olarak döner (ör. sayım). Veritabanı: abarow / abarow.
   */
  async sql(layer, service, query) {
    const { stdout } = await this.compose(layer, ['exec', '-T', service, 'psql', '-U', 'abarow', '-d', 'abarow', '-tA', '-c', query])
    return stdout.trim()
  }

  /** Bir stream'deki mesaj sayısıdır (stream yoksa 0). */
  async count(layer, service, stream) {
    return (await this.streams(layer, service))[stream]?.messages ?? 0
  }

  /** Kasanın NATS'ında stream mesaj sayısı. */
  registerCount(id, stream) {
    return this.count(id, 'nats', stream)
  }

  /** Operasyonun NATS'ında stream mesaj sayısı. */
  bhCount(stream) {
    return this.count('store', 'store', stream)
  }

  /** Merkezin NATS'ında stream mesaj sayısı. */
  headquartersCount(stream) {
    return this.count('headquarters', 'headquarters', stream)
  }
}

/**
 * until, koşul sağlanana kadar (en çok timeout ms) every ms'de bir dener ve
 * son değeri döner; sağlanmazsa neyi beklediğini söyleyen hata atar.
 */
export async function until(what, fn, { timeout = 150_000, every = 2_000 } = {}) {
  const deadline = Date.now() + timeout
  let last
  for (;;) {
    try {
      last = await fn()
      if (last) return last
    } catch (e) {
      last = e
    }
    if (Date.now() > deadline) {
      throw new Error(`${what}: ${timeout / 1000} sn içinde olmadı (son durum: ${last instanceof Error ? last.message : JSON.stringify(last)})`)
    }
    await new Promise((r) => setTimeout(r, every))
  }
}

/**
 * stays, koşulun süre boyunca (every aralıkla) bozulmadığını denetler: "hiçbir
 * şey silinmedi" gibi olumsuz iddialar için. Bozulursa hata atar.
 */
export async function stays(what, fn, { duration = 20_000, every = 2_000 } = {}) {
  const deadline = Date.now() + duration
  for (;;) {
    const v = await fn()
    if (!v.ok) throw new Error(`${what}: bozuldu (${v.detail ?? ''})`)
    if (Date.now() > deadline) return v
    await new Promise((r) => setTimeout(r, every))
  }
}
