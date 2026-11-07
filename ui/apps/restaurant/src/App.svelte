<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Restaurant screen: sign-in and shift (shared), floor and tables, check and
  // menu with images, sending to the kitchen, the bill, payment and splitting the bill. A change made in another
  // terminal is refreshed here too through the event stream.
  import { onMount } from 'svelte'
  import { api, hasToken, onAuthLost, setToken, watchChanges, watchDevice } from './lib/api.js'
  import { methodName, money } from '@abarow/core/lib/format.js'
  import CashDialog from '@abarow/core/CashDialog.svelte'
  import Login from '@abarow/core/Login.svelte'
  import Payment from '@abarow/core/Payment.svelte'
  import Reports from '@abarow/core/Reports.svelte'
  import ShiftClose from '@abarow/core/ShiftClose.svelte'
  import ShiftGate from '@abarow/core/ShiftGate.svelte'
  import Floor from './Floor.svelte'
  import FloorEditor from './FloorEditor.svelte'
  import OrderPanel from './OrderPanel.svelte'
  import MenuPanel from './MenuPanel.svelte'

  let config = $state({})
  let cashier = $state(null)
  let loginNotice = $state('')
  let shift = $state(null)
  let closingShift = $state(false)
  let cashOpen = $state(false)
  let reports = $state(false)
  let editingFloor = $state(false)
  let areas = $state([])
  let categories = $state([])
  let order = $state(null) // açık adisyon (masa seçildiyse)
  let sale = $state(null) // ödeme penceresindeki satış
  let done = $state(null) // tamamlanan satış
  let unfinished = $state([]) // ödemesi alınmış, fişi basılamamış satışlar
  let error = $state('')
  let busy = $state(false)
  let printer = $state(null)

  const onShift = $derived(!!cashier && shift?.cashier_id === cashier.id)
  const isManager = $derived(cashier?.role === 'manager')
  const tables = $derived(areas.flatMap((a) => a.tables))
  const tableName = (id) => tables.find((t) => t.id === id)?.name ?? id
  let printerWarning = $derived(
    !cashier || !printer?.message
      ? ''
      : printer.state === 'paper_near_end'
        ? `${printer.message}: yeni rulo hazırlayın.`
        : `${printer.message}: fiş basılamaz.`,
  )

  onAuthLost(() => {
    reset()
    loginNotice = 'Oturum sona erdi; yeniden giriş yapın.'
  })

  onMount(async () => {
    await run(async () => (config = await api.config()))
    if (hasToken()) {
      try {
        start(await api.me())
      } catch {
        setToken('')
      }
    }
  })

  // The printer's state (paper out ...) shows at the top on every screen.
  $effect(() => {
    if (!cashier) return
    const stop = new AbortController()
    watchDevice({ onScan: () => {}, onPrinter: (p) => (printer = p) }, stop.signal)
    return () => {
      stop.abort()
      printer = null
    }
  })

  // Change stream: the floor and the open check are refreshed.
  $effect(() => {
    if (!onShift) return
    const stop = new AbortController()
    watchChanges((e) => {
      if (!e || e.type === 'floor' || e.type === 'order') refreshFloor()
      if (order && (!e || e.order_id === order.id) && !sale) refreshOrder()
    }, stop.signal)
    return () => stop.abort()
  })

  function reset() {
    cashier = shift = order = sale = done = null
    closingShift = cashOpen = reports = editingFloor = false
  }

  async function start(c) {
    cashier = c
    loginNotice = ''
    await run(async () => (shift = (await api.shift()).shift))
    if (onShift) openFloor()
  }

  function openFloor() {
    run(async () => {
      ;[areas, categories] = await Promise.all([api.floor(), api.menu()])
      unfinished = await api.sales('charged').catch(() => unfinished)
    })
  }

  async function refreshFloor() {
    areas = await api.floor().catch(() => areas)
  }

  async function refreshOrder() {
    if (!order) return
    const o = await api.order(order.id).catch(() => null)
    if (o) order = o.closed ? null : o
  }

  async function run(fn) {
    busy = true
    error = ''
    try {
      await fn()
    } catch (e) {
      error = e.message
      if (e.status === 409 && order) await refreshOrder()
    } finally {
      busy = false
    }
  }

  function loggedIn(session) {
    setToken(session.token)
    start(session.cashier)
  }

  async function logout() {
    await api.logout().catch(() => {})
    setToken('')
    reset()
  }

  function shiftClosed(report) {
    closingShift = false
    shift = null
    order = null
    if (report.shift.cashier_id === cashier.id) logout()
  }

  const openTable = (t) =>
    run(async () => {
      order = t.order ? await api.order(t.order.id) : await api.openTable(t.id)
    })

  // Payment: the selected lines (all if none) are turned into a sale, the payment window opens.
  const pay = (parts = []) =>
    run(async () => {
      const r = await api.payOrder(order.id, order.version, parts)
      order = r.order
      sale = await api.sale(r.sale_id)
    })

  // When a check with a payment in progress is opened the payment window opens where it left off.
  $effect(() => {
    if (order?.payment && !sale && !done) run(async () => (sale = await api.sale(order.payment.sale_id)))
  })

  async function completed(s) {
    done = s
    sale = null
    await run(async () => {
      const o = await api.settle(order.id)
      order = o.closed ? null : o
      await refreshFloor()
    })
  }

  // A sale whose payment has not been taken is cancelled; the lines return to the check.
  const cancelPayment = () =>
    run(async () => {
      await api.voidSale(sale.id)
      sale = null
      order = await api.settle(order.id)
    })

  function dismissSale() {
    sale = null
    run(async () => (unfinished = await api.sales('charged')))
  }
