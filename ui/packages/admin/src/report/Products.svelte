<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Products: by amount; first the first TOP_PRODUCTS are shown. more is the text of the
  // button that opens the whole list (it takes the number of products).
  import { money, quantity } from '../lib/format.js'

  let { products, more } = $props()

  const TOP_PRODUCTS = 15
  let all = $state(false)
  let shown = $derived(all ? products : products.slice(0, TOP_PRODUCTS))
</script>

<div class="panel">
  <h3>Ürünler</h3>
  <table>
    <thead><tr><th scope="col">Ürün</th><th scope="col" class="num">Miktar</th><th scope="col" class="num">Satış</th></tr></thead>
    <tbody>
      {#each shown as p (p.product_id + p.name)}
        <tr><th scope="row">{p.name}</th><td class="num">{quantity(p.qty_milli)}</td><td class="num">{money(p.total_kurus)}</td></tr>
      {/each}
    </tbody>
  </table>
  {#if products.length > TOP_PRODUCTS}
    <button class="link" onclick={() => (all = !all)}>
      {all ? `İlk ${TOP_PRODUCTS} ürünü göster` : more(products.length)}
    </button>
  {/if}
</div>
