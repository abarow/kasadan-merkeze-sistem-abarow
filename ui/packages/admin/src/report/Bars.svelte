<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Time distribution (hours or days): bars by sales amount.
  // rows: [{key, label, total_kurus, receipt_count}].
  import { money } from '../lib/format.js'

  let { title, rows, days = false } = $props()

  let peak = $derived(Math.max(1, ...rows.map((r) => r.total_kurus)))
</script>

<div class="panel">
  <h3>{title}</h3>
  <ul class="hours" class:days>
    {#each rows as r (r.key)}
      <li>
        <span class="hour">{r.label}</span>
        <span class="bar" aria-hidden="true"><span style:width="{(100 * r.total_kurus) / peak}%"></span></span>
        <span class="num">{money(r.total_kurus)}</span>
        <span class="num muted">{r.receipt_count} fiş</span>
      </li>
    {/each}
  </ul>
</div>
