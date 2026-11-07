<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { onMount, tick } from 'svelte'
  import { api, hasToken, onAuthLost, setToken, watchDevice } from './lib/api.js'
  import { displayPublisher } from './lib/display.js'
  import { methodName, money, quantity } from '@abarow/core/lib/format.js'
  import CashDialog from '@abarow/core/CashDialog.svelte'
  import Login from '@abarow/core/Login.svelte'
  import Payment from '@abarow/core/Payment.svelte'
  import Reports from '@abarow/core/Reports.svelte'
  import ShiftClose from '@abarow/core/ShiftClose.svelte'
  import ShiftGate from '@abarow/core/ShiftGate.svelte'

  let config = $state({})
  let cashier = $state(null) // oturumdaki kasiyer; yoksa giriş ekranı
  let loginNotice = $state('')
  let shift = $state(null) // kasadaki açık vardiya; yoksa null
  let closingShift = $state(false) // vardiya kapanış penceresi açık mı
  let cashOpen = $state(false) // kasa işlemleri penceresi açık mı
  let basket = $state(null)
  let products = $state([])
  let query = $state('')
  let barcode = $state('')
  let error = $state('')
  let busy = $state(false)
  let sale = $state(null) // ödeme paneli açıkken ödenecek satış
  let done = $state(null) // tamamlanan satış
  let reports = $state(false) // raporlar paneli açık mı
  // Sales whose payment was taken but whose receipt could not be printed; while they exist the shift
  // does not close and the end of day is not taken.
  let unfinished = $state([])
  let scanInput = $state()

  // The sales screen is shown only while the cashier's own shift is open.
  const onShift = $derived(!!cashier && shift?.cashier_id === cashier.id)

  // The state that goes to the customer screen (HDMI second screen, /customer.html): the basket,
  // the amount to pay, the paid and the change. Whatever is on the screen goes.
  const customer = displayPublisher()
  const display = $derived.by(() => {
    const base = { fiscal_mode: config.fiscal_mode, lines: [] }
    if (!onShift) return { ...base, status: 'closed' }
    if (done) {
      const paid = done.payments.reduce((sum, p) => sum + p.amount_kurus, 0)
      return { ...base, status: 'done', total_kurus: done.total_kurus, paid_kurus: paid, change_kurus: done.change_kurus }
    }
    if (lines.length === 0 && !sale) return { ...base, status: 'idle' }
    return {
      ...base,
      status: sale ? 'payment' : 'basket',
      lines: lines.map((l) => ({
        name: l.name,
        qty: `${quantity(l.qty_milli, l.weighed)} × ${money(l.unit_price_kurus)}`,
        amount_kurus: l.total_kurus,
      })),
      total_kurus: sale ? sale.total_kurus : total,
    }
  })
  $effect(() => customer.publish(display))
  const lines = $derived(basket?.lines ?? [])
  const total = $derived(basket?.total_kurus ?? 0)

  onAuthLost(() => {
    reset()
    loginNotice = 'Oturum sona erdi; yeniden giriş yapın.'
  })

  onMount(async () => {
    await run(async () => {
      config = await api.config()
    })
    if (hasToken()) {
      try {
        start(await api.me())
      } catch {
        setToken('')
      }
    }
  })

  // printer is the receipt printer's last state; if there is a problem it shows at the top of
  // every screen (paper out, cover open ...).
  let printer = $state(null)
  let printerWarning = $derived(
    !cashier || !printer?.message
      ? ''
      : printer.state === 'paper_near_end'
        ? `${printer.message}: yeni rulo hazırlayın.`
        : `${printer.message}: fiş basılamaz.`,
  )

  // The barcode reader is set aside for device at the register and sends what it reads over the stream: the code
  // goes to the basket wherever the cursor is. The printer's state comes over
  // the same stream. The stream opens when the cashier signs in and closes when they sign out.
  $effect(() => {
    if (!cashier) return
    const stop = new AbortController()
    watchDevice({ onScan: scanned, onPrinter: (p) => (printer = p) }, stop.signal)
    return () => {
      stop.abort()
      printer = null
    }
  })

  // scanned adds the code from the reader to the basket; while payment is in progress or
  // the sales screen is not open it does not add, it says so.
  function scanned(code) {
    if (!onShift || basket?.status !== 'open' || sale) {
      error = `Okutulan barkod (${code}) sepete eklenmedi: önce açık işlemi bitirin.`
      return
    }
    run(async () => (basket = await api.addBarcode(basket.id, code)))
  }

  function reset() {
    cashier = null
    shift = null
    closingShift = false
    cashOpen = false
    basket = null
    sale = null
    done = null
    reports = false
  }

  async function start(c) {
    cashier = c
    loginNotice = ''
    await run(async () => {
      shift = (await api.shift()).shift
    })
    if (onShift) openSales()
  }

  // openSales, kasiyerin son sepetinden devam eder (ekran ya da servis
  // restarted); if the basket moved on to payment and the sale is unpaid it opens
  // the payment window. If there is no basket to continue a new basket is opened.
  function openSales() {
    run(async () => {
      const current = await api.currentBasket().catch((e) => {
        if (e.status === 404) return null
        throw e
      })
      done = null
      sale = null
      if (current?.status === 'open') {
        basket = current
      } else if (current?.status === 'checked_out') {
        const s = await api.sale(current.sale_id)
        if (s.status === 'open' || s.status === 'charged') {
          basket = current
          sale = s
        } else {
          basket = await api.newBasket()
        }
      } else {
        basket = await api.newBasket()
      }
      await refreshUnfinished()
    })
    search()
  }

  function shiftOpened(s) {
    shift = s
    openSales()
  }

  // The session of a cashier who closes their own shift closes too; a manager who closes
  // someone else's shift stays on the shift screen (new shift or Z).
  function shiftClosed(report) {
    closingShift = false
    shift = null
    basket = null
    if (report.shift.cashier_id === cashier.id) logout()
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

  // run runs the request, shows the error on the screen and gives the focus
  // back to the barcode field (the reader always types there).
  async function run(fn) {
    busy = true
    error = ''
    try {
      await fn()
    } catch (e) {
      error = e.message
      // If the shift was closed elsewhere it returns to the shift screen.
      if (e.status === 409 && cashier) shift = (await api.shift().catch(() => ({ shift }))).shift
    } finally {
      busy = false
      await tick()
      if (!sale) scanInput?.focus()
    }
  }

  const newBasket = () =>
    run(async () => {
      basket = await api.newBasket()
      sale = null
      done = null
      await refreshUnfinished()
    })

  async function refreshUnfinished() {
    unfinished = await api.sales('charged').catch(() => unfinished)
  }

  // The window of a sale whose receipt could not be printed is closed; if the sale was opened from the basket
  // it moves to a new basket, the sale is completed later with "Fişi tamamla".
  function dismissSale() {
    const fromBasket = basket?.sale_id === sale?.id
    sale = null
    if (fromBasket) newBasket()
    else refreshUnfinished()
  }

  const search = () =>
    run(async () => {
      products = await api.products(query)
    })

  function scan(event) {
    event.preventDefault()
    const code = barcode.trim()
    barcode = ''
    if (code) run(async () => (basket = await api.addBarcode(basket.id, code)))
  }

  const add = (p) => run(async () => (basket = await api.addBarcode(basket.id, p.barcode)))
  const remove = (i) => run(async () => (basket = await api.removeLine(basket.id, i)))

  function step(i, line, delta) {
    const next = line.qty_milli + delta * 1000
    if (next <= 0) return remove(i)
    run(async () => (basket = await api.setQty(basket.id, i, next)))
  }

  const pay = () =>
    run(async () => {
      basket = await api.checkoutBasket(basket.id)
      sale = await api.sale(basket.sale_id)
    })

  // A sale whose payment was not taken is cancelled and the basket is opened for editing.
  const cancelPayment = () =>
    run(async () => {
      await api.voidSale(sale.id)
      basket = await api.reopenBasket(basket.id)
      sale = null
    })

  function completed(s) {
    done = s
    sale = null
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
    onopened={shiftOpened}
    oncash={() => (cashOpen = true)}
    oncloseshift={() => (closingShift = true)}
    onreports={() => (reports = true)}
    onlogout={logout}
  />
{:else}
  <main style:--bars={(config.fiscal_mode === 'simulator' ? 1 : 0) + (printerWarning ? 1 : 0)}>
    <section class="basket">
      <header>
        <h1>Sepet</h1>
        <span class="muted">{lines.length} satır</span>
        <span class="who">{cashier.name}</span>
        <button class="tool" onclick={() => (reports = true)} disabled={busy}>Raporlar</button>
        <button class="tool" onclick={() => (cashOpen = true)} disabled={busy || !!sale}>Kasa</button>
        <button class="tool" onclick={logout} disabled={busy || !!sale}>Çıkış</button>
      </header>
      {#if unfinished.length > 0 && lines.length === 0 && !sale}
        <p class="warning unfinished" role="alert">
          Ödemesi alınmış ama fişi basılamamış {unfinished.length} satış var ({money(unfinished[0].total_kurus)}). Müşteriden
          tekrar ödeme almayın.
          <button onclick={() => (sale = unfinished[0])} disabled={busy}>Fişi tamamla</button>
        </p>
      {/if}
      <ul class="lines">
        {#each lines as line, i (i)}
          <li>
            <div class="name">
              {line.name}
              <small>{quantity(line.qty_milli, line.weighed)} × {money(line.unit_price_kurus)}</small>
            </div>
            {#if !line.weighed}
              <button class="step" onclick={() => step(i, line, -1)} disabled={busy} aria-label="Azalt">−</button>
              <button class="step" onclick={() => step(i, line, 1)} disabled={busy} aria-label="Artır">+</button>
            {/if}
            <strong class="amount">{money(line.total_kurus)}</strong>
            <button class="remove" onclick={() => remove(i)} disabled={busy} aria-label="Satırı sil">✕</button>
          </li>
        {:else}
          <li class="empty">Barkod okutun ya da sağdan ürün seçin.</li>
        {/each}
      </ul>
      <footer>
        <div class="total"><span>TOPLAM</span><strong>{money(total)}</strong></div>
        <button class="primary pay" onclick={pay} disabled={busy || lines.length === 0}>ÖDE</button>
      </footer>
    </section>

    <section class="side">
      <form onsubmit={scan}>
        <input
          class="scan"
          bind:this={scanInput}
          bind:value={barcode}
          placeholder="Barkod yazın (okuyucu doğrudan sepete ekler)"
          autocomplete="off"
          inputmode="numeric"
          aria-label="Barkod"
        />
      </form>
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <form
        class="search"
        onsubmit={(e) => {
          e.preventDefault()
          search()
        }}
      >
        <input bind:value={query} placeholder="Ürün ara" aria-label="Ürün ara" />
        <button type="submit" disabled={busy}>Ara</button>
      </form>
      <div class="grid">
        {#each products as p (p.id)}
          <button class="product" onclick={() => add(p)} disabled={busy}>
            <span>{p.name}</span>
            <small>{money(p.unit_price_kurus)}{p.unit === 'kg' ? ' / kg · terazi' : ''}</small>
          </button>
        {/each}
      </div>
    </section>
  </main>
{/if}

{#if sale}
  <Payment
    {sale}
    isManager={cashier?.role === 'manager'}
    oncompleted={completed}
    oncancel={cancelPayment}
    ondismiss={dismissSale}
  />
{/if}

{#if cashOpen}
  <CashDialog
    {shift}
    {cashier}
    canClose={lines.length === 0}
    onclose={() => {
      cashOpen = false
      scanInput?.focus()
    }}
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
  <Reports
    canCloseDay={cashier?.role === 'manager'}
    onclose={() => {
      reports = false
      scanInput?.focus()
    }}
  />
{/if}

{#if done}
  <div class="overlay">
    <div class="dialog done">
      <h2>Satış tamamlandı</h2>
      <dl>
        <dt>Fiş no</dt>
        <dd>{done.receipt_no}</dd>
        <dt>Mali no</dt>
        <dd>{done.fiscal_no}</dd>
        <dt>Toplam</dt>
        <dd>{money(done.total_kurus)}</dd>
        <dt>KDV</dt>
        <dd>{money(done.tax_total_kurus)}</dd>
        {#each done.payments as p, i (i)}
          <dt>{methodName(p.method)}</dt>
          <dd>{money(p.amount_kurus)}</dd>
        {/each}
      </dl>
      {#if done.change_kurus > 0}
        <p class="change">Para üstü <strong>{money(done.change_kurus)}</strong></p>
      {/if}
      <button class="primary" onclick={newBasket}>Yeni satış</button>
    </div>
  </div>
{/if}
