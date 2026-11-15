# abarow — Tekrarlanan veri ve kod: neler birleştirilebilir

> Tarih: 30.10.2026 (inceleme), 04.11.2026 (uygulama durumu). Aşağıdaki öneriler **uygulandı**; ne yapıldığı ve sapmalar "Uygulama durumu" bölümündedir. Bölümlerin metni inceleme anındaki durumu anlatır.
> İlke (sizin önerdiğiniz): **bir veri ve onu işleyen kod bir kez yazılır.** Veri kasadan operasyona, oradan merkeze çıktıkça yalnızca **kapsam sütunları** eklenir (mağaza, kasa); aynı modül her katmanda çalışır, değişen yalnızca depolama adaptörüdür (kasada ve operasyonda SQLite, merkezde PostgreSQL).
> Kısıt (önceki karar): **tek binary yok**, her servis ayrı container olarak kalır. Birleştirme servisleri tek programda toplamak değil, **ortak Go paketleri** (`pkg/...`) ve ortak ekran paketleridir; her servis aynı paketi kullanır.

## Uygulama durumu (04.11.2026)

Yedi adım sırayla, her biri ayrı commit olarak yapıldı; her adımda yerel kontroller, mağaza simülasyonu ve CI geçti. Davranış korundu; küçük farklar aşağıda.

| Adım | Yapılan | Yeri |
|---|---|---|
| 1 | Fiş düzeni, ödeme türü, Türkçe arama anahtarı | `pkg/layout`, `pkg/paymethod`, `pkg/text` |
| 2 | Satış defteri: tek model, tek olay çözücü, tek yazıcı ve rapor sorgusu (SQLite + PostgreSQL); kasa X/Z, operasyon gün raporu ve merkez özeti aynı hesap; operasyon tabloları merkezle aynı şemaya taşındı | `pkg/ledger`, `pkg/ledger/ledgerdb` |
| 3 | SQLite açma, ekran sunucusu, outbox tablosu, NATS yayıncı ve tüketici döngüsü, SQL lehçesi | `pkg/sqlitedb`, `pkg/service`, `pkg/outbox`, `pkg/natsio`, `pkg/sqldialect` |
| 4 | Fiyat takvimi; katalog ürün mesajı çözücüsü | `pkg/price`, `pkg/catalog` |
| 5 | Kasiyer kuralları ve mesajı; deneme sayacı, kilit, oturum anahtarı; merkezde oturumu yalnızca reporting denetler, kişi başlıkla iletilir | `pkg/personnel`, `pkg/login` |
| 6 | Yönetim ekranları: biçim, stok girişi, transferler, rapor parçaları, ortak CSS | `ui/packages/admin` |
| 7 | Tek stok servisi: mağaza kapsamı (SQLite) ve merkez kapsamı (PostgreSQL) | `inventory` |
| 8 | Rapor çekirdeği (05.11.2026): tek dönem özeti ve JSON (`GET /api/reports/summary`), ortak giriş uçları, güvenlik başlıkları, arkadaki servislere iletim, ortak JSON yazıcı. Operasyonun kasalar, kasiyerler, fiyatlar ve mesaj yolu gibi katmana özgü işleri kendi servisinde kalır ve bu çekirdeği kullanır | `pkg/report`, `pkg/login` (`Web`), `pkg/httpjson` |

**Kararlar ve sapmalar**
- **Katalog kopyaları kaldı** (4. bölümün 2. adımı yapılmadı; karar 04.11.2026). Her servis kendi ürün kopyasını tutar ama hepsi aynı çözücüyle (`pkg/catalog`) dolar; bir servis kapanınca öteki etkilenmez.
- **Stok servisi tek modül, iki depolama adaptörü.** Kapsam yapılandırmadan gelir (`ABAROW_DATABASE_URL` varsa merkez, yoksa `ABAROW_STORE_ID` ile mağaza). Mağaza adaptörü tek yerlidir; yer sütunu eklenmedi. İmaj adları değişmedi (`abarow-inventory`, `abarow-inventory-headquarters`), ikisi aynı modülden derlenir.
- **Satış defteri geçişi:** operasyon tabloları tek migration'a işlendi (kayıtlar korundu, satır tutarı kasadaki yuvarlamayla hesaplandı); merkezde `unfinished` tablosu aynı sütunlara geçti.
- **Kasa oturum anahtarı** hex (64 karakter) yerine base64url (43) oldu; biçime bağlı başka yer yok.
- **Stok yanıtı** iki kapsamda aynı biçimde (`here`, yer başına miktar); operasyon ekranı ve register-sim uyarlandı. Operasyon report stok servisine kişiyi `X-Abarow-Person` yerine `pkg/login` başlıklarıyla iletir.
- **Küçük davranış farkları:** operasyon report ve kasa staff'ın satış tüketicileri de tüketici silinince ya da bağlantı kopunca kendini yeniden kurar; işlenemeyen mesaj her yerde 2 saniye sonra tekrar gelir.
- **Doğrulama:** simülasyon, operasyonun gün raporunu merkezin aynı gün ve mağaza özetiyle karşılaştırır (fiş, toplam, KDV, iptal, ödemeler, KDV dökümü, kasiyerler birebir aynı olmalı).

