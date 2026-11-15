<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Stock: the headquarters warehouse and the stores (the stores the person sees), per
  // product; when a quantity is clicked the movements at that place. The administrator and the catalog user enter
  // goods receipt, counts and transfers for the headquarters warehouse.
  import { api } from './lib/api.js'
  import { dateTime, kindName, quantity, unitName } from './lib/format.js'
  import StockEntry from '@abarow/admin/StockEntry.svelte'
  import Transfers from '@abarow/admin/Transfers.svelte'

  let { storeNames, onunauthorized } = $props()

  let query = $state('')
  let data = $state(null) // {here, locations, items, can_edit}
  let transfers = $state(null) // {here, locations [{id, name}], transfers, can_edit}
  let error = $state('')
  let entry = $state(null) // 'receipt' | 'count' | 'transfer' | 'transfers'
  let selected = $state(null) // {item, location}
  let movements = $state([])

  const placeName = (l) => (l === data?.here ? 'Merkez deposu' : (storeNames[l] ?? l))
  const total = (it) => Object.values(it.qty_milli).reduce((n, q) => n + q, 0)
  const stores = $derived((transfers?.locations ?? []).filter((l) => l.id !== transfers.here))
  const arriving = $derived((transfers?.transfers ?? []).filter((t) => t.pending && t.to === transfers.here).length)

  async function loadTransfers() {
    try {
      transfers = await api.transfers()
    } catch (e) {
      if (e.status === 401) onunauthorized()
      else error = e.message
    }
  }
  loadTransfers()

  async function load() {
    try {
      data = await api.stock(query)
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

  async function show(item, location) {
    selected = { item, location }
    try {
      movements = await api.stockMovements(item.product_id, location)
    } catch (e) {
      if (e.status === 401) onunauthorized()
      else error = e.message
    }
  }

  function saved() {
    load()
    loadTransfers()
    if (selected) show(selected.item, selected.location)
  }

  const signed = (milli) => (milli > 0 ? '+' : '') + quantity(milli)
</script>

<section aria-labelledby="stok">
  <h2 id="stok">Stok</h2>
  <p class="note">
    Mağazaların stoğu kendi operasyonlarında tutulur ve buraya hareketleriyle gelir (satış, mal kabul, sayım,
    transfer). Merkez deposunun mal kabulü, sayımı ve transferleri buradan girilir. Eksi stok kırmızıdır; sayımla
    düzeltilir.
  </p>
  <div class="toolbar">
    <label class="search">
      Ara
      <input type="search" bind:value={query} placeholder="ad ya da barkod" />
    </label>
    {#if data?.can_edit}
      <button onclick={() => (entry = 'receipt')}>Mal kabul</button>
      <button onclick={() => (entry = 'count')}>Sayım</button>
      <button onclick={() => (entry = 'transfer')}>Transfer gönder</button>
    {/if}
    <button onclick={() => (entry = 'transfers')}>Transferler{arriving ? ` (${arriving} depoya yolda)` : ''}</button>
  </div>
  {#if error}
    <p class="alert" role="alert">{error.charAt(0).toLocaleUpperCase('tr-TR') + error.slice(1)}</p>
  {/if}

  {#if entry === 'transfers'}
    {#if transfers}
      <Transfers
        {api}
        scope="headquarters"
        here={transfers.here}
        transfers={transfers.transfers}
        names={Object.fromEntries(transfers.locations.map((l) => [l.id, l.name]))}
        canReceive={transfers.can_edit}
        onclose={() => (entry = null)} onsaved={saved} {onunauthorized} />
    {/if}
  {:else if entry}
    <StockEntry {api} scope="headquarters" mode={entry} locations={stores} onclose={() => (entry = null)} onsaved={saved} {onunauthorized} />
  {/if}

  {#if selected}
    <div class="panel editor" role="region" aria-label={`Hareketler: ${selected.item.name}, ${placeName(selected.location)}`}>
      <div class="section-head">
        <h3>{selected.item.name} · {placeName(selected.location)}</h3>
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
                  {#if m.kind === 'sale'}<span class="muted">kasa</span>{:else if m.counterpart}{m.kind === 'transfer_out' ? '→' : '←'} {placeName(m.counterpart)}{:else}{m.supplier ? `${m.supplier} · ` : ''}{m.ref}{/if}
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

  {#if data}
    {#if data.items.length === 0}
      <p class="muted">{query ? 'Aramayla eşleşen ürün yok.' : 'Henüz ürün yok: katalog ya da mağazaların ilk hareketi gelince görünür.'}</p>
    {:else}
      <div class="panel scroll">
        <table class="stock">
          <thead>
            <tr>
              <th scope="col">Ürün</th>
              {#each data.locations as l (l)}<th scope="col" class="num">{placeName(l)}</th>{/each}
              <th scope="col" class="num">Toplam</th>
            </tr>
          </thead>
          <tbody>
            {#each data.items as it (it.product_id)}
              <tr>
                <th scope="row">
                  {it.name}
                  {#if it.discontinued}<span class="pill off">satışta değil</span>{/if}
                </th>
                {#each data.locations as l (l)}
                  {@const q = it.qty_milli[l] ?? 0}
                  <td class="num" class:negative={q < 0}>
                    <button class="link" aria-label={`${it.name}, ${placeName(l)}: ${quantity(q)} ${unitName(it.unit)}`} onclick={() => show(it, l)}
                      >{quantity(q)}</button
                    >
                  </td>
                {/each}
                <td class="num" class:negative={total(it) < 0} data-testid="stock-total">{quantity(total(it))} {unitName(it.unit)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</section>
