<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Store stock: the quantity in hand per product, goods receipt, count, transfer and
  // the product's movements (the inventory module).
  import { api } from './lib/api.js'
  import { dateTime, kindName, quantity, unitName } from './lib/format.js'
  import StockEntry from '@abarow/admin/StockEntry.svelte'
  import Transfers from '@abarow/admin/Transfers.svelte'

  let { onunauthorized } = $props()

  let query = $state('')
  let items = $state(null)
  let error = $state('')
  let entry = $state(null) // 'receipt' | 'count' | 'transfer' | 'transfers'
  let selected = $state(null) // hareketleri gösterilen ürün
  let movements = $state([])
  let here = $state('')
  let transfers = $state([])
  let locations = $state([]) // transfer gönderilebilecek yerler (bu mağaza hariç)

  const names = $derived(Object.fromEntries(locations.map((l) => [l.id, l.name])))
  const arriving = $derived(transfers.filter((t) => t.to === here && t.pending).length)

  async function loadTransfers() {
    try {
      const d = await api.transfers()
      here = d.here
      transfers = d.transfers
      locations = await api.stockLocations()
    } catch (e) {
      if (e.status === 401) onunauthorized()
      else error = e.message
    }
  }
  loadTransfers()

  async function load() {
    try {
      items = await api.stock(query)
      error = ''
    } catch (e) {
      if (e.status === 401) onunauthorized()
      else error = e.message
    }
  }

  $effect(() => {
    query
    const timer = setTimeout(load, 250)
    return () => clearTimeout(timer)
  })

  async function show(item) {
    selected = item
    try {
      movements = await api.stockMovements(item.product_id)
    } catch (e) {
      if (e.status === 401) onunauthorized()
      else error = e.message
    }
  }

  function saved() {
    load()
    loadTransfers()
    if (selected) show(selected)
  }

  const signed = (milli) => (milli > 0 ? '+' : '') + quantity(milli)
</script>

<section aria-labelledby="stok">
  <h2 id="stok">Stok</h2>
  <p class="note">
    Stok satışlar, mal kabuller, sayımlar ve transferlerle tutulur. Kasadaki satış stoğu kendiliğinden düşer; stok eksiye
    düşebilir (satış durmaz), sayımla düzeltilir.
  </p>
  <div class="toolbar">
    <label class="search">
      Ara
      <input type="search" bind:value={query} placeholder="ad ya da barkod" />
    </label>
    <button onclick={() => (entry = 'receipt')}>Mal kabul</button>
    <button onclick={() => (entry = 'count')}>Sayım</button>
    <button onclick={() => (entry = 'transfer')}>Transfer gönder</button>
    <button onclick={() => (entry = 'transfers')}>Transferler{arriving ? ` (${arriving} yolda)` : ''}</button>
  </div>
  {#if error}
    <p class="alert" role="alert">{error.charAt(0).toLocaleUpperCase('tr-TR') + error.slice(1)}</p>
  {/if}

  {#if entry === 'transfers'}
    <Transfers {api} {here} {transfers} {names} onclose={() => (entry = null)} onsaved={saved} {onunauthorized} />
  {:else if entry}
    <StockEntry {api} mode={entry} {locations} onclose={() => (entry = null)} onsaved={saved} {onunauthorized} />
  {/if}

  {#if selected}
    <div class="panel editor" role="region" aria-label={`Hareketler: ${selected.name}`}>
      <div class="section-head">
        <h3>{selected.name}</h3>
        <button onclick={() => (selected = null)}>Kapat</button>
      </div>
      {#if movements.length === 0}
        <p class="muted">Hareket yok.</p>
      {:else}
        <table class="movements">
          <thead>
            <tr><th scope="col">Zaman</th><th scope="col">Hareket</th><th scope="col">Dayanak</th><th scope="col" class="num">Miktar</th><th scope="col">Kişi</th></tr>
          </thead>
          <tbody>
            {#each movements as m (m.id)}
              {@const l = m.lines[0]}
              <tr>
                <td>{dateTime(m.at)}</td>
                <td>{kindName(m.kind)}</td>
                <td>
                  {#if m.kind === 'sale'}<span class="muted">kasa</span>{:else if m.counterpart}{m.kind === 'transfer_out' ? '→' : '←'} {names[m.counterpart] ?? m.counterpart}{:else}{m.supplier ? `${m.supplier} · ` : ''}{m.ref}{/if}
                  {#if m.kind === 'count'}<br /><small class="muted">{`sayılan ${quantity(l.counted_milli)}, sistemde ${quantity(l.system_milli)}`}</small>{/if}
                </td>
                <td class="num" class:negative={l.qty_milli < 0}>{signed(l.qty_milli)}</td>
                <td>{m.by}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    </div>
  {/if}

  {#if items}
    {#if items.length === 0}
      <p class="muted">{query ? 'Aramayla eşleşen ürün yok.' : 'Henüz ürün yok: merkezin kataloğu ya da ilk satış gelince görünür.'}</p>
    {:else}
      <div class="panel">
        <table class="stock">
          <thead>
            <tr><th scope="col">Ürün</th><th scope="col" class="num">Stok</th></tr>
          </thead>
          <tbody>
            {#each items as it (it.product_id)}
              <tr>
                <th scope="row">
                  <button class="link" onclick={() => show(it)}>{it.name}</button>
                  {#if it.discontinued}<span class="pill off">satışta değil</span>{/if}
                </th>
                <td class="num" class:negative={it.qty_milli < 0} data-testid="stock-qty">{quantity(it.qty_milli)} {unitName(it.unit)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</section>
