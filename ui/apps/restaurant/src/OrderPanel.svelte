<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Check: lines (not sent to the kitchen, sent, cancelled, paid),
  // total and remaining; send to the kitchen, bill (the check printout), pay, split
  // the bill, move or merge, cancel (manager approval). Every operation is on the server;
  // the returned check is written to the screen.
  import { api } from './lib/api.js'
  import { money } from '@abarow/core/lib/format.js'
  import MoveDialog from './MoveDialog.svelte'
  import SplitDialog from './SplitDialog.svelte'
  import VoidDialog from './VoidDialog.svelte'

  let { order, areas, isManager, busy: appBusy, tableName, onchange, onerror, onpay, onclosed } = $props()

  let busy = $state(false)
  let notice = $state('')
  let splitting = $state(false)
  let moving = $state(false)
  let voiding = $state(null) // iptal edilecek satır

  const lines = $derived(order.lines)
  const live = $derived(lines.filter((l) => l.status !== 'voided'))
  const paid = $derived(order.total_kurus - order.due_kurus)

  async function act(fn, message = '') {
    busy = true
    notice = ''
    onerror('')
    try {
      const o = await fn()
      if (o) onchange(o)
      notice = message
    } catch (e) {
      onerror(e.message)
      // If a station could not be printed the check's latest state comes too.
      if (e.data?.order) onchange(e.data.order)
    } finally {
      busy = false
    }
  }

  const step = (l, delta) => act(() => api.setLine(order.id, l.id, l.qty + delta, l.note))
  const send = () => act(() => api.send(order.id), 'Mutfağa gönderildi.')
  const bill = () => act(() => api.bill(order.id), 'Adisyon basıldı.')
  const closeEmpty = () =>
    act(async () => {
      await api.closeEmpty(order.id)
      onclosed()
    })
  const statusName = { pending: 'gönderilmedi', sent: 'mutfakta', voided: 'iptal' }
</script>

<section class="order" aria-label={`Adisyon: ${tableName}`}>
  <ul class="lines">
    {#each lines as l (l.id)}
      <li class={l.status} class:paid={l.paid === l.qty}>
        <div class="name">
          <span class="label">{l.label}</span>
          <small>
            {l.qty} × {money(l.unit_price_kurus)} · {statusName[l.status]}{l.paid ? ` · ${l.paid} ödendi` : ''}{l.held ? ` · ${l.held} ödemede` : ''}
            {#if l.note}· not: {l.note}{/if}
            {#if l.status === 'voided'}· {l.void_reason}{/if}
          </small>
        </div>
        {#if l.status === 'pending'}
          <button class="step" onclick={() => step(l, -1)} disabled={busy || appBusy} aria-label={`${l.label} azalt`}>−</button>
          <button class="step" onclick={() => step(l, 1)} disabled={busy || appBusy || l.qty >= 99} aria-label={`${l.label} artır`}>+</button>
        {:else if l.status === 'sent' && l.open_qty > 0}
          <button class="link" onclick={() => (voiding = l)} disabled={busy || appBusy}>İptal</button>
        {/if}
        <strong class="amount">{money(l.total_kurus)}</strong>
      </li>
    {:else}
      <li class="empty">Sağdaki menüden ürün ekleyin.</li>
    {/each}
  </ul>
  {#if notice}<p class="notice" role="status">{notice}</p>{/if}
  <footer>
    <div class="total"><span>TOPLAM</span><strong>{money(order.total_kurus)}</strong></div>
    {#if paid > 0}
      <div class="total due"><span>KALAN</span><strong>{money(order.due_kurus)}</strong></div>
    {/if}
    <div class="actions">
      <button class="primary" onclick={send} disabled={busy || appBusy || order.pending === 0}>
        Mutfağa gönder{order.pending ? ` (${order.pending})` : ''}
      </button>
      <button onclick={bill} disabled={busy || appBusy || live.length === 0}>Hesap</button>
      <button onclick={() => (splitting = true)} disabled={busy || appBusy || order.due_kurus === 0 || order.pending > 0}>Hesabı böl</button>
      <button onclick={() => (moving = true)} disabled={busy || appBusy || !!order.payment}>Taşı / birleştir</button>
      {#if lines.length === 0}
        <button onclick={closeEmpty} disabled={busy || appBusy}>Masayı kapat</button>
      {/if}
      <button class="primary pay" onclick={() => onpay([])} disabled={busy || appBusy || order.due_kurus === 0 || order.pending > 0}>
        ÖDE {money(order.due_kurus)}
      </button>
    </div>
    {#if order.pending > 0 && order.due_kurus > 0}
      <p class="muted">Ödemeden önce bekleyen satırları mutfağa gönderin.</p>
    {/if}
  </footer>
</section>

{#if splitting}
  <SplitDialog
    {order}
    onpay={(parts) => {
      splitting = false
      onpay(parts)
    }}
    oncancel={() => (splitting = false)}
  />
{/if}

{#if moving}
  <MoveDialog
    {order}
    {areas}
    onmove={(tableID) => {
      moving = false
      act(() => api.move(order.id, order.version, tableID), 'Adisyon taşındı.')
    }}
    onmerge={(other) => {
      moving = false
      act(() => api.merge(order.id, order.version, other.id, other.version), 'Adisyonlar birleştirildi.')
    }}
    oncancel={() => (moving = false)}
  />
{/if}

{#if voiding}
  <VoidDialog
    line={voiding}
    {isManager}
    onvoid={(qty, reason, approval) => {
      const line = voiding
      voiding = null
      act(() => api.voidLine(order.id, line.id, qty, reason, approval), 'İptal edildi; istasyona iptal fişi basıldı.')
    }}
    oncancel={() => (voiding = null)}
  />
{/if}