## Özet tablo

| # | Konu | Bugün kaç kez yazılmış | Nerede | Öneri | Öncelik |
|---|---|---|---|---|---|
| 1 | Satış verisi ve raporları | 3 model, 4 olay çözücü, 3 rapor hesabı | kasa `staff`, operasyon `report`, merkez `ingest` + `reporting`, operasyon `inventory` | Tek **satış defteri** modülü (`pkg/ledger`) | 1 |
| 2 | Ödeme türü eşlemesi | 12 dosya | sales, payment, fiscal, staff, gateway, report, ingest, register-sim | Tek eşleme fonksiyonu | 1 (hızlı) |
| 3 | Fiyat takvimi (merkez fiyatı, mağaza istisnası, ileri tarih) | 3 | kasa `catalog`, operasyon `report`, merkez `catalog-admin` | `pkg/price` | 2 |
| 4 | Ürün (katalog) kopyaları | 5 kopya, 4 çözücü | kasa catalog, operasyon report ve inventory, merkez catalog-admin ve inventory | `pkg/catalog` (tek tip + tek çözücü) | 2 |
| 5 | Kasiyer verisi ve doğrulaması | 3 kopya, 2 doğrulama | kasa staff, operasyon report, merkez ingest | `pkg/personnel` | 3 |
| 6 | Giriş, deneme sayacı, kilit, oturum | 3 | kasa staff (PIN), operasyon report, merkez reporting (+ oturum sorgusu 3 kez: catalog-admin, inventory, reporting) | `pkg/login` + merkezde tek oturum denetimi | 3 |
| 7 | Stok servisi | 2 servis (domain ortak, gerisi ayrı) | operasyon ve merkez inventory | Tek servis, iki depolama adaptörü | 4 |
| 8 | Altyapı: outbox tablosu, NATS yayıncı, tüketici döngüsü, SQLite açma | 6 + 6 + 6 + 8 | hemen her servis | `pkg/outbox`'a SQL deposu, `pkg/natsio`, `pkg/sqlitedb` | 2 (mekanik) |
| 9 | Türkçe arama anahtarı, fiş satır düzeni | 5 + 2 | catalog, report, inventory ×2, catalog-admin; restoran fişi `pkg/layout`'u kullanmıyor | `pkg/text`, `pkg/layout` | 2 (hızlı) |
| 10 | Yönetim ekranları (operasyon, merkez) | biçim, API, stok girişi, transfer, CSS iki kopya | `ui/apps/store`, `ui/apps/headquarters` | Ortak yönetim ekranı paketi | 3 |

Aşağıda her biri: bugün ne var, nasıl birleşir, ne kazanılır, nesi riskli.

---

## 1. Satış verisi ve raporları (en büyüğü)

**Bugün:**
- Satışın kendisi kasada `sales.db`'de (kaynak).
- Aynı satış **üç kez daha** ayrı tabloya ve ayrı Go tipine yazılıyor:
  - kasa `staff.db` (`completed_sales`, `voided_sales`, `periods`): X/Z için;
  - operasyon `report.db` (`sales`, `sale_lines`, `sale_payments`, `sale_taxes`, `voids`, `unfinished`, `shifts`);
  - merkez PostgreSQL (aynı adlarla aynı tablolar + `store_id`).
- Operasyon `report.Sale` ile merkez `ingest.Sale` **alan alan aynı tip**, iki ayrı dosyada.
- `sale.completed.v1` olayını protobuf'tan çözen kod 4 yerde: staff, report, ingest, operasyon inventory.
- Toplam hesapları 3 yerde: kasa X/Z (ödeme, KDV), operasyon gün raporu (ödeme, KDV, kasa, kasiyer, saat, ürün), merkez özeti (aynıları + gün, mağaza).
- Geçmişte bunun bedelini ödedik: yemek kartı eklenirken eşleme birkaç yerde yanlış kaldı ("kart" sayıldı) ve tek tek düzeltildi.

