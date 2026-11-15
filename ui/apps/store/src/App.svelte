<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { api } from './lib/api.js'
  import Cashiers from './Cashiers.svelte'
  import Catalog from './Catalog.svelte'
  import UpdateStatus from '@abarow/admin/UpdateStatus.svelte'
  import CorporateIdentity from '@abarow/admin/CorporateIdentity.svelte'
  import { oidcNotice } from '@abarow/admin/lib/oidc.js'
  import DayReport from './DayReport.svelte'
  import Login from './Login.svelte'
  import Prices from './Prices.svelte'
  import Registers from './Registers.svelte'
  import Stock from './Stock.svelte'

  // An empty date is today (in store time, the server determines it). While today
  // is shown the screen refreshes itself; a past day does not change.
  const REFRESH_MS = 15000

  // Session: null unknown, false sign-in required, true open. If the session expires
  // or the service restarts (401) it returns to the sign-in screen.
  let authed = $state(null)
  // The person who signed in: {username, name, role, ...}; username is empty for one who signed in with the store password.
  let who = $state(null)
  // Whether the corporate sign-in (OIDC) is installed: if not the endpoint returns 404 and the section
  // is not shown.
  let corporate = $state(false)
  // Screen: reports, store prices or cashiers.
  let view = $state('reports')
  let authError = $state('')
  let date = $state('')
  let registers = $state(null)
  let report = $state(null)
  let registersError = $state('')
  let reportError = $state('')
  let updated = $state(null)

  api
    .session()
    .then((w) => {
      signedIn(w)
      // On return from the IdP (the identity was linked or could not be linked) the result is
      // shown in the corporate identity section.
      if (oidcNotice.linked || oidcNotice.error) view = 'corporate'
    })
    .catch((e) => {
      if (e.status === 401) authed = false
      else authError = e.message
    })

  function signedIn(w) {
    who = w
    authed = true
  }

  $effect(() => {
    if (!authed) {
      corporate = false
      return
    }
    api
      .oidcStatus()
      .then(() => (corporate = true))
      .catch(() => (corporate = false))
  })

  const message = (r) => (r.status === 'rejected' ? r.reason.message : '')

  // The registers' state and the report are loaded separately: if one cannot be read (e.g. the
  // message path is down) the other is still shown.
  async function refresh(day) {
    const [regs, rep] = await Promise.allSettled([api.registers(), api.day(day)])
    if ([regs, rep].some((r) => r.status === 'rejected' && r.reason.status === 401)) {
      authed = false
      return
    }
    registersError = message(regs)
    if (regs.status === 'fulfilled') registers = regs.value
    reportError = message(rep)
    if (rep.status === 'fulfilled') report = rep.value
    updated = new Date()
  }

  $effect(() => {
    if (!authed || view !== 'reports') return
    const day = date
    refresh(day)
    if (day !== '') return
    const timer = setInterval(() => refresh(day), REFRESH_MS)
    return () => clearInterval(timer)
  })

  async function logout() {
    await api.logout().catch(() => {})
    registers = null
    report = null
    authed = false
  }
</script>

{#if authed === false}
  <Login onlogin={signedIn} />
{:else if authed}
  <header class="top">
    <div>
      <h1>Mağaza</h1>
      <span class="muted">abarow operasyon</span>
    </div>
    <nav class="tabs" aria-label="Bölümler">
      <button aria-current={view === 'reports' ? 'page' : undefined} onclick={() => (view = 'reports')}>Raporlar</button>
      <button aria-current={view === 'catalog' ? 'page' : undefined} onclick={() => (view = 'catalog')}>Ürünler</button>
      <button aria-current={view === 'prices' ? 'page' : undefined} onclick={() => (view = 'prices')}>Fiyatlar</button>
      <button aria-current={view === 'cashiers' ? 'page' : undefined} onclick={() => (view = 'cashiers')}>Kasiyerler</button>
      <button aria-current={view === 'stock' ? 'page' : undefined} onclick={() => (view = 'stock')}>Stok</button>
      <button aria-current={view === 'update' ? 'page' : undefined} onclick={() => (view = 'update')}>Güncelleme</button>
    </nav>
    <div class="controls">
      {#if view === 'reports'}
        <label>
          Gün
          <input type="date" value={report?.from ?? ''} onchange={(e) => (date = e.currentTarget.value)} />
        </label>
        <button onclick={() => (date = '')} disabled={date === ''}>Bugün</button>
      {/if}
      {#if who}
        <span class="who" data-testid="who">{who.username ? who.name : 'Mağaza parolasıyla girildi'}</span>
      {/if}
      {#if corporate}
        <button aria-current={view === 'corporate' ? 'page' : undefined} onclick={() => (view = 'corporate')}>Kurumsal kimlik</button>
      {/if}
      <button onclick={logout}>Çıkış</button>
    </div>
  </header>

  {#if view === 'catalog'}
    <main>
      <Catalog onunauthorized={() => (authed = false)} />
    </main>
  {:else if view === 'prices'}
    <main>
      <Prices onunauthorized={() => (authed = false)} />
    </main>
  {:else if view === 'cashiers'}
    <main>
      <Cashiers me={who?.username ?? ''} {corporate} onunauthorized={() => (authed = false)} />
    </main>
  {:else if view === 'corporate'}
    <main>
      <CorporateIdentity
        status={api.oidcStatus}
        unlink={api.oidcUnlink}
        notice={oidcNotice}
        personal={!!who?.username}
        onunauthorized={() => (authed = false)}
      />
    </main>
  {:else if view === 'stock'}
    <main>
      <Stock onunauthorized={() => (authed = false)} />
    </main>
  {:else if view === 'update'}
    <main>
      <UpdateStatus fetchStatus={api.updateStatus} act={api.updateAct} onunauthorized={() => (authed = false)} />
    </main>
  {:else}
  <main>
    <Registers {registers} error={registersError} />
    <DayReport {report} error={reportError} />
  </main>

  <footer class="muted">
    <p>
      Bu rapor bilgi amaçlıdır, mali değeri yoktur. Her kasanın gün sonu (Z) raporu kasada alınır.
    </p>
    {#if updated}
      <p>
        Son güncelleme {updated.toLocaleTimeString('tr-TR')}{#if date === ''}{` · bugün gösterilirken ${REFRESH_MS / 1000} saniyede bir tazelenir`}{/if}
      </p>
    {/if}
  </footer>
  {/if}
{:else if authError}
  <main><p class="alert" role="alert">Operasyona ulaşılamadı: {authError}</p></main>
{/if}
