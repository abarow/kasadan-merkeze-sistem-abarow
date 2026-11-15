# abarow — Operasyonun depolaması: SQLite yerine PostgreSQL

> Tarih: 04.11.2026 (karar: "operasyonu da PostgreSQL ile çalıştıralım ve oturumları PostgreSQL'de tutalım"). Kasa SQLite'ta kalır (her kasada tek süreç, çevrimdışı öncelikli); mağaza sunucusu ve merkez PostgreSQL kullanır. Böylece iki katmanın depolaması tek teknoloji olur ve [PEOPLE.md](PEOPLE.md)'deki ortak SQL (`defterdb`, `personeldb`, `outbox`) lehçe farkı olmadan çalışır.

## Neden, neyi bedel ödeyerek

- **Kazanç:** operasyon ve merkez aynı veritabanı, aynı migration biçimi, aynı backup/geri yükleme (`pg_dump`); oturumlar kalıcı (servis yeniden başlayınca düşmez); `sessions` tablosu iki katmanda aynı; SQLite adaptörlerinin ikinci kopyası kalkar.
- **Bedel:** mağaza sunucusunda bir PostgreSQL container'ı daha (yaklaşık 50–100 MB RAM ve bir veri klasörü); mağaza kurulumu ve yedeği bir adım artar; SQLite'ın "tek dosya" kolaylığı gider. Kasalar bundan etkilenmez: kasa operasyona bağlanamasa da satışa devam eder (outbox).

## Kararlar

1. **Bir mağaza, bir PostgreSQL container'ı (`abarow-db`), bir veritabanı (`abarow`).** report ve inventory aynı veritabanını ayrı tablolarla ve ayrı goose sürüm tablolarıyla kullanır (merkezde reporting, ingest, catalog-admin ve inventory-headquarters'nun yaptığı gibi).
2. **Bağlantı `ABAROW_DATABASE_URL`** (merkezle aynı ad); `ABAROW_REPORT_DB` kalkar. Adres, parola dosyasından `podman secret` (`abarow-db-url`) olarak gelir, merkezdeki gibi.
3. **Mevcut SQLite verisi taşınmaz.** Proje henüz canlı mağazaya çıkmadı; kurulum betiği yeni veritabanıyla başlar. (Canlı bir mağaza olursa tek seferlik içe aktarma komutu ayrıca yazılır.)
4. **Oturumlar `sessions` tablosunda** (`personeldb`, parmak izi sütunuyla): müdürün PIN'i değişince eski oturumlar yine kapanır.
5. **Testler:** SQLite'a bağlı testler PostgreSQL'e geçer (`ABAROW_TEST_POSTGRES`; CI'da servis container'ı var). HTTP testleri veritabanı yerine `personeltest` deposunu kullanır.

## Adımlar

1. **report servisi:** `internal/adapters/out/postgres` (sorgular ve tek ilk migration: defter, vardiya, katalog, mağaza fiyatı, outbox, kişiler, oturumlar); `ABAROW_DATABASE_URL`; oturumlar veritabanında; SQLite adaptörü silinir.
2. **inventory (mağaza kapsamı):** mağaza stoğu PostgreSQL adaptörüyle; kapsam seçimi `ABAROW_DATABASE_URL` varlığına değil açık bir ayara bağlanır.
3. **Dağıtım:** `deploy/compose/store.yaml` (PostgreSQL servisi), `deploy/quadlet/store` (`abarow-db`, yedek zamanlayıcısı), `store-install.sh` (parola/secret, yedek, geri yükleme), CI.
4. **Belge ve doğrulama:** DESIGN.md, DATA-FLOW.md, mağaza + merkez simülasyonu, tarayıcı testleri.

## Uygulama durumu

| Adım | Durum |
|---|---|
| 1 | **Yapıldı (04.11.2026).** `services/store/internal/adapters/out/postgres`: SQLite adaptörünün yerine; tek ilk migration (defter, vardiya, katalog, mağaza fiyatı, outbox, kişiler, oturumlar), sürüm tablosu `report_goose_db_version`. `ABAROW_DATABASE_URL` (eski `ABAROW_REPORT_DB` kalktı). Oturumlar `sessions` tablosunda; müdür oturumu PIN özetine bağlı kalır (`Config.BoundSessions`: PIN değişince eski oturumlar kapanır, kendi PIN'ini değiştirenin oturumu açık kalır). Testler PostgreSQL'de (`postgrestest.Open`); kasiyer HTTP testleri `personeltest` ile. |
| 2 | **Yapıldı.** `inventory`: mağaza kapsamı da PostgreSQL adaptörüyle (`Store.Shop(here)`: tek yer, outbox mesajları mağaza kimliğiyle, `stock_locations` tablosu). Kapsam artık `ABAROW_STORE_ID` varlığına bağlı (varsa mağaza, yoksa merkez); `ABAROW_DATABASE_URL` ikisinde de zorunlu. SQLite adaptörü silindi; mağaza kapsamı testleri PostgreSQL'de. |
| 3 | **Yapıldı.** `deploy/compose/store.yaml` (`store-db`), Quadlet (`abarow-store-db`, `backup/abarow-store-backup.*`), `store-install.sh` (rastgele DB parolası ve secret'lar, günlük yedek, `restore`). |
| 4 | **Yapıldı.** TASARIM v0.65, VERI-AKISI, Quadlet README. Mağaza + merkez simülasyonu temiz ortamda geçti (131 kontrol). **Not:** eski bir simülasyon ortamında NATS verisi kalmışken veritabanı yeni açılırsa operasyon eski olayları görmez (kalıcı tüketici konumu NATS'tadır); `scripts/store-sim.sh down` ile temiz başlanır. |
