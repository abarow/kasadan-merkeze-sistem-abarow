<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { dateTime, money } from './lib/format.js'

  // items: the products with different prices at the stores; if product is given the product
  // column is not shown (the product's own page).
  let { items, product = false } = $props()

  const signed = (kurus) => (kurus > 0 ? `+${money(kurus)}` : money(kurus))
</script>

<table class="store-prices">
  <thead>
    <tr>
      <th scope="col">Mağaza</th>
      {#if !product}<th scope="col">Ürün</th>{/if}
      <th scope="col" class="num">Merkez</th>
      <th scope="col" class="num">Mağaza</th>
      <th scope="col" class="num">Fark</th>
      <th scope="col">Gelecek</th>
      <th scope="col">Son değişiklik</th>
    </tr>
  </thead>
  <tbody>
    {#each items as e (e.store_id + '/' + e.product_id)}
      <tr>
        <th scope="row">{e.store_name}</th>
        {#if !product}<td>{e.product_name}</td>{/if}
        <td class="num">{money(e.headquarters_price_kurus)}</td>
        <td class="num" data-testid="store-price">
          {#if e.store_price_kurus !== undefined}{money(e.store_price_kurus)}{:else}<span class="muted">merkez fiyatı</span>{/if}
        </td>
        <td class="num" class:different={e.difference_kurus}>{e.difference_kurus !== undefined ? signed(e.difference_kurus) : ''}</td>
        <td>
          {#each e.upcoming as u (u.from)}
            <div class="small">{u.clear ? 'merkez fiyatına dönüş' : money(u.unit_price_kurus)} · {dateTime(u.from)}</div>
          {/each}
        </td>
        <td class="muted small">{dateTime(e.updated_at)} · {e.updated_by}</td>
      </tr>
    {/each}
  </tbody>
</table>
