<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { dateTime, money, rate, unitName } from './lib/format.js'
  import ProductEditor from './ProductEditor.svelte'
  import StorePrices from './StorePrices.svelte'

  // Product and price management; headquarters (scope 'headquarters': headquarters' catalog) and the back
  // house (scope 'store': the store's catalog) use the same component.
  // api: the application's API client ({products, product, createProduct, ...};
  // storePrices exists only at headquarters). canEdit: can change products and prices;
  // if not the catalog is only read, the reason is in readOnlyNote.
  let { api, canEdit, scope = 'headquarters', readOnlyNote = '', onunauthorized } = $props()

  let query = $state('')
  let products = $state(null)
  let error = $state('')
  // The code of the product being edited; '' is a new product; null is closed.
  let editing = $state(null)
  let exceptions = $state(null)

  async function load() {
    try {
      ;[products, exceptions] = await Promise.all([api.products(query), api.storePrices ? api.storePrices() : null])
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

  // Takvimdeki ilk ileri tarihli fiyat.
  const next = (p) => p.prices.find((pr) => pr.from && new Date(pr.from) > new Date())

  function saved() {
    load()
  }
</script>

<section aria-labelledby="urunler">
  <div class="section-head">
    <h2 id="urunler">Ürünler</h2>
    {#if canEdit}
      <button class="primary" onclick={() => (editing = '')}>Yeni ürün</button>
    {/if}
  </div>
  {#if !canEdit}
    <p class="note" data-testid="read-only">
      {readOnlyNote || 'Kataloğu yalnızca okuyabilirsiniz (rapor rolü); ürün ve fiyatları yönetici ya da katalog kullanıcısı değiştirir.'}
    </p>
  {/if}
  {#if scope === 'store'}
    <p class="note">
      Ürün, barkod, KDV ve fiyat burada değişir; değişiklik kasalara kendiliğinden iner. İleri tarihli fiyat, kasada o
      anda geçer (bağlantı kesikken de).
    </p>
  {:else}
    <p class="note">
      Ürün, barkod, KDV ve merkez fiyatı burada değişir; değişiklik mağazalara ve kasalara kendiliğinden iner. İleri
      tarihli fiyat, kasada o anda geçer (internet kesikken de).
    </p>
  {/if}
  <label class="search">
    Ara
    <input type="search" bind:value={query} placeholder="ad, kod ya da barkod" />
  </label>
  {#if error}
    <p class="alert" role="alert">Ürünler okunamadı: {error}</p>
  {/if}

  {#if editing !== null}
    <ProductEditor {api} {scope} code={editing} {canEdit} onclose={() => (editing = null)} onsaved={saved} {onunauthorized} />
  {/if}

  {#if exceptions}
    <h3 id="store-fiyatlari">Mağaza fiyatları</h3>
    {#if exceptions.length === 0}
      <p class="muted">Mağazalarda merkez fiyatından farklı fiyatlı ürün yok.</p>
    {:else}
      <p class="note">
        Mağaza müdürlerinin operasyon ekranında verdiği fiyatlar (istisnalar); merkez fiyatı değişse de geçerlidir.
      </p>
      <div class="panel" role="region" aria-labelledby="store-fiyatlari">
        <StorePrices items={exceptions} />
      </div>
    {/if}
  {/if}

  {#if products}
    {#if products.length === 0}
      <p class="muted">{query ? 'Aramayla eşleşen ürün yok.' : 'Katalogda ürün yok. "Yeni ürün" ile ekleyin ya da kurulum belgesindeki gibi kasaların catalog.json dosyasını yükleyin.'}</p>
    {:else}
      <div class="panel">
        <table class="products" aria-label="Ürün listesi">
          <thead>
            <tr>
              <th scope="col">Ürün</th>
              <th scope="col">Kod</th>
              <th scope="col">Birim</th>
              <th scope="col" class="num">KDV</th>
              <th scope="col" class="num">Fiyat</th>
            </tr>
          </thead>
          <tbody>
            {#each products as p (p.code)}
              {@const upcoming = next(p)}
              <tr class:off={p.discontinued}>
                <th scope="row">
                  <button class="link" onclick={() => (editing = p.code)}>{p.name}</button>
                  {#if p.discontinued}<span class="pill off">satışta değil</span>{/if}
                </th>
                <td>{p.code}</td>
                <td>{unitName(p.unit)}</td>
                <td class="num">{rate(p.tax_rate_bp)}</td>
                <td class="num">
                  {money(p.unit_price_kurus)}
                  {#if upcoming}<br /><small class="muted">{`${money(upcoming.unit_price_kurus)} · ${dateTime(upcoming.from)}`}</small>{/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</section>
