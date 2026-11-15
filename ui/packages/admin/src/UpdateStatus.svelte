<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { onMount } from 'svelte'

  // The state of this unit's update agent. In order to pull images from the publisher the unit
  // registers with the unit above; this
  // ekrandan okursunuz. fetchStatus: () => Promise<{state, device, code, update, ...}>.
  // The new version announcement is in the update field; if act is given (for an authorized person)
  // the approve, postpone and skip buttons appear: act(op, body) op: apply, snooze, skip.
  let { fetchStatus, onunauthorized, act = null } = $props()

  const REFRESH_MS = 5000
  const states = {
    starting: { text: 'Başlıyor', css: 'off' },
    pending: { text: 'Onay bekliyor', css: 'off' },
    approved: { text: 'Onaylı', css: 'ok' },
    rejected: { text: 'Reddedildi', css: 'bad' },
    revoked: { text: 'İptal edildi', css: 'bad' },
  }

  let st = $state(null)
  let missing = $state(false)
  let error = $state('')
  let actError = $state('')
  let busy = $state(false)

  const phases = {
    available: { text: 'Hazır', css: 'ok' },
    downloading: { text: 'İndiriliyor', css: 'off' },
    applying: { text: 'Uygulanıyor', css: 'off' },
  }
  const age = (iso) => {
    const ms = Date.now() - new Date(iso).getTime()
    const h = Math.floor(ms / 3_600_000)
    if (h < 1) return 'az önce geldi'
    if (h < 48) return `${h} saattir bekliyor`
    return `${Math.floor(h / 24)} gündür bekliyor`
  }
  const when = (iso) => new Date(iso).toLocaleString('tr-TR')

  async function run(op, body) {
    busy = true
    actError = ''
    try {
      await act(op, body)
      await load()
    } catch (err) {
      if (err.status === 401) return onunauthorized?.()
      actError = err.message.charAt(0).toLocaleUpperCase('tr-TR') + err.message.slice(1)
    } finally {
      busy = false
    }
  }

  async function load() {
    try {
      st = await fetchStatus()
      missing = false
      error = ''
    } catch (err) {
      if (err.status === 401) return onunauthorized?.()
      if (err.status === 404) {
        missing = true
        error = ''
      } else {
        error = err.message
      }
    }
  }

  onMount(() => {
    load()
    const t = setInterval(load, REFRESH_MS)
    return () => clearInterval(t)
  })
</script>

<section aria-labelledby="update-durumu">
  <h2 id="update-durumu">Güncelleme</h2>
  {#if missing}
    <p class="muted" data-testid="update-missing">Güncelleme bu birimde kurulu değil.</p>
  {:else if error}
    <p class="alert" role="alert">Güncelleme durumu okunamadı: {error}</p>
  {:else if st}
    {@const s = states[st.state] ?? { text: st.state, css: 'off' }}
    <div class="card" data-testid="update-status">
      <div class="card-title">
        <strong>Bu birim</strong>
        <span class="pill {s.css}">{s.text}</span>
      </div>
      <dl>
        <dt>Cihaz kimliği</dt>
        <dd data-testid="update-device">{st.device}</dd>
        <dt>Güncelleme seviyesi</dt>
        <dd>{st.level}</dd>
        {#if st.state === 'approved' && st.token_expiry}
          <dt>Erişim süresi</dt>
          <dd>{when(st.token_expiry)} (süre dolmadan kendiliğinden yenilenir)</dd>
        {/if}
      </dl>
      {#if st.state === 'pending'}
        <p class="note">
          Yayıncının imajlarını çekebilmek için bu birimin üst birimde onaylanması gerekir. Onaylayan kişiye bu kodu söyleyin:
        </p>
        <p data-testid="update-code" style="font-size: 2rem; font-weight: 700; letter-spacing: 0.1em">{st.code}</p>
      {:else if st.state === 'rejected' || st.state === 'revoked'}
        <p class="alert" role="alert">
          Bu birimin güncelleme erişimi {st.state === 'rejected' ? 'reddedildi' : 'iptal edildi'}; üst birimdeki yöneticiye başvurun.
        </p>
      {/if}
      {#if st.error && st.state !== 'rejected' && st.state !== 'revoked'}
        <p class="note">Son hata: {st.error}</p>
      {/if}
      <p class="note">Güncellemeler kendiliğinden kurulmaz; yeni sürüm geldiğinde bildirilir ve onayınızla kurulur.</p>
    </div>

    {#if st.update}
      {@const u = st.update}
      {#if u.phase && u.phase !== 'none' && u.phase !== 'rolled_back'}
        {@const ph = phases[u.phase] ?? { text: u.phase, css: 'off' }}
        <div class="card" data-testid="update-offer">
          <div class="card-title">
            <strong>Yeni sürüm: {u.version}</strong>
            <span class="pill {ph.css}" data-testid="update-phase">{ph.text}</span>
          </div>
          <dl>
            <dt>Kanal</dt>
            <dd>{u.channel}</dd>
            {#if u.urgency === 'security'}
              <dt>Aciliyet</dt>
              <dd data-testid="update-urgency">Güvenlik yaması</dd>
            {/if}
            {#if u.first_seen}
              <dt>Bekleme</dt>
              <dd>{age(u.first_seen)}</dd>
            {/if}
            <dt>Yenilenecek</dt>
            <dd>{(u.images ?? []).map((i) => i.unit.replace(/^abarow-/, '')).join(', ')}</dd>
            <dt>İmza</dt>
            <dd>Kanal imzası doğrulandı{u.downloaded ? ' · imajlar önceden indirildi' : ''}</dd>
          </dl>
          {#if u.notes}<p class="note" data-testid="update-notes">{u.notes}</p>{/if}
          {#if u.blocked}
            <p class="alert" role="alert" data-testid="update-blocked">Şu an uygulanamaz: {u.blocked}</p>
          {/if}
          {#if u.remind_at && new Date(u.remind_at) > new Date()}
            <p class="note">Hatırlatma {when(u.remind_at)} saatine ertelendi.</p>
          {/if}
          {#if actError}<p class="alert" role="alert" data-testid="update-error">{actError}</p>{/if}
          {#if act && u.phase === 'available'}
            <div class="actions">
              <button class="primary" disabled={busy || !!u.blocked} onclick={() => run('apply', { seq: u.seq })}>Şimdi uygula</button>
              <button disabled={busy} onclick={() => run('snooze', { minutes: 240 })}>Sonra hatırlat</button>
              <button disabled={busy} onclick={() => run('skip', {})}>Bu sürümü atla</button>
            </div>
          {:else if u.phase === 'applying' || u.phase === 'downloading'}
            <p class="note">İşlem sürüyor; sağlıklı açılmazsa önceki sürüme kendiliğinden dönülür.</p>
          {/if}
        </div>
      {/if}
      {#if u.error}
        <p class="note" data-testid="update-poll-error">Kanal yoklaması: {u.error}</p>
      {/if}
      {#if u.last && u.last.result === 'rolled_back'}
        <p class="alert" role="alert" data-testid="update-rolled-back">
          {u.last.version} sürümüne güncelleme başarısız oldu ve önceki sürüme geri alındı: {u.last.detail}
        </p>
      {:else if u.last && u.last.result === 'applied'}
        <p class="note" data-testid="update-applied">Son güncelleme: {u.last.version} uygulandı ({u.last.by}, {when(u.last.time)}).</p>
      {:else if u.last && u.last.result === 'skipped'}
        <p class="note">Atlanan sürüm: {u.last.version} ({u.last.by}).</p>
      {/if}
    {/if}
  {/if}
</section>
