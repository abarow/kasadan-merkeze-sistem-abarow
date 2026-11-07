<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { dateTime, money, moneyInput, parseMoney } from './lib/format.js'
  import { shrink } from './lib/image.js'
  import { fromGroups, toGroups } from './lib/options.js'
  import OptionGroups from './OptionGroups.svelte'
  import StorePrices from './StorePrices.svelte'

  // If code is '' a new product is added.
  // If canEdit is false the product is only read.
  // api: the application's API client; scope 'headquarters' (headquarters' catalog, the store
  // prices are shown too) or 'store' (the store's catalog).
  let { api, scope = 'headquarters', code, canEdit = true, onclose, onsaved, onunauthorized } = $props()

  const TAX_RATES = [0, 100, 1000, 2000]
  const ACTIONS = {
    created: 'Eklendi',
    updated: 'Bilgi değişti',
    price_scheduled: 'Fiyat',
    price_cancelled: 'Fiyat geri alındı',
    discontinued: 'Satıştan kaldırıldı',
    reactivated: 'Satışa açıldı',
    imported: 'Dosyadan yüklendi',
    image_set: 'Görsel',
    image_removed: 'Görsel kaldırıldı',
  }

  let product = $state(null)
  let history = $state([])
  let exceptions = $state([])
  let form = $state({
    code: '',
    name: '',
    unit: 'piece',
    tax_rate_bp: 2000,
    barcodes: '',
    quick_key: false,
    price: '',
    category: '',
    menu_order: 0,
    station: '',
    option_groups: [],
  })
  let priceForm = $state({ amount: '', when: 'now', at: '' })
  let error = $state('')
  let notice = $state('')
  let busy = $state(false)
  let isNew = $derived(code === '')

  const fill = (p) => {
    product = p
    form = {
      code: p.code,
      name: p.name,
      unit: p.unit,
      tax_rate_bp: p.tax_rate_bp,
      barcodes: p.barcodes.join(', '),
      quick_key: p.quick_key,
      price: '',
      category: p.category,
      menu_order: p.menu_order,
      station: p.station,
      option_groups: fromGroups(p.option_groups),
    }
  }

  async function load() {
    try {
      const [d, ex] = await Promise.all([api.product(code), api.storePrices ? api.storePrices(code) : []])
      fill(d.product)
      history = d.history
      exceptions = ex
    } catch (e) {
      handle(e)
    }
  }

  $effect(() => {
    if (code !== '') load()
  })

  function handle(e) {
    if (e.status === 401) return onunauthorized()
    const m = e.message
    error = m.charAt(0).toLocaleUpperCase('tr-TR') + m.slice(1)
    // If someone else changed it in the meantime the latest state is shown.
    if (e.status === 409 && !isNew) load()
  }

  async function run(fn, message) {
    busy = true
    error = ''
    notice = ''
    try {
      await fn()
      onsaved()
      if (!isNew) await load()
      notice = message
    } catch (e) {
      handle(e)
    } finally {
      busy = false
    }
  }

  const barcodes = () =>
    form.barcodes
      .split(/[\s,;]+/)
      .map((b) => b.trim())
      .filter(Boolean)
  const info = () => ({
    name: form.name,
    unit: form.unit,
    tax_rate_bp: Number(form.tax_rate_bp),
    barcodes: barcodes(),
    quick_key: form.quick_key,
    category: form.category,
    menu_order: Number(form.menu_order) || 0,
    station: form.station,
    option_groups: toGroups(form.option_groups),
  })

  // uploadImage shrinks the selected image and uploads it.
  async function uploadImage(e) {
    const file = e.currentTarget.files?.[0]
    e.currentTarget.value = ''
    if (!file) return
    run(async () => {
      const data = await shrink(file)
      await api.setImage(code, product.version, data)
    }, 'Görsel yüklendi; kasalara iniyor.')
  }

  function save(e) {
    e.preventDefault()
    try {
      toGroups(form.option_groups)
    } catch (err) {
      error = err.message
      return
    }
    if (isNew) {
      const price = parseMoney(form.price)
      if (price === null) {
        error = 'Fiyat geçersiz (ör. 16,50).'
        return
      }
      run(async () => {
        await api.createProduct({ code: form.code, ...info(), unit_price_kurus: price })
        onclose()
      }, 'Ürün eklendi.')
      return
    }
    run(() => api.updateProduct(code, { version: product.version, ...info() }), 'Kaydedildi; kasalara iniyor.')
  }

  function schedule(e) {
    e.preventDefault()
    const price = parseMoney(priceForm.amount)
    if (price === null) {
      error = 'Fiyat geçersiz (ör. 16,50).'
      return
    }
    let from = ''
    if (priceForm.when === 'later') {
      if (!priceForm.at) {
        error = 'Yürürlük tarihini ve saatini seçin.'
        return
      }
      // The date and time are chosen with this device's clock. A past moment counts as
      // "immediately" at the server; it is rejected so that the price does not change right away by a wrong date.
      const at = new Date(priceForm.at)
      if (at <= new Date()) {
        error = "Seçilen tarih ve saat geçmişte. Hemen geçerli olacaksa 'Hemen'i seçin."
        return
      }
      from = at.toISOString()
    }
    run(async () => {
      await api.schedulePrice(code, product.version, price, from)
      priceForm = { amount: '', when: 'now', at: '' }
    }, from ? 'İleri tarihli fiyat kaydedildi.' : 'Yeni fiyat geçerli; kasalara iniyor.')
  }

  const upcoming = $derived(product ? product.prices.filter((p) => p.from && new Date(p.from) > new Date()) : [])
