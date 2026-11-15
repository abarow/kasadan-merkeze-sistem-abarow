<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Payments and the VAT breakdown.
  import { method, money, rate } from '../lib/format.js'

  let { report } = $props()
</script>

<div class="panel">
  <h3>Ödemeler</h3>
  <table>
    <tbody>
      {#each report.payments as p (p.method)}
        <tr><th scope="row">{method(p.method)}</th><td class="num">{money(p.amount_kurus)}</td></tr>
      {/each}
    </tbody>
  </table>
  <p class="note">Nakit, verilen para üstü düşülmüş tutardır.</p>
</div>

<div class="panel">
  <h3>KDV</h3>
  <table>
    <thead><tr><th scope="col">Oran</th><th scope="col" class="num">KDV dahil</th><th scope="col" class="num">KDV</th></tr></thead>
    <tbody>
      {#each report.taxes as t (t.rate_bp)}
        <tr><th scope="row">{rate(t.rate_bp)}</th><td class="num">{money(t.gross_kurus)}</td><td class="num">{money(t.tax_kurus)}</td></tr>
      {/each}
    </tbody>
  </table>
</div>
