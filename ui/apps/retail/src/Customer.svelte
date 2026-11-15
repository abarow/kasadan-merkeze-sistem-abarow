<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Customer screen: the basket, total and change. It comes from the cashier screen
  // with BroadcastChannel (lib/display.js); it is not touch.
  import { watchDisplay } from './lib/display.js'
  import { money } from '@abarow/core/lib/format.js'

  let state = $state({ status: 'closed', lines: [] })
  let list = $state()

  $effect(() => watchDisplay((s) => (state = s)))

  // Let the last added line be visible.
  $effect(() => {
    state.lines?.length
    list?.lastElementChild?.scrollIntoView({ block: 'end' })
  })
</script>

{#if state.fiscal_mode === 'simulator'}
  <div class="banner" role="alert">Deneme kasası: basılan fişlerin mali değeri yoktur.</div>
{/if}

<main class="customer" style:--bars={state.fiscal_mode === 'simulator' ? 1 : 0}>
  {#if state.status === 'closed'}
    <p class="message">Bu kasa kapalı</p>
  {:else if state.status === 'idle'}
    <p class="message">Hoş geldiniz</p>
  {:else if state.status === 'done'}
    <section class="done" aria-label="Ödeme">
      <p class="row"><span>Toplam</span><strong>{money(state.total_kurus)}</strong></p>
      <p class="row"><span>Ödenen</span><strong>{money(state.paid_kurus)}</strong></p>
      {#if state.change_kurus > 0}
        <p class="row change"><span>Para üstü</span><strong>{money(state.change_kurus)}</strong></p>
      {/if}
      <p class="thanks">Teşekkür ederiz</p>
    </section>
  {:else}
    <ul class="items" bind:this={list}>
      {#each state.lines as line, i (i)}
        <li>
          <span class="name">{line.name}<small>{line.qty}</small></span>
          <strong>{money(line.amount_kurus)}</strong>
        </li>
      {/each}
    </ul>
    <footer class="total">
      <span>{state.status === 'payment' ? 'Ödenecek tutar' : 'Toplam'}</span>
      <strong>{money(state.total_kurus)}</strong>
    </footer>
  {/if}
</main>
