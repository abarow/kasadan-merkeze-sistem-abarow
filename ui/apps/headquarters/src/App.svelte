<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { api } from './lib/api.js'
  import { addDays, monthStart, roleName } from './lib/format.js'
  import CorporateIdentity from '@abarow/admin/CorporateIdentity.svelte'
  import { oidcNotice } from '@abarow/admin/lib/oidc.js'
  import Login from './Login.svelte'
  import Password from './Password.svelte'
  import Products from '@abarow/admin/Products.svelte'
  import Stock from './Stock.svelte'
  import Stores from './Stores.svelte'
  import Summary from './Summary.svelte'
  import Update from './Update.svelte'
  import Users from './Users.svelte'

  // If the period includes today the screen refreshes itself; a past period does not change.
  const REFRESH_MS = 30000

  // Session: null unknown, false sign-in required, object open (the user:
  // their role and the stores they see). If the session expires (401) it
  // returns to the sign-in screen.
  let user = $state(null)
  let authError = $state('')
  // Whether the corporate sign-in (OIDC) is installed: if not the endpoint returns 404 and the section
  // is not shown.
  let corporate = $state(false)
  // Screen: reports, products (catalog management) or users
  // (administrator only).
  let view = $state('reports')
  let admin = $derived(user?.role === 'admin')
  // The administrator and the catalog user change products and prices; the report
  // user only reads (the server rejects it too).
  let canEdit = $derived(user?.role === 'admin' || user?.role === 'catalog')
  // An empty from/to is today (in store time, the server determines it); an empty store is
  // all the stores.
  let period = $state({ from: '', to: '', store: '' })
  let today = $state('')
  let stores = $state(null)
  let summary = $state(null)
  let storesError = $state('')
  let summaryError = $state('')
  let updated = $state(null)

  api
    .session()
    .then((u) => {
      user = u
      // On return from the IdP (the identity was linked or could not be linked) the result is
      // shown in the corporate identity section.
      if (oidcNotice.linked || oidcNotice.error) view = 'corporate'
    })
    .catch((e) => {
      if (e.status === 401) user = false
      else authError = e.message
    })

  $effect(() => {
    if (!user) {
      corporate = false
      return
    }
    api
      .oidcStatus()
      .then(() => (corporate = true))
      .catch(() => (corporate = false))
  })

  const message = (r) => (r.status === 'rejected' ? r.reason.message : '')
  let storeNames = $derived(Object.fromEntries((stores ?? []).map((s) => [s.id, s.name])))
  let live = $derived(period.to === '' || (today !== '' && period.to >= today))

  // The stores and the summary are loaded separately: if one cannot be read the other is still shown.
  async function refresh(p) {
    const [st, sum] = await Promise.allSettled([api.stores(), api.summary(p)])
    if ([st, sum].some((r) => r.status === 'rejected' && r.reason.status === 401)) {
      user = false
      return
    }
    storesError = message(st)
    if (st.status === 'fulfilled') {
      stores = st.value.stores
      today = st.value.today
    }
    summaryError = message(sum)
    if (sum.status === 'fulfilled') summary = sum.value
    updated = new Date()
  }

  $effect(() => {
    if (!user || view !== 'reports') return
    const p = { ...period }
    refresh(p)
    if (!live) return
    const timer = setInterval(() => refresh(p), REFRESH_MS)
    return () => clearInterval(timer)
  })

  // The ready-made periods are by the server's "today".
  const presets = [
    { name: 'Bugün', range: () => ['', ''] },
    { name: 'Dün', range: (t) => [addDays(t, -1), addDays(t, -1)] },
    { name: 'Son 7 gün', range: (t) => [addDays(t, -6), t] },
    { name: 'Bu ay', range: (t) => [monthStart(t), t] },
  ]
  function choose(preset) {
    const [from, to] = preset.range(today)
    period = { ...period, from, to }
  }
  function setDate(field, value) {
    const p = { ...period, [field]: value }
    // If the start is chosen after the end the period becomes a single day.
    if (p.from && p.to && p.from > p.to) {
      if (field === 'from') p.to = p.from
      else p.from = p.to
    }
    if (!p.from) p.from = p.to
    period = p
  }

  async function logout() {
    await api.logout().catch(() => {})
    stores = null
    summary = null
    period = { from: '', to: '', store: '' }
    view = 'reports'
    user = false
  }
