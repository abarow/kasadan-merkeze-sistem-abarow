<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // The store's daily report. The figures are from the sales ledger (the same calculation as
  // headquarters' summary); the pieces are in the management screens' shared package.
  import Bars from '@abarow/admin/report/Bars.svelte'
  import Groups from '@abarow/admin/report/Groups.svelte'
  import Payments from '@abarow/admin/report/Payments.svelte'
  import Products from '@abarow/admin/report/Products.svelte'
  import Tiles from '@abarow/admin/report/Tiles.svelte'

  let { report, error } = $props()

  let title = $derived(
    report ? new Date(`${report.from}T12:00:00`).toLocaleDateString('tr-TR', { dateStyle: 'full' }) : '',
  )
  // The cashier's name (if any) and code; the code of a sale with no cashier is empty.
  let cashiers = $derived(
    (report?.cashiers ?? []).map((c) => ({
      ...c,
      key: c.cashier_id,
      name: c.cashier_name || c.cashier_id || 'Kasiyersiz',
      detail: c.cashier_name ? ` · ${c.cashier_id}` : '',
    })),
  )
  // Hourly distribution: from the hour of the first sale to the hour of the last sale.
  let hours = $derived.by(() => {
    if (!report) return []
    const busy = report.hours.filter((h) => h.receipt_count > 0).map((h) => h.hour)
    if (busy.length === 0) return []
    return report.hours
      .slice(Math.min(...busy), Math.max(...busy) + 1)
      .map((h) => ({ ...h, key: h.hour, label: `${String(h.hour).padStart(2, '0')}:00` }))
  })
</script>

<section aria-labelledby="gun">
  <h2 id="gun">Günlük rapor {#if title}<small class="muted">{title}</small>{/if}</h2>
  {#if error}
    <p class="alert" role="alert">Rapor okunamadı: {error}</p>
  {/if}
  {#if report}
    <Tiles {report} testid="day" />

    {#if report.receipt_count === 0}
      <p class="muted">Bu gün satış yok.</p>
    {:else}
      <div class="panels">
        <Payments {report} />
        <Groups title="Kasalar" label="Kasa" rows={report.registers.map((g) => ({ ...g, key: g.id }))} />
        <Groups title="Kasiyerler" label="Kasiyer" rows={cashiers} />
        <Bars title="Saatlik" rows={hours} />
        <Products products={report.products} more={(n) => `Bütün ürünler (${n})`} />
      </div>
    {/if}
  {/if}
</section>
