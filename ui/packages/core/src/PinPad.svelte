<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<!-- PIN keypad: on the sign-in screen and in manager approval. A physical
     keyboard works alongside it. The PIN is 4–8 digits. -->
<script>
  import { onMount } from 'svelte'

  let { pin = $bindable(''), busy = false, label = 'Giriş', onsubmit } = $props()

  // If there is an input field that stays focused behind (e.g. a description) the PIN would be
  // typed there too; the focus is released, keys coming from input fields are ignored.
  onMount(() => document.activeElement?.blur?.())

  function press(digit) {
    if (pin.length < 8) pin += digit
  }

  function submit() {
    if (pin.length >= 4 && !busy) onsubmit()
  }

  function onkeydown(e) {
    if (e.target instanceof HTMLInputElement) return
    if (/^\d$/.test(e.key)) press(e.key)
    else if (e.key === 'Backspace') pin = pin.slice(0, -1)
    else if (e.key === 'Enter') submit()
  }
</script>

<svelte:window {onkeydown} />

<div class="pin" aria-label="Girilen PIN">{'●'.repeat(pin.length)}</div>
<div class="keypad">
  {#each ['1', '2', '3', '4', '5', '6', '7', '8', '9'] as d (d)}
    <button onclick={() => press(d)} disabled={busy}>{d}</button>
  {/each}
  <button onclick={() => (pin = pin.slice(0, -1))} disabled={busy} aria-label="Sil">⌫</button>
  <button onclick={() => press('0')} disabled={busy}>0</button>
  <button class="primary" onclick={submit} disabled={busy || pin.length < 4}>{label}</button>
</div>