**Öneri: `pkg/ledger` (satış defteri) modülü**
- **Tek model:** `Satis`, `Satir`, `Odeme`, `KDV`, `Iptal`, `FissizOdeme`, `Vardiya`. Her kayıtta kapsam alanları her zaman var: `MagazaID`, `KasaID`. Kasada bunlar sabittir (kendisi), operasyonda kasa değişir, merkezde ikisi de.
- **Tek çözücü:** olay → model (`sale.*.v1`, `shift.*.v1`). Yeni olay türü ya da ödeme türü tek yerde eklenir.
- **Tek yazıcı (projeksiyon):** modeli tablolara yazan kod bir kez. Tablo şeması her katmanda aynı (aynı sütunlar, kapsam sütunları dahil). Depolama bir port; iki adaptör: SQLite ve PostgreSQL. SQL'in büyük kısmı ikisinde aynı çalışır, yalnızca küçük lehçe farkları (tarih fonksiyonu, `RETURNING`) adaptörde kalır.
- **Tek rapor hesabı:** `Rapor(filtre)`: tarih aralığı, mağazalar, kasalar, kasiyerler → ödeme, KDV, kasa, kasiyer, saat, ürün, gün kırılımları. Operasyon gün raporu "bu mağaza, bir gün"; merkez özeti "seçili mağazalar, aralık"; kasa X "bu kasa, açık dönem".
- **Kasanın Z'si:** GİB düzeni ve dönem kuralları (iki Z arası, eksik raporla Z yok) staff'ta kalır; ama toplamları aynı modülden alır. Böylece Z ile operasyon raporu aynı hesaptan çıkar.
- **Kim kullanır:** staff (kasa), report (operasyon), ingest + reporting (merkez). Servisler ayrı kalır, kodları ortaktır.

**Kazanç:** tutarsız rapor riski kalkar, yeni rapor ya da ödeme türü bir kez yazılır, testler bir kez (bugünkü üç ayrı test grubu birleşir).

**Risk ve geçiş:**
- **Merkez:** ham olayları (`events`) sakladığı için yeni tablolar baştan yeniden hesaplanabilir (`ingest replay`); veri kaybı yok.
- **Operasyon:** stream yalnızca 90 gün tutuyor. Eski `report.db` tablolarını bir kez SQL ile yeni şemaya taşımak gerekir (aynı sütunlar, yalnızca ad ve kapsam farkı; basit).
- **Kasa (staff):** açık dönem taşınır; kapanmış Z'ler olduğu gibi kalır (yasal kayıt değişmez).

## 2. Ödeme türü eşlemesi

**Bugün:** `PAYMENT_METHOD_*` ↔ "cash/card/meal_card/other" çevirisi 12 dosyada ayrı ayrı.
**Öneri:** sözleşmenin yanında tek çevirici (ör. `pkg/odeme`: `Ad(yontem)`, `Yontem(ad)`, ekran adı "Nakit, Kredi kartı, Yemek kartı, Diğer"). Ekranlardaki (`format.js`) ad listesi de tek pakete.
**Kazanç:** yeni ödeme türü tek satır. **Risk:** yok; birinci adımın parçası olarak hemen yapılabilir.

## 3. Fiyat takvimi

**Bugün:** "Hangi fiyat ne zaman geçerli" kuralı 3 yerde:
- merkez `catalog-admin` (takvim, ileri tarih, geri alma, budama);
- kasa `catalog` (merkez fiyatı + mağaza istisnası, satış anındaki fiyat);
- operasyon `report` (mağaza istisnası takvimi, ekrandaki "şu anki fiyat").

**Öneri:** `pkg/price`: takvim tipi, `Gecerli(an)`, `Planla`, `GeriAl`, `Buda`, headquarters/mağaza önceliği. Üç servis aynısını kullanır.
**Kazanç:** kasadaki fiyat ile ekranlardaki "şu anki fiyat" aynı koddan çıkar. **Risk:** düşük; davranış aynı, testler tek pakette toplanır.

## 4. Ürün (katalog) kopyaları

