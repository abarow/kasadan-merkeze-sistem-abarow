<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Menu: category tabs and product cards with images. Touching a product
  // with no options adds 1 piece; for one with options the options window opens.
  // If there is no image (or it has not come down to the register yet) the name's initials show.
  import { api, newLineID } from './lib/api.js'
  import { money } from '@abarow/core/lib/format.js'
  import OptionsDialog from './OptionsDialog.svelte'

  let { categories, order, onadded, onerror } = $props()

  let current = $state('')
  let choosing = $state(null) // opsiyon penceresindeki ürün
  let failed = $state({}) // görseli yüklenemeyen ürünler
  let busy = $state(false)

  const category = $derived(categories.find((c) => c.name === current) ?? categories[0])
  const initials = (name) =>
    name
      .split(/\s+/)
      .slice(0, 2)
      .map((w) => w.charAt(0).toLocaleUpperCase('tr-TR'))
      .join('')

  async function add(item, line = { qty: 1, choices: [], note: '' }) {
    busy = true
    onerror('')
    try {
      onadded(await api.addLine(order.id, { id: newLineID(), product_id: item.id, ...line }))
      choosing = null
    } catch (e) {
      onerror(e.message)
    } finally {
      busy = false
    }
  }

  const pick = (item) => (item.option_groups.length > 0 ? (choosing = item) : add(item))
</script>

<section class="menu" aria-label="Menü">
  {#if categories.length === 0}
    <p class="notice">Menü boş: ürünlere merkezden (ya da başlangıç dosyasından) kategori verin.</p>
  {:else}
    <div class="tabs" role="tablist">
      {#each categories as c (c.name)}
        <button role="tab" aria-selected={c.name === category.name} class:active={c.name === category.name} onclick={() => (current = c.name)}>
          {c.name}
        </button>
      {/each}
    </div>
    <div class="cards">
      {#each category.items as item (item.id)}
        <button class="card" onclick={() => pick(item)} disabled={busy || !!order.payment} aria-label={item.name}>
          {#if item.image && !failed[item.id]}
            <img src={item.image} alt="" loading="lazy" onerror={() => (failed[item.id] = true)} />
          {:else}
            <span class="initials" aria-hidden="true">{initials(item.name)}</span>
          {/if}
          <span class="name">{item.name}</span>
          <span class="price">{money(item.unit_price_kurus)}</span>
        </button>
      {/each}
    </div>
  {/if}
</section>

{#if choosing}
  <OptionsDialog item={choosing} {busy} onadd={(line) => add(choosing, line)} oncancel={() => (choosing = null)} />
{/if}
