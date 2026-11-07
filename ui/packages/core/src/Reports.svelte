<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { api } from './lib/api.js'
  import { methodName as method, money } from './lib/format.js'
  import ManagerApproval from './ManagerApproval.svelte'
  import PrintButton from './PrintButton.svelte'
  import ShiftHistory from './ShiftHistory.svelte'
  import UpdateStatus from './UpdateStatus.svelte'

  let { onclose, canCloseDay = false } = $props()

  let report = $state(null)
  let kind = $state('X')
  let confirming = $state(false)
  let error = $state('')
  let busy = $state(false)
  let history = $state(false) // vardiya raporları gösteriliyor mu
  let update = $state(false) // güncelleme durumu gösteriliyor mu
  let approving = $state(false) // Z için müdür onayı bekleniyor mu

  async function load(fetchReport, reportKind) {
    busy = true
    error = ''
    try {
      report = await fetchReport()
      kind = reportKind
    } catch (e) {
      error = e.message
    } finally {
      busy = false
      confirming = false
    }
  }

  load(api.xReport, 'X')

  const rate = (bp) => `%${String(bp / 100).replace('.', ',')}`
  const time = (iso) => (iso ? new Date(iso).toLocaleString('tr-TR') : '—')
</script>

<div class="overlay">
  <div class="dialog report">
    {#if history}
      <ShiftHistory onback={() => (history = false)} />
    {:else if update}
      <UpdateStatus onback={() => (update = false)} />
    {:else}
      <h2>{kind} raporu {#if report}<small>· Z no {report.z_no}</small>{/if}</h2>

      {#if report}
        <dl>
          <dt>Dönem başı</dt>
          <dd>{time(report.opened_at)}</dd>
          {#if report.closed_at}
            <dt>Dönem sonu</dt>
            <dd>{time(report.closed_at)}</dd>
          {/if}
          <dt>Fiş sayısı</dt>
          <dd>
            {report.receipt_count}
            {#if report.receipt_count > 0}(no {report.first_receipt_no}–{report.last_receipt_no}){/if}
          </dd>
        </dl>

        <table>
          <thead>
            <tr><th>KDV</th><th>Toplam (KDV dahil)</th><th>KDV tutarı</th></tr>
          </thead>
          <tbody>
            {#each report.taxes as t (t.rate_bp)}
              <tr><td>{rate(t.rate_bp)}</td><td>{money(t.gross_kurus)}</td><td>{money(t.tax_kurus)}</td></tr>
            {/each}
          </tbody>
          <tfoot>
            <tr><th>TOPLAM</th><th>{money(report.total_kurus)}</th><th>{money(report.tax_total_kurus)}</th></tr>
          </tfoot>
        </table>

        <dl>
          {#each report.payments as p (p.method)}
            <dt>{method(p.method)}</dt>
            <dd>{money(p.amount_kurus)}</dd>
          {/each}
          <dt>İptal edilen satış</dt>
          <dd>{report.voided_count} ({money(report.voided_total_kurus)})</dd>
        </dl>
      {/if}

      {#if error}<p class="error" role="alert">{error}</p>{/if}
      {#if report && !confirming}
        <PrintButton print={() => api.printDay(kind === 'Z' ? report.z_no : 0)} />
      {/if}

      {#if kind === 'X'}
        {#if confirming}
          <p class="warning">Gün sonu (Z) alınsın mı? Açık dönem kapanır ve yeni dönem başlar; geri alınamaz.</p>
          <button
            class="primary danger"
            onclick={() => (canCloseDay ? load(api.closeDay, 'Z') : (approving = true))}
            disabled={busy}>Evet, Z raporu al</button
          >
          <button class="secondary" onclick={() => (confirming = false)} disabled={busy}>Vazgeç</button>
        {:else}
          <button class="secondary" onclick={() => load(api.xReport, 'X')} disabled={busy}>Yenile</button>
          <button class="secondary" onclick={() => (history = true)} disabled={busy}>Vardiya raporları</button>
          <button class="primary" onclick={() => (confirming = true)} disabled={busy || !report}>
            Gün sonu (Z) al{canCloseDay ? '' : ' (müdür onayı)'}
          </button>
        {/if}
      {/if}
    {/if}
    {#if !update && !history}
      <button class="secondary" onclick={() => (update = true)}>Güncelleme</button>
    {/if}
    <button class="secondary" onclick={onclose}>Kapat</button>
  </div>
</div>

{#if approving}
  <ManagerApproval
    action="close_day"
    what="Gün sonu (Z)"
    onapproved={(token) => {
      approving = false
      load(() => api.closeDay(token), 'Z')
    }}
    oncancel={() => {
      approving = false
      confirming = false
    }}
  />
{/if}
