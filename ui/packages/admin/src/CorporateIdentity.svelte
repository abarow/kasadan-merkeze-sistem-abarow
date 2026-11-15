<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // The person's own corporate identity (OIDC): it links it to their account or removes
  // the link. The person proves the identity themselves at the IdP; once linked "sign in with
  // corporate sign-in" is enough at later sign-ins. The role and store permission are from the IdP
  // gelmez, abarow'daki hesaptan okunur.
  //
  // status() → {linked}; unlink(): removes the link. notice: the result of the return
  // from the IdP (lib/oidc.js). In a session not tied to a person (store password)
  // it cannot be linked.
  import { capitalize } from './lib/format.js'

  let { status, unlink, notice, onunauthorized, personal = true } = $props()

  let linked = $state(null)
  let error = $state('')
  let done = $state('')
  let busy = $state(false)

  async function load() {
    try {
      linked = (await status()).linked
    } catch (e) {
      if (e.status === 401) onunauthorized()
      else error = capitalize(e.message)
    }
  }
  load()

  async function remove() {
    busy = true
    error = done = ''
    try {
      await unlink()
      done = 'Kurumsal kimlik bağı kaldırıldı.'
      await load()
    } catch (e) {
      if (e.status === 401) onunauthorized()
      else error = capitalize(e.message)
    } finally {
      busy = false
    }
  }
</script>

<section aria-labelledby="kurumsal-identity">
  <h2 id="kurumsal-identity">Kurumsal kimlik</h2>
  {#if notice?.error}
    <p class="alert" role="alert">{notice.error}</p>
  {/if}
  {#if notice?.linked}
    <p class="ok-note" role="status">Kurumsal kimliğiniz hesabınıza bağlandı.</p>
  {/if}
  {#if error}
    <p class="alert" role="alert">{error}</p>
  {/if}
  {#if done}
    <p class="ok-note" role="status">{done}</p>
  {/if}
  <div class="panel editor">
    {#if !personal}
      <p class="muted">Mağaza parolasıyla girdiniz; kurumsal kimlik kişisel hesaba bağlanır. Müdür kodunuz ve PIN'inizle girin.</p>
    {:else if linked === null}
      <p class="muted">Yükleniyor…</p>
    {:else if linked}
      <p data-testid="oidc-state">Hesabınız bir kurumsal kimliğe bağlı: giriş ekranında "kurumsal girişle gir" ile girebilirsiniz.</p>
      <div class="actions"><button disabled={busy} onclick={remove}>Bağı kaldır</button></div>
    {:else}
      <p data-testid="oidc-state">Hesabınız bir kurumsal kimliğe bağlı değil.</p>
      <div class="actions"><a class="primary" href="/api/auth/oidc/link">Kurumsal kimliği bağla</a></div>
    {/if}
  </div>
</section>
