<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { money, when } from './lib/format.js'

  let { registers, error } = $props()

  const links = {
    connected: { text: 'Bağlı', css: 'ok' },
    disconnected: { text: 'Bağlantı yok', css: 'bad' },
    never: { text: 'Hiç bağlanmadı', css: 'off' },
  }

  // The API returns at most 100 missing receipt ranges for a register.
  const MAX_RANGES = 100
  const ranges = (list) => list.map((r) => (r.from === r.to ? `${r.from}` : `${r.from}–${r.to}`)).join(', ')

  // The first few of the sales whose receipt could not be printed are shown in detail; the rest is
  // summarized with a count so that the card does not grow long.
  const SHOWN_UNFINISHED = 3
  const sum = (list) => list.reduce((t, u) => t + u.total_kurus, 0)

  // Cash difference: counted − expected; minus is a cash shortage, plus a cash surplus.
  function difference(kurus) {
    if (kurus === 0) return 'kasa farkı yok'
    return kurus < 0 ? `kasa açığı ${money(-kurus)}` : `kasa fazlası ${money(kurus)}`
  }
</script>

<section aria-labelledby="kasalar">
  <h2 id="kasalar">Kasa durumu</h2>
  {#if error}
    <p class="alert" role="alert">Kasaların durumu okunamadı: {error}</p>
  {/if}
  {#if registers}
    {#if registers.length === 0}
      <p class="muted">Operasyona bağlı kasa yok. Kasa eklemek için kurulum belgesine bakın.</p>
    {/if}
    <ul class="registers">
      {#each registers as r (r.id)}
        {@const link = links[r.link] ?? links.never}
        <li class="card" aria-label={r.name}>
          <div class="card-title">
            <strong>{r.name}</strong>
            <span class="pill {link.css}">{link.text}</span>
          </div>
          <dl>
            <dt>Son haber</dt>
            <dd>{when(r.last_seen)}</dd>
            {#if r.link === 'connected' && r.pending_events > 0}
              <dt>Bekleyen olay</dt>
              <dd>{r.pending_events}</dd>
            {/if}
            <dt>Gelen fiş</dt>
            <dd><span data-testid="receipts">{r.receipt_count}</span>{#if r.last_receipt_no}<span class="muted">{` · son no ${r.last_receipt_no}`}</span>{/if}</dd>
            <dt>Son satış</dt>
            <dd>{when(r.last_sale_at)}</dd>
            <dt>Vardiya</dt>
            <dd data-testid="open-shift">
              {#if r.open_shift}{r.open_shift.cashier_name}<span class="muted">{` · açılış ${when(r.open_shift.opened_at)}`}</span>{:else}<span class="muted">açık vardiya yok</span>{/if}
            </dd>
            {#if r.last_closed_shift}
              {@const c = r.last_closed_shift}
              <dt>Son vardiya</dt>
              <dd data-testid="last-shift">
                {c.cashier_name}<span class="muted">{` · kapanış ${when(c.closed_at)} · `}</span><span
                  class={c.difference_kurus === 0 ? 'muted' : 'bad'}>{difference(c.difference_kurus)}</span>
              </dd>
            {/if}
          </dl>
          {#if r.link === 'disconnected'}
            <p class="note">Kasa satışa devam eder; satışlar kasada birikir, bağlantı gelince aktarılır.</p>
          {/if}
          {#if r.unfinished_sales.length > 0}
            <div class="alert">
              <strong>Fişi kesilemeyen satış: {r.unfinished_sales.length} ({money(sum(r.unfinished_sales))})</strong>
              <ul class="unfinished">
                {#each r.unfinished_sales.slice(0, SHOWN_UNFINISHED) as u (u.sale_id)}
                  <li>{money(u.total_kurus)} · ilk deneme {when(u.since)}{#if u.cashier_id}{` · ${u.cashier_id}`}{/if} · {u.reason}</li>
                {/each}
                {#if r.unfinished_sales.length > SHOWN_UNFINISHED}
                  <li>ve {r.unfinished_sales.length - SHOWN_UNFINISHED} satış daha</li>
                {/if}
              </ul>
              <span class="small">Ödemesi alındı; kasada "Fişi tamamla" ya da müdür onayıyla iade edilmeli.</span>
            </div>
          {/if}
          {#if r.missing_receipts.length > 0}
            <p class="alert">
              Eksik fiş (henüz gelmedi): {ranges(r.missing_receipts)}{#if r.missing_receipts.length >= MAX_RANGES} ve
                daha fazlası{/if}
            </p>
          {/if}
          {#if r.missing_events?.length > 0}
            <p class="alert" data-testid="missing-events">
              Eksik kayıt (kasa numaralamış ama gelmedi): {r.missing_events
                .map((g) => `${g.producer === 'sales' ? 'satış' : g.producer === 'staff' ? 'vardiya' : g.producer} ${g.from === g.to ? `#${g.from}` : `#${g.from}–${g.to}`}`)
                .join(', ')}
            </p>
          {/if}
          {#if r.error}
            <p class="note">Mesaj yolu: {r.error}</p>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>
