<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // The report's summary tiles: sales, receipts, average receipt, VAT, cancellations. The back
  // house's day report and headquarters' summary come from the same sales ledger
  // (pkg/ledger); their screens are drawn with the same pieces too. testid is the prefix
  // the browser tests find the sales and receipt tiles by.
  import { money } from '../lib/format.js'

  let { report, testid } = $props()

  let average = $derived(report.receipt_count > 0 ? Math.round(report.total_kurus / report.receipt_count) : 0)
</script>

<div class="tiles">
  <div class="tile">
    <span>Satış (KDV dahil)</span>
    <strong data-testid="{testid}-total">{money(report.total_kurus)}</strong>
  </div>
  <div class="tile">
    <span>Fiş</span>
    <strong data-testid="{testid}-receipts">{report.receipt_count}</strong>
  </div>
  <div class="tile">
    <span>Ortalama fiş</span>
    <strong>{money(average)}</strong>
  </div>
  <div class="tile">
    <span>KDV</span>
    <strong>{money(report.tax_total_kurus)}</strong>
  </div>
  <div class="tile">
    <span>İptal</span>
    <strong>{report.voided_count}</strong>
    {#if report.voided_count > 0}<small class="muted">{money(report.voided_total_kurus)}</small>{/if}
  </div>
</div>
