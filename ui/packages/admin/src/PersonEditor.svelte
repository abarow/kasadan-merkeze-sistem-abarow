<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  // Person editor: a cashier, a manager and a headquarters user are the same model
  // (pkg/personnel); the difference is in the role, the credential (PIN or password) and
  // the store scope. The store server and headquarters use the same component with their own
  // configurations (config).
  //
  // config.api: {get(id) → {item, history}, add(body) → id, update(id, body),
  //   setSecret(id, secret), setDisabled(id, disabled), unlinkIdentity(id)};
  //   body: {id, name, role, secret, allStores, stores, email}. config.text:
  //   ekran metinleri. config.emailRoles: e-posta adresi istenen roller
  //   (verilmezse hepsi).
  // corporate: if the corporate sign-in (OIDC) is on, the email address by which the person will be recognized at the first sign-in
  //   is asked for and the identity link is shown.
  // config.roles: [{value, label}]; config.adminRole: the role that sees
  //   all the stores (no scope is chosen).
  // If id is '' a new person is added; when added its page is opened with onadded(id).
  // self: the signed-in person's own record; they cannot close their own account or lower
  // their own role (someone else does it). stores: the stores that can be selected ({id, name});
  // if not given no scope is chosen (store staff).
  import { untrack } from 'svelte'
  import { capitalize, dateTime } from './lib/format.js'

  let { config, id, self, stores, corporate = false, onclose, onsaved, onadded, onunauthorized } = $props()
  const t = $derived(config.text)

  let item = $state(null)
  let history = $state([])
  let form = $state({ id: '', name: '', email: '', role: untrack(() => config.defaultRole), scope: 'some', stores: [], secret: '', secret2: '' })
  let secretForm = $state({ secret: '', secret2: '' })
  let error = $state('')
  let notice = $state('')
  let busy = $state(false)
  let isNew = $derived(id === '')
  // The stores that can be selected: those known and those given to the person beforehand.
  let choices = $derived([
    ...(stores ?? []),
    ...(item?.stores ?? []).filter((s) => !(stores ?? []).some((k) => k.id === s)).map((s) => ({ id: s, name: s })),
  ])

  async function load() {
    try {
      const d = await config.api.get(id)
      item = d.item
      history = d.history
      form = {
        id: d.item.id,
        name: d.item.name,
        email: d.item.email ?? '',
        role: d.item.role,
        scope: d.item.all_stores ? 'all' : 'some',
        stores: [...(d.item.stores ?? [])],
        secret: '',
        secret2: '',
      }
    } catch (e) {
      handle(e)
    }
  }

  $effect(() => {
    if (id !== '') load()
  })

  function handle(e) {
    if (e.status === 401) return onunauthorized()
    error = capitalize(e.message)
  }

  async function run(fn, message) {
    busy = true
    error = ''
    notice = ''
    try {
      await fn()
      onsaved()
      if (!isNew) await load()
      notice = message
    } catch (e) {
      handle(e)
    } finally {
      busy = false
    }
  }

  // The credential is typed twice: so that a typo does not lock the person out.
  function same(a, b) {
    if (a !== b) {
      error = t.mismatch
      return false
    }
    return true
  }

  const scoped = () => stores !== undefined && form.role !== config.adminRole
  const allStores = () => stores !== undefined && form.scope === 'all'
  const wantsEmail = () => corporate && (config.emailRoles ?? [form.role]).includes(form.role)
  const body = () => ({
    name: form.name,
    // If the address is not asked for it stays as it is (the server counts empty as "remove").
    email: wantsEmail() ? form.email.trim() : (item?.email ?? ''),
    role: form.role,
    allStores: allStores(),
    stores: scoped() && form.scope === 'some' ? form.stores : [],
  })

  function save(e) {
    e.preventDefault()
    if (isNew) {
      if (!same(form.secret, form.secret2)) return
      run(async () => {
        const newID = await config.api.add({ id: form.id.trim(), secret: form.secret, ...body() })
        onadded(newID)
      }, t.added)
      return
    }
    run(() => config.api.update(id, body()), t.saved)
  }

  function changeSecret(e) {
    e.preventDefault()
    if (!same(secretForm.secret, secretForm.secret2)) return
    run(async () => {
      await config.api.setSecret(id, secretForm.secret)
      secretForm = { secret: '', secret2: '' }
    }, t.secretChanged)
  }

  const mode = $derived(config.numeric ? 'numeric' : undefined)
</script>

