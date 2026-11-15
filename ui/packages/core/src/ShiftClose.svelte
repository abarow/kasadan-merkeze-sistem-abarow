<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<!-- Shift closing. The count is blind: the cashier does not see the expected amount before counting
     the cash in the drawer; the expected amount and the difference are shown in the
     report after closing. If a cashier is closing someone else's shift manager
     approval is needed. -->
<script>
  import { api } from './lib/api.js'
  import { parseTL } from './lib/format.js'
  import ManagerApproval from './ManagerApproval.svelte'
  import ShiftReportView from './ShiftReportView.svelte'

  let { shift, needsApproval = false, onclosed, oncancel } = $props()

  let counted = $state('')
  let report = $state(null)
  let error = $state('')
  let busy = $state(false)
  let approving = $state(false)

  const countedKurus = $derived(parseTL(counted))

  function close(e) {
    e.preventDefault()
    if (Number.isNaN(countedKurus) || busy) return
    if (needsApproval) approving = true
    else submit()
  }

  async function submit(approval) {
    busy = true
    error = ''
    try {
      report = await api.closeShift(countedKurus, approval)
    } catch (err) {
      error = err.message
    } finally {
      busy = false
    }
  }
</script>

<div class="overlay">
  <div class="dialog report">
    {#if !report}
      <h2>Vardiya kapanışı <small>· {shift.cashier_name}</small></h2>
      <form class="cash" onsubmit={close}>
        <p>Çekmecedeki nakdi sayıp girin. Beklenen tutar sayımdan sonra gösterilir.</p>
        <label>
          Sayılan nakit (TL)
          <input bind:value={counted} inputmode="decimal" autocomplete="off" placeholder="0,00" />
        </label>
        <button class="primary danger" type="submit" disabled={busy || Number.isNaN(countedKurus)}>Vardiyayı kapat</button>
      </form>
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <button class="secondary" onclick={oncancel} disabled={busy}>Vazgeç</button>
    {:else}
      <ShiftReportView {report} />
      <button class="primary" onclick={() => onclosed(report)}>Tamam</button>
    {/if}
  </div>
</div>

{#if approving}
  <ManagerApproval
    action="close_shift"
    what="Başkasının vardiyasını kapatmak"
    onapproved={(token) => {
      approving = false
      submit(token)
    }}
    oncancel={() => (approving = false)}
  />
{/if}
