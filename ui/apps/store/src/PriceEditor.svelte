<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { api } from './lib/api.js'
  import { dateTime, money, moneyInput, parseMoney } from './lib/format.js'

  let { id, onclose, onsaved, onunauthorized } = $props()

  let product = $state(null)
  let history = $state([])
  let form = $state({ amount: '', when: 'now', at: '' })
  let error = $state('')
  let notice = $state('')
  let busy = $state(false)

  async function load() {
    try {
      const d = await api.price(id)
      product = d.product
      history = d.history
    } catch (e) {
      handle(e)
    }
  }

  $effect(() => {
    id
    load()
  })

  function handle(e) {
    if (e.status === 401) return onunauthorized()
    error = e.message.charAt(0).toLocaleUpperCase('tr-TR') + e.message.slice(1)
  }

  async function run(fn, message) {
    busy = true
    error = ''
    notice = ''
    try {
      await fn()
      onsaved()
      await load()
      notice = message
    } catch (e) {
      handle(e)
    } finally {
      busy = false
    }
  }

  // The date and time are chosen with this device's clock (store time). A past
  // moment counts as "immediately" at the server; so that the price does not change right away by a wrong date
  // reddedilir.
  function from() {
    if (form.when !== 'later') return ''
    if (!form.at) {
      error = 'Yürürlük tarihini ve saatini seçin.'
      return null
    }
    const at = new Date(form.at)
    if (at <= new Date()) {
      error = "Seçilen tarih ve saat geçmişte. Hemen geçerli olacaksa 'Hemen'i seçin."
      return null
    }
    return at.toISOString()
  }

  function save(e) {
    e.preventDefault()
    const price = parseMoney(form.amount)
    if (price === null) {
      error = 'Fiyat geçersiz (ör. 16,50).'
      return
    }
    const f = from()
    if (f === null) return
    run(async () => {
      await api.setPrice(id, price, f)
      form = { amount: '', when: 'now', at: '' }
    }, f ? 'İleri tarihli mağaza fiyatı kaydedildi.' : 'Mağaza fiyatı geçerli; kasalara iniyor.')
  }

  function clearPrice() {
    const f = from()
    if (f === null) return
    run(() => api.clearPrice(id, f), f ? 'Merkez fiyatına dönüş kaydedildi.' : 'Mağaza fiyatı kaldırıldı; merkez fiyatı geçerli.')
  }
</script>

<div class="panel editor" role="region" aria-label={`Fiyat: ${product?.name ?? id}`}>
  <div class="section-head">
    <h3>{product?.name ?? id}</h3>
    <button onclick={onclose}>Kapat</button>
  </div>
  {#if error}
    <p class="alert" role="alert">{error}</p>
  {/if}
  {#if notice}
    <p class="ok-note" role="status">{notice}</p>
  {/if}

  {#if product}
    <p>
      Merkez fiyatı: <strong>{money(product.headquarters_price_kurus)}</strong>
      {#each product.headquarters_upcoming as p (p.from)}<span class="muted">{` · ${money(p.unit_price_kurus)}, ${dateTime(p.from)} itibarıyla`}</span>{/each}
    </p>
    <p>
      Mağaza fiyatı:
      {#if product.store_price_kurus !== undefined}
        <strong class="different" data-testid="current-store-price">{money(product.store_price_kurus)}</strong>
      {:else}
        <span class="muted" data-testid="current-store-price">yok (merkez fiyatı geçerli)</span>
      {/if}
    </p>
    {#if product.store_upcoming.length > 0}
      <ul class="upcoming">
        {#each product.store_upcoming as p (p.from)}
          <li>
            {p.clear ? 'Merkez fiyatına dönüş' : money(p.unit_price_kurus)} · {dateTime(p.from)} itibarıyla
            <button class="link" disabled={busy} onclick={() => run(() => api.cancelPrice(id, p.from), 'İleri tarihli kayıt geri alındı.')}>Geri al</button>
          </li>
        {/each}
      </ul>
    {/if}

    <form class="grid-form" onsubmit={save}>
      <label>
        Yeni mağaza fiyatı
        <input bind:value={form.amount} inputmode="decimal" placeholder={moneyInput(product.store_price_kurus ?? product.headquarters_price_kurus)} />
      </label>
      <fieldset>
        <legend>Ne zaman</legend>
        <label class="check"><input type="radio" bind:group={form.when} value="now" /> Hemen</label>
        <label class="check"><input type="radio" bind:group={form.when} value="later" /> İleri tarihte</label>
      </fieldset>
      {#if form.when === 'later'}
        <label>
          Tarih ve saat
          <input type="datetime-local" bind:value={form.at} />
        </label>
      {/if}
      <div class="actions">
        <button class="primary" disabled={busy}>Fiyatı kaydet</button>
        {#if product.store_price_kurus !== undefined || product.store_upcoming.length > 0}
          <button type="button" disabled={busy} onclick={clearPrice}>Merkez fiyatına dön</button>
        {/if}
      </div>
    </form>

    <h4>Değişiklikler</h4>
    {#if history.length === 0}
      <p class="muted">Bu üründe mağaza fiyatı verilmedi.</p>
    {:else}
      <ul class="history">
        {#each history as c, i (i)}
          <li><span class="muted">{dateTime(c.at)} · {c.by}</span> · {c.detail}</li>
        {/each}
      </ul>
    {/if}
  {/if}
</div>
