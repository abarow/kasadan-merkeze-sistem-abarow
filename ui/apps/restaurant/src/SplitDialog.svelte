<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Splitting the bill: the lines to be paid and their quantities are selected (e.g. "I am paying for 2 ayran and
  // the köfte"); the selected ones become a separate sale (receipt), the rest
  // stays on the check.
  import { money } from '@abarow/core/lib/format.js'

  let { order, onpay, oncancel } = $props()

  const open = $derived(order.lines.filter((l) => l.open_qty > 0))
  // svelte-ignore state_referenced_locally
  let qty = $state(Object.fromEntries(order.lines.map((l) => [l.id, 0])))
  const total = $derived(open.reduce((n, l) => n + l.unit_price_kurus * qty[l.id], 0))

  const set = (l, n) => (qty[l.id] = Math.max(0, Math.min(l.open_qty, n)))
  const parts = () => open.filter((l) => qty[l.id] > 0).map((l) => ({ line_id: l.id, qty: qty[l.id] }))
</script>

<div class="overlay">
  <div class="dialog split" role="dialog" aria-label="Hesabı böl">
    <h2>Hesabı böl</h2>
    <p class="muted">Bu ödemede ödenecek adetleri seçin; kalanı adisyonda kalır.</p>
    <ul class="lines">
      {#each open as l (l.id)}
        <li>
          <div class="name">{l.label}<small>{l.open_qty} açık × {money(l.unit_price_kurus)}</small></div>
          <button class="step" onclick={() => set(l, qty[l.id] - 1)} aria-label={`${l.label} azalt`}>−</button>
          <strong aria-label={`${l.label} ödenecek adet`}>{qty[l.id]}</strong>
          <button class="step" onclick={() => set(l, qty[l.id] + 1)} aria-label={`${l.label} artır`}>+</button>
          <button class="link" onclick={() => set(l, l.open_qty)}>Hepsi</button>
        </li>
      {/each}
    </ul>
    <div class="total"><span>Bu ödeme</span><strong>{money(total)}</strong></div>
    <div class="row">
      <button onclick={oncancel}>Vazgeç</button>
      <button class="primary" onclick={() => onpay(parts())} disabled={total === 0}>Seçilenleri öde</button>
    </div>
  </div>
</div>
