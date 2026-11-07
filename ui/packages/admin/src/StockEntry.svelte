<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Stock entry: goods receipt (mode 'receipt': supplier, delivery note no, quantities
  // received), count (mode 'count': counted quantities) or sending a transfer
  // (mode 'transfer': the place to send to, the quantities sent). The product
  // is scanned with a barcode or searched by name. The store server (scope 'store', this
  // store's stock) and headquarters (scope 'headquarters', the headquarters warehouse) use the same component;
  // only the texts differ.
  //
  // api: {stockByBarcode, stockSearch, receive, count, sendTransfer}
  // (the application's API client). locations: places a transfer can be sent to
  // yerler [{id, name}].
  import { capitalize, parseQty, quantity, unitName } from './lib/format.js'

  let { api, scope = 'store', mode, locations = [], onclose, onsaved, onunauthorized } = $props()

  let supplier = $state('')
  let ref = $state('')
  let to = $state('')
  let lines = $state([]) // [{product, qty}] qty: girilen metin
  let find = $state('')
  let matches = $state([])
  let error = $state('')
  let notice = $state('')
  let busy = $state(false)

  const headquarters = $derived(scope === 'headquarters')
  const title = $derived(
    headquarters
      ? { receipt: 'Mal kabul (merkez deposu)', count: 'Sayım (merkez deposu)', transfer: 'Transfer gönder (merkez deposundan)' }[mode]
      : { receipt: 'Mal kabul', count: 'Sayım', transfer: 'Transfer gönder' }[mode],
  )

  function add(product) {
    if (lines.some((l) => l.product.product_id === product.product_id)) {
      error = `${product.name} zaten listede; miktarını değiştirin.`
      return
    }
    lines.push({ product, qty: '' })
    find = ''
    matches = []
    error = ''
  }

  // A scanned barcode is added directly; if not found it is searched by name.
  async function lookup(e) {
    e.preventDefault()
    const q = find.trim()
    if (!q) return
    try {
      add(await api.stockByBarcode(q))
    } catch (err) {
      if (err.status === 401) return onunauthorized()
      if (err.status !== 404) return (error = err.message)
      try {
        matches = await api.stockSearch(q)
        error = matches.length === 0 ? `"${q}" ile eşleşen ürün yok.` : ''
      } catch (err2) {
        if (err2.status === 401) onunauthorized()
        else error = err2.message
      }
    }
  }

  async function save(e) {
    e.preventDefault()
    const out = []
    for (const l of lines) {
      const milli = parseQty(l.qty, l.product.unit)
      if (milli === null) {
        error = `${l.product.name}: miktar geçersiz (${l.product.unit === 'kg' ? 'ör. 12,500' : 'tam sayı'}).`
        return
      }
      out.push(mode === 'count' ? { product_id: l.product.product_id, counted_milli: milli } : { product_id: l.product.product_id, qty_milli: milli })
    }
    busy = true
    error = ''
    try {
      if (mode === 'receipt') {
        await api.receive(supplier, ref, out)
        notice = `İrsaliye ${ref} stoğa girildi.`
      } else if (mode === 'transfer') {
        await api.sendTransfer(to, out)
        const name = locations.find((l) => l.id === to)?.name ?? to
        notice = headquarters
          ? `Transfer ${name} için gönderildi; merkez deposundan düştü. ${name} teslim alınca onun stoğuna girer.`
          : `Transfer ${name} için gönderildi; stoktan düştü. ${name} teslim alınca onun stoğuna girer.`
      } else {
        await api.count(ref, out)
        notice = 'Sayım işlendi; farklar stoğa yazıldı.'
      }
      lines = []
      supplier = ''
      ref = ''
      to = ''
      onsaved()
    } catch (err) {
      if (err.status === 401) onunauthorized()
      else error = capitalize(err.message)
    } finally {
      busy = false
    }
  }
</script>

<div class="panel editor" role="region" aria-label={title}>
  <div class="section-head">
    <h3>{title}</h3>
    <button onclick={onclose}>Kapat</button>
  </div>
  {#if mode === 'count'}
    <p class="note">Rafta saydığınız miktarı girin. Sistemdeki stokla fark sayım farkı olarak kaydedilir.</p>
  {:else if mode === 'transfer'}
    {#if headquarters}
      <p class="note">
        Gönderilen mal hemen merkez deposunun stoğundan düşer. Mağaza malı teslim alınca (gelen miktarı girerek) onun
        stoğuna girer; arada transfer yoldadır.
      </p>
    {:else}
      <p class="note">
        Gönderilen mal hemen bu mağazanın stoğundan düşer. Alan yer malı teslim alınca (gelen miktarı girerek) onun stoğuna
        girer; arada transfer yoldadır.
      </p>
      {#if locations.length === 0}
        <p class="alert" role="alert">Gönderilecek yer yok: yer listesi merkezden gelir (operasyon merkeze bağlı olmalı).</p>
      {/if}
    {/if}
  {/if}
  {#if error}
    <p class="alert" role="alert">{error}</p>
  {/if}
  {#if notice}
    <p class="ok-note" role="status">{notice}</p>
  {/if}

  <form class="grid-form" onsubmit={lookup}>
    <label>
      Ürün ekle
      <input bind:value={find} placeholder="barkodu okutun ya da adını yazın" aria-label="Ürün ekle" />
    </label>
    <div class="actions"><button type="submit">Bul</button></div>
  </form>
  {#if matches.length > 0}
    <ul class="matches">
      {#each matches as m (m.product_id)}
        <li><button class="link" onclick={() => add(m)}>{m.name}</button> <span class="muted">{unitName(m.unit)}</span></li>
      {/each}
    </ul>
  {/if}

  <form class="grid-form" onsubmit={save}>
    {#if mode === 'receipt'}
      <label>
        Tedarikçi
        <input bind:value={supplier} />
      </label>
      <label>
        İrsaliye no
        <input bind:value={ref} />
      </label>
    {:else if mode === 'transfer'}
      <label>
        {headquarters ? 'Gönderilecek mağaza' : 'Gönderilecek yer'}
        <select bind:value={to}>
          <option value="">Seçin</option>
          {#each locations as l (l.id)}
            <option value={l.id}>{l.name}</option>
          {/each}
        </select>
      </label>
    {:else}
      <label>
        Sayım no (isteğe bağlı)
        <input bind:value={ref} />
      </label>
    {/if}
    {#if lines.length > 0}
      <table class="entry">
        <thead>
          <tr>
            <th scope="col">Ürün</th>
            {#if mode === 'count'}<th scope="col" class="num">Sistemde</th>{/if}
            <th scope="col" class="num">{{ receipt: 'Gelen', count: 'Sayılan', transfer: 'Gönderilen' }[mode]}</th>
            <th scope="col"><span class="visually-hidden">Çıkar</span></th>
          </tr>
        </thead>
        <tbody>
          {#each lines as l, i (l.product.product_id)}
            <tr>
              <th scope="row">{l.product.name}</th>
              {#if mode === 'count'}<td class="num">{quantity(l.product.qty_milli)} {unitName(l.product.unit)}</td>{/if}
              <td class="num">
                <input class="qty" bind:value={l.qty} inputmode="decimal" aria-label={`${l.product.name} miktarı`} />
                {unitName(l.product.unit)}
              </td>
              <td><button type="button" class="link" onclick={() => lines.splice(i, 1)}>Çıkar</button></td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
    <div class="actions">
      <button type="submit" class="primary" disabled={busy || lines.length === 0}>{{ receipt: 'Stoğa gir', count: 'Sayımı işle', transfer: 'Gönder' }[mode]}</button>
    </div>
  </form>
</div>
