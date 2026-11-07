<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { money, when } from './lib/format.js'

  let { stores, error } = $props()

  const links = {
    connected: { text: 'Bağlı', css: 'ok' },
    disconnected: { text: 'Bağlantı yok', css: 'bad' },
    never: { text: 'Hiç bağlanmadı', css: 'off' },
    unknown: { text: 'Durum bilinmiyor', css: 'off' },
  }
</script>

<section aria-labelledby="magazalar">
  <h2 id="magazalar">Mağazalar</h2>
  {#if error}
    <p class="alert" role="alert">Mağazaların durumu okunamadı: {error}</p>
  {/if}
  {#if stores}
    {#if stores.length === 0}
      <p class="muted">Merkeze bağlı mağaza yok. Mağaza eklemek için kurulum belgesine bakın.</p>
    {/if}
    <ul class="registers">
      {#each stores as s (s.id)}
        {@const link = links[s.link] ?? links.unknown}
        <li class="card" aria-label={s.name}>
          <div class="card-title">
            <strong>{s.name}</strong>
            <span class="pill {link.css}">{link.text}</span>
          </div>
          <dl>
            <dt>Bugün</dt>
            <dd>
              <span data-testid="today-total">{money(s.today_total_kurus)}</span><span class="muted"
                >{` · ${s.today_receipt_count} fiş`}</span
              >
            </dd>
            <dt>Son haber</dt>
            <dd>{when(s.last_seen)}</dd>
            {#if s.link === 'connected' && s.pending_events > 0}
              <dt>Bekleyen olay</dt>
              <dd>{s.pending_events}</dd>
            {/if}
            <dt>Son olay</dt>
            <dd>{when(s.last_event_at)}</dd>
          </dl>
          {#if s.link === 'disconnected'}
            <p class="note">Mağaza satışa devam eder; satışlar mağazada birikir, bağlantı gelince aktarılır.</p>
          {:else if s.link === 'unknown'}
            <p class="note">Olay alma servisi (ingest) durum yazmıyor; son yazma: {when(s.checked_at)}.</p>
          {/if}
          {#if s.missing_events?.length > 0}
            <p class="alert" data-testid="missing-events">
              Eksik kayıt (kasa numaralamış ama merkeze ulaşmadı): {s.missing_events
                .map((g) => `${g.register} ${g.producer === 'sales' ? 'satış' : g.producer === 'staff' ? 'vardiya' : g.producer} ${g.from === g.to ? `#${g.from}` : `#${g.from}–${g.to}`}`)
                .join(', ')}
            </p>
          {/if}
          {#if s.error}
            <p class="note">Mesaj yolu: {s.error}</p>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>
