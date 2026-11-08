# abarow

Türkiye'deki mağazalar ve restoranlar için, Linux üzerinde çalışan, açık kaynak ve modüler satış noktası (POS) yazılımı. İnternet yokken de satış yapar; kasa, mağaza sunucusu ve merkez kendi donanımınızda çalışır.

> **Durum: alfa.** Üç katman (kasa, operasyon, merkez), güncelleme merkezi ve ekranlar çalışır ve uçtan uca testlerden geçer. Gerçek bir ÖKC henüz bağlı değildir: fişi şimdilik **ÖKC simülatörü** basar ve bu fişin mali değeri yoktur. Canlı satış için bkz. [Mali mevzuat uyarısı](#mali-mevzuat-uyarısı).

## Küçük makinede çalışmak için yazıldı: bellek

Kasanın 9 servisi birlikte **boşta ≈68 MiB** bellek kullanır, ağır yük altında bile **≈115 MiB**'ı geçmez. Bir kasa, mağaza sunucusu ve merkez dahil **29 container'ın tamamı boşta 445 MiB'tır**: yarım gigabayt altında üç katmanlı bir mağaza.

| Ölçülen bellek (abarow servisleri) | Boşta | 75 sn ağır yük altında¹ |
|---|---|---|
| **Bir kasa** (9 servis) | **≈68 MiB** | en çok ≈115 MiB |
| Operasyon, mağaza sunucusu (5 container) | 158 MiB | 220 MiB |
| Merkez (6 container) | 145 MiB | 170 MiB |
| **Hepsi birlikte** (2 kasa + operasyon + merkez, 29 container) | **445 MiB** | 582 MiB (en çok 613 MiB) |
| Operasyon veritabanı (ayarlı) | ≈76 MiB | en çok 106 MiB |

¹ İki kasada durmadan tam satış, artı üç katmanın ekranlarına 40'tan fazla eşzamanlı okuma: gerçek bir mağazanın yoğun saatinden çok daha ağır.

**Gerçek ihtiyaç (asgari / önerilen bellek):**

| Kurulum | Asgari | Önerilen |
|---|---|---|
| Yalnız kasa | **256 MB** | 736 MB |
| Tek makinede 1 kasa + operasyon | **640 MB** | 1280 MB |
| Operasyon makinesi, 50 kasalı mağaza | 1152 MB | 1312 MB |

Bu sayılar yalnızca abarow'un kendi container'larıdır; işletim sistemi ve kasadaki Chromium kiosk ayrıca bellek ister (ikisi ölçülmedi, kasa için 4 GB hedeflenir). Asgari değer, yük altında ölçülen en yüksek kullanımın %40 üstüdür.

**Neden bu kadar az?**

- Her servis tek amaçlı, statik derlenmiş bir Go programıdır; imaj `scratch` tabanlıdır (işletim sistemi, kabuk ve çalışma zamanı yok).
- Her container'a bellek tavanı konur; bir servisin sızıntısı makineyi doyurmaz.
- Üç katmanda da tek veritabanı vardır: PostgreSQL. Kasadaki container küçük ve sabit ayarlıdır (zirve 55 MiB); mağaza sunucusu ve merkezde ayar kasa sayısına göre kendiliğinden hesaplanır (`pgtune`).
- Önyüz Svelte ile derlenir ve Go servisinin içinden sunulur; çalışma zamanında Node.js yoktur.

## Hız

Bir satış (sepet aç, 3 ürün okut, sepeti kapat, nakit öde: 5 çağrı) hafif yükte **p50 119 ms**, yukarıdaki ağır yükte p50 211 ms / p95 325 ms sürdü. 682 satış ve ≈113 bin okuma isteğinde **hata 0**. 50 kasalı mağazada günlük rapor 0,8 sn, kasa durumu 0,4 sn; 50 mağazalı merkezde mağaza durumu 35 ms.

Ölçüm koşulları, kasa ve mağaza sayısına göre tablolar ve her ayarın gerekçesi: [docs/HARDWARE.md](docs/HARDWARE.md) ve [docs/PERFORMANCE.md](docs/PERFORMANCE.md). Sayılar geliştirici bilgisayarında tek çalıştırmayla alındı; mağaza donanımı için garanti değildir, ama sıralama ve oranlar gerçektir.

## ABAROW ne demek?

- **A – Anlık:** POS tarafındaki saniyelik satış ve hızlı işlemler
- **B – Basit:** Herkesin kolayca kullanabileceği, karmaşadan uzak, kullanıcı dostu arayüz
- **A – Açık:** Apache 2.0 ile gelen özgür ve şeffaf açık kaynak kod yapısı
- **R – Raporlama:** Merkez tarafındaki detaylı analiz, Z raporu ve ciro takibi
- **O – Otomasyon:** Satış yapıldığında stoktan ve depodan otomatik düşen akıllı akış
- **W – Web:** Tarayıcıdan, her yerden kolayca erişilebilen yönetim paneli; işletmenin kendi sunucusunda ya da bulutta çalışır

## Nasıl çalışır?

- **Çevrimdışı öncelikli:** İnternet yokken de satış yapılır; satışlar bağlantı gelince üst katmana aktarılır.
- **Modüler:** Market modülüyle başlar; modül değiştirilerek restorana geçilebilir.
- **Kendi sunucunda:** Her işletme kendi merkezini çalıştırır; verileri kendisinde kalır.
- **Teknoloji:** Go ile hexagonal mikroservisler. Her servis ayrı bir container'da (Podman) çalışır ve tek başına güncellenebilir. Önyüz Svelte ile yazılır ve Chromium kiosk modunda açılır. Veri üç katmanda da PostgreSQL'de tutulur.

## Deneyin

Docker kurulu bir bilgisayarda, depo kökünden (deneme verisiyle; gerçek kurulumda kullanmayın):

```sh
# tek kasa: http://localhost:8088
docker compose -f deploy/compose/compose.yaml up --build -d

# üç katman: merkez + mağaza sunucusu + iki kasa (kasa ekranları :8091 ve :8092,
# operasyon :8090, merkez :9090)
bash scripts/store-sim.sh
```

## Kurulum

- **Kasa (Linux):** Podman + systemd (Quadlet); her servis ayrı container'dır ve tek başına güncellenir: [deploy/quadlet/README.md](deploy/quadlet/README.md).
- **Operasyon (mağaza sunucusu, birden fazla kasalı mağaza):** Aynı yöntemle kurulur, kasalar ona bağlanır; mağaza raporları ve kasa durumu tarayıcıdan görülür: [deploy/quadlet/README.md](deploy/quadlet/README.md#store-mağaza-sunucusu).
- **Güncelleme:** Yeni sürüm kendiliğinden uygulanmaz; imzası doğrulanır, ekrandan yetkili kişi onaylayınca servis servis alınır: [docs/UPDATE.md](docs/UPDATE.md).

Örnek verideki deneme PIN'leri ve deneme parolaları gerçek kurulumda kullanılmamalıdır.

## Mali mevzuat uyarısı

Türkiye'de tüketiciye yapılan satışlarda mali belge (ÖKC fişi veya e-Arşiv) zorunludur. abarow şimdilik yalnızca bir ÖKC simülatörüyle gelir. Simülatör "MALİ DEĞERİ YOKTUR" yazan bir fiş basar; **canlı satışta tek başına kullanılmamalıdır.** Gerçek ÖKC entegrasyonu planlanıyor. Bu bölüm hukuki ya da mali görüş değildir; mali yükümlülüklerinizi mali müşavirinizle doğrulayın.

## Kişisel veriler (KVKK)

abarow kasiyer ve kullanıcı bilgisi (ad, rol, PIN ve parola özeti) ile satış kayıtlarını **sizin kendi sunucunuzda** tutar. Kişi ve satış verisi başka bir yere gönderilmez; yalnızca güncelleme merkezi, sürüm denetimi için cihaz kimliğini ve çalışan sürüm bilgisini (imaj özetleri) bağlı olduğu güncelleme merkezine bildirir. Yazılımı kuran işletme, kendi verileri için veri sorumlusudur: aydınlatma metni, saklama süresi, erişim yetkisi ve yedekleme sizin sorumluluğunuzdadır. Bu bölüm hukuki görüş değildir.

## Güvenlik

Açık bulduysanız herkese açık issue açmayın; [SECURITY.md](SECURITY.md) dosyasındaki yolu izleyin.

## Katkı

Katkı kuralları (commit imzası, dosya başlıkları, adını ekleme) [CONTRIBUTING.md](CONTRIBUTING.md) dosyasında. Davranış kuralları: [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Katkı yapan herkes adını [AUTHORS](AUTHORS) dosyasına ekleyebilir. Tasarım: [docs/DESIGN.md](docs/DESIGN.md); belgelerin tam listesi: [docs/](docs/).

## Lisans

abarow, [Apache License 2.0](LICENSE) ile lisanslıdır.

Copyright 2026 Mehmet ERGULCU and the abarow contributors

Yazılım, lisansta belirtildiği gibi "olduğu gibi" (AS IS), hiçbir garanti olmaksızın sunulur.
