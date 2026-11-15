<!-- Copyright 2026 Mehmet ERGULCU and the abarow contributors -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script>
  import PersonSection from '@abarow/admin/PersonSection.svelte'
  import { api } from './lib/api.js'
  import { roleName } from './lib/format.js'

  // me: the signed-in administrator's username.
  // corporate: the corporate sign-in (OIDC) is on; the user's email address is asked for.
  let { me, onunauthorized, corporate = false } = $props()

  // A user and a cashier are the same person model (pkg/personnel): the section is drawn by the same
  // component as the back house cashiers; here are the roles specific to headquarters,
  // the store scope and the texts. The server gives the username as `username`;
  // the component expects `id`.
  const person = (u) => ({ ...u, id: u.username })

  const config = {
    api: {
      list: () => api.users().then((l) => l.map(person)),
      stores: () => api.stores().then((st) => st.stores.map((s) => ({ id: s.id, name: s.name || s.id }))),
      get: async (id) => {
        const d = await api.user(id)
        return { item: person(d.user), history: d.history }
      },
      add: async (b) =>
        (
          await api.addUser({
            username: b.id,
            password: b.secret,
            name: b.name,
            role: b.role,
            all_stores: b.allStores,
            stores: b.stores,
            email: b.email,
          })
        ).username,
      update: (id, b) => api.updateUser(id, { name: b.name, role: b.role, all_stores: b.allStores, stores: b.stores, email: b.email }),
      unlinkIdentity: api.unlinkUserIdentity,
      setSecret: api.setUserPassword,
      setDisabled: api.setUserDisabled,
    },
    roles: [
      { value: 'admin', label: 'Yönetici' },
      { value: 'catalog', label: 'Katalog' },
      { value: 'report', label: 'Rapor' },
    ],
    adminRole: 'admin',
    defaultRole: 'report',
    numeric: false,
    sectionID: 'kullanicilar',
    tableClass: 'products',
    roleName,
    lastHead: 'Mağazalar',
    last: (u, names) => (u.all_stores ? 'Bütün mağazalar' : u.stores.map((id) => names[id] ?? id).join(', ')),
    lastMuted: false,
    text: {
      heading: 'Kullanıcılar',
      newButton: 'Yeni kullanıcı',
      intro:
        'Yönetici kullanıcıları yönetir, ürün ve fiyatları değiştirir, bütün mağazaları görür. Katalog kullanıcısı ürün ve fiyatları değiştirir; rapor kullanıcısı yalnızca raporları görür. İkisi de yalnızca kendilerine verilen mağazaların raporlarını görür. Değişiklik hemen geçerli olur. İşten ayrılanın hesabı kapatılır.',
      loadError: (e) => `Kullanıcılar okunamadı: ${e}`,
      listLabel: 'Kullanıcı listesi',
      idLabel: 'Kullanıcı adı',
      newTitle: 'Yeni kullanıcı',
      regionPrefix: 'Kullanıcı',
      adminScope: 'Yönetici bütün mağazaları görür.',
      secretNew: 'Parola (en az 10 karakter)',
      secretAgain: 'Parola (tekrar)',
      addNote:
        'Kullanıcı adı sonradan değişmez: küçük harf (Türkçe karakter olmadan), rakam, nokta, - ya da _. Parolayı kişiye güvenli bir yolla iletin.',
      secretHeading: 'Parola',
      secretFresh: 'Yeni parola',
      secretFreshAgain: 'Yeni parola (tekrar)',
      secretButton: 'Parolayı değiştir',
      secretHint: 'Parolasını unutana yeni parola verilir; açık oturumları kapanır.',
      mismatch: 'Parolalar aynı değil.',
      added: 'Kullanıcı eklendi.',
      saved: 'Kaydedildi; hemen geçerli.',
      secretChanged: 'Parola değişti; kullanıcının açık oturumları kapandı.',
      accountSelf: 'Hesap açık. Kendi hesabınızı kapatamazsınız; gerekirse başka bir yönetici kapatır.',
      accountClosed: 'Hesap kapalı: ekrana giremez.',
      accountOpen: (u) => `Hesap açık · ${roleName(u.role)}.`,
      closedNotice: 'Hesap kapatıldı; açık oturumları kapandı.',
    },
  }
</script>

<PersonSection {config} {me} {onunauthorized} {corporate} />