**Bugün:** ürünün son hali 5 yerde tutuluyor:
- kasa `catalog.db`;
- operasyon `report.db` (`catalog_products`, fiyat ekranı için);
- operasyon `inventory.db` (`products`);
- merkez `catalog_*` (asıl kaynak);
- merkez `stock_products`.

Kopyaların 4'ü `ProductState` mesajını ayrı kodla çözüyor. Türkçe arama anahtarı da her kopyada ayrı.

**Öneri, iki adım:**
1. `pkg/catalog`: tek `Urun` tipi, tek çözücü (ürün ve görsel mesajı), tek arama anahtarı. Kopyalar kalır ama aynı kodla dolar.
2. Daha ileri: operasyonda katalog kopyasını **tek servis** tutar (kasadaki `catalog` servisinin aynısı operasyonda da çalışır). report ve inventory ürün bilgisini ondan gRPC ile sorar, kendi tablolarını tutmaz. Merkezde de stok, ürünü catalog-admin'den okur.

**Kazanç:** "ürün adı burada değişti ama orada değişmedi" olmaz; katalog stream'ini okuyan tüketici sayısı azalır. **Risk:** 2. adımda operasyonda yeni bir servis bağımlılığı doğar (catalog kapalıysa stok ekranı ürün adını gösteremez). Bu yüzden önce 1. adım.

## 5. Kasiyer verisi

**Bugün:** kasiyer kasada (`staff.db`), operasyonda (`report.db`, asıl yönetim yeri) ve merkezde (`cashiers`, yalnızca ad) tutuluyor. Doğrulama (PIN biçimi, rol, ad temizleme) staff ve report'ta ayrı. Mesajı (`config.cashier`) üreten ve çözen kod ayrı.

**Öneri:** `pkg/personnel`: `Kasiyer` tipi (ad, rol, PIN özeti, açık/kapalı), doğrulama, mesaj çevirisi. Kapsam yine sütunla: kasada kendi mağazası, merkezde `MagazaID`. Zincirde kasiyerler merkezden yönetilirse (bkz. DATA-FLOW.md) aynı paketle yalnızca "kaynak" değişir.

## 6. Giriş, kilit ve oturum

**Bugün:**
- Yanlış denemede kilit, oturum anahtarının özetini saklama ve süre 3 yerde ayrı yazılmış:
  - kasa staff (PIN);
  - operasyon report (müdür PIN'i ve mağaza parolası);
  - merkez reporting (kullanıcı parolası).
- Merkezde oturumu doğrulayan SQL sorgusu 3 serviste kopya: catalog-admin, inventory, reporting.
- Operasyonda ise oturumu report denetliyor, inventory'ye kişiyi başlıkla iletiyor (`X-Abarow-Person`).

**Öneri:**
- `pkg/login`: deneme sayacı ve kilit kuralı, oturum anahtarı üretme ve özetleme, süre. Üç servis aynısını kullanır (PIN ya da parola yalnızca "doğrulayıcı" farkı).
- **Merkezde operasyondaki yöntem:** oturumu yalnızca reporting denetler, catalog-admin ve inventory'ye kişiyi (ad, rol, görebildiği mağazalar) başlıkla iletir. Bu iki servis veritabanında oturum tablolarına bakmaz. İki katmanda tek yöntem olur.

**Risk:** merkezde catalog-admin ve inventory'nin portu dışarı açık olmamalı (bugün de değil); başlığı yalnızca reporting koyabilmeli.

## 7. Stok servisi

**Bugün:** kurallar ortak (`pkg/stock`). Ama iki ayrı servis var:
- `store/inventory` (SQLite);
- `headquarters/inventory` (PostgreSQL).

Depolama, HTTP API, tüketici, transfer işleri ayrı yazılmış; ekranları da ayrı.

**Öneri:** sizin ilkenizin tam karşılığı: **tek stok servisi**, iki depolama adaptörü. Kapsamı ayardan alır:
- operasyonda "bu mağaza" (yer sabit);
- merkezde "bütün yerler + merkez deposu" (yer sütunu dolu).

Transferin iki ucu (gönderen, alan) aynı kodla çalışır. Merkezdeki "mağazadan mağazaya iletme" yalnızca merkez kapsamında açılır.

**Kazanç:** iki servis × (depo + API + tüketici + ekran) tek olur. **Risk:** bugün çalışıyor ve iki taraf da testli. Birleştirme, adaptörler ve kapsamlar dikkatle ayrılmadan yapılırsa transfer akışı bozulabilir. 1–3 bittikten sonra.

## 8. Altyapı tekrarları (mekanik, düşük risk)

| Ne | Kaç kez | Öneri |
|---|---|---|
| Outbox tablosu ve `Pending`/`MarkPublished`/`CountPending` SQL'i | 6 (sales, staff, report, inventory ×2, catalog-admin) | `pkg/outbox`'a SQLite ve PostgreSQL deposu; servis yalnızca tablo adını verir |
| NATS'a CloudEvents başlıklarıyla yayınlayan yayıncı | 6 | `pkg/natsio.Publisher` (stream'i kurma seçeneğiyle) |
| Kalıcı tüketici döngüsü (yeniden kurma, tekrar deneme, okunamayanı reddetme) | 6 | `pkg/natsio.Consumer` |
| SQLite açma + migration | 8 | `pkg/sqlitedb.Open(yol, migrations)` |
| Ekranı sunan `spa` fonksiyonu | 2 (retail, restaurant) | `pkg/service` içine |

