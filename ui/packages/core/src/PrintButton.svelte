<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<!-- Sends the report to the receipt printer and shows the result. -->
<script>
  let { print, label = 'Yazdır' } = $props()

  let busy = $state(false)
  let done = $state(false)
  let error = $state('')

  async function run() {
    busy = true
    done = false
    error = ''
    try {
      await print()
      done = true
    } catch (e) {
      error = e.message
    } finally {
      busy = false
    }
  }
</script>

<button class="secondary" onclick={run} disabled={busy}>{label}</button>
{#if done}<p class="notice" role="status">Yazıcıya gönderildi.</p>{/if}
{#if error}<p class="error" role="alert">{error}</p>{/if}
