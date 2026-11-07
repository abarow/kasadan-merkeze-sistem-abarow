<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import { api } from './lib/api.js'

  // The person changes their own password (with their current password).
  let { onunauthorized } = $props()

  let form = $state({ current: '', next: '', next2: '' })
  let error = $state('')
  let notice = $state('')
  let busy = $state(false)

  async function submit(e) {
    e.preventDefault()
    error = ''
    notice = ''
    // The new password is typed twice: so that a typo does not lock the person out.
    if (form.next !== form.next2) {
      error = 'Yeni parolalar aynı değil.'
      return
    }
    busy = true
    try {
      await api.changePassword(form.current, form.next)
      form = { current: '', next: '', next2: '' }
      notice = 'Parolanız değişti; başka cihazlardaki oturumlarınız kapandı.'
    } catch (err) {
      if (err.status === 401) return onunauthorized()
      error = err.message.charAt(0).toLocaleUpperCase('tr-TR') + err.message.slice(1)
    } finally {
      busy = false
    }
  }
</script>

<section aria-labelledby="parolam">
  <h2 id="parolam">Parolam</h2>
  <p class="note">
    Parola en az 10 karakterdir. Değişince başka cihazlarda açık kalan oturumlarınız kapanır; bu ekran açık kalır.
  </p>
  <div class="panel editor">
    {#if error}
      <p class="alert" role="alert">{error}</p>
    {/if}
    {#if notice}
      <p class="ok-note" role="status">{notice}</p>
    {/if}
    <form class="grid-form" onsubmit={submit}>
      <label>
        Şu anki parola
        <input type="password" bind:value={form.current} autocomplete="current-password" required />
      </label>
      <label>
        Yeni parola
        <input type="password" bind:value={form.next} autocomplete="new-password" required />
      </label>
      <label>
        Yeni parola (tekrar)
        <input type="password" bind:value={form.next2} autocomplete="new-password" required />
      </label>
      <div class="actions">
        <button class="primary" disabled={busy}>Parolamı değiştir</button>
      </div>
    </form>
  </div>
</section>
