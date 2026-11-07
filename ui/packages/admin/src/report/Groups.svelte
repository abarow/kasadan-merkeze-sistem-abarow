<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // The table of a breakdown (store, register, cashier). rows: [{key, name,
  // detail, receipt_count, total_kurus, voided_count}]; detail is written
  // faded next to the name.
  import { money } from '../lib/format.js'

  let { title, label, rows } = $props()
</script>

<div class="panel">
  <h3>{title}</h3>
  <table>
    <thead><tr><th scope="col">{label}</th><th scope="col" class="num">Fiş</th><th scope="col" class="num">Satış</th><th scope="col" class="num">İptal</th></tr></thead>
    <tbody>
      {#each rows as g (g.key)}
        <tr>
          <th scope="row">{g.name}{#if g.detail}<span class="muted">{g.detail}</span>{/if}</th>
          <td class="num">{g.receipt_count}</td>
          <td class="num">{money(g.total_kurus)}</td>
          <td class="num">{g.voided_count}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>
