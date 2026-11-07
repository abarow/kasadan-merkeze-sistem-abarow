<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Move or merge: touching an empty table moves the check there; touching an
  // occupied table joins that table's check to this one (that table empties).
  let { order, areas, onmove, onmerge, oncancel } = $props()
</script>

<div class="overlay">
  <div class="dialog move" role="dialog" aria-label="Taşı ya da birleştir">
    <h2>Taşı ya da birleştir</h2>
    <p class="muted">Boş masa: adisyon oraya taşınır. Dolu masa: o masanın adisyonu bununla birleşir.</p>
    {#each areas as a (a.id)}
      <h3>{a.name}</h3>
      <div class="tables small">
        {#each a.tables.filter((t) => t.id !== order.table_id) as t (t.id)}
          <button
            class="table {t.order ? 'busy' : 'free'}"
            disabled={!!t.order?.payment}
            onclick={() => (t.order ? onmerge(t.order) : onmove(t.id))}
            aria-label={`${t.name}: ${t.order ? 'birleştir' : 'taşı'}`}
          >
            <strong>{t.name}</strong>
            <small>{t.order ? 'birleştir' : 'taşı'}</small>
          </button>
        {/each}
      </div>
    {/each}
    <div class="row">
      <button onclick={oncancel}>Vazgeç</button>
    </div>
  </div>
</div>
