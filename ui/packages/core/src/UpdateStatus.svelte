<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { onMount } from 'svelte'
  import { api } from './lib/api.js'
  import ManagerApproval from './ManagerApproval.svelte'

  // The state of this register's update agent. In order to pull the publisher's images the
  // register registers with the store server; the manager enters the code here when approving
  // from the store server (and from headquarters). When a new version comes it is announced here;
  // it is not installed by itself, it is installed with manager approval (a manager session or manager PIN).
  let { onback } = $props()

  const REFRESH_MS = 5000
  const states = {
    starting: 'Başlıyor',
    pending: 'Onay bekliyor',
    approved: 'Onaylı',
    rejected: 'Reddedildi',
    revoked: 'İptal edildi',
  }
  const phases = { available: 'Hazır', downloading: 'İndiriliyor', applying: 'Uygulanıyor' }

  let st = $state(null)
  let missing = $state(false)
  let error = $state('')
  let actError = $state('')
  let busy = $state(false)
  let approving = $state(null) // müdür onayı bekleyen işlem: {op, body}

  async function load() {
    try {
      st = await api.updateStatus()
      missing = false
      error = ''
    } catch (e) {
      if (e.status === 404) missing = true
      else error = e.message
    }
  }

  onMount(() => {
    load()
    const t = setInterval(load, REFRESH_MS)
    return () => clearInterval(t)
  })

  // A cashier in a manager session approves directly; otherwise (403) the manager PIN is asked for.
  async function run(op, body, token) {
    busy = true
    actError = ''
    try {
      await api.updateAct(op, body, token)
      approving = null
      await load()
    } catch (e) {
      if (e.status === 403 && !token) approving = { op, body }
      else actError = e.message
    } finally {
      busy = false
    }
  }

  const time = (iso) => (iso ? new Date(iso).toLocaleString('tr-TR') : '—')
  const age = (iso) => {
    const h = Math.floor((Date.now() - new Date(iso).getTime()) / 3_600_000)
    if (h < 1) return 'az önce geldi'
    if (h < 48) return `${h} saattir bekliyor`
    return `${Math.floor(h / 24)} gündür bekliyor`
  }
</script>

<h2>Güncelleme</h2>
{#if missing}
  <p data-testid="update-missing">Güncelleme bu kasada kurulu değil.</p>
{:else if error}
  <p class="error" role="alert">Durum okunamadı: {error}</p>
{:else if st}
  <dl data-testid="update-status">
    <dt>Durum</dt>
    <dd>{states[st.state] ?? st.state}</dd>
    <dt>Cihaz kimliği</dt>
    <dd data-testid="update-device">{st.device}</dd>
    {#if st.state === 'approved' && st.token_expiry}
      <dt>Erişim süresi</dt>
      <dd>{time(st.token_expiry)}</dd>
    {/if}
  </dl>
  {#if st.state === 'pending'}
    <p>Bu kasanın onaylanması için onaylayan kişiye bu kodu söyleyin:</p>
    <p data-testid="update-code" style="font-size: 2.5rem; font-weight: 700; letter-spacing: 0.1em; text-align: center">{st.code}</p>
  {:else if st.state === 'rejected' || st.state === 'revoked'}
    <p class="error" role="alert">Bu kasanın güncelleme erişimi {st.state === 'rejected' ? 'reddedildi' : 'iptal edildi'}; müdüre başvurun.</p>
  {/if}
  {#if st.error && st.state !== 'rejected' && st.state !== 'revoked'}<p>Son hata: {st.error}</p>{/if}

  {#if st.update}
    {@const u = st.update}
    {#if u.phase && u.phase !== 'none' && u.phase !== 'rolled_back'}
      <div data-testid="update-offer">
        <h3>Yeni sürüm: {u.version} <small data-testid="update-phase">· {phases[u.phase] ?? u.phase}</small></h3>
        {#if u.urgency === 'security'}<p data-testid="update-urgency"><strong>Güvenlik yaması</strong></p>{/if}
        {#if u.first_seen}<p class="muted">{age(u.first_seen)}</p>{/if}
        {#if u.notes}<p data-testid="update-notes">{u.notes}</p>{/if}
        <p class="muted">Kanal imzası doğrulandı{u.downloaded ? ' · imajlar önceden indirildi' : ''}</p>
        {#if u.blocked}<p class="error" role="alert" data-testid="update-blocked">Şu an uygulanamaz: {u.blocked}</p>{/if}
        {#if actError}<p class="error" role="alert" data-testid="update-error">{actError}</p>{/if}
        {#if u.phase === 'available'}
          <button class="primary" disabled={busy || !!u.blocked} onclick={() => run('apply', { seq: u.seq })}>Şimdi uygula</button>
          <button class="secondary" disabled={busy} onclick={() => run('snooze', { minutes: 240 })}>Sonra hatırlat</button>
          <button class="secondary" disabled={busy} onclick={() => run('skip', {})}>Bu sürümü atla</button>
        {:else}
          <p class="muted">İşlem sürüyor; sağlıklı açılmazsa önceki sürüme kendiliğinden dönülür.</p>
        {/if}
      </div>
    {/if}
    {#if u.error}<p class="muted" data-testid="update-poll-error">Kanal yoklaması: {u.error}</p>{/if}
    {#if u.last && u.last.result === 'rolled_back'}
      <p class="error" role="alert" data-testid="update-rolled-back">
        {u.last.version} sürümüne güncelleme başarısız oldu ve önceki sürüme geri alındı: {u.last.detail}
      </p>
    {:else if u.last && u.last.result === 'applied'}
      <p class="muted" data-testid="update-applied">Son güncelleme: {u.last.version} uygulandı ({u.last.by}, {time(u.last.time)}).</p>
    {/if}
  {/if}
{/if}
<button class="secondary" onclick={onback}>Geri</button>

{#if approving}
  <ManagerApproval
    action="apply_update"
    what="Güncelleme"
    onapproved={(token) => run(approving.op, approving.body, token)}
    oncancel={() => (approving = null)}
  />
{/if}
