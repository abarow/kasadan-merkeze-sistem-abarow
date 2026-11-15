<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<!-- Register operations: putting money into and taking money out of the register apart from sales,
     moving on to shift closing. A manager takes money out, or a cashier does with manager
     approval; if a cashier could record a withdrawal in their own name they could hide the
     shortage in the blind count. -->
<script>
  import { api } from './lib/api.js'
  import { money, parseTL } from './lib/format.js'
  import ManagerApproval from './ManagerApproval.svelte'

  let { shift, cashier, canClose = true, onclose, onshiftclose } = $props()

  let mode = $state(null) // 'in' | 'out' | 'drawer'; null ise işlem seçimi
  let amount = $state('')
  let note = $state('')
  let done = $state('')
  let error = $state('')
  let busy = $state(false)
  let approving = $state(false) // müdür onayı bekleniyor mu

  const amountKurus = $derived(parseTL(amount))
  const valid = $derived(note.trim() !== '' && (mode === 'drawer' || (!Number.isNaN(amountKurus) && amountKurus > 0)))
  const isManager = $derived(cashier.role === 'manager')
  // The approval name of the operation done with manager approval.
  const approvalAction = $derived(mode === 'drawer' ? 'open_drawer' : 'cash_out')
  const opened = (iso) => new Date(iso).toLocaleTimeString('tr-TR', { hour: '2-digit', minute: '2-digit' })

  function start(kind) {
    mode = kind
    amount = ''
    note = ''
    error = ''
    done = ''
  }

  function save(e) {
    e.preventDefault()
    if (!valid || busy) return
    if ((mode === 'out' || mode === 'drawer') && !isManager) approving = true
    else record()
  }

  async function record(approval) {
    busy = true
    error = ''
    try {
      if (mode === 'drawer') {
        await api.openDrawer(note, approval)
        done = 'Çekmece açıldı; açılış vardiyaya kaydedildi.'
      } else {
        const m = await api.cashMovement(mode, amountKurus, note, approval)
        done = mode === 'in' ? `Kasaya ${money(m.amount_kurus)} kondu.` : `Kasadan ${money(m.amount_kurus)} alındı.`
      }
      mode = null
    } catch (err) {
      error = err.message
    } finally {
      busy = false
    }
  }
</script>

<div class="overlay">
  <div class="dialog report">
    <h2>Kasa işlemleri <small>· {shift.cashier_name}, açılış {opened(shift.opened_at)}</small></h2>
    {#if mode}
      <form class="cash" onsubmit={save}>
        {#if mode === 'drawer'}
          <p>Çekmece satış olmadan açılır; açılış nedeniyle vardiyaya kaydedilir.</p>
        {:else}
          <p>{mode === 'in' ? 'Kasaya konan nakit (ör. bozuk para).' : 'Kasadan alınan nakit (ör. ara para, masraf).'}</p>
          <label>
            Tutar (TL)
            <input bind:value={amount} inputmode="decimal" autocomplete="off" placeholder="0,00" />
          </label>
        {/if}
        <label>
          {mode === 'drawer' ? 'Neden' : 'Açıklama'}
          <input bind:value={note} maxlength="200" autocomplete="off" />
        </label>
        <button class="primary" type="submit" disabled={busy || !valid}>
          {mode === 'in' ? 'Kasaya koy' : mode === 'out' ? 'Kasadan al' : 'Çekmeceyi aç'}
        </button>
        <button class="secondary" type="button" onclick={() => (mode = null)} disabled={busy}>Vazgeç</button>
      </form>
    {:else}
      {#if done}<p class="notice" role="status">{done}</p>{/if}
      <button class="primary" onclick={() => start('in')}>Kasaya para koy</button>
      <button class="primary" onclick={() => start('out')}>
        Kasadan para al{isManager ? '' : ' (müdür onayı)'}
      </button>
      <button class="secondary" onclick={() => start('drawer')}>
        Çekmeceyi aç (satışsız){isManager ? '' : ' (müdür onayı)'}
      </button>
      <button class="primary danger" onclick={onshiftclose} disabled={!canClose}>Vardiyayı kapat (kasa sayımı)</button>
      {#if !canClose}<p class="muted">Vardiya, sepet boşken kapatılabilir.</p>{/if}
      <button class="secondary" onclick={onclose}>Kapat</button>
    {/if}
    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </div>
</div>

{#if approving}
  <ManagerApproval
    action={approvalAction}
    what={mode === 'drawer' ? 'Çekmeceyi satış olmadan açmak' : 'Kasadan para almak'}
    onapproved={(token) => {
      approving = false
      record(token)
    }}
    oncancel={() => (approving = false)}
  />
{/if}
