<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Person section: list and editor. The store server's cashiers and headquarters'
  // users are shown with the same component (pkg/personnel's single model);
  // the screen texts, roles and columns come from config.
  //
  // config.api.list() → people; config.api.stores?() → [{id, name}]
  // (if given the scope is chosen and the store names show in the list).
  // config.lastHead / config.last(item, storeNames): the last column's title and
  // value; lastMuted shows it faded. config.roleName(role): the role's name.
  // me: the signed-in person's code (they cannot close their own account). banner: additional content
  // to show above the list (optional). corporate: whether the corporate sign-in
  // (OIDC) is on (passed to PersonEditor).
  import PersonEditor from './PersonEditor.svelte'

  let { config, me, onunauthorized, banner, corporate = false } = $props()
  const t = $derived(config.text)

  let items = $state(null)
  // The stores that can be selected; undefined if config.api.stores is absent (store staff).
  let stores = $state(undefined)
  let error = $state('')
  // The code of the person being edited; '' is a new person; null is closed.
  let editing = $state(null)
  let storeNames = $derived(Object.fromEntries((stores ?? []).map((s) => [s.id, s.name])))

  async function load() {
    try {
      const [list, st] = await Promise.all([config.api.list(), config.api.stores?.()])
      items = list
      if (st) stores = st
      error = ''
    } catch (e) {
      if (e.status === 401) onunauthorized()
      else error = e.message
    }
  }

  load()
</script>

<section aria-labelledby={config.sectionID}>
  <div class="section-head">
    <h2 id={config.sectionID}>{t.heading}</h2>
    <button class="primary" onclick={() => (editing = '')}>{t.newButton}</button>
  </div>
  <p class="note">{t.intro}</p>
  {@render banner?.()}
  {#if error}
    <p class="alert" role="alert">{t.loadError(error)}</p>
  {/if}

  {#if editing !== null}
    <PersonEditor
      {config}
      id={editing}
      self={editing !== '' && editing === me}
      {stores}
      {corporate}
      onclose={() => (editing = null)}
      onsaved={load}
      onadded={(id) => (editing = id)}
      {onunauthorized}
    />
  {/if}

  {#if items}
    {#if items.length === 0 && t.empty}
      <p class="muted">{t.empty}</p>
    {:else}
      <div class="panel">
        <table class={config.tableClass} aria-label={t.listLabel}>
          <thead>
            <tr>
              <th scope="col">Ad</th>
              <th scope="col">{t.idLabel}</th>
              <th scope="col">Rol</th>
              <th scope="col">{config.lastHead}</th>
            </tr>
          </thead>
          <tbody>
            {#each items as c (c.id)}
              <tr class:off={c.disabled}>
                <th scope="row">
                  <button class="link" onclick={() => (editing = c.id)}>{c.name}</button>
                  {#if c.disabled}<span class="pill off">hesap kapalı</span>{/if}
                </th>
                <td>{c.id}</td>
                <td>{config.roleName(c.role)}</td>
                <td class:muted={config.lastMuted}>{config.last(c, storeNames)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</section>
