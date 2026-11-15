# abarow — Tek giriş mantığı, değişen kimlik adaptörleri

> Tarih: 04.11.2026 (kararlar alındı, uygulama sürüyor). İlke [CONSOLIDATION.md](CONSOLIDATION.md)'deki gibi: **bir kural bir kez yazılır, değişen yalnızca adaptördür.** Giriş hexagonal bir modül olur: çekirdek (oturum, kilit, kişi, yetki başlıkları) tektir; kişinin kim olduğunu söyleyen **kimlik adaptörleri** (yerel parola/PIN, OIDC) takılıp çıkarılır. Operasyon ve merkez aynı çekirdeği farklı adaptörlerle kurar.

## Bugünkü durum

İki `auth.go` var (operasyon 314, merkez 260 satır); sayaç ve kilit `pkg/login`'te ortak, gerisi ayrı yazılmış.

| | Operasyon (`operasyon`) | Merkez (`services/headquarters/reporting`) |
|---|---|---|
| Kimlik doğrulama | Mağaza parolası ya da müdür kodu + PIN | Kullanıcı adı + parola |
| Kişi kaynağı | Kasiyer tablosu (müdür rolü) | `users` tablosu (rol + mağaza yetkisi) |
| Oturum | Bellekte (yeniden başlayınca düşer) | Veritabanında (yeniden başlayınca durur) |
| Kilit | 5 yanlış → 5 dk, her kilit 2 katı, en çok 1 saat | 5 yanlış → 5 dk |
| Oturum kapanması | PIN değişince / hesap kapanınca | Parola değişince / hesap kapanınca |
| HTTP | çerez `abarow_report`, login/logout/oturum uçları | aynı uçlar, ayrı kod |
| Ekran | `Login.svelte` (iki sekme) | `Login.svelte` (tek form) |

## Hedef yapı

```
            ┌────────────── pkg/login (çekirdek) ──────────────┐
 ekran ───▶ │ HTTP: /api/login, /api/logout, /api/oturum       │
            │ Oturumlar (Store portu) · kilit · kişi başlıkları│
            └───────▲───────────────▲───────────────▲──────────┘
                    │ Kimlik portu  │               │
        ┌───────────┴──┐   ┌────────┴─────┐   ┌─────┴────────┐
        │ yerel: parola│   │ yerel: PIN   │   │ OIDC         │
        │ (kullanıcı)  │   │ (müdür)      │   │ (dış IdP)    │
        └──────────────┘   └──────────────┘   └──────────────┘
          merkez        operasyon         ikisinde de isteğe bağlı
```

**Çekirdek (tek):**
- `Kimlik` portu: bir adaptör kimlik bilgisini alır, `Person` (kullanıcı adı, ad, rol, mağazalar; mevcut `login.Person`) döner ya da reddeder. Form tabanlı adaptörler `Giris(ctx, bilgi)`, yönlendirmeli adaptörler (OIDC) `Baslat` ve `Don` ile çalışır.
- `Oturumlar`: anahtar üretme, özet saklama, süre, kişiye göre toplu kapatma (`Kapat(kişi)`); `Store` portu (bellek, SQLite, PostgreSQL).
- Kilit: sayaç `login.Tries` olarak kalır; kural adaptör başına ayarlanır, kaynağı (kullanıcı, müdür, mağaza parolası) çekirdek tutar.
- HTTP: giriş, çıkış, oturum sorgusu, çerez bayrakları, `Strip/SetHeaders` ile kişiyi arkadaki servislere iletme. Bugün iki serviste ayrı.
- Ekran: `ui/packages/admin`'e tek `Login.svelte`; `GET /api/oturum/yontemler` hangi yöntemlerin açık olduğunu söyler (parola formu, PIN formu, "Kurumsal girişle gir" düğmesi).

**Adaptörler:**
- `login/yerel`: parola (bcrypt) ve PIN; kişi kaynağı bir port (merkez `users`, operasyon kasiyerler). Bugünkü davranış aynen.
- `login/oidc`: OpenID Connect yetki kodu akışı, PKCE, `state` ve `nonce`, kimlik jetonunun imza/aud/exp denetimi. Kurulum: `ABAROW_OIDC_ISSUER`, `..._CLIENT_ID`, `..._CLIENT_SECRET` (dosyadan), `..._REDIRECT_URL`.

## Kararlar (04.11.2026)

