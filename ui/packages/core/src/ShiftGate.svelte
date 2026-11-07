<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<!-- Shown after sign-in if the cashier has no open shift: the shift is opened
     with the opening cash. If someone else's shift is open at the register (a single drawer)
     no sale can be made; a manager can do register operations in that shift and can close
     the shift with a count. -->
<script>
  import { api } from './lib/api.js'
  import { parseTL } from './lib/format.js'

  let { cashier, shift, notice = '', onopened, oncash, oncloseshift, onreports, onlogout } = $props()

  let cash = $state('')
  let error = $state('')
  let busy = $state(false)

  const cashKurus = $derived(parseTL(cash))
  const since = (iso) => new Date(iso).toLocaleTimeString('tr-TR', { hour: '2-digit', minute: '2-digit' })

  async function open(e) {
    e.preventDefault()
    if (Number.isNaN(cashKurus) || busy) return
    busy = true
    error = ''
    try {
      onopened(await api.openShift(cashKurus))
    } catch (err) {
      error = err.message
    } finally {
      busy = false
    }
  }
</script>

<div class="login">
  <h1>{cashier.name}</h1>
  {#if !shift}
    <form class="cash" onsubmit={open}>
      <p>Vardiyanız açık değil. Çekmecedeki nakdi sayıp açılış tutarını girin.</p>
      <label>
        Açılış nakdi (TL)
        <input bind:value={cash} inputmode="decimal" autocomplete="off" placeholder="0,00" />
      </label>
      <button class="primary" type="submit" disabled={busy || Number.isNaN(cashKurus)}>Vardiyayı aç</button>
    </form>
  {:else}
    <p class="warning">
      Kasada açık vardiya var: <strong>{shift.cashier_name}</strong> (açılış {since(shift.opened_at)}). Kasada aynı anda
      tek vardiya açık olabilir.
    </p>
    {#if cashier.role === 'manager'}
      <button class="primary" onclick={oncash}>Kasa işlemleri (para al, vardiyayı kapat)</button>
    {:else}
      <button class="primary" onclick={oncloseshift}>Vardiyayı kapat (müdür onayıyla)</button>
    {/if}
  {/if}
  {#if error || notice}<p class="error" role="alert">{error || notice}</p>{/if}
  <button class="secondary" onclick={onreports}>Raporlar</button>
  <button class="secondary" onclick={onlogout}>Çıkış</button>
</div>
