<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Cancelling a line sent to the kitchen: quantity and reason; if not a manager manager
  // approval (PIN) is obtained. A cancellation ticket is printed at the station.
  import ManagerApproval from '@abarow/core/ManagerApproval.svelte'

  let { line, isManager, onvoid, oncancel } = $props()

  let qty = $state(1)
  let reason = $state('')
  let approving = $state(false)

  const reasons = ['Müşteri vazgeçti', 'Yanlış girildi', 'Döküldü / bozuk']

  function submit(e) {
    e.preventDefault()
    if (isManager) onvoid(qty, reason, '')
    else approving = true
  }
</script>

{#if approving}
  <ManagerApproval
    action="void_order_line"
    what={`${line.label} iptali`}
    onapproved={(token) => onvoid(qty, reason, token)}
    oncancel={() => (approving = false)}
  />
{:else}
  <div class="overlay">
    <form class="dialog void" onsubmit={submit} aria-label={`İptal: ${line.label}`}>
      <h2>İptal: {line.label}</h2>
      <p class="muted">Mutfağa gönderilmiş; iptal müdür onayı ister ve istasyona iptal fişi basılır.</p>
      <div class="qty">
        <button type="button" class="step" onclick={() => (qty = Math.max(1, qty - 1))} aria-label="Azalt">−</button>
        <strong aria-label="İptal adedi">{qty}</strong>
        <button type="button" class="step" onclick={() => (qty = Math.min(line.open_qty, qty + 1))} aria-label="Artır">+</button>
        <span class="muted">/ {line.open_qty}</span>
      </div>
      <div class="choices">
        {#each reasons as r (r)}
          <button type="button" class="choice" class:on={reason === r} onclick={() => (reason = r)}>{r}</button>
        {/each}
      </div>
      <label class="note">
        Neden
        <input bind:value={reason} required />
      </label>
      <div class="row">
        <button type="button" onclick={oncancel}>Vazgeç</button>
        <button class="primary danger" disabled={!reason.trim()}>İptal et</button>
      </div>
    </form>
  </div>
{/if}