1. **OIDC yalnızca kimliği kanıtlar; rol ve mağaza yetkisi abarow'da kalır.** Kişi önceden kayıtlıdır (merkez kullanıcısı, operasyon müdürü) ve IdP kimliği (`sub`) kişiye bağlanır; kayıtsız kişi girişte reddedilir. Her IdP'yle (Keycloak, Entra, Google, Authentik) özel claim ayarı gerekmez, yetki abarow'da denetlenir.
2. **OIDC iki serviste de tek yöntem olabilir** (`ABAROW_LOGIN=oidc`); yerel adaptörler (parola, PIN, mağaza parolası) birlikte (`ABAROW_LOGIN=yerel,oidc`) ya da tek başına (`yerel`, varsayılan) seçilir. **Dikkat:** OIDC tek yöntemken IdP'ye erişilemezse mağazada ekrana girilemez (satış etkilenmez, kasalar çalışır; yalnızca operasyon ekranı). Kurulum betiği bunu uyarı olarak yazar.

## Uygulama durumu

| Adım | Durum |
|---|---|
| 1 | **Yapıldı (04.11.2026).** `pkg/login/session.go`: `Guard` (kilit; doğrulama kilit altında), `Sessions` (aç, kişiyi çöz, kapat, kişinin hepsini kapat, `Rebind`), `Store` portu (bellek `Memory`), `Directory` portu. HTTP uçları ve çerez bu adımda yapılmadı; 3. adımda ekranla birlikte. |
| 2 | **Yapıldı (04.11.2026).** Merkez: `Auth` kullanıcı adı + parola adaptörü, oturumlar PostgreSQL'de (`Store`). Operasyon: mağaza parolası ve müdür kodu + PIN adaptörleri, oturumlar bellekte. Kullanıcıya görünen davranış aynı; mevcut testler değişmeden geçti (yalnızca oturum depolama sahteleri `login.Store`'a uyarlandı). |
| 3 | **Yapıldı (04.11.2026).** `pkg/login`: `Cookie`, `OverHTTPS`, `JSONRequest`; yöntem tanımı (`Method`, `Field`, `Methods`). İki serviste `GET /api/auth/methods`. Tek `Giris.svelte` (`ui/packages/admin`): yöntemleri sunucudan öğrenip formu çizer; `redirect` türü OIDC düğmesi için hazır. Mağaza + merkez simülasyonu ve tarayıcı testleri yerelde geçti. |
| 3b | **Yapıldı (05.11.2026).** Giriş uçları da ortak: `pkg/login.Authenticator` portu (`Methods`, `Login`, `Session`, `Logout`) ve `login.Web` (`POST /api/auth/login`, `POST /api/auth/logout`, `GET /api/auth/session`, `GET /api/auth/methods`, `Require`, `Forward`). İki serviste ayrı yazılmış login işleyicisi ve hata eşlemesi kalktı; hata sınıfları `login.Wrong` (401), `login.Refused` (403), `LockedError` (429), diğerleri 503 (`login.Status`). Oturum yanıtı iki serviste aynı (`username`, `name`, `role`, `all_stores`, `stores`); eski `manager`/`password`/`cashier_id`/`method` işaretleri kalktı (mağaza parolasıyla girende `username` boş). Operasyonda da giriş, çıkış ve yazma istekleri yalnızca JSON kabul eder. |
| 4 | **Yapıldı (05.11.2026).** `pkg/login/oidc`: yetki kodu akışı, PKCE (S256), `state`, `nonce`, kimlik jetonunun imza/aud/exp/iss denetimi; `pkg/personnel`: kişiye `email`, `idp_issuer`, `idp_subject` (iki servis için migration), `Manage.Identify/LinkOwn/Unlink`. Ekran: giriş düğmesi, IdP dönüş hataları, "Kurumsal kimlik" bölümü (bağla/kaldır), kullanıcı düzenleyicide e-posta alanı. Sahte IdP (`oidctest`) ile uçtan uca testler, iki serviste de. |
| 5 | **Yapıldı (05.11.2026).** `ABAROW_LOGIN`, kurulum betiklerinde `--login`/`--oidc-*`, `email`/`identity-remove` komutları, TASARIM bölüm 14–15. Tarayıcı testi ekranın kurumsal parçalarını sunucu yanıtlarını taklit ederek dener; **gerçek bir IdP'yle (Keycloak, Entra...) denenmedi** (aşağıda "Denenenler"). |

## Kurumsal giriş (OIDC)

### Ayarlar

Servis (`report`, `reporting`) ortam değişkenlerinden okur; kurulum betikleri bunları birim ek dosyasına yazar.

