// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Publisher role: in the update stack (scripts/e2e.sh up update) it prepares a channel
// manifest, signs it with the channel key using `server sign-channel` and
// writes it to the publisher's channel directory. The agents poll the manifest, verify it and
// announce it on their screens.

import { execFile } from 'node:child_process'
import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { promisify } from 'node:util'
import { root } from './stack.mjs'

const run = promisify(execFile)
const IMAGE = 'ghcr.io/abarow/abarow-update-server:dev'
const dir = resolve(process.env.ABAROW_E2E_TRUST ?? join(root, 'tools/e2e/out/trust'))

/** Denemede kurulu sayılan birimler: her katmanın birimleri ve ajanın kendi birimi. */
export const UNITS = [
  'abarow-reporting',
  'abarow-ingest',
  'abarow-headquarters-update',
  'abarow-report',
  'abarow-inventory',
  'abarow-store-update',
  'abarow-sales',
  'abarow-gateway',
  'abarow-update',
]

/** Sağlıklı açılmayan (geri almayı sınamak için) digest; compose ayarıyla aynıdır. */
export const BAD_DIGEST = `sha256:${'c'.repeat(64)}`
export const digest = (c) => `sha256:${String(c).repeat(64)}`

/**
 * manifest, bildirim gövdesini kurar. images: birim → digest; verilmeyen birimler
 * için varsayılan digest (def) kullanılır.
 */
export function manifest({ seq, version, urgency = 'normal', notes = '', channel = 'prod', images = {}, def = '2', expiresInH = 48 }) {
  const now = Date.now()
  return {
    channel,
    version,
    seq,
    issued: new Date(now - 60_000).toISOString(),
    expires: new Date(now + expiresInH * 3_600_000).toISOString(),
    urgency,
    notes,
    images: UNITS.map((unit) => ({ unit, repo: `ghcr.io/abarow/${unit}`, digest: images[unit] ?? digest(def) })),
  }
}

/** sign, bildirimi verilen rolün (ya da key ile verilen anahtar dosyasının) anahtarıyla imzalar ve imzalı JSON'u (metin) döner. */
export async function sign(m, role = 'prod', key = role) {
  const inDir = join(dir, 'in')
  mkdirSync(inDir, { recursive: true })
  writeFileSync(join(inDir, 'm.json'), JSON.stringify(m))
  const { stdout } = await run('docker', [
    'run', '--rm', '--user', '0:0',
    '-v', `${join(dir, 'keys')}:/keys:ro`,
    '-v', `${inDir}:/in:ro`,
    IMAGE, 'sign-channel', role, `/keys/${key}.key`, '/in/m.json',
  ])
  return stdout
}

/** publish, bildirimi imzalayıp kanal dizinine yazar (imzalı metin dönülür). */
export async function publish(m, { role = 'prod', key = role, as = m.channel } = {}) {
  const signed = await sign(m, role, key)
  mkdirSync(join(dir, 'channel'), { recursive: true })
  writeFileSync(join(dir, 'channel', `${as}.json`), signed)
  return signed
}

/** unpublish, kanaldaki bildirimi siler. */
export function unpublish(channel = 'prod') {
  rmSync(join(dir, 'channel', `${channel}.json`), { force: true })
}

/** generateKey, yeni bir kanal anahtarı üretir (keys/<ad>.key) ve genel anahtarını döner. */
export async function generateKey(name) {
  mkdirSync(join(dir, 'keys'), { recursive: true })
  const { stdout } = await run('docker', ['run', '--rm', '--user', '0:0', '-v', `${join(dir, 'keys')}:/keys`, IMAGE, 'generate-key', `/keys/${name}.key`])
  return stdout.trim().split('\n').pop().replace(/^.*: /, '').trim()
}

/**
 * editTrust, güven dosyasını (trust.json) okuyup fn ile değiştirir ve YERİNDE yazar:
 * ajanlar dosyayı tek dosya bağlamasıyla görür, yeniden adlandırma bağlamayı koparırdı.
 * Eski içeriği döner (geri yüklemek için).
 */
export function editTrust(fn) {
  const path = join(dir, 'trust.json')
  const before = readFileSync(path, 'utf8')
  writeFileSync(path, JSON.stringify(fn(JSON.parse(before))))
  return before
}

/** restoreTrust, editTrust'ın döndüğü içeriği geri yazar. */
export function restoreTrust(content) {
  writeFileSync(join(dir, 'trust.json'), content)
}
