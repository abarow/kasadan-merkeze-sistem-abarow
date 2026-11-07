// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// The publisher's image registry (publisher-registry in the update stack): like CI it writes
// images and pulls with a unit token. skopeo runs in a container on the stack's Docker network;
// no port is opened to the host.

import { execFile } from 'node:child_process'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { promisify } from 'node:util'
import { root } from './stack.mjs'

const run = promisify(execFile)
const NETWORK = 'abarow-store'
const SKOPEO = 'quay.io/skopeo/stable:latest'
export const REGISTRY = 'publisher-registry:5000'
export const CI_CREDS = 'ci:ci-test-token-0123456789abcdef0123456789abcdef'
const dir = resolve(process.env.ABAROW_E2E_TRUST ?? join(root, 'tools/e2e/out/trust'))
export const work = join(dir, 'image')

/** skopeo, ağdaki bir container'da skopeo çalıştırır; work dizini /w olarak bağlıdır. */
export async function skopeo(args, { env = {}, entrypoint, allowFail = false } = {}) {
  mkdirSync(work, { recursive: true })
  const argv = ['run', '--rm', '--network', NETWORK, '-v', `${work}:/w`, '-v', `${join(root, 'scripts')}:/scripts:ro`]
  for (const [k, v] of Object.entries(env)) argv.push('-e', `${k}=${v}`)
  if (entrypoint) argv.push('--entrypoint', entrypoint)
  argv.push(SKOPEO, ...args)
  try {
    const { stdout } = await run('docker', argv, { maxBuffer: 16 << 20 })
    return { ok: true, out: stdout.trim() }
  } catch (e) {
    if (!allowFail) throw new Error(`skopeo ${args.join(' ')}: ${e.stderr || e.message}`)
    return { ok: false, out: String(e.stderr || e.message) }
  }
}

/** pushAlpine, alpine imajını CI gibi yayıncının kayıt defterine yazar ve digest'ini döner. */
export async function pushAlpine(path, tag = '1') {
  mkdirSync(work, { recursive: true })
  await run('docker', ['pull', '--quiet', 'alpine:latest'])
  await run('docker', ['save', 'alpine:latest', '-o', join(work, 'alp.tar')])
  const dst = `docker://${REGISTRY}/${path}:${tag}`
  await skopeo(['copy', '--dest-tls-verify=false', '--dest-creds', CI_CREDS, 'docker-archive:/w/alp.tar', dst])
  const { out } = await skopeo(['inspect', '--tls-verify=false', '--creds', CI_CREDS, '--format', '{{.Digest}}', dst])
  return out
}

/** writeDigest, image-copy.sh'ın okuduğu {unit, repo, digest} kaydını yazar. */
export function writeDigest(dirName, name, digest) {
  const d = join(work, dirName)
  mkdirSync(d, { recursive: true })
  writeFileSync(join(d, `${name}.json`), JSON.stringify({ unit: name, repo: `ghcr.io/abarow/${name}`, digest }))
}

/** unitToken, merkez ajanının yayıncıdan aldığı imaj çekme jetonudur (ajan veri hacminden). */
export async function unitToken() {
  const { stdout } = await run('docker', ['run', '--rm', '-v', 'abarow-headquarters_headquarters-agent-data:/d:ro', 'alpine', 'cat', '/d/token.json'])
  return JSON.parse(stdout).token
}

const execFileP = run

/** cosignCommand, image-sign.sh'ın COSIGN değişkeni için ağdaki cosign container komutudur. */
export function cosignCommand() {
  return `docker run --rm --user 0:0 --network ${NETWORK} -v ${join(dir, 'cosign')}:/k -e COSIGN_PASSWORD= ghcr.io/sigstore/cosign/cosign:v2.4.1`
}

/** signImages, image-sign.sh ile digest kayıtlarındaki imajları verilen cosign anahtarıyla imzalar. */
export async function signImages(prefix, digestDir, keyName) {
  const bash = process.env.ABAROW_BASH ?? 'bash'
  const { stdout } = await execFileP(bash, ['scripts/image-sign.sh', prefix, digestDir], {
    cwd: root,
    env: { ...process.env, MSYS_NO_PATHCONV: '1', COSIGN: cosignCommand(), COSIGN_KEY: `/k/${keyName}.key`, DST_CREDS: CI_CREDS, COSIGN_ALLOW_HTTP: '1' },
  })
  return stdout
}

/**
 * generatePolicy, sunucu generate-policy ile seviyenin politikasını bir dizine yazar
 * (ve Podman'ın düz HTTP aynaya bağlanması için deneme ayarı ekler).
 */
export async function generatePolicy(level, name, trustFile = 'trust.json') {
  const target = join(work, 'policy', name)
  mkdirSync(join(target, 'registries.conf.d'), { recursive: true })
  writeFileSync(join(target, 'registries.conf.d', 'test.conf'), `[[registry]]\nlocation="${REGISTRY}"\ninsecure=true\n`)
  await execFileP('docker', [
    'run', '--rm', '--user', '0:0', '-v', `${dir}:/g:ro`, '-v', `${target}:/p`,
    'ghcr.io/abarow/abarow-update-server:dev', 'generate-policy', level, REGISTRY, `/g/${trustFile}`, '/p',
  ])
  return target
}

/** podmanPull, politikayı kullanan ayrıcalıksız bir Podman'la imajı çekmeyi dener. */
export async function podmanPull(policyDir, ref, creds) {
  try {
    const { stdout } = await execFileP('docker', [
      'run', '--rm', '--network', NETWORK, '--device', '/dev/fuse', '--security-opt', 'label=disable', '--security-opt', 'seccomp=unconfined',
      '--cap-add', 'SYS_ADMIN', '--security-opt', 'apparmor=unconfined', '-v', `${policyDir}:/root/.config/containers:ro`, 'quay.io/podman/stable:v5.4',
      'podman', '--storage-driver', 'vfs', 'pull', '--creds', creds, ref,
    ], { maxBuffer: 16 << 20, timeout: 180_000 })
    return { ok: true, out: stdout }
  } catch (e) {
    return { ok: false, out: String(e.stderr || e.message) }
  }
}

/** cosignKeyPair, yeni bir cosign anahtar çifti üretir (cosign/<ad>.key, .pub) ve genel anahtarı (PEM) döner. */
export async function cosignKeyPair(name) {
  const k = join(dir, 'cosign')
  mkdirSync(k, { recursive: true })
  await execFileP('docker', ['run', '--rm', '--user', '0:0', '-v', `${k}:/k`, '-w', '/k', '-e', 'COSIGN_PASSWORD=', 'ghcr.io/sigstore/cosign/cosign:v2.4.1', 'generate-key-pair', '--output-key-prefix', name])
  return readFileSync(join(k, `${name}.pub`), 'utf8')
}

/** trustVariant, güven dosyasının fn ile değiştirilmiş bir kopyasını trust-<ad>.json olarak yazar ve dosya adını döner. */
export function trustVariant(name, fn) {
  const file = `trust-${name}.json`
  writeFileSync(join(dir, file), JSON.stringify(fn(JSON.parse(readFileSync(join(dir, 'trust.json'), 'utf8')))))
  return file
}
