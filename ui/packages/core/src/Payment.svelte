<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { api } from './lib/api.js'
  import { money, parseTL, toTL } from './lib/format.js'
  import ManagerApproval from './ManagerApproval.svelte'

  // ondismiss: the window of a sale whose receipt could not be printed (charged) is closed;
  // the sale is completed later at the register with "Fişi tamamla" or its payment is
  // refunded. isManager: manager approval is not needed for the refund.
  let { sale, oncompleted, oncancel, ondismiss, isManager = false } = $props()

  // The panel is reopened for each sale; the sale's first state is copied and
  // this copy follows the state that changes during payment (e.g. charged).
  // structuredClone cannot copy a reactive object (Proxy); $state.snapshot is used.
  // svelte-ignore state_referenced_locally
  let current = $state($state.snapshot(sale))
  let cash = $state('')
  let error = $state('')
  let busy = $state(false)
  let confirmingRefund = $state(false)
  let approving = $state(false)
  let refunded = $state(null) // iade edilen satış

  const charged = $derived(current.status === 'charged')
  const cashKurus = $derived(parseTL(cash))
  const change = $derived(Number.isNaN(cashKurus) ? 0 : cashKurus - current.total_kurus)
  const quickAmounts = [5000, 10000, 20000, 50000]
  // In cash, what was taken from the customer and (if change was given) what stays in the drawer.
  const cashTaken = $derived(current.payments.filter((p) => p.method === 'cash').reduce((n, p) => n + p.amount_kurus, 0))
  const cashKept = $derived(cashTaken - current.change_kurus)
  const taken = (method) => current.payments.filter((p) => p.method === method).reduce((n, p) => n + p.amount_kurus, 0)
  const cardTaken = $derived(taken('card'))
  const mealTaken = $derived(taken('meal_card'))

  // The payment of a sale whose receipt could not be printed is refunded and the sale is cancelled
  // (since no receipt was ever issued no fiscal document is needed). It is a manager's job.
  async function refund(approval) {
    busy = true
    error = ''
    try {
      refunded = await api.refundCharged(current.id, approval)
    } catch (e) {
      error = e.message
    } finally {
      busy = false
      confirmingRefund = false
    }
  }

  async function pay(payments) {
    busy = true
    error = ''
    try {
      oncompleted(await api.pay(current.id, payments))
    } catch (e) {
      error = e.message
      // 503: the payment may have been taken but the receipt not printed; get the current state.
      if (e.status === 503) {
        current = await api.sale(current.id).catch(() => current)
      }
    } finally {
      busy = false
    }
  }

  const payCard = () => pay([{ method: 'card', amount_kurus: current.total_kurus }])
  const payMeal = () => pay([{ method: 'meal_card', amount_kurus: current.total_kurus }])
  const payCash = () => pay([{ method: 'cash', amount_kurus: cashKurus }])
  // Mixed payment: if the cash taken is less than the amount the rest is charged by card or meal
  // card. Change is given only from cash; in a mixed payment there
  // is no change.
  const payMixed = (method) =>
    pay([
      { method: 'cash', amount_kurus: cashKurus },
      { method, amount_kurus: current.total_kurus - cashKurus },
    ])
  // charged: sales continues with the recorded payment; the customer is not charged again.
  const retry = () => pay(current.payments)
</script>

<div class="overlay">
  <div class="dialog">
    <h2>Ödeme</h2>
    <p class="due">Ödenecek <strong>{money(current.total_kurus)}</strong></p>

    {#if charged}
      <p class="warning">
        Ödeme alındı ama fiş basılamadı. Müşteriden tekrar ödeme almayın; yazıcıyı kontrol edip tekrar deneyin.
      </p>
      {#if refunded}
        <p class="notice" role="status">
          Ödeme iade edildi, satış iptal edildi.
          {#if cardTaken > 0}Kart işlemi ({money(cardTaken)}) geri alındı.{/if}
          {#if mealTaken > 0}Yemek kartı işlemi ({money(mealTaken)}) geri alındı.{/if}
          {#if cashTaken > 0}
            Müşteriye nakit {money(cashKept)} verin{cashKept !== cashTaken
              ? ` (para üstü verilmediyse ${money(cashTaken)})`
              : ''}.
          {/if}
        </p>
        <button class="primary" onclick={ondismiss}>Tamam</button>
      {:else if confirmingRefund}
        <p class="warning">
          Ödeme müşteriye geri verilecek ve satış iptal edilecek: kart ve yemek kartı işlemi geri alınır, nakdi çekmeceden siz
          verirsiniz. Fiş kesilmediği için mali belge gerekmez.
        </p>
        <button class="primary danger" onclick={() => (isManager ? refund() : (approving = true))} disabled={busy}>
          Evet, ödemeyi iade et
        </button>
        <button class="secondary" onclick={() => (confirmingRefund = false)} disabled={busy}>Vazgeç</button>
      {:else}
        <button class="primary" onclick={retry} disabled={busy}>Fişi tekrar dene</button>
        <button class="secondary" onclick={ondismiss} disabled={busy}>Sonra tamamla</button>
        <button class="secondary" onclick={() => (confirmingRefund = true)} disabled={busy}>
          Ödemeyi iade et{isManager ? '' : ' (müdür onayı)'}
        </button>
      {/if}
    {:else}
      <div class="pos">
        <button class="primary" onclick={payCard} disabled={busy}>Kart ile öde</button>
        <button class="primary" onclick={payMeal} disabled={busy}>Yemek kartı ile öde</button>
      </div>

      <div class="cash">
        <label>
          Alınan nakit (TL)
          <input bind:value={cash} inputmode="decimal" placeholder={toTL(current.total_kurus)} aria-label="Alınan nakit" />
        </label>
        <div class="quick">
          <button onclick={() => (cash = toTL(current.total_kurus))} disabled={busy}>Tam tutar</button>
          {#each quickAmounts as amount (amount)}
            <button onclick={() => (cash = toTL(amount))} disabled={busy}>{money(amount)}</button>
          {/each}
        </div>
        {#if cashKurus > 0}
          <p class="change">{change >= 0 ? 'Para üstü' : 'Eksik'} <strong>{money(Math.abs(change))}</strong></p>
        {/if}
        <button class="primary" onclick={payCash} disabled={busy || !(cashKurus > 0) || change < 0}>Nakit al</button>
        {#if cashKurus > 0 && change < 0}
          <button class="primary" onclick={() => payMixed('card')} disabled={busy}>Kalan {money(-change)} kartla öde</button>
          <button class="primary" onclick={() => payMixed('meal_card')} disabled={busy}>
            Kalan {money(-change)} yemek kartıyla öde
          </button>
        {/if}
      </div>

      <button class="secondary" onclick={oncancel} disabled={busy}>Vazgeç</button>
    {/if}

    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </div>
</div>

{#if approving}
  <ManagerApproval
    action="refund_charged"
    what="Ödemeyi iade etmek"
    onapproved={(token) => {
      approving = false
      refund(token)
    }}
    oncancel={() => (approving = false)}
  />
{/if}