Bunlar davranışı değiştirmez; her servisten 50–150 satır gider, hatalar tek yerde düzelir.

## 9. Küçük ve hızlı olanlar

- **Türkçe arama anahtarı** (`strings.ToLowerSpecial(TurkishCase, ...)` ve sıralama anahtarı) 5 yerde ayrı. Öneri: `pkg/text`.
- **Fiş satır düzeni:** `pkg/layout` (ortala, iki yana yasla, satır kaydır) zaten var ve staff kullanıyor; benim yazdığım restoran fişleri (`services/modules/restaurant/internal/domain/ticket.go`) aynısını yeniden yazıyor. Restoran `pkg/layout`'u kullanmalı. **Bu benim hatam;** ilk iş olarak düzeltilir.

## 10. Yönetim ekranları

**Bugün:** kasa ekranları ortak paketi kullanıyor (`ui/packages/core`). Operasyon ve merkez ekranları ise birbirinin kopyası parçalar taşıyor:
- biçim yardımcıları (`format.js`: para, miktar, tarih, ödeme adı);
- API istemcisi;
- stok girişi (`StockEntry.svelte`) ve transfer listesi (`Transfers.svelte`);
- CSS'in çoğu.

**Öneri:** `ui/packages/admin` (yönetim ekranlarının ortak paketi). Biçim ve ödeme adları kasa paketiyle de ortak olabilir (`ui/packages/common`). 1. maddedeki tek rapor hesabıyla gün raporu ve merkez özeti **tek rapor bileşeniyle** çizilir: merkezde mağaza seçimi ve sütunu eklenir.

---

## Önerilen sıra

1. **Hızlı ve risksiz:** restoranın `pkg/layout`'u kullanması (9), ödeme türü eşlemesi (2), Türkçe arama anahtarı (9).
2. **Satış defteri** (1): `pkg/ledger`. Önce merkez (yeniden hesaplanabildiği için en güvenlisi), sonra operasyon (tek seferlik tablo taşıma), en son kasa X/Z'nin toplamları. Her adımda simülasyon iki tarafın rakamlarını karşılaştırır (bugün yok, bu adımla gelir).
3. **Altyapı** (8): outbox deposu, yayıncı, tüketici, SQLite açma.
4. **Fiyat takvimi** (3) ve **katalog tipi ve çözücüsü** (4, 1. adım).
5. **Personel ve giriş** (5, 6), merkezde tek oturum denetimi.
6. **Yönetim ekranları paketi** (10), tek rapor bileşeni.
7. **Tek stok servisi** (7) ve operasyonda tek katalog kopyası (4, 2. adım): en büyük yapısal değişiklikler, en sona.

Her adım ayrı commit; davranış değişmez, yalnızca kod tek yere toplanır. Bitince her adımda simülasyon ve tarayıcı testleri aynen geçmelidir.

## Birleştirmeyi önermediğim yerler

- **Kasadaki `sales.db` ile defter:** satışın kendisi (ödeme ve mali kayıt akışı, fiş numarası) sales'te kalmalı. Defter onun **okuma kopyasıdır**; ikisini tek tabloya indirmek satış yolunu raporla bağımlı yapardı.
- **Servisleri tek programda toplamak:** önceki kararınız gereği her servis ayrı container kalır. Ortaklık paket düzeyinde.
- **Ham olay deposu (`events`):** yalnızca merkezde; operasyona eklemeyi önermiyorum (90 günlük stream yeterli, operasyon diskini büyütür).
