<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Floor and tables: floor tabs, table boxes. An empty table is opened,
  // the check of an occupied table is opened. State: empty, occupied (amount, time, lines
  // not sent to the kitchen), bill requested, in payment.
  import { money } from '@abarow/core/lib/format.js'

  let { areas, busy, onopen } = $props()

  let current = $state('')
  const area = $derived(areas.find((a) => a.id === current) ?? areas[0])

  const minutes = (iso) => Math.max(0, Math.round((Date.now() - new Date(iso)) / 60000))
  const status = (o) => (!o ? 'free' : o.payment ? 'paying' : o.bill_at ? 'bill' : 'busy')
  const stateName = { free: 'boş', busy: 'dolu', bill: 'hesap istendi', paying: 'ödemede' }
</script>

<section class="floor" aria-label="Masalar">
  {#if areas.length === 0}
    <p class="notice">Henüz salon ve masa tanımlanmadı: "Salon düzeni"nden ekleyin (müdür).</p>
  {:else}
    <div class="tabs" role="tablist">
      {#each areas as a (a.id)}
        <button role="tab" aria-selected={a.id === area.id} class:active={a.id === area.id} onclick={() => (current = a.id)}>
          {a.name}
          <small>{a.tables.filter((t) => t.order).length}/{a.tables.length}</small>
        </button>
      {/each}
    </div>
    <div class="tables">
      {#each area.tables as t (t.id)}
        {@const s = status(t.order)}
        <button class="table {s}" onclick={() => onopen(t)} disabled={busy} aria-label={`${t.name}: ${stateName[s]}`}>
          <strong>{t.name}</strong>
          {#if t.order}
            <span class="amount">{money(t.order.due_kurus)}</span>
            <small>
              {minutes(t.order.opened_at)} dk · {stateName[s]}
              {#if t.order.pending > 0}· {t.order.pending} gönderilmedi{/if}
            </small>
          {:else}
            <small>boş</small>
          {/if}
        </button>
      {/each}
    </div>
  {/if}
</section>