| Değişken | Anlamı |
|---|---|
| `ABAROW_LOGIN` | `yerel` (varsayılan), `oidc` ya da `yerel,oidc`. Yerel kapalıyken servisin kendi giriş uçları da reddeder (ekrandan gizlemek yetmez). Geçersiz değer ya da `oidc` olup ayarların eksik olması servisi **açmaz** (yanlış kurulmuş giriş sessizce kapalı kalmasın). |
| `ABAROW_OIDC_ISSUER` | IdP adresi (`/.well-known/openid-configuration` altında). HTTPS olmalı; yalnızca `localhost`/`127.0.0.1`'de HTTP olur. |
| `ABAROW_OIDC_CLIENT_ID` | IdP'de kayıtlı istemci kimliği. |
| `ABAROW_OIDC_CLIENT_SECRET_FILE` | İstemci sırrının dosyası (Podman secret olarak bağlanır). PKCE'li açık istemcide verilmez. Sır ortam değişkeninde taşınmaz. |
| `ABAROW_OIDC_REDIRECT_URL` | IdP'ye kayıtlı geri dönüş adresi; HTTPS, yolu `/api/auth/oidc/callback`. |
| `ABAROW_OIDC_LABEL` | Düğme metni (varsayılan "Kurumsal girişle gir"). |

### Akış

`GET /api/auth/oidc/start` → IdP (yetki kodu + PKCE + `state` + `nonce`) → `GET /api/auth/oidc/callback` → oturum çerezi → `/`.

- Akışın durumu (state, nonce, PKCE doğrulayıcısı) tarayıcıdaki **mühürlü çerezde** (AES-GCM, süreç başına rastgele anahtar, 10 dk, `HttpOnly`, `SameSite=Lax`, yolu callback) durur; servis bellekte bekleyen giriş tutmaz ve ona dışarıdan yük bindirilemez. `Lax`, IdP'den dönüşün başka sitenin üst düzey gezinmesi olması içindir; oturum çerezi `Strict` kalır.
- Kod bir kez geçer: ele geçen çerez ve kodla ikinci deneme IdP'de reddedilir. Servis yeniden başlarsa süren girişler düşer (kişi yeniden dener).
- IdP erişilemezse servis yine açılır; keşif ilk girişte yapılır, başarısızlık saklanmaz (IdP geri gelince yeniden başlatmak gerekmez).
- Hata ekrana **kod** olarak döner (`?oidc_error=denied|expired|refused|unavailable|taken`); adreste ileti taşınmaz, metni ekran yazar: adrese yazılmış rastgele metin gösterilmez. Sonuç adresten silinir.

### Kimlik eşleme (karar: kimliği IdP kanıtlar, yetkiyi abarow verir)

1. `(issuer, sub)` bir kişiye bağlıysa kişi odur. IdP başka e-posta bildirse de, adres değişse de.
2. Bağlı değilse ve IdP e-postayı **doğruladıysa** (`email_verified`) adres, hesabı açık ve kimliği henüz bağlanmamış bir kişinin **ön tanımlı adresi**yle (`email`; yönetici verir, küçük harf, iki kişide aynı olamaz) aynıysa kimlik o kişiye bağlanır (ilk giriş). Doğrulanmamış adres hiçbir şeye yetmez; kimliği bağlı kişinin adresini söyleyen başka kimlik ona geçemez.
3. Başka durumda giriş reddedilir (`refused`). Kapalı hesap girmez. Operasyonda yalnızca müdürler girer (kasiyer ve müdür olmayanlar reddedilir).
4. Rol ve mağaza yetkisi her zaman abarow'daki kişi kaydından okunur; IdP'nin hiçbir claim'i (grup, rol) yetki vermez. Oturum her istekte kayıttan yeniden çözülür: hesap kapanırsa oturum kapanır. Operasyonda oturum PIN özetine bağlıdır (PIN değişince kapanır); merkezde parola değişince kapanır.

