<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<!-- The report of a closed shift: the sales and the cash count. Used in shift
     closing and in past shift reports. -->
<script>
  import { api } from './lib/api.js'
  import { methodName as method, money } from './lib/format.js'
  import PrintButton from './PrintButton.svelte'

  let { report } = $props()

  const time = (iso) => (iso ? new Date(iso).toLocaleString('tr-TR') : '—')
  const diffLabel = (d) => (d < 0 ? 'Kasa açığı' : d > 0 ? 'Kasa fazlası' : 'Fark')
</script>

<h2>Vardiya raporu <small>· No {report.shift.id}, {report.shift.cashier_name}</small></h2>
<dl>
  <dt>Açılış</dt>
  <dd>{time(report.shift.opened_at)}</dd>
  <dt>Kapanış</dt>
  <dd>{time(report.shift.closed_at)}</dd>
  <dt>Fiş sayısı</dt>
  <dd>{report.receipt_count}</dd>
  <dt>Satış toplamı</dt>
  <dd>{money(report.total_kurus)}</dd>
  {#each report.payments as p (p.method)}
    <dt>{method(p.method)}</dt>
    <dd>{money(p.amount_kurus)}</dd>
  {/each}
  <dt>İptal edilen satış</dt>
  <dd>{report.voided_count} ({money(report.voided_total_kurus)})</dd>
</dl>
<dl class="cashup">
  <dt>Açılış nakdi</dt>
  <dd>{money(report.shift.opening_cash_kurus)}</dd>
  <dt>Kasaya konan</dt>
  <dd>{money(report.cash_in_kurus)}</dd>
  <dt>Kasadan alınan</dt>
  <dd>{money(report.cash_out_kurus)}</dd>
  <dt>Satışsız çekmece açılışı</dt>
  <dd>{report.drawer_opens}</dd>
  <dt>Beklenen nakit</dt>
  <dd>{money(report.expected_cash_kurus)}</dd>
  <dt>Sayılan nakit</dt>
  <dd>{money(report.shift.counted_cash_kurus)}</dd>
  <dt>{diffLabel(report.difference_kurus)}</dt>
  <dd class:short={report.difference_kurus < 0}>{money(report.difference_kurus)}</dd>
</dl>
<PrintButton print={() => api.printShift(report.shift.id)} />
