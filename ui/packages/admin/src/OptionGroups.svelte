<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // A product's option groups on the restaurant menu (e.g. "Pişirme": rare, medium,
  // well done; "Ekstralar": cheddar +15 TL). groups is edited; the price difference is entered
  // as text and turned into kuruş when saving (see toGroups).
  let { groups = $bindable([]) } = $props()

  const addGroup = () => groups.push({ id: '', name: '', min: 0, max: 1, options: [{ id: '', name: '', price: '' }] })
  const addOption = (g) => g.options.push({ id: '', name: '', price: '' })
</script>

<fieldset class="options wide">
  <legend>Opsiyonlar (restoran)</legend>
  {#each groups as g, gi (gi)}
    <div class="group" role="group" aria-label={`Opsiyon grubu ${gi + 1}`}>
      <label>
        Grup adı
        <input bind:value={g.name} placeholder="ör. Pişirme" aria-label={`Grup ${gi + 1} adı`} />
      </label>
      <label>
        En az
        <input type="number" min="0" bind:value={g.min} aria-label={`Grup ${gi + 1} en az seçim`} />
      </label>
      <label>
        En çok
        <input type="number" min="1" bind:value={g.max} aria-label={`Grup ${gi + 1} en çok seçim`} />
      </label>
      <button type="button" class="link" onclick={() => groups.splice(gi, 1)}>Grubu çıkar</button>
      <ul>
        {#each g.options as o, oi (oi)}
          <li>
            <input bind:value={o.name} placeholder="seçenek" aria-label={`Grup ${gi + 1} seçenek ${oi + 1}`} />
            <input
              class="price"
              bind:value={o.price}
              inputmode="decimal"
              placeholder="+0,00"
              aria-label={`Grup ${gi + 1} seçenek ${oi + 1} fiyat farkı`}
            />
            <button type="button" class="link" onclick={() => g.options.splice(oi, 1)} disabled={g.options.length === 1}>Çıkar</button>
          </li>
        {/each}
      </ul>
      <button type="button" class="link" onclick={() => addOption(g)}>Seçenek ekle</button>
    </div>
  {/each}
  <p class="muted">En az 0 ise isteğe bağlıdır; 1 ise garson bir seçenek seçmeden ekleyemez.</p>
  <button type="button" onclick={addGroup}>Opsiyon grubu ekle</button>
</fieldset>
