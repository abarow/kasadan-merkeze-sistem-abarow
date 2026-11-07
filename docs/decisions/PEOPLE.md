# abarow — Tek kişi modeli: kasiyer bir roldür

> Tarih: 04.11.2026 (karar: "users mantıklı, cashier rol olur"). Giriş birleştirmesinin ([LOGIN.md](LOGIN.md)) devamı: oturum ve kilit ortak oldu; şimdi **kişinin kendisi** (kasiyer, müdür, merkez kullanıcısı) tek modele ve tek yönetim koduna iner. İlke aynı: bir veri ve onu işleyen kod bir kez yazılır, değişen yalnızca depolama ve yetki adaptörüdür.

## Bugünkü durum

| | Operasyon (kasiyer) | Merkez (kullanıcı) |
|---|---|---|
| Model | `Cashier` (`pkg/personnel`; kod, ad, rol, PIN özeti, kapalı) | `User` (kullanıcı adı, ad, rol, mağazalar, kapalı) |
| Roller | `cashier`, `manager` | `admin`, `catalog`, `report` |
| Kimlik bilgisi | PIN (4–8 hane, bcrypt) | Parola (≥10 karakter, bcrypt) |
| Kapsam | Tek mağaza | Bütün mağazalar ya da seçilenler |
| Yönetim kodu | `app/cashiers.go` (273 satır) | `app/users.go` (196 satır) |
| Kural farkları | — | kendini kilitleyemez, son yönetici korunur |
| Kayıt | `cashiers`, `cashier_changes` (SQLite) | `users`, `user_stores`, `user_changes` (PostgreSQL) |
| Yayılım | kasalara (`config.cashier.<kod>`, PIN özetiyle) ve merkeze (PIN'siz) | yok |
| Yönetim ekranı | `Cashiers.svelte` + `CashierEditor.svelte` | `Users.svelte` + `UserEditor.svelte` |

İki tarafta da: ekle, ad/rol/kapsam değiştir, kimlik bilgisini sıfırla, hesabı kapat/aç, değişiklik geçmişi, kimin yaptığı kaydı. Aynı iş iki kez yazılmış.

## Hedef

**Tek `User`:** kod/kullanıcı adı, ad, rol, kapsam (`AllStores`, `Stores`), kapalı, kimlik bilgileri, kayıt alanları. Roller tek listede:

| Rol | Kimlik bilgisi | Kapsam | Nerede yönetilir |
|---|---|---|---|
| `cashier` | PIN | mağaza | operasyon (kasaya iner) |
| `manager` | PIN | mağaza | operasyon (kasaya iner) |
| `admin`, `catalog`, `report` | parola | bütün/seçilen mağazalar | merkez |

- **Veri nerede durur: değişmez.** Mağaza personeli mağazada yönetilir ve kasaya iner (karar 28.10.2026: "kasiyerler mağazada"); merkez kullanıcıları merkezde. Aynı modül iki kapsamda çalışır (stok servisi gibi: SQLite / PostgreSQL adaptörü). Merkez, mağaza personelini olaylardan salt okunur görür (rapor adları); kendi `users` tablosuna karışmaz.
- **Rol → kimlik bilgisi kuralı:** `cashier`/`manager` PIN taşır, `admin`/`catalog`/`report` parola taşır. Bir kişide ikisi de olabilir (ileride müdür için); kural rol tablosundadır, kodda dağınık değildir.
- **Kod kuralı rol başınadır:** PIN'li roller (NATS konusuna girer) `a-z 0-9 - _`, ≤ 32; parolalı roller aynı kümeye ek olarak `.` kullanabilir, 2–32.
- **Yetki bir porttur (`Policy`):** operasyonda oturum açmış müdür (ya da mağaza parolası) mağaza personelini yönetir; merkezde yalnızca `admin` kullanıcıları yönetir. Kural farkları ("kendini kilitleyemez", "son yönetici korunur") yetki adaptörünün değil kişi yönetiminin kuralıdır; hangi rolün "yönetici" sayıldığını politika söyler.
- **Yayılım bir porttur (`Encoder`):** operasyon kişiyi kasaya (PIN özetiyle) ve merkeze (PIN'siz) mesaj olarak çıkarır; merkezde yayılım yoktur. Mesaj sözleşmesi (`staff.v1.CashierState`) değişmez; kasa, ingest ve merkez raporları aynen çalışır.
- **OIDC bağı:** kimlik eşlemesi kullanıcı kaydına eklenir (IdP `sub`, e-posta); ayrı tablo gerekmez. Uygulandı (05.11.2026): `email`, `idp_issuer`, `idp_subject` sütunları ve `Manage.Identify/LinkOwn/Unlink` ([LOGIN.md](LOGIN.md)).

## Uygulama durumu

| Adım | Durum |
|---|---|
| 1 | **Yapıldı (04.11.2026).** `pkg/personnel/person.go`: `User`, rol tablosu (rol → kimlik bilgisi, mağaza personeli mi), kod/kapsam doğrulamaları. `ValidateRole` kasa için eskisi gibi yalnızca kasiyer/müdür. `CleanName` tekrarlanan boşlukları da toplar. |
| 2 | **Yapıldı.** `pkg/personnel/manage.go`: `Manage` (ekle, güncelle, PIN/parola, kapat/aç, geçmiş, dosyadan yükle) + `Store`, `Encoder` portları; yetki `Actor.Manage` ile, korunan roller `Config.Admins` ile. |
| 3 | **Yapıldı.** `pkg/personnel/personneldb`: SQLite ve PostgreSQL için tek `users`, `user_stores`, `user_changes`, `sessions` (+ outbox) SQL'i; oturumlar `login.Store` olarak da. İki veritabanında test. |
| 4 | **Yapıldı.** Merkez: `domain.User` artık `personnel.User`; `Auth` giriş adaptörü olarak kaldı, yönetim `Manage`'e geçti; `users`, `user_stores`, `user_changes`, `sessions` tabloları personeldb şemasına taşındı (migration 00003, veri korunur: `username`→`id`, `changed_at`→`at`, `changed_by`→`by_user`, PIN özeti ve kayıt sütunları eklendi). Ortak test deposu `pkg/personnel/personneltest`. |
| 5 | **Yapıldı.** Operasyon: `domain.Cashier` artık `personnel.User`; `app.Cashiers` `Manage`'in ince sarmalayıcısı (müdür/mağaza parolası = `Actor`); `cashierproto.Encoder` `personnel.Encoder` (kasaya PIN'li, merkeze PIN'siz iki mesaj, aynı transaction'da). `cashiers`/`cashier_changes` tabloları `users`/`user_changes`'e taşındı (migration 00007, kayıtlar ve geçmiş korunur; testli). Oturumlar bellekte kalır (`personneldb.Store.NoSessions`). Kasa ve merkez mesajları değişmedi. |
| 6 | **Yapıldı.** Ekran: `ui/packages/admin` `KisiBolumu.svelte` (liste) ve `KisiEditor.svelte` (ekle, düzenle, PIN/parola, hesap, geçmiş); iki uygulamanın `Cashiers.svelte` ve `Users.svelte` dosyaları yalnızca rol, metin ve API eşlemesini verir. API uçları ve JSON biçimi değişmedi (`/api/cashiers`, `/api/users`; tarayıcı testleri aynı etiketleri bulur). |
| 7 | Belge (DESIGN.md v0.64) yapıldı; mağaza + merkez simülasyonu ve tarayıcı testleri sürüyor. |

## Adımlar

Her adım ayrı commit; mevcut testler değişmeden geçer, kullanıcıya görünen davranış korunur (kasa, operasyon, merkez, simülasyon, tarayıcı testleri).

1. **Model:** `pkg/personnel`'de `User`, rol tablosu (rol → kimlik bilgisi, kapsam türü), kod/ad/PIN/parola/kapsam doğrulamaları; `Cashier` ile mesaj çözücü `User`'dan türer (kasa ve ingest etkilenmez).
2. **Yönetim servisi:** tek `Manage` (ekle, güncelle, PIN/parola sıfırla, kapat/aç, geçmiş, dosyadan yükle, liste) + `Store`, `Policy`, `Encoder` portları; iki kodun kuralları birleşik testlerle.
3. **SQL adaptörü:** SQLite ve PostgreSQL için tek `users`, `user_stores`, `user_changes` ve oturum tabloları; `sqldialect` ile bir kez yazılır (`defterdb` gibi).
4. **Merkez** `users`'a geçer (PIN sütunu eklenir, şema genişler).
5. **Operasyon** `cashiers` tablolarından `users`'a geçer (migration; kayıtlar korunur), kasa ve merkez mesajları aynı.
6. **HTTP ve ekran:** tek kullanıcı uçları ve tek `Kisiler.svelte` (liste, düzenleyici; roller ve kimlik bilgisi türü kapsamdan gelir).
7. **Belge ve doğrulama:** DESIGN.md, kurulum betikleri, mağaza simülasyonu, tarayıcı testi.
