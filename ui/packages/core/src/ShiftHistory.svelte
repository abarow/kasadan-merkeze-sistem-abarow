<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<!-- Closed shifts, from newest to oldest. A manager sees all the shifts, a cashier
     only their own shifts (staff checks). -->
<script>
  import { api } from './lib/api.js'
  import ShiftReportView from './ShiftReportView.svelte'

  let { onback } = $props()

  let shifts = $state(null)
  let report = $state(null)
  let error = $state('')
  let busy = $state(false)

  const time = (iso) => new Date(iso).toLocaleString('tr-TR', { dateStyle: 'short', timeStyle: 'short' })

  api
    .shifts()
    .then((list) => (shifts = list))
    .catch((e) => (error = e.message))

  async function open(sh) {
    busy = true
    error = ''
    try {
      report = await api.shiftReport(sh.id)
    } catch (e) {
      error = e.message
    } finally {
      busy = false
    }
  }
</script>

{#if report}
  <ShiftReportView {report} />
  <button class="secondary" onclick={() => (report = null)}>Listeye dön</button>
{:else}
  <h2>Vardiya raporları</h2>
  {#if shifts?.length === 0}<p class="muted">Kapanmış vardiya yok.</p>{/if}
  <ul class="shifts">
    {#each shifts ?? [] as sh (sh.id)}
      <li>
        <button onclick={() => open(sh)} disabled={busy}>
          <span>No {sh.id} · {sh.cashier_name}</span>
          <small>{time(sh.opened_at)} – {time(sh.closed_at)}</small>
        </button>
      </li>
    {/each}
  </ul>
  <button class="secondary" onclick={onback}>Geri</button>
{/if}
{#if error}<p class="error" role="alert">{error}</p>{/if}
