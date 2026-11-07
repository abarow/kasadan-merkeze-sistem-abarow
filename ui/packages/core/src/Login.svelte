<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { api } from './lib/api.js'
  import PinPad from './PinPad.svelte'

  let { onlogin, notice = '' } = $props()

  let cashiers = $state([])
  let selected = $state(null)
  let pin = $state('')
  // svelte-ignore state_referenced_locally
  let error = $state(notice)
  let busy = $state(false)

  api
    .cashiers()
    .then((list) => (cashiers = list))
    .catch((e) => (error = e.message))

  function choose(c) {
    selected = c
    pin = ''
    error = ''
  }

  async function submit() {
    busy = true
    error = ''
    try {
      onlogin(await api.login(selected.id, pin))
    } catch (e) {
      error = e.message
      pin = ''
    } finally {
      busy = false
    }
  }
</script>

<div class="login">
  <h1>abarow kasa</h1>
  {#if !selected}
    <p class="muted">Kasiyer seçin</p>
    <div class="cashiers">
      {#each cashiers as c (c.id)}
        <button class="cashier" onclick={() => choose(c)}>
          {c.name}
          <small>{c.role === 'manager' ? 'Müdür' : 'Kasiyer'}</small>
        </button>
      {/each}
    </div>
  {:else}
    <p>{selected.name} — PIN girin</p>
    <PinPad bind:pin {busy} onsubmit={submit} />
    <button class="secondary" onclick={() => choose(null)} disabled={busy}>Başka kasiyer</button>
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</div>