<div class="panel editor" role="region" aria-label={isNew ? t.newTitle : `${t.regionPrefix}: ${item?.name ?? id}`}>
  <div class="section-head">
    <h3>{isNew ? t.newTitle : (item?.name ?? id)}</h3>
    <button onclick={onclose}>Kapat</button>
  </div>
  {#if error}
    <p class="alert" role="alert">{error}</p>
  {/if}
  {#if notice}
    <p class="ok-note" role="status">{notice}</p>
  {/if}

  {#if isNew || item}
    <form class="grid-form" onsubmit={save}>
      <label>
        {t.idLabel}
        <input bind:value={form.id} disabled={!isNew} autocapitalize="none" spellcheck="false" placeholder="ör. ayse" required />
      </label>
      <label>
        Ad
        <input bind:value={form.name} required />
      </label>
      <label>
        Rol
        <select bind:value={form.role} disabled={self}>
          {#each config.roles as r (r.value)}
            <option value={r.value}>{r.label}</option>
          {/each}
        </select>
      </label>
      {#if stores !== undefined}
        {#if form.role === config.adminRole}
          <p class="muted small wide">{t.adminScope}</p>
        {:else}
          <fieldset class="wide">
            <legend>Mağazalar</legend>
            <label class="check"><input type="radio" bind:group={form.scope} value="all" /> Bütün mağazalar</label>
            <label class="check"><input type="radio" bind:group={form.scope} value="some" /> Seçilen mağazalar</label>
          </fieldset>
          {#if form.scope === 'some'}
            <fieldset class="wide stores" aria-label="Seçilen mağazalar">
              {#each choices as s (s.id)}
                <label class="check"><input type="checkbox" bind:group={form.stores} value={s.id} /> {s.name}</label>
              {:else}
                <span class="muted">Merkeze bağlı mağaza yok.</span>
              {/each}
            </fieldset>
          {/if}
        {/if}
      {/if}
      {#if wantsEmail()}
        <label>
          E-posta (kurumsal giriş)
          <input type="email" bind:value={form.email} autocapitalize="none" spellcheck="false" placeholder="ör. ayse@firma.com" />
        </label>
      {/if}
      {#if isNew}
        <label>
          {t.secretNew}
          <input type="password" bind:value={form.secret} inputmode={mode} autocomplete="new-password" required />
        </label>
        <label>
          {t.secretAgain}
          <input type="password" bind:value={form.secret2} inputmode={mode} autocomplete="new-password" required />
        </label>
      {/if}
      <div class="actions">
        <button class="primary" disabled={busy}>{isNew ? 'Ekle' : 'Kaydet'}</button>
      </div>
    </form>
    {#if isNew}
      <p class="muted small">{t.addNote}</p>
    {/if}
  {/if}

  {#if item}
    <h4>{t.secretHeading}</h4>
    <form class="grid-form" onsubmit={changeSecret}>
      <label>
        {t.secretFresh}
        <input type="password" bind:value={secretForm.secret} inputmode={mode} autocomplete="new-password" required />
      </label>
      <label>
        {t.secretFreshAgain}
        <input type="password" bind:value={secretForm.secret2} inputmode={mode} autocomplete="new-password" required />
      </label>
      <div class="actions">
        <button class="primary" disabled={busy}>{t.secretButton}</button>
      </div>
    </form>
    <p class="muted small">{t.secretHint}</p>

    <h4>Hesap</h4>
    <p data-testid="account">
      {#if self}
        {t.accountSelf}
      {:else if item.disabled}
        {t.accountClosed}
        <button disabled={busy} onclick={() => run(() => config.api.setDisabled(id, false), 'Hesap açıldı.')}>Hesabı aç</button>
      {:else}
        {t.accountOpen(item)}
        <button disabled={busy} onclick={() => run(() => config.api.setDisabled(id, true), t.closedNotice)}>Hesabı kapat</button>
      {/if}
    </p>

    {#if corporate && wantsEmail()}
      <h4>Kurumsal kimlik</h4>
      <p data-testid="identity">
        {#if item.linked}
          Kurumsal kimliğe bağlı: kişi "kurumsal girişle gir" ile girer.
          <button disabled={busy} onclick={() => run(() => config.api.unlinkIdentity(id), 'Kimlik bağı kaldırıldı.')}>Bağı kaldır</button>
        {:else if item.email}
          Henüz bağlı değil: kişi ilk kurumsal girişinde e-posta adresiyle tanınır ve kimliği bağlanır.
        {:else}
          Bağlı değil. İlk kurumsal girişte tanınması için e-posta adresi yazın.
        {/if}
      </p>
      {#if item.linked}
        <p class="muted small">Bağı kaldırmak kişinin kurumsal kimliğini kaybettiği ya da IdP'nin değiştiği durumlar içindir; sonraki kurumsal girişte kişi e-posta adresiyle yeniden tanınır.</p>
      {/if}
    {/if}

    <h4>Değişiklikler</h4>
    <ul class="history">
      {#each history as c, i (i)}
        <li><span class="muted">{dateTime(c.at)} · {c.by}</span> · {c.detail}</li>
      {/each}
    </ul>
  {/if}
</div>
