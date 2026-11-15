<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<!-- Manager approval: while in a cashier session the manager gives approval for a single operation
     with their PIN. The approval is valid for one minute and is used once; the manager
     counts as having done the operation. A wrong PIN counts toward the manager's attempt counter. -->
<script>
  import { api } from './lib/api.js'
  import PinPad from './PinPad.svelte'

  // action: 'close_day' | 'cash_out' | 'close_shift'; what: the operation's name on the screen.
  let { action, what, onapproved, oncancel } = $props()

  let managers = $state([])
  let selected = $state(null)
  let pin = $state('')
  let error = $state('')
  let busy = $state(false)

  api
    .cashiers()
    .then((list) => {
      managers = list.filter((c) => c.role === 'manager')
      if (managers.length === 1) selected = managers[0]
    })
    .catch((e) => (error = e.message))

  async function submit() {
    busy = true
    error = ''
    try {
      const r = await api.approve(selected.id, pin, action)
      onapproved(r.approval_token)
    } catch (e) {
      error = e.message
      pin = ''
    } finally {
      busy = false
    }
  }
</script>

<div class="overlay">
  <div class="dialog approval">
    <h2>Müdür onayı</h2>
    <p>{what} için müdür onayı gerekir.</p>
    {#if !selected}
      <div class="cashiers">
        {#each managers as m (m.id)}
          <button class="cashier" onclick={() => (selected = m)}>{m.name}</button>
        {/each}
      </div>
    {:else}
      <p class="muted">{selected.name} — PIN girin</p>
      <PinPad bind:pin {busy} label="Onayla" onsubmit={submit} />
    {/if}
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button class="secondary" onclick={oncancel} disabled={busy}>Vazgeç</button>
  </div>
</div>
