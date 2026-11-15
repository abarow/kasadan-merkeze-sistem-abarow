<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // The period's sales summary: the selected store or all the stores. The figures are
  // from the sales ledger (the same calculation as the store server's day report); the pieces are
  // in the management screens' shared package, here the store and day breakdown
  // eklenir.
  import Bars from '@abarow/admin/report/Bars.svelte'
  import Groups from '@abarow/admin/report/Groups.svelte'
  import Payments from '@abarow/admin/report/Payments.svelte'
  import Products from '@abarow/admin/report/Products.svelte'
  import Tiles from '@abarow/admin/report/Tiles.svelte'
  import { longDate, shortDate } from './lib/format.js'

  let { summary, error, storeNames } = $props()

  let title = $derived.by(() => {
    if (!summary) return ''
    const store = summary.store ? `${storeNames[summary.store] ?? summary.store} · ` : 'Bütün mağazalar · '
    return summary.from === summary.to
      ? store + longDate(summary.from)
      : `${store}${longDate(summary.from)} – ${longDate(summary.to)}`
  })
  const storeName = (id) => storeNames[id] ?? id
  // The cashier's name (if any), code and, if all the stores are shown, their store.
  let cashiers = $derived(
    (summary?.cashiers ?? []).map((c) => ({
      ...c,
      key: `${c.store_id}/${c.cashier_id}`,
      name: c.cashier_name || c.cashier_id || 'Kasiyersiz',
      detail: (c.cashier_name ? ` · ${c.cashier_id}` : '') + (summary.store ? '' : ` · ${storeName(c.store_id)}`),
    })),
  )
</script>

<section aria-labelledby="ozet">
  <h2 id="ozet">Satış özeti {#if title}<small class="muted">{title}</small>{/if}</h2>
  {#if error}
    <p class="alert" role="alert">Özet okunamadı: {error}</p>
  {/if}
  {#if summary}
    <Tiles report={summary} testid="summary" />

    {#if summary.receipt_count === 0}
      <p class="muted">Bu dönemde satış yok.</p>
    {:else}
      <div class="panels">
        {#if !summary.store}
          <Groups title="Mağazalar" label="Mağaza" rows={summary.stores.map((g) => ({ ...g, key: g.id }))} />
        {/if}
        {#if summary.days.length > 1}
          <Bars title="Günler" days rows={summary.days.map((d) => ({ ...d, key: d.date, label: shortDate(d.date) }))} />
        {/if}
        <Payments report={summary} />
        <Groups title="Kasiyerler" label="Kasiyer" rows={cashiers} />
        <Products products={summary.products} more={(n) => `En çok satan ${n} ürün`} />
      </div>
    {/if}
  {/if}
</section>
