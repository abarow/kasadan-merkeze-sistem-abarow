<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Floor layout: floors and their tables are added, named, removed.
  // Saving is a manager's job (manager approval if not a manager). A table with an open check
  // cannot be removed (the server rejects it).
  import { api } from './lib/api.js'
  import ManagerApproval from '@abarow/core/ManagerApproval.svelte'

  let { areas, isManager, onsaved, oncancel } = $props()

  // svelte-ignore state_referenced_locally
  let draft = $state(areas.map((a) => ({ id: a.id, name: a.name, tables: a.tables.map((t) => ({ id: t.id, name: t.name })) })))
  let error = $state('')
  let busy = $state(false)
  let approving = $state(false)

  // Ids are given once and do not change (checks are tied to the table by id).
  let seq = 0
  const newID = (prefix) => `${prefix}${Date.now().toString(36)}${(seq++).toString(36)}`

  const addArea = () => draft.push({ id: newID('s'), name: `Salon ${draft.length + 1}`, tables: [] })
  function addTables(a, n) {
    const start = a.tables.length
    for (let i = 1; i <= n; i++) a.tables.push({ id: newID('m'), name: String(start + i) })
  }

  async function save(approval) {
    busy = true
    error = ''
    try {
      onsaved(await api.setFloor(draft, approval))
    } catch (e) {
      error = e.message
    } finally {
      busy = false
      approving = false
    }
  }
</script>

{#if approving}
  <ManagerApproval action="edit_floor" what="Salon düzeni" onapproved={save} oncancel={() => (approving = false)} />
{:else}
  <div class="overlay">
    <div class="dialog floor-editor" role="dialog" aria-label="Salon düzeni">
      <h2>Salon düzeni</h2>
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      {#each draft as a, ai (a.id)}
        <fieldset>
          <legend>
            <input bind:value={a.name} aria-label={`Salon ${ai + 1} adı`} />
            <button class="link" onclick={() => draft.splice(ai, 1)}>Salonu çıkar</button>
          </legend>
          <div class="table-names">
            {#each a.tables as t, ti (t.id)}
              <span class="table-name">
                <input bind:value={t.name} aria-label={`${a.name} masa ${ti + 1} adı`} />
                <button class="link" onclick={() => a.tables.splice(ti, 1)} aria-label={`${t.name} masasını çıkar`}>✕</button>
              </span>
            {/each}
          </div>
          <button onclick={() => addTables(a, 1)}>Masa ekle</button>
          <button onclick={() => addTables(a, 5)}>5 masa ekle</button>
        </fieldset>
      {/each}
      <button onclick={addArea}>Salon ekle</button>
      <div class="row">
        <button onclick={oncancel}>Vazgeç</button>
        <button class="primary" disabled={busy} onclick={() => (isManager ? save('') : (approving = true))}>Kaydet</button>
      </div>
    </div>
  </div>
{/if}