</script>

{#if config.fiscal_mode === 'simulator'}
  <div class="banner" role="alert">SİMÜLATÖR MODU — Basılan fişlerin mali değeri yoktur. Canlı satışta kullanmayın.</div>
{/if}
{#if printerWarning}
  <div class="banner printer" class:soon={printer.state === 'paper_near_end'} role="alert">Fiş yazıcısı: {printerWarning}</div>
{/if}

{#if !cashier}
  <Login onlogin={loggedIn} notice={loginNotice} />
{:else if !onShift}
  <ShiftGate
    {cashier}
    {shift}
    notice={error}
    onopened={(s) => {
      shift = s
      openFloor()
    }}
    oncash={() => (cashOpen = true)}
    oncloseshift={() => (closingShift = true)}
    onreports={() => (reports = true)}
    onlogout={logout}
  />
{:else}
  <main class="restaurant" style:--bars={(config.fiscal_mode === 'simulator' ? 1 : 0) + (printerWarning ? 1 : 0)}>
    <header class="top">
      <h1>{order ? tableName(order.table_id) : 'Masalar'}</h1>
      {#if order}
        <button class="tool" onclick={() => (order = null)} disabled={busy}>← Masalar</button>
      {/if}
      <span class="who">{cashier.name}</span>
      {#if !order}
        <button class="tool" onclick={() => (editingFloor = true)} disabled={busy}>Salon düzeni</button>
      {/if}
      <button class="tool" onclick={() => (reports = true)} disabled={busy}>Raporlar</button>
      <button class="tool" onclick={() => (cashOpen = true)} disabled={busy || !!sale}>Kasa</button>
      <button class="tool" onclick={logout} disabled={busy || !!sale}>Çıkış</button>
    </header>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    {#if unfinished.length > 0 && !order && !sale}
      <p class="warning unfinished" role="alert">
        Ödemesi alınmış ama fişi basılamamış {unfinished.length} satış var ({money(unfinished[0].total_kurus)}). Müşteriden
        tekrar ödeme almayın.
        <button onclick={() => (sale = unfinished[0])} disabled={busy}>Fişi tamamla</button>
      </p>
    {/if}

    {#if !order}
      <Floor {areas} {busy} onopen={openTable} />
    {:else}
      <div class="order-view">
        <OrderPanel
          {order}
          {areas}
          {isManager}
          {busy}
          tableName={tableName(order.table_id)}
          onchange={(o) => (order = o)}
          onerror={(m) => (error = m)}
          onpay={pay}
          onclosed={() => {
            order = null
            refreshFloor()
          }}
        />
        <MenuPanel
          {categories}
          {order}
          onadded={(o) => (order = o)}
          onerror={(m) => (error = m)}
        />
      </div>
    {/if}
  </main>
{/if}

{#if sale}
  <Payment {sale} {isManager} oncompleted={completed} oncancel={cancelPayment} ondismiss={dismissSale} />
{/if}

{#if editingFloor}
  <FloorEditor
    {areas}
    {isManager}
    onsaved={(a) => {
      areas = a
      editingFloor = false
    }}
    oncancel={() => (editingFloor = false)}
  />
{/if}

{#if cashOpen}
  <CashDialog
    {shift}
    {cashier}
    canClose={!order}
    onclose={() => (cashOpen = false)}
    onshiftclose={() => {
      cashOpen = false
      closingShift = true
    }}
  />
{/if}

{#if closingShift}
  <ShiftClose
    {shift}
    needsApproval={shift.cashier_id !== cashier.id && cashier.role !== 'manager'}
    onclosed={shiftClosed}
    oncancel={() => (closingShift = false)}
  />
{/if}

{#if reports}
  <Reports canCloseDay={isManager} onclose={() => (reports = false)} />
{/if}

{#if done}
  <div class="overlay">
    <div class="dialog done">
      <h2>Ödeme alındı</h2>
      <dl>
        <dt>Fiş no</dt>
        <dd>{done.receipt_no}</dd>
        <dt>Mali no</dt>
        <dd>{done.fiscal_no}</dd>
        <dt>Toplam</dt>
        <dd>{money(done.total_kurus)}</dd>
        {#each done.payments as p, i (i)}
          <dt>{methodName(p.method)}</dt>
          <dd>{money(p.amount_kurus)}</dd>
        {/each}
      </dl>
      {#if done.change_kurus > 0}
        <p class="change">Para üstü <strong>{money(done.change_kurus)}</strong></p>
      {/if}
      <button class="primary" onclick={() => (done = null)}>{order ? 'Adisyona dön' : 'Masalara dön'}</button>
    </div>
  </div>
{/if}
