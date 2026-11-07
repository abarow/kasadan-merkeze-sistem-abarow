<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import Products from '@abarow/admin/Products.svelte'
  import { api } from './lib/api.js'
  import { catalogApi } from './lib/catalog.js'

  // Product and price management at the store. While headquarters counts as connected the catalog
  // comes from headquarters and is only read here; when the manager closes the connection it is
  // managed at the store (a single setting: connected / disconnected).
  let { onunauthorized } = $props()

  // {installed, connected, catalog_writable}; null is loading.
  let office = $state(null)
  let error = $state('')
  let notice = $state('')
  let busy = $state(false)
  // The change waiting for confirmation: 'disconnect', 'connect' or ''.
  let confirming = $state('')

  async function load() {
    try {
      office = await api.headquarters()
      error = ''
    } catch (e) {
      if (e.status === 401) onunauthorized()
      else error = e.message
    }
  }
  load()

  async function change(connected) {
    busy = true
    notice = ''
    try {
      const res = await api.setHeadquarters(connected)
      office = res
      confirming = ''
      notice = res.applied
        ? connected
          ? 'Merkez bağlı sayılıyor; ürünler merkezden gelir.'
          : 'Merkez kopuk sayılıyor; ürünler bu mağazada yönetilir.'
        : 'Ayar kaydedildi; katalog akışı bağlantı gelince uygulanacak.'
      error = ''
    } catch (e) {
      if (e.status === 401) onunauthorized()
      else error = e.message
    } finally {
      busy = false
    }
  }
</script>

<section aria-labelledby="catalog">
  <div class="section-head">
    <h2 id="catalog">Ürünler</h2>
  </div>

  {#if error}
    <p class="alert" role="alert">{error}</p>
  {/if}
  {#if notice}
    <p class="note" role="status" data-testid="office-notice">{notice}</p>
  {/if}

  {#if office}
    <div class="panel" role="region" aria-label="Merkez bağlantısı">
      {#if !office.installed}
        <p data-testid="office-state">Bu mağaza merkeze bağlı değil; ürün ve fiyatları burada yönetirsiniz.</p>
      {:else}
        <p data-testid="office-state">
          Merkez: <strong>{office.connected ? 'bağlı' : 'kopuk'}</strong>
          — {office.connected ? 'ürünler ve merkez fiyatları merkezden gelir; burada yalnızca okunur.' : 'ürünler bu mağazada yönetilir; merkezin kataloğu alınmaz.'}
        </p>
        {#if confirming === ''}
          <button
            disabled={busy}
            data-testid="office-toggle"
            onclick={() => (confirming = office.connected ? 'disconnect' : 'connect')}
          >{office.connected ? 'Merkez bağlantısını kapat' : "Merkeze bağla"}</button>
        {:else}
          <p class="note" data-testid="office-warning">
            {#if confirming === 'disconnect'}
              Bağlantıyı kapatınca merkezin kataloğu bu mağazaya alınmaz: merkezde yapılan ürün ve fiyat değişiklikleri
              buraya inmez, ürünleri bu ekrandan yönetirsiniz.
            {:else}
              Yeniden bağlayınca katalog merkezden gelir ve burada yalnızca okunur. Mağazada eklenen ürünler katalogda
              kalır; merkezden gelenle aynı koda sahipse merkezinki geçerli olur.
            {/if}
          </p>
          <button class="primary" disabled={busy} data-testid="office-confirm" onclick={() => change(confirming === 'connect')}>
            {confirming === 'disconnect' ? 'Evet, kapat' : 'Evet, bağla'}
          </button>
          <button disabled={busy} onclick={() => (confirming = '')}>Vazgeç</button>
        {/if}
      {/if}
    </div>

    {#key office.connected}
      <Products
        api={catalogApi}
        scope="store"
        canEdit={office.catalog_writable}
        readOnlyNote="Ürünler merkezden (merkez) yönetiliyor; burada yalnızca okunur. Mağazada değiştirmek için yukarıdan merkez bağlantısını kapatın."
        {onunauthorized}
      />
    {/key}
  {/if}
</section>
