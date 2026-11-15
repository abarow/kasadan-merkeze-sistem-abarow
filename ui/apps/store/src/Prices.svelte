<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { api } from './lib/api.js'
  import { dateTime, money } from './lib/format.js'
  import PriceEditor from './PriceEditor.svelte'

  let { onunauthorized } = $props()

  let query = $state('')
  let products = $state(null)
  let error = $state('')
  let editing = $state(null)

  async function load() {
    try {
      products = await api.prices(query)
      error = ''
    } catch (e) {
      if (e.status === 401) onunauthorized()
      else error = e.message
    }
  }

  $effect(() => {
    query
    const timer = setTimeout(load, 250)
    return () => clearTimeout(timer)
  })

  // The first future-dated store record in the schedule.
  const next = (p) => p.store_upcoming[0]
</script>

<section aria-labelledby="fiyatlar">
  <h2 id="fiyatlar">Fiyatlar</h2>
  <p class="note">
    Ürünler ve merkez fiyatı merkezden gelir. Bir ürüne bu mağazada farklı fiyat (mağaza fiyatı) verilebilir; hemen ya da
    ileri tarihte geçerli olur, kasalara kendiliğinden iner ve merkeze bildirilir.
  </p>
  <label class="search">
    Ara
    <input type="search" bind:value={query} placeholder="ad, kod ya da barkod" />
  </label>
  {#if error}
    <p class="alert" role="alert">{error.charAt(0).toLocaleUpperCase('tr-TR') + error.slice(1)}</p>
  {/if}

  {#if editing !== null}
    <PriceEditor id={editing} onclose={() => (editing = null)} onsaved={load} {onunauthorized} />
  {/if}

  {#if products}
    {#if products.length === 0}
      <p class="muted">{query ? 'Aramayla eşleşen ürün yok.' : 'Merkezin kataloğu henüz gelmedi.'}</p>
    {:else}
      <div class="panel">
        <table class="prices">
          <thead>
            <tr>
              <th scope="col">Ürün</th>
              <th scope="col" class="num">Merkez fiyatı</th>
              <th scope="col" class="num">Mağaza fiyatı</th>
            </tr>
          </thead>
          <tbody>
            {#each products as p (p.id)}
              {@const upcoming = next(p)}
              <tr>
                <th scope="row">
                  <button class="link" onclick={() => (editing = p.id)}>{p.name}</button>
                  {#if p.discontinued}<span class="pill off">satışta değil</span>{/if}
                </th>
                <td class="num">{money(p.headquarters_price_kurus)}</td>
                <td class="num" data-testid="store-price">
                  {#if p.store_price_kurus !== undefined}<span class="different">{money(p.store_price_kurus)}</span>{:else}<span class="muted">—</span>{/if}
                  {#if upcoming}<br /><small class="muted"
                      >{`${upcoming.clear ? 'merkez fiyatına dönüş' : money(upcoming.unit_price_kurus)} · ${dateTime(upcoming.from)}`}</small
                    >{/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</section>
