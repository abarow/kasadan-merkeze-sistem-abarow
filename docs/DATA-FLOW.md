# abarow — Veriler nerede, nasıl gidip geliyor, raporlar neyden çıkıyor

> Tarih: 30.10.2026. Bu belge sistemin **bugünkü** halini anlatır; ayrıntılar ve kararların gerekçeleri `DESIGN.md`'dedir.
> **"Ben olsam"** ile başlayan kutular önerimdir, karar değildir; değiştirmek istediğiniz yeri bu dosyada düzeltip bana söylemeniz yeter.

## 1. Büyük resim

Üç katman vardır. Her katman kendi verisini kendi diskinde tutar; katmanlar arasında veri yalnızca **olay** olarak, NATS JetStream üzerinden akar.

```
   REGISTER (her kasada)                 STORE (mağaza sunucusu)          HEADQUARTERS (merkez)
 ┌──────────────────────┐          ┌──────────────────────────┐          ┌──────────────────────────┐
 │ PostgreSQL: satış,   │  olaylar │ PostgreSQL: mağaza raporu,│  olaylar │ PostgreSQL: bütün        │
 │ ödeme, vardiya, kat. │ ───────▶ │ mağaza fiyatı, kasiyer,  │ ───────▶ │ mağazaların satışları,   │
 │ sepet / adisyon      │  (yukarı)│ mağaza stoğu             │  (yukarı)│ katalog, stok, kullanıcı │
 │                      │ ◀─────── │                          │ ◀─────── │                          │
 │ NATS (makbuzla 30 gün)│ ayarlar │ NATS (makbuzla 90 gün)   │ katalog, │ NATS (90 gün)            │
 └──────────────────────┘  (aşağı) └──────────────────────────┘ transfer  └──────────────────────────┘
```

- **Yukarı** (kasa → operasyon → merkez): satışlar, iptaller, vardiyalar, stok hareketleri, mağaza fiyatı ve kasiyer değişiklikleri.
- **Aşağı** (merkez → operasyon → kasa): katalog (ürün, fiyat, görsel), transferler; operasyondan kasaya mağaza fiyatları ve kasiyerler.
- **Bağlantı kesilirse:** her katman çalışmaya devam eder; olaylar diskte bekler, bağlantı gelince sırayla gider. Aynı olay iki kez gelse de bir kez işlenir (olay kimliği).

## 2. Hangi veri nerede tutuluyor

### 2.1 Kasa

