<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Transfers. The receiving place takes delivery of a transfer in transit: the quantity that came is
  // entered (less if it came short, zero if it never came); the entered quantity enters the receiving
  // place's stock.
  //
  // Store server (scope 'store'): those coming to this store and those going from here;
  // here is this store. Headquarters (scope 'headquarters'): those sent from the headquarters warehouse and
  // the stores (those of the places the person sees); here is the headquarters
  // warehouse, canReceive is the permission to enter the warehouse.
  //
  // api: {receiveTransfer}. names: place id → name.
  import { capitalize, dateTime, parseQty, quantity } from './lib/format.js'

  let { api, scope = 'store', here, transfers, names, canReceive = true, onclose, onsaved, onunauthorized } = $props()

  let receiving = $state(null) // {transfer, qty: {ürün kimliği: girilen metin}}
  let error = $state('')
  let notice = $state('')
  let busy = $state(false)

  const headquarters = $derived(scope === 'headquarters')
  const name = (id) => names[id] ?? id
  const input = (milli) => String(milli / 1000).replace('.', ',')

  function start(t) {
    receiving = { transfer: t, qty: Object.fromEntries(t.lines.map((l) => [l.product_id, input(l.qty_milli)])) }
    error = ''
    notice = ''
  }

  async function receive(e) {
    e.preventDefault()
    const t = receiving.transfer
    const lines = []
    for (const l of t.lines) {
      // The unit is not known here: the server rejects a fractional quantity for a product counted in pieces.
      const milli = parseQty(receiving.qty[l.product_id], 'kg')
      if (milli === null) {
        error = `${l.name}: miktar geçersiz (hiç gelmediyse 0 yazın).`
        return
      }
      lines.push({ product_id: l.product_id, qty_milli: milli })
    }
    busy = true
    error = ''
    try {
      await api.receiveTransfer(t.id, lines)
      notice = `${name(t.from)} transferi teslim alındı; gelen miktar ${headquarters ? 'merkez deposuna' : 'stoğa'} girdi.`
      receiving = null
      onsaved()
    } catch (err) {
      if (err.status === 401) onunauthorized()
      else error = capitalize(err.message)
    } finally {
      busy = false
    }
  }
</script>

<div class="panel editor" role="region" aria-label="Transferler">
  <div class="section-head">
    <h3>Transferler</h3>
    <button onclick={onclose}>Kapat</button>
  </div>
  {#if headquarters}
    <p class="note">
      Gönderilen mal gönderen yerin stoğundan hemen düşer; alan yer teslim alınca onun stoğuna girer. Mağazadan mağazaya
      giden transfer merkez üzerinden iletilir.
    </p>
  {:else}
    <p class="note">
      Gelen transfer, mal mağazaya ulaşınca teslim alınır; teslim alınana kadar yoldadır ve stoğa girmez. Gönderdiğiniz
      transfer gönderildiği anda stoktan düşer.
    </p>
  {/if}
  {#if error}
    <p class="alert" role="alert">{error}</p>
  {/if}
  {#if notice}
    <p class="ok-note" role="status">{notice}</p>
  {/if}

  {#if receiving}
    {@const t = receiving.transfer}
    <form class="grid-form" onsubmit={receive} aria-label="Teslim al">
      <p>
        <strong>{name(t.from)}</strong> gönderdi ({dateTime(t.at)}, {t.by}). Gelen miktarı girin: eksik geleni az, hiç
        gelmeyeni 0 yazın.
      </p>
      <table class="entry">
        <thead>
          <tr><th scope="col">Ürün</th><th scope="col" class="num">Gönderilen</th><th scope="col" class="num">Gelen</th></tr>
        </thead>
        <tbody>
          {#each t.lines as l (l.product_id)}
            <tr>
              <th scope="row">{l.name}</th>
              <td class="num">{quantity(l.qty_milli)}</td>
              <td class="num">
                <input class="qty" bind:value={receiving.qty[l.product_id]} inputmode="decimal" aria-label={`${l.name} gelen miktar`} />
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
      <div class="actions">
        <button type="button" onclick={() => (receiving = null)}>Vazgeç</button>
        <button type="submit" class="primary" disabled={busy}>Teslim al</button>
      </div>
    </form>
  {/if}

  {#if transfers.length === 0}
    <p class="muted">Transfer yok.</p>
  {:else}
    <table class="transfers">
      <thead>
        <tr><th scope="col">Zaman</th><th scope="col">{headquarters ? 'Nereden → nereye' : 'Transfer'}</th><th scope="col">Ürünler</th><th scope="col">Durum</th></tr>
      </thead>
      <tbody>
        {#each transfers as t (t.id)}
          {@const incoming = t.to === here}
          <tr>
            <td>{dateTime(t.at)}</td>
            <td>
              {#if headquarters}{name(t.from)} → {name(t.to)}{:else}{incoming ? `Gelen: ${name(t.from)}` : `Giden: ${name(t.to)}`}{/if}<br /><small
                class="muted">{t.by}</small
              >
            </td>
            <td>
              {#each t.lines as l (l.product_id)}
                <div>
                  {l.name}: {quantity(l.qty_milli)}
                  {#if l.received_milli !== undefined && l.received_milli !== l.qty_milli}
                    <span class="negative">(gelen {quantity(l.received_milli)})</span>
                  {/if}
                </div>
              {/each}
            </td>
            <td>
              {#if !headquarters && !incoming}
                <span class="pill">gönderildi</span>
              {:else if t.pending}
                <span class="pill bad">yolda</span>
                {#if incoming && canReceive}
                  <button onclick={() => start(t)}>Teslim al</button>
                {/if}
              {:else}
                <span class="pill ok">teslim alındı</span><br /><small class="muted">{dateTime(t.received_at)}, {t.received_by}</small>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</div>
