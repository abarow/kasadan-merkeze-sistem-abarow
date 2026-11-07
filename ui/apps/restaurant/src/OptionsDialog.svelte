<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Options window: in each group at least and at most that many options, quantity and
  // note. A single-choice group behaves like a radio. The rules are also
  // checked on the server; here a missing choice disables "Ekle".
  import { money } from '@abarow/core/lib/format.js'

  let { item, busy, onadd, oncancel } = $props()

  // svelte-ignore state_referenced_locally
  let picked = $state(Object.fromEntries(item.option_groups.map((g) => [g.id, []])))
  let qty = $state(1)
  let note = $state('')

  function toggle(g, id) {
    const list = picked[g.id]
    if (list.includes(id)) picked[g.id] = list.filter((x) => x !== id)
    else if (g.max === 1) picked[g.id] = [id]
    else if (list.length < g.max) picked[g.id] = [...list, id]
  }

  const missing = $derived(item.option_groups.filter((g) => picked[g.id].length < g.min))
  const unitPrice = $derived(
    item.unit_price_kurus +
      item.option_groups.flatMap((g) => g.options.filter((o) => picked[g.id].includes(o.id))).reduce((n, o) => n + o.price_delta_kurus, 0),
  )

  function submit(e) {
    e.preventDefault()
    onadd({ qty, note, choices: item.option_groups.map((g) => ({ group: g.id, options: picked[g.id] })) })
  }
</script>

<div class="overlay">
  <form class="dialog options" onsubmit={submit} aria-label={`Seçim: ${item.name}`}>
    <h2>{item.name}</h2>
    {#each item.option_groups as g (g.id)}
      <fieldset>
        <legend>
          {g.name}
          <small class="muted">{g.min > 0 ? (g.max === 1 ? 'birini seçin' : `en az ${g.min}`) : g.max === 1 ? 'isteğe bağlı' : `en çok ${g.max}`}</small>
        </legend>
        <div class="choices">
          {#each g.options as o (o.id)}
            <button type="button" class="choice" class:on={picked[g.id].includes(o.id)} aria-pressed={picked[g.id].includes(o.id)} onclick={() => toggle(g, o.id)}>
              {o.name}
              {#if o.price_delta_kurus}<small>{o.price_delta_kurus > 0 ? '+' : ''}{money(o.price_delta_kurus)}</small>{/if}
            </button>
          {/each}
        </div>
      </fieldset>
    {/each}
    <label class="note">
      Not
      <input bind:value={note} maxlength="60" placeholder="ör. soğansız" />
    </label>
    <div class="qty">
      <button type="button" class="step" onclick={() => (qty = Math.max(1, qty - 1))} aria-label="Azalt">−</button>
      <strong aria-label="Adet">{qty}</strong>
      <button type="button" class="step" onclick={() => (qty = Math.min(99, qty + 1))} aria-label="Artır">+</button>
      <span class="total">{money(unitPrice * qty)}</span>
    </div>
    {#if missing.length > 0}<p class="muted">{missing.map((g) => g.name).join(', ')}: seçin</p>{/if}
    <div class="row">
      <button type="button" onclick={oncancel}>Vazgeç</button>
      <button class="primary" disabled={busy || missing.length > 0}>Ekle</button>
    </div>
  </form>
</div>