</script>

{#if user === false}
  <Login onlogin={(u) => (user = u)} />
{:else if user}
  <header class="top">
    <div>
      <h1>Merkez</h1>
      <span class="muted">abarow merkez</span>
    </div>
    <nav class="tabs" aria-label="Bölümler">
      <button aria-current={view === 'reports' ? 'page' : undefined} onclick={() => (view = 'reports')}>Raporlar</button>
      <button aria-current={view === 'products' ? 'page' : undefined} onclick={() => (view = 'products')}>Ürünler</button>
      <button aria-current={view === 'stock' ? 'page' : undefined} onclick={() => (view = 'stock')}>Stok</button>
      {#if admin}
        <button aria-current={view === 'update' ? 'page' : undefined} onclick={() => (view = 'update')}>Güncelleme</button>
        <button aria-current={view === 'users' ? 'page' : undefined} onclick={() => (view = 'users')}>Kullanıcılar</button>
      {/if}
    </nav>
    <div class="controls">
      <span class="muted" data-testid="user">{user.name} · {roleName(user.role)}</span>
      <button aria-current={view === 'password' ? 'page' : undefined} onclick={() => (view = 'password')}>Parolam</button>
      {#if corporate}
        <button aria-current={view === 'corporate' ? 'page' : undefined} onclick={() => (view = 'corporate')}>Kurumsal kimlik</button>
      {/if}
      <button onclick={logout}>Çıkış</button>
    </div>
  </header>

  {#if view === 'products'}
    <main>
      <Products {api} {canEdit} onunauthorized={() => (user = false)} />
    </main>
  {:else if view === 'stock'}
    <main>
      <Stock {storeNames} onunauthorized={() => (user = false)} />
    </main>
  {:else if view === 'password'}
    <main>
      <Password onunauthorized={() => (user = false)} />
    </main>
  {:else if view === 'corporate'}
    <main>
      <CorporateIdentity status={api.oidcStatus} unlink={api.oidcUnlink} notice={oidcNotice} onunauthorized={() => (user = false)} />
    </main>
  {:else if view === 'update' && admin}
    <main>
      <Update onunauthorized={() => (user = false)} />
    </main>
  {:else if view === 'users' && admin}
    <main>
      <Users me={user.username} {corporate} onunauthorized={() => (user = false)} />
    </main>
  {:else}
  <main>
    <Stores {stores} error={storesError} />

    <div class="filters" role="group" aria-label="Dönem ve mağaza">
      <label>
        Başlangıç
        <input type="date" value={summary?.from ?? ''} max={today} onchange={(e) => setDate('from', e.currentTarget.value)} />
      </label>
      <label>
        Bitiş
        <input type="date" value={summary?.to ?? ''} max={today} onchange={(e) => setDate('to', e.currentTarget.value)} />
      </label>
      <label>
        Mağaza
        <select aria-label="Mağaza" value={period.store} onchange={(e) => (period = { ...period, store: e.currentTarget.value })}>
          <option value="">{user.all_stores ? 'Bütün mağazalar' : 'Mağazalarımın hepsi'}</option>
          {#each stores ?? [] as s (s.id)}
            <option value={s.id}>{s.name}</option>
          {/each}
        </select>
      </label>
      <div class="presets">
        {#each presets as p (p.name)}
          <button onclick={() => choose(p)} disabled={today === ''}>{p.name}</button>
        {/each}
      </div>
    </div>

    <Summary {summary} error={summaryError} {storeNames} />
  </main>
  {/if}

  {#if view === 'reports'}
  <footer class="muted">
    <p>
      Bu rapor bilgi amaçlıdır, mali değeri yoktur. Her kasanın gün sonu (Z) raporu kasada alınır. Satışlar mağazadan
      merkeze ulaştıkça rapora girer; bağlantısı kesik mağazanın son satışları henüz görünmeyebilir.
    </p>
    {#if updated}
      <p>
        Son güncelleme {updated.toLocaleTimeString('tr-TR')}{#if live}{` · bugünü içeren dönem ${REFRESH_MS / 1000} saniyede bir tazelenir`}{/if}
      </p>
    {/if}
  </footer>
  {/if}
{:else if authError}
  <main><p class="alert" role="alert">Merkeze ulaşılamadı: {authError}</p></main>
{/if}