| Servis | Dosya | İçinde ne var | Ne kadar kalır |
|---|---|---|---|
| sales | `sales.db` | Satışlar (satırlar, KDV, ödemeler, durum), fiş sayacı, **outbox** (gönderilecek olaylar) | Silinmez |
| payment | `payment.db` | Kart/POS işlemleri (çekim, iade) | Silinmez |
| staff | `staff.db` | Kasiyerler (ad, rol, **PIN'in bcrypt özeti**), oturumlar, vardiyalar, kasa hareketleri (para koyma/alma, çekmece açma), müdür onayları, dönemler (X/Z), outbox | Oturumlar süresi bitince silinir; gerisi kalır |
| catalog | `catalog.db` | Ürünler, barkodlar, fiyat takvimi (merkez + mağaza fiyatı), menü bilgileri, **ürün görselleri** | Son hal (eskisi üzerine yazılır) |
| retail (market) | `retail.db` | Sepetler | 7 gün |
| restaurant | `restaurant.db` | Salon ve masalar, adisyonlar | Kapanan adisyon 30 gün |
| fiscal (simülatör) | — (bellekte) | Sahte mali numara sayacı | Servis yeniden başlayınca sıfırlanır |
| NATS | `ABAROW_SALES` | Satış olayları (`sale.completed`, `sale.voided`, `sale.charged` ...) | en az 30 gün, operasyonun makbuzuyla (DESIGN.md "Teslim makbuzu") |
| NATS | `ABAROW_STAFF` | Vardiya olayları (`shift.opened`, `shift.closed` + vardiya raporu) | en az 30 gün, operasyonun makbuzuyla |
| NATS | `ABAROW_CATALOG` (kopya) | Operasyondan inen katalog ve mağaza fiyatları | Ürün başına son hal |
| NATS | `ABAROW_CASHIERS` (kopya) | Operasyondan inen kasiyerler | Kasiyer başına son hal |

### 2.2 Operasyon (mağaza sunucusu)

| Servis | Dosya | İçinde ne var | Ne kadar kalır |
|---|---|---|---|
| report | PostgreSQL (`abarow-store-db`, `report_*` tabloları) | Kasaların satışları (satır, ödeme, KDV), iptaller, fişi kesilemeyen satışlar, vardiyalar (satış defteri: tablolar merkezdekiyle aynı, `pkg/ledger`); mağaza fiyatları ve değişiklik geçmişi; **kasiyerler** (PIN özeti dahil) ve değişiklik geçmişi; merkez kataloğunun kopyası (fiyat ekranı için); outbox | Silinmez |
| inventory | PostgreSQL (aynı veritabanı, `stock_*` tabloları) | Ürünler, stok hareketleri (satış, mal kabul, sayım, transfer), anlık stok, transferler, yer listesi, outbox | Silinmez |
| NATS | `ABAROW_STORE` | Kasaların satış ve vardiya olayları (kasa başına kaynak) + mağazanın kendi olayları (`store.price.changed`, `store.cashier.changed`, `store.stock.moved`) | en az 90 gün, merkezin makbuzuyla; merkez yoksa hiç silinmez |
| NATS | `ABAROW_STORE_CONFIG` | Kasalara inen ayarlar: `config.price.<ürün>`, `config.cashier.<kasiyer>` | Konu başına son hal |
| NATS | `ABAROW_CATALOG` (kopya) | Merkezden inen katalog | Ürün başına son hal |
| NATS | `ABAROW_TRANSFERS` (kopya) | Bu mağazaya gönderilen transferler, yer listesi | Konu başına son hal |

### 2.3 Merkez

| Servis | Tablolar (PostgreSQL) | İçinde ne var | Ne kadar kalır |
|---|---|---|---|
| ingest | `events` | **Ham olaylar**: bütün mağazalardan gelen her olay, olduğu gibi (protobuf) | Silinmez |
| ingest | `sales`, `sale_lines`, `sale_payments`, `sale_taxes`, `voids`, `unfinished`, `shifts`, `store_stats`, `registers`, `store_prices`, `cashiers`, `store_links` | Ham olaylardan çıkarılan **okuma modelleri** (raporların kaynağı) | Silinmez |
| catalog-admin | `catalog_products`, `catalog_barcodes`, `catalog_prices`, `catalog_images`, `catalog_changes`, `catalog_outbox` | Merkez kataloğu, fiyat takvimi, görseller, kim neyi ne zaman değiştirdi | Silinmez |
| inventory | `stock_*` | Bütün yerlerin stok hareketleri ve stoğu, transferler, outbox | Silinmez |
| reporting | `users`, `sessions`, `user_stores`, `user_changes` | Merkez ekranı kullanıcıları (**parolanın bcrypt özeti**), rolleri, görebildikleri mağazalar | Oturumlar süresi bitince silinir |
| NATS | `ABAROW_HEADQUARTERS` | Mağaza stream'lerinden gelen bütün olaylar (konunun başında mağaza kimliği) | 90 gün |
| NATS | `ABAROW_CATALOG` | Katalog (ürün ve görsel; son hal) | Ürün başına son hal |
| NATS | `ABAROW_TRANSFERS` | Mağazalara gönderilen transferler, yer listesi | Konu başına son hal |
| Yedek | `pg_dump` dosyaları | Bütün veritabanı | Günlük, 14 gün; istenirse makine dışına kopya |

**Müşteri verisi tutulmuyor:** sistemde müşteri adı, telefonu, kart numarası yok. Kişisel veri olarak yalnızca personel (kasiyer adı, rol, PIN özeti) ve merkez kullanıcıları var.

> **Yedekler:** Operasyonun PostgreSQL'i günlük yedeklenir (pg_dump, 14 gün; `store-install.sh restore`); makine dışına kopyalama henüz yalnızca merkezde (`--backup-target`). Yedekten sonra gelen satışlar operasyonda kendiliğinden yeniden işlenmez; merkezde ve kasalarda durur. Kasada yedek gerekmez: kasa diski bozulursa yalnızca henüz operasyona gitmemiş olaylar kaybolur; ayrıca gerçek ÖKC'de mali kayıt ÖKC'nin kendi belleğindedir.

> **Ben olsam (saklama süresi):** Kasadaki `sales.db` ve `staff.db` hiç küçülmüyor. Yılda birkaç yüz MB eder; kasa için sorun değil ama sınırsız büyümesin diye "merkeze ulaştığı doğrulanan ve 1 yıldan eski satışlar kasadan silinir" kuralı koyardım. Yasal saklama (VUK'ta defter ve belgeler için 5 yıl; mali müşavirle teyit edilmeli) merkezin PostgreSQL'i ve yedekleriyle karşılanır; kasa yasal arşiv olmamalı.

## 3. Veriler nasıl gidip geliyor

### 3.1 Ortak yöntem (her akışta aynı)

1. **Outbox:** Servis kendi kaydını ve gönderilecek olayı **aynı veritabanı işleminde** yazar. "Satış kaydedildi ama olayı gitmedi" durumu oluşmaz.
2. **Relay:** Outbox'taki olaylar yazılış sırasıyla NATS'a yayınlanır; NATS kapalıysa bekler, açılınca gönderir.
3. **Sourcing (kaynak alma):** Üst katmanın stream'i alt katmanınkini kaynak olarak alır (NATS leaf bağlantısı, TLS, cihaz başına anahtar). Bağlantı kesikken olaylar alt katmanda birikir.
4. **Kalıcı tüketici:** Olayı işleyen servis kaldığı yeri hatırlar; kapalıyken gelenleri açılınca işler. Olay kimliği tekrarları ayıklar.
5. **Son hal:** Ayarlar (katalog, mağaza fiyatı, kasiyer, transfer) konu başına yalnızca son halini taşır; yeni kurulan cihaz bütün ayarları geçmişsiz alır.

### 3.2 Yukarı giden veriler

| Veri | Üreten | Konu | Kasa → operasyon | Operasyon → merkez | Kim işler |
|---|---|---|---|---|---|
| Satış tamamlandı / iptal / ödemesi alınıp fişi kesilemedi | sales (kasa) | `sale.completed.v1`, `sale.voided.v1`, `sale.charged.v1` | `ABAROW_SALES` → `ABAROW_STORE` | `ABAROW_STORE` → `ABAROW_HEADQUARTERS` | staff (kasada X/Z), report (operasyon), ingest (merkez), inventory (operasyon stoğu) |
| Vardiya açıldı / kapandı (+ vardiya raporu) | staff (kasa) | `shift.opened.v1`, `shift.closed.v1` | `ABAROW_STAFF` → `ABAROW_STORE` | → `ABAROW_HEADQUARTERS` | report, ingest |
| Mağaza fiyatı değişti | report (operasyon) | `store.price.changed.v1` | — | → `ABAROW_HEADQUARTERS` | ingest (merkezde "farklı fiyatlı ürünler") |
| Kasiyer eklendi/değişti | report (operasyon) | `store.cashier.changed.v1` | — | → `ABAROW_HEADQUARTERS` | ingest (merkezde kasiyer adları) |
| Stok hareketi (satış, mal kabul, sayım, transfer) | inventory (operasyon) | `store.stock.moved.v1` | — | → `ABAROW_HEADQUARTERS` | merkez inventory |

### 3.3 Aşağı inen veriler

| Veri | Üreten | Konu | Merkez → operasyon | Operasyon → kasa | Kim uygular |
|---|---|---|---|---|---|
| Ürün (ad, barkod, KDV, birim, fiyat takvimi, menü bilgisi) | catalog-admin (merkez) | `catalog.product.<kod>` | `ABAROW_CATALOG` → kopya | kopya → kasa kopyası | kasa catalog; operasyon report (fiyat ekranı) ve inventory; merkez inventory |
| Ürün görseli | catalog-admin | `catalog.image.<kod>` | aynı yol | aynı yol | kasa catalog |
| Mağaza fiyatı (istisna) | report (operasyon) | `config.price.<ürün>` | — | `ABAROW_STORE_CONFIG` → kasa katalog kopyası | kasa catalog |
| Kasiyer (ad, rol, PIN özeti, hesap açık/kapalı) | report (operasyon) | `config.cashier.<kasiyer>` | — | `ABAROW_STORE_CONFIG` → `ABAROW_CASHIERS` | kasa staff |
| Transfer (mağazaya gönderilen), yer listesi | inventory (merkez) | `transfer.<mağaza>.<transfer>.sent.v1`, `transfer.locations.v1` | `ABAROW_TRANSFERS` → mağaza kopyası (yalnızca kendi konuları) | — | operasyon inventory |

**Fiyatın önceliği:** kasada satış anında geçerli fiyat = mağaza istisnası varsa o, yoksa merkez fiyatı; ileri tarihli fiyatlar zamanı gelince kendiliğinden geçer (internet kesikken de).

**Kim neyi göremez:** kasa başka kasaların satışlarını, mağaza başka mağazaların olaylarını alamaz (NATS izinleri); mağaza başka mağaza adına olay yazamaz (merkez olayın mağazasını konudan alır).

> **Ben olsam (kasiyerler):** Kasiyerler bugün operasyondan yönetiliyor; merkez yalnızca adlarını görüyor. Tek mağazalı işletme için doğru. Zincirde personel mağazalar arasında gezer; o zaman kasiyerleri merkezde yönetip mağazalara indirirdim (katalog gibi), operasyonda yalnızca "bu mağazada çalışır" işareti kalırdı. Zincir müşteri gelene kadar bekletirdim.

> **Ben olsam (tek kasalı işletme):** Tasarımda "tek kasa doğrudan merkeze bağlanır" yazıyor ama bu yol yazılmadı; bugün kasa yalnızca operasyona bağlanabiliyor. Ayrı bir yol yazmak yerine tek kasalı yerde **operasyonu kasayla aynı makineye** kurmayı standart yapardım: kod tek yol kalır, mağaza raporu ve fiyat ekranı da gelir. Kasa makinesi 4 GB'la buna yeter (operasyon servisleri birkaç yüz MB).

## 4. Raporlar nerede, hangi veriden çıkıyor

| Rapor / ekran | Nerede | Kaynağı | Not |
|---|---|---|---|
| **X raporu** (açık dönemin özeti) | Kasa ekranı → staff | Kasadaki `ABAROW_SALES` olayları (staff'ın kalıcı tüketicisi) → `staff.db` dönemi | KDV oranı bazında toplamlar, fiş sayısı, ödeme türleri |
| **Z raporu** (gün sonu) | Kasa ekranı → staff (müdür) | Aynı; Z öncesi sales'in outbox'ı boşalmış olmalı (eksik raporla Z yok) | Simülatörde mali değeri yok; gerçek ÖKC'de **yasal Z, ÖKC'nin kendi Z'sidir** |
| Vardiya raporu | Kasa (kapanışta) | `staff.db`: vardiyadaki satışlar, açılış nakdi, kasa hareketleri, sayılan nakit | Olayla operasyona ve merkeze de gider |
| Kasa durumu (bağlı mı, bekleyen olay, son fiş) | Operasyon ekranı → report | NATS'ın kaynak bilgisi (`ABAROW_STORE` kaynaklarının gecikmesi, son görülme) + PostgreSQL (report) | Ayrı "heartbeat" servisi yok |
| Günlük mağaza raporu (kasa, kasiyer, saat, ürün, ödeme, KDV) | Operasyon ekranı → report | PostgreSQL, report (kasaların olaylarından) | Mağaza saatine göre gün; merkezin özetiyle aynı hesap (satış defteri, `pkg/ledger`) |
| Eksik fiş | Operasyon | PostgreSQL, report: kasa başına fiş numarası boşlukları | Olay henüz gelmediyse de görünür |
| Ödemesi alınmış, fişi kesilmemiş satışlar | Kasa (uyarı) ve operasyon | `sale.charged.v1` olayı / sales | Müşteriden ikinci kez ödeme alınmasın diye |
| Mağaza fiyatları | Operasyon | PostgreSQL, report | Değişiklik geçmişi kişi adıyla |
| Mağaza stoğu, hareketler, transferler | Operasyon → inventory | PostgreSQL, stok tabloları | Satış stoktan kendiliğinden düşer |
| Mağazalar (bağlantı, bugünkü satış) | Merkez ekranı → reporting | PostgreSQL `store_stats`, `registers` (ingest) | |
| Özet (gün/dönem, mağaza, ödeme, KDV, kasiyer, ürün) | Merkez ekranı → reporting | PostgreSQL okuma modelleri (ingest) | Rapor kullanıcısı yalnızca kendi mağazalarını görür; operasyonun gün raporuyla aynı hesap (`pkg/ledger`), simülasyon iki katmanın rakamlarını karşılaştırır |
| Farklı fiyatlı ürünler | Merkez | `store_prices` (ingest, `store.price.changed`'ten) | |
| Bütün yerlerin stoğu, merkez deposu | Merkez → inventory | `stock_*` (mağazaların hareketlerinden + depo girişleri) | |

> **Ben olsam (rapor hesapları iki yerde):** Operasyondaki report ile merkezdeki ingest aynı olaylardan **ayrı kodla** okuma modeli kuruyor (iki ayrı `sales`, `sale_lines` ... şeması). Bugün iki taraf da kendi testleriyle doğrulanıyor ama birbiriyle karşılaştırılmıyor; ileride biri değişip öteki unutulursa mağaza raporu ile merkez raporu farklı rakam gösterir. Olayı satırlara çeviren ve toplamları hesaplayan kodu ortak bir pakete (`pkg/report` gibi) alıp iki tarafta aynı testle denetlerdim.

> **Ben olsam (merkezde yeniden hesaplama):** Merkez ham olayları (`events`) sakladığı için yeni bir rapor eklendiğinde geçmiş veriden de hesaplanabilir (`ingest replay`). Bu iyi; operasyonda bu yok (report yalnızca okuma modelini tutar, stream 90 gün). Operasyona yeni rapor eklerken 90 günden eskisi hesaplanamaz; bunu bilerek kabul ederdim, mağaza raporu zaten yakın geçmiş içindir.

> **Ben olsam (gerçek ÖKC ile mutabakat):** Gerçek ÖKC gelince iki kayıt olacak: ÖKC'nin mali belleği (yasal) ve bizim `sales.db`'miz. Her Z'de ÖKC'nin Z toplamını bizim dönem toplamıyla karşılaştırıp fark varsa ekranda ve merkezde uyarı verirdim. Beko X30TR belgesi gelince bunu tasarlarım.

## 5. Restoranda veri (bugün ve çok terminalde)

**Bugün (tek kasa):** masalar ve adisyonlar kasanın `restaurant.db`'sinde. Fiyat satır eklenince katalogdan alınır ve adisyonda sabit kalır. Ödeme anında seçilen satırlar `sales`'e satış olarak verilir; oradan sonrası marketle aynıdır (yukarıdaki akışlar). Adisyonun kendisi yukarı gitmez; merkez yalnızca satışları görür.

> **Ben olsam (çok terminal ve garson telefonu):**
> - Masalar ve adisyonlar **operasyona** taşınır (aynı restaurant servisi orada çalışır). Bütün kasalar ve telefonlar oraya bağlanır; hangi cihazdan bakılırsa bakılsın aynı masa aynı görünür.
> - **Ödeme ve mali fiş kasada kalır:** kasa adisyonu operasyondan okur, ödemeyi kendi `sales`'iyle alır, sonra operasyona "ödendi" der. Telefon ödeme almaz.
> - **Mutfak ve bar yazıcıları** operasyondan basılır (telefondan gönderilen sipariş de basılsın diye); operasyona device servisi eklenir.
> - **Garson girişi:** kasiyerler zaten operasyonda; garson telefonda kendi PIN'iyle girer, telefon yalnızca sipariş yetkisi alır (iptal yine müdür onaylı).
> - **Telefon ekranı:** ayrı uygulama değil, telefonun tarayıcısında açılan sayfa (mağaza Wi-Fi'sinde operasyon adresi). Uygulama mağazası gerekmez, güncellemesi sunucuyla gelir.
> - **Operasyon kapanırsa:** masalar görünmez (tek kopya orada). Bu yüzden çok terminalli restoranda operasyona UPS önerirdim; ayrıca kasaya "operasyon yokken masa açmadan hızlı satış" yolu bırakırdım.
> - Adisyonları merkeze göndermeyi (masa süreleri, iptal oranı raporu için) ikinci adım yapardım; önce çalışır hale gelsin.

## 6. Kimlik, anahtar ve gizli bilgiler

| Ne | Nerede | Nasıl saklanır |
|---|---|---|
| Kasiyer PIN'i | Kasa `staff.db`, operasyon PostgreSQL (`users`), aradaki `config.cashier` mesajı | bcrypt özeti; PIN'in kendisi hiçbir yerde yok |
| Kasiyer oturumu | Kasa `staff.db` | Anahtarın özeti; süresi dolunca silinir |
| Operasyon ekranı parolası (müdür yokken) | Operasyon (Podman secret) | bcrypt özeti |
| Merkez kullanıcı parolası | Merkez `users` | bcrypt özeti |
| NATS kimlikleri | Her cihaz (Podman secret) | Servis başına NKey; cihaz değişirse eski anahtar listeden çıkarılır |
| Leaf TLS | Operasyon ve merkez kendi CA'sını üretir | Alt katman CA sertifikasıyla üst katmanı doğrular |
| Veritabanı parolası (merkez) | Merkez (Podman secret) | Kurulumda üretilir |

> **Ben olsam (PIN'in operasyondan geçmesi):** PIN özeti operasyondan kasalara iniyor. bcrypt olduğu için özetten PIN çıkarılması zor ama 4–6 haneli PIN'de deneme sayısı azdır. Kasada yanlış PIN kilidi var; yine de operasyon diskine erişen biri özetleri alıp çevrimdışı deneyebilir. Bunu kabul edilebilir buluyorum (operasyon mağazanın içinde, kilitli dolapta); daha sıkı istenirse PIN'i kasaya özel tuzla ayrıca özetlerdim.

## 7. Değişiklik yapmak isterseniz nereye bakılır

- **Bir verinin yerini değiştirmek** (ör. kasiyerleri merkeze taşımak): o verinin "üreten" servisi ve konusu değişir (bölüm 3), NATS izinleri `tools/natsauth`'ta güncellenir, eski yerden yenisine bir kez taşıma yapılır.
- **Yeni rapor:** merkezde `events`'ten geçmişiyle hesaplanabilir (bölüm 4); operasyonda yalnızca 90 gün.
- **Saklama sürelerini değiştirmek:** kasa ve operasyon stream'leri kendiliğinden silinmez; silmeyi makbuz temizleyicisi yapar, süreleri `ABAROW_CLEAN_MAX_AGE` (kasada 30, operasyonda 90 gün), `ABAROW_CLEAN_INTERVAL` ve `ABAROW_RECEIPT_MAX_AGE` ortam değişkenleridir. Merkez stream'i (`ABAROW_HEADQUARTERS`) 90 gün tutar. Veritabanı silme kuralları servislerin kendisinde (bölüm 2).