</script>

<div class="panel editor" role="region" aria-label={isNew ? 'Yeni ürün' : `Ürün: ${product?.name ?? code}`}>
  <div class="section-head">
    <h3>{isNew ? 'Yeni ürün' : (product?.name ?? code)}</h3>
    <button onclick={onclose}>Kapat</button>
  </div>
  {#if error}
    <p class="alert" role="alert">{error}</p>
  {/if}
  {#if notice}
    <p class="ok-note" role="status">{notice}</p>
  {/if}

  {#if isNew || product}
    <form class="grid-form" onsubmit={save}>
      <fieldset class="contents" disabled={!canEdit}>
      <label>
        Kod
        <input bind:value={form.code} disabled={!isNew} autocapitalize="none" spellcheck="false" required />
      </label>
      <label>
        Ad
        <input bind:value={form.name} required />
      </label>
      <label>
        Birim
        <select bind:value={form.unit}>
          <option value="piece">adet</option>
          <option value="kg">kg (tartılı)</option>
        </select>
      </label>
      <label>
        KDV
        <select bind:value={form.tax_rate_bp}>
          {#each TAX_RATES as r (r)}
            <option value={r}>%{r / 100}</option>
          {/each}
        </select>
      </label>
      <label class="wide">
        Barkodlar
        <input bind:value={form.barcodes} placeholder="virgülle ayırın" spellcheck="false" />
      </label>
      <label class="check">
        <input type="checkbox" bind:checked={form.quick_key} />
        Kasada hızlı tuş
      </label>
      <label>
        Menü kategorisi
        <input bind:value={form.category} placeholder="restoranda: ör. Çorbalar" />
      </label>
      <label>
        Menü sırası
        <input type="number" bind:value={form.menu_order} />
      </label>
      <label>
        İstasyon
        <select bind:value={form.station}>
          <option value="">mutfak</option>
          <option value="bar">bar</option>
        </select>
      </label>
      <OptionGroups bind:groups={form.option_groups} />
      {#if isNew}
        <label>
          Fiyat (KDV dahil)
          <input bind:value={form.price} inputmode="decimal" placeholder="16,50" required />
        </label>
      {/if}
      </fieldset>
      {#if canEdit}
        <div class="actions">
          <button class="primary" disabled={busy}>{isNew ? 'Ekle' : 'Kaydet'}</button>
        </div>
      {/if}
    </form>
  {/if}

  {#if product}
    <h4>Fiyat</h4>
    <p>
      Şu anki fiyat: <strong data-testid="current-price">{money(product.unit_price_kurus)}</strong>
      {#if scope === 'headquarters'}<span class="muted">(merkez fiyatı; mağazalardaki farklı fiyatlar aşağıda)</span>{/if}
    </p>
    {#if upcoming.length > 0}
      <ul class="upcoming">
        {#each upcoming as p (p.from)}
          <li>
            {money(p.unit_price_kurus)} · {dateTime(p.from)} itibarıyla
            {#if canEdit}
              <button class="link" disabled={busy} onclick={() => run(() => api.cancelPrice(code, product.version, p.from), 'İleri tarihli fiyat geri alındı.')}>Geri al</button>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
    {#if canEdit}
    <form class="grid-form" onsubmit={schedule}>
      <label>
        Yeni fiyat
        <input bind:value={priceForm.amount} inputmode="decimal" placeholder={moneyInput(product.unit_price_kurus)} />
      </label>
      <fieldset>
        <legend>Ne zaman</legend>
        <label class="check"><input type="radio" bind:group={priceForm.when} value="now" /> Hemen</label>
        <label class="check"><input type="radio" bind:group={priceForm.when} value="later" /> İleri tarihte</label>
      </fieldset>
      {#if priceForm.when === 'later'}
        <label>
          Tarih ve saat
          <input type="datetime-local" bind:value={priceForm.at} />
        </label>
      {/if}
      <div class="actions">
        <button class="primary" disabled={busy}>Fiyatı kaydet</button>
      </div>
    </form>
    {/if}

    <h4>Görsel</h4>
    <div class="image-row">
      {#if product.image_sha256}
        <img class="product-image" src={api.imageURL(code, product.image_sha256)} alt={product.name} />
      {:else}
        <p class="muted">Görsel yok; restoran ekranında adın baş harfleri gösterilir.</p>
      {/if}
      {#if canEdit}
        <label class="file">
          Görsel seç
          <input type="file" accept="image/jpeg,image/png,image/webp" onchange={uploadImage} disabled={busy} />
        </label>
        {#if product.image_sha256}
          <button disabled={busy} onclick={() => run(() => api.removeImage(code, product.version), 'Görsel kaldırıldı.')}>Görseli kaldır</button>
        {/if}
      {/if}
    </div>
    <p class="muted">En çok 640 px'e küçültülür (JPEG). Restoran ekranında ürün kartında görünür.</p>

    {#if scope === 'headquarters'}
      <h4>Mağazalarda</h4>
      {#if exceptions.length === 0}
        <p class="muted">Bütün mağazalarda merkez fiyatı geçerli.</p>
      {:else}
        <StorePrices items={exceptions} product />
      {/if}
    {/if}

    <h4>Satış</h4>
    <p>
      {#if product.discontinued}
        Ürün satışta değil (kasada bulunmaz).
        {#if canEdit}
          <button disabled={busy} onclick={() => run(() => api.setDiscontinued(code, product.version, false), 'Ürün yeniden satışta.')}>Satışa aç</button>
        {/if}
      {:else}
        Ürün satışta.
        {#if canEdit}
          <button disabled={busy} onclick={() => run(() => api.setDiscontinued(code, product.version, true), 'Ürün satıştan kaldırıldı.')}>Satıştan kaldır</button>
        {/if}
      {/if}
    </p>

    <h4>Değişiklikler</h4>
    <ul class="history">
      {#each history as c, i (i)}
        <li><span class="muted">{dateTime(c.at)} · {c.by}</span> · {ACTIONS[c.action] ?? c.action}: {c.detail}</li>
      {/each}
    </ul>
  {/if}
</div>
