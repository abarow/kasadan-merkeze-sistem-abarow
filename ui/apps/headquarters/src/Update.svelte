<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { api } from './lib/api.js'
  import { capitalize, dateTime } from './lib/format.js'
  import UpdateStatus from '@abarow/admin/UpdateStatus.svelte'

  // The administrator approves the update registration of the store servers and registers connected to headquarters.
  // For approval the code on the unit's screen is typed: it prevents approving the wrong unit.
  let { onunauthorized } = $props()

  const statuses = [
    { value: 'pending', label: 'Bekleyen' },
    { value: 'approved', label: 'Onaylı' },
    { value: 'rejected', label: 'Reddedilen' },
    { value: 'revoked', label: 'İptal edilen' },
  ]
  const tierName = (t) => ({ store: 'Operasyon', register: 'Kasa', headquarters: 'Merkez' })[t] ?? t

  const phaseName = (p) => ({ available: 'hazır', downloading: 'indiriliyor', applying: 'uygulanıyor', rolled_back: 'geri alındı' })[p] ?? ''
  const STALE_MS = 15 * 60_000
  const stale = (r) => r.report && Date.now() - new Date(r.report.at).getTime() > STALE_MS
  const short = (d) => d.replace('sha256:', '').slice(0, 12)

  let status = $state('pending')
  let list = $state(null)
  let missing = $state(false)
  let error = $state('')
  let codes = $state({})
  let busy = $state('')

  async function load() {
    try {
      list = (await api.updateRegistrations(status)).registrations ?? []
      missing = false
      error = ''
    } catch (err) {
      if (err.status === 401) return onunauthorized()
      if (err.status === 404) missing = true
      else error = capitalize(err.message)
    }
  }

  $effect(() => {
    status
    load()
    // The units report their running versions themselves; the list refreshes by itself.
    const t = setInterval(load, 5000)
    return () => clearInterval(t)
  })

  async function act(device, fn) {
    busy = device
    error = ''
    try {
      await fn()
      codes = { ...codes, [device]: '' }
      await load()
    } catch (err) {
      if (err.status === 401) return onunauthorized()
      error = capitalize(err.message)
    } finally {
      busy = ''
    }
  }
</script>

<UpdateStatus fetchStatus={api.updateStatus} act={api.updateAct} {onunauthorized} />

<section aria-labelledby="kayitlar">
  <h2 id="kayitlar">Birim kayıtları</h2>
  {#if missing}
    <p class="muted">Güncelleme kayıt sunucusu bu merkezde kurulu değil.</p>
  {:else}
    <p class="note">
      Bağlı birimler yayıncının imajlarını çekebilmek için buradan onay bekler. Onaylamadan önce birimin ekranındaki kodu yazın.
    </p>
    <nav class="tabs" aria-label="Kayıt durumu">
      {#each statuses as s (s.value)}
        <button aria-current={status === s.value ? 'page' : undefined} onclick={() => (status = s.value)}>{s.label}</button>
      {/each}
    </nav>
    {#if error}
      <p class="alert" role="alert">{error}</p>
    {/if}
    {#if list && list.length === 0}
      <p class="muted">Bu durumda kayıt yok.</p>
    {/if}
    <ul class="registers">
      {#each list ?? [] as r (r.device)}
        <li class="card" aria-label={r.device}>
          <div class="card-title">
            <strong>{r.name || r.device}</strong>
            <span class="pill off">{tierName(r.tier)}</span>
          </div>
          <dl>
            <dt>Cihaz kimliği</dt>
            <dd>{r.device}</dd>
            {#if r.store}
              <dt>Mağaza</dt>
              <dd>{r.store}{r.register ? ` · ${r.register}` : ''}</dd>
            {/if}
            <dt>Seviye</dt>
            <dd>{r.level}</dd>
            <dt>İstek</dt>
            <dd>{dateTime(r.created_at)}</dd>
            {#if r.decided_at}
              <dt>Karar</dt>
              <dd>{dateTime(r.decided_at)}{r.decided_by ? ` · ${r.decided_by}` : ''}</dd>
            {/if}
            {#if r.status === 'approved'}
              <dt>Çalışan sürüm</dt>
              <dd data-testid="running-version">
                {#if r.report}
                  {r.report.version || 'bilinmiyor'}{#if r.report.offered}
                    <span class="muted"> · yeni sürüm {r.report.offered} {phaseName(r.report.phase)}</span>{/if}
                {:else}
                  henüz bildirmedi
                {/if}
              </dd>
              {#if r.report}
                <dt>Son bildirim</dt>
                <dd>{dateTime(r.report.at)}{#if stale(r)}<span class="muted" data-testid="report-stale"> · eski</span>{/if}</dd>
              {/if}
            {/if}
          </dl>
          {#if r.report?.units && Object.keys(r.report.units).length > 0}
            <details>
              <summary>İmajlar ({Object.keys(r.report.units).length})</summary>
              <ul class="muted" data-testid="report-units">
                {#each Object.entries(r.report.units).sort() as [unit, digest] (unit)}
                  <li>{unit.replace(/^abarow-/, '')}: <code>{short(digest)}</code></li>
                {/each}
              </ul>
            </details>
          {/if}
          {#if r.status === 'pending'}
            <form
              class="grid-form"
              onsubmit={(e) => {
                e.preventDefault()
                act(r.device, () => api.approveRegistration(r.device, codes[r.device] ?? ''))
              }}
            >
              <label>
                Birimin ekranındaki kod
                <input bind:value={codes[r.device]} placeholder="XXXX-XXXX" autocomplete="off" required />
              </label>
              <div class="actions">
                <button class="primary" disabled={busy === r.device}>Onayla</button>
                <button type="button" disabled={busy === r.device} onclick={() => act(r.device, () => api.rejectRegistration(r.device))}>Reddet</button>
              </div>
            </form>
          {:else if r.status === 'approved'}
            <div class="actions">
              <button disabled={busy === r.device} onclick={() => act(r.device, () => api.revokeRegistration(r.device))}>Erişimi iptal et</button>
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>
