<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import PersonSection from '@abarow/admin/PersonSection.svelte'
  import { api } from './lib/api.js'
  import { capitalize, dateTime, roleName } from './lib/format.js'

  // me: the signed-in manager's code (empty if signed in with the store password).
  // corporate: the corporate sign-in (OIDC) is on; the manager's email address is asked for.
  let { me, onunauthorized, corporate = false } = $props()

  // A cashier is a role (pkg/personnel): the section is drawn by the same component as the
  // users at headquarters; here are only the roles and texts specific to the store server.
  const config = {
    api: {
      list: api.cashiers,
      get: async (id) => {
        const d = await api.cashier(id)
        return { item: d.cashier, history: d.history }
      },
      add: async (b) => (await api.addCashier({ id: b.id, name: b.name, role: b.role, pin: b.secret, email: b.email })).id,
      update: (id, b) => api.updateCashier(id, b.name, b.role, b.email),
      unlinkIdentity: api.unlinkCashierIdentity,
      setSecret: api.setCashierPIN,
      setDisabled: api.setCashierDisabled,
    },
    roles: [
      { value: 'cashier', label: 'Kasiyer' },
      { value: 'manager', label: 'Müdür' },
    ],
    defaultRole: 'cashier',
    // Only managers can sign in with the corporate sign-in.
    emailRoles: ['manager'],
    numeric: true,
    sectionID: 'kasiyerler',
    tableClass: 'cashiers',
    roleName,
    lastHead: 'Son değişiklik',
    last: (c) => `${dateTime(c.updated_at)} · ${c.updated_by}`,
    lastMuted: true,
    text: {
      heading: 'Kasiyerler',
      newButton: 'Yeni kasiyer',
      intro:
        "Kasiyerler burada eklenir ve değişir; kasalara kendiliğinden iner. İşten ayrılanın hesabı kapatılır: kasada giriş yapamaz, satış kayıtları silinmez. Müdür gün sonu (Z) alır ve kasada onay verir. PIN'ler burada gösterilmez.",
      loadError: (e) => capitalize(e),
      empty:
        'Operasyon\'ta kasiyer yok; kasalar kurulumdaki kasiyer dosyasını kullanıyor. "Yeni kasiyer" ile ekleyin ya da kurulum belgesindeki gibi kasiyer dosyasını yükleyin.',
      listLabel: 'Kasiyer listesi',
      idLabel: 'Kod',
      newTitle: 'Yeni kasiyer',
      regionPrefix: 'Kasiyer',
      secretNew: 'PIN (4–8 hane)',
      secretAgain: 'PIN (tekrar)',
      addNote:
        'Kod kasada ve raporlarda görünür; sonradan değişmez. Küçük harf (Türkçe karakter olmadan), rakam, - ya da _.',
      secretHeading: 'PIN',
      secretFresh: 'Yeni PIN',
      secretFreshAgain: 'Yeni PIN (tekrar)',
      secretButton: "PIN'i değiştir",
      secretHint:
        "PIN'ini unutan ya da kasada kilitlenen kasiyere yeni PIN verilir. Müdürün PIN'i değişince o PIN'le açılmış operasyon oturumları kapanır.",
      mismatch: "PIN'ler aynı değil.",
      added: 'Kasiyer eklendi; kasalara iniyor.',
      saved: 'Kaydedildi; kasalara iniyor.',
      secretChanged: 'PIN değişti; kasalara iniyor.',
      accountSelf: 'Hesap açık. Kendi hesabınızı kapatamazsınız; gerekirse başka bir müdür kapatır.',
      accountClosed: 'Hesap kapalı: kasada giriş yapamaz.',
      accountOpen: () => 'Hesap açık.',
      closedNotice: 'Hesap kapatıldı; kasadaki oturumu da kapanır.',
    },
  }
</script>

<PersonSection {config} {me} {onunauthorized} {corporate}>
  {#snippet banner()}
    {#if me === ''}
      <p class="note" data-testid="first-manager">
        Mağaza parolasıyla girdiniz. İlk müdür kaydedilince mağaza parolası kapanır: ekrandan çıkarsınız, bundan sonra
        müdür kodu ve PIN'iyle girilir.
      </p>
    {/if}
  {/snippet}
</PersonSection>
