<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Sign-in screen: it learns from the server which routes are open
  // (GET /api/auth/methods, pkg/login) and draws the forms accordingly. Password,
  // PIN or corporate sign-in (OIDC) are not known separately; whichever adapters the service has connected
  // are listed.
  //
  // heading: the title ("Mağaza", "Merkez").
  // loadMethods(): returns the method list ({methods: [...]}).
  // signIn(body): signs in with the form fields, returns the signed-in person's information.
  // onlogin(who): called when the sign-in is done.
  import { onMount } from 'svelte'
  import { oidcNotice } from './lib/oidc.js'

  let { heading, loadMethods, signIn, onlogin } = $props()

  let methods = $state(null)
  let active = $state(0)
  let values = $state({})
  // The reason for failure on return from the corporate sign-in (lib/oidc.js).
  let error = $state(oidcNotice.error ?? '')
  let busy = $state(false)

  onMount(() => {
    loadMethods()
      .then((m) => (methods = m.methods ?? []))
      .catch((err) => {
        methods = []
        error = capitalize(err.message)
      })
  })

  let method = $derived(methods?.[active])
  let others = $derived((methods ?? []).filter((_, i) => i !== active))

  // The server's message starts with a lower-case letter ("parola yanlış").
  function capitalize(s) {
    return s.charAt(0).toLocaleUpperCase('tr-TR') + s.slice(1)
  }

  function switchTo(id) {
    active = methods.findIndex((m) => m.id === id)
    values = {}
    error = ''
  }

  let ready = $derived(method?.kind === 'form' && method.fields.every((f) => (values[f.name] ?? '') !== ''))

  async function submit(e) {
    e.preventDefault()
    busy = true
    error = ''
    try {
      const body = {}
      for (const f of method.fields) body[f.name] = f.type === 'text' ? values[f.name].trim() : values[f.name]
      const who = await signIn(body)
      values = {}
      onlogin(who)
    } catch (err) {
      error = capitalize(err.message)
    } finally {
      busy = false
    }
  }
</script>

<main class="login">
  {#if method?.kind === 'form'}
    <form class="card" onsubmit={submit} aria-label={method.title}>
      <h1>{heading}</h1>
      {#if method.lead}<p class="muted">{method.lead}</p>{/if}
      {#each method.fields as f, i (f.name)}
        <label>
          {f.label}
          <!-- svelte-ignore a11y_autofocus -->
          <input
            type={f.type}
            bind:value={values[f.name]}
            inputmode={f.numeric ? 'numeric' : undefined}
            autocomplete={f.type === 'password' ? 'current-password' : 'username'}
            autocapitalize={f.type === 'text' ? 'none' : undefined}
            spellcheck={f.type === 'text' ? 'false' : undefined}
            autofocus={i === 0}
          />
        </label>
      {/each}
      {#if error}
        <p class="alert" role="alert">{error}</p>
      {/if}
      <button class="primary" disabled={busy || !ready}>Giriş</button>
      {#each others as o (o.id)}
        {#if o.kind === 'redirect'}
          <a class="link" href={o.url}>{o.label}</a>
        {:else}
          <button type="button" class="link" onclick={() => switchTo(o.id)}>{o.label}</button>
        {/if}
      {/each}
      {#if method.note}
        <p class="muted small" data-testid={method.note_id}>{method.note}</p>
      {/if}
    </form>
  {:else if method?.kind === 'redirect'}
    <div class="card" role="group" aria-label={method.title ?? method.label}>
      <h1>{heading}</h1>
      {#if method.lead}<p class="muted">{method.lead}</p>{/if}
      {#if error}
        <p class="alert" role="alert">{error}</p>
      {/if}
      <a class="primary" href={method.url}>{method.label}</a>
      {#each others as o (o.id)}
        {#if o.kind === 'redirect'}
          <a class="link" href={o.url}>{o.label}</a>
        {:else}
          <button type="button" class="link" onclick={() => switchTo(o.id)}>{o.label}</button>
        {/if}
      {/each}
    </div>
  {:else if methods}
    <div class="card">
      <h1>{heading}</h1>
      {#if error}
        <p class="alert" role="alert">{error}</p>
      {:else}
        <p class="alert" role="alert">Giriş yöntemi tanımlı değil.</p>
      {/if}
    </div>
  {/if}
</main>