Bağı kişinin kendisi kurar (giriş yaptıktan sonra "Kurumsal kimlik" bölümü → "Kurumsal kimliği bağla": kimliği IdP'de kendisi kanıtlar, başkası adına bağlanamaz; kimlik başka kişideyse reddedilir) ya da yönetici ön tanımlı adresle bırakır. Bağı kişi ya da yönetici kaldırır (kimlik kaybı, IdP değişimi); sonraki girişte kişi adresiyle yeniden tanınır. Kimlik bağlama oturumu açık kişiyi başlatırken mühürler (dönüşte `Strict` çerez gönderilmez). Mağaza parolasıyla girenin kişisel hesabı olmadığı için bağlayamaz. Her bağ işlemi kişinin değişiklik geçmişine yazılır.

### Kurulum ve komutlar

```
sudo deploy/install/headquarters-install.sh ... --domain report.ornek.com --login yerel,oidc \
     --oidc-issuer https://idp.ornek.com/realms/ornek --oidc-client abarow-headquarters [--oidc-secret-file dosya]
sudo deploy/install/store-install.sh ... --login yerel,oidc --oidc-issuer ... --oidc-client abarow-store \
     --oidc-url https://store.ornek.com [--oidc-secret-file dosya]
```

Sağlayıcıya kaydedilecek geri dönüş adresini betik yazdırır (`https://<adres>/api/auth/oidc/callback`). Merkezde adres `--domain`'dir (Caddy HTTPS'i zaten sunar). Operasyon ekranı HTTPS sunar (mağaza CA'sının sertifikası, `https://<adres>:8090`); IdP'nin istediği HTTPS geri dönüş adresi bu adrestir. Geri dönüşü tarayıcı yapar; müdür cihazı mağaza CA'sını tanıdığı sürece uyarı çıkmaz.

Yerel giriş olmayan kurulumda ilk kişiyi bağlamak için (adres ekrandan verilemediğinden) komut satırı: `headquarters-install.sh hesap eposta <kullanıcı> <adres>`, `store-install.sh email <müdür-kodu> <adres>`; bağı kaldırmak için `hesap identity-remove`, `identity-remove`.

### Sınırlar ve denenenler

- **Yalnızca `oidc` iken IdP erişilemezse ekrana girilemez.** Satış etkilenmez (kasalar çalışır); yalnızca store/headquarters ekranı. Kurulum betiği uyarı yazar. Sağlayıcı kesintisine karşı `yerel,oidc` önerilir.
- **E-posta eşleşmesi IdP'ye güvenir.** Doğrulanmış adres ilk girişte kişiyi seçer; IdP'si kullanıcıların adreslerini doğrulamadan serbestçe seçtirmesine izin veriyorsa o IdP ile ilk giriş eşleşmesi kullanılmamalıdır (bağı kişinin kendisi kurmalıdır: ön tanımlı adres vermeyin).
- **Denenenler:** protokol (PKCE, state, nonce, aud, exp, imza, kodun yeniden kullanımı, IdP kesintisi) ve iki servisin uçtan uca akışları sahte bir IdP'yle (`pkg/login/oidc/oidctest`) denendi. Gerçek bir IdP'yle (Keycloak, Entra ID, Google, Authentik) **denenmedi**; her birinin kendine özgü davranışları (ör. `email_verified` biçimi, `token_endpoint_auth_methods`) sahada çıkabilir. Tarayıcı testi ekranın kurumsal parçalarını sunucu yanıtlarını taklit ederek dener. Kurulum betiklerinin yeni parçaları yalnızca `bash -n` ve işlev denemesiyle sınandı, gerçek Linux/Podman'da çalıştırılmadı.
- Servis yeniden başlayınca süren IdP girişleri düşer; çıkış yalnızca abarow oturumunu kapatır (IdP oturumuna dokunulmaz, tek oturum kapatma yok).
- Her IdP kullanıcısı abarow'da bir kişiye bağlı olmak zorundadır; IdP'den kullanıcı otomatik oluşturma (JIT) yoktur.

**Sapma / düzeltme:** kilit kaydının unutulma süresi artık en uzun kilit kadar (`Idle`): önceden operasyon kayıtları hiç silmiyordu; ortak koda geçince kilidin bitmesini bekleyip sayacı sıfırlamak mümkün olmasın diye süre `max(Lock, MaxLock)` oldu (test: `TestGuardEscalationSurvivesWaitingOutTheLock`). Operasyonda müdür kodu başına kilit kayıtları artık bellekte sonsuza kadar birikmez.

## Adımlar

Her adım ayrı commit; mevcut testler değişmeden geçer, kullanıcı davranışı korunur.

1. **Çekirdek:** `pkg/login`'e `Person` etrafında `Oturumlar`, `Store` portu (bellek + SQL), kilit sarmalayıcı, HTTP uçları ve çerez; testler.
2. **Yerel adaptörler:** merkez `Auth` ve operasyon `Auth` çekirdeğe geçer. Oturum depolaması `Store` adaptörüdür (merkez PostgreSQL, operasyon bellek ya da SQLite); PIN/parola değişince oturum kapanması korunur.
3. **Ortak ekran:** tek `Login.svelte`, yöntemleri sunucudan öğrenir.
4. **OIDC adaptörü:** sahte IdP (`httptest`) ile uçtan uca test; kimlik eşlemesi tablosu (IdP `sub` → abarow kişisi); kullanıcı/müdür ekranında "kimliği bağla". İlk bağlama: kişi yerel yöntemle girip bağlar ya da yönetici e-posta ile ön tanımlar (ilk OIDC girişinde eşleşir). Yerel yöntemsiz kurulumda ilk yönetici komut satırından bağlanır.
5. **Belge ve doğrulama:** DESIGN.md (bölüm 14, 15), kurulum betikleri (`store-install.sh`, `headquarters-install.sh` OIDC ayarları), mağaza simülasyonu ve tarayıcı testi.

Yeni bağımlılık: OIDC için `github.com/coreos/go-oidc/v3` ve `golang.org/x/oauth2` (ikisi Apache-2.0).
