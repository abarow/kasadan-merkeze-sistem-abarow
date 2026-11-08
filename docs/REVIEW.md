# abarow — Beş bakış açısıyla inceleme (11.11.2026)

> Kod `35d20a5` üzerinde okundu ve araçlarla tarandı; hiçbir şey değiştirilmedi. Bulgular sırayla Go uzmanı, veritabanı yöneticisi (DBA), zayıf bağlı uygulama uzmanı, mikroservis mimarı ve performanslı/hafif/ölçeklenebilir yazılım uzmanı gözüyle yazıldı. "Denenmedi" yazan bulgular yalnızca kod okumasına dayanır.
>
> Kullanılan araçlar: `staticcheck`, `govulncheck`, `gosec` (bütün modüller), `go vet` ve testler (CI), elle okuma. Ölçüm rakamları [PERFORMANCE.md](PERFORMANCE.md) ve [REGISTER-POSTGRES.md](decisions/REGISTER-POSTGRES.md) içindendir.

## Özet

| # | Bakış | Önem | Bulgu |
|---|---|---|---|
| D1 | DBA | **Yüksek** | Hiçbir tabloda saklama süresi yok; merkez ve kasa verisi sınırsız büyüyor |
| B1 | Zayıf bağlılık | **Yüksek** | Merkezde `reporting`, `ingest` ve `catalog-admin`'in tablolarını doğrudan okuyor |
| G1 | Go | Orta | gRPC sunucularında panik kurtarma yok; miktarın üst sınırı yok, taşma servisi düşürebilir |
| G2 | Go | Orta | Go sürümü sabitlenmemiş; yerel ve yayın akışında düzeltilmiş standart kütüphane açıkları |
| D2 | DBA | Orta | Anlık kurtarma (PITR) yok; kasada yedek yok |
| D3 | DBA | Orta | Veritabanı izleme ve bakım planı yok |
| D4 | DBA | Orta | Yayınlanmış migration'ın değişmediğini denetleyen bir şey yok |
| B2 | Zayıf bağlılık | Orta | Kasada her ekran isteği `staff`'a, her satış `payment` ve `fiscal`'a senkron bağlı |
| M1 | Mikroservis | Orta | Metrik, iz ve istek kimliği yok; yalnız JSON günlük ve `/healthz` |
| P1 | Performans | Orta | Dönem raporları her istekte ham satırlardan hesaplanıyor |
| P2 | Performans | Orta | Merkez `ingest` tek işçi; kapasite 44–60 satış/sn |
| G3–G6, D5–D8, B3–B4, M2–M4, P3–P5 | | Düşük | Aşağıda |

---

## 1. Go uzmanı

**Genel durum iyi.** Ortak `pkg/service` paketi HTTP sunucularına `ReadHeaderTimeout`, düzgün kapanma, gRPC keepalive ve sağlık ucu veriyor. Para tamsayı kuruşla tutuluyor, taşma denetleniyor. Para işlemi yapan gRPC çağrılarında zaman aşımı var (`paymentgrpc`, `fiscalgrpc`, gateway). `staticcheck` yalnızca 9 küçük bulgu verdi (aşağıda G6).

**G1 — Orta: gRPC sunucularında panik kurtarma yok, miktarın üst sınırı yok (denenmedi).**
- `pkg/money/money.go:96-112` taşmada `panic` atıyor; bu bilinçli bir güvenlik ağı.
- Satış satırı yalnızca `Qty <= 0` diye denetleniyor (`services/core/sales/internal/domain/sale.go:74`); market sepeti de öyle (`services/modules/retail/internal/domain/basket.go:100,131`). Üst sınır yok.
- `pkg/service` gRPC sunucusuna kurtarma ara katmanı (recovery interceptor) eklemiyor. grpc-go bir işleyicideki paniği yakalamaz, süreç çöker.
- Sonuç: oturum açmış bir kasiyer (ya da hatalı bir terazi okuması) dev bir miktar gönderirse satır toplamı hesaplanırken `sales` süreci düşebilir. Systemd yeniden başlatır, veri kaybolmaz, ama satış ekranı kesilir. HTTP tarafında `net/http` paniği istek başına yakalar; sepet bu miktarla kaydedilmişse her görüntülemede yeniden patlar.
- Öneri: alan düzeyinde makul bir üst sınır (örneğin satır başına miktar ve tutar) ve `pkg/service`'te gRPC için panik kurtarma.

**G2 — Orta: Go sürümü sabitlenmemiş; düzeltilmiş açıklar.**
- `govulncheck`, Go 1.27.0 standart kütüphanesinde 14 bilinen açık buldu (`net/http`, `http2`, `crypto/tls`, `crypto/x509`, `html/template` ve diğerleri; GO-2026-6599…6859). Çağrı izleri servis kodundan geçiyor. Düzeltmeleri 1.27.2–1.27.4 sürümlerinde.
- `go.mod` dosyaları `go 1.26.0` diyor, `toolchain` satırı yok. CI `1.27` (en son yama) kullanıyor, imajlar `golang:1.27` gezici etiketiyle derleniyor, terfi akışı (`.github/workflows/promotion.yml:67`) `go-version-file` ile **1.26.0**'ı kuruyor.
- Sonuç: bir imajın hangi yamayla derlendiği derleme anına bağlı, yeniden üretilebilir değil. Yama sürümü çıkınca eski imajlar kendiliğinden düzelmez.
- Öneri: `toolchain go1.27.x` satırı, imajda sürüm ve özet (digest) sabitleme, açık çıktığında yeniden derleme kuralı.

**G3 — Düşük: JSON gövde sınırı tutarsız.** On üç uçta `http.MaxBytesReader` var. Şu uçlarda yok: gateway `handler.go:252,292,315`, market `handler.go:109,122`, mağaza `office.go:118`. Gateway yalnız 127.0.0.1'e açık olduğu için risk düşük.

**G4 — Düşük: Güncelleme ajanının Podman istemcisinde zaman aşımı yok.** `services/update/internal/adapters/out/podman/podman.go:98` (`&http.Client{}`) ve Unix soketi istemcisi. Takılan bir Podman API çağrısı güncellemeyi süresiz bekletebilir.

**G5 — Düşük: Adlandırma kuralı tam uygulanmadı.** Kullanıcıya görünen iletiler bilerek Türkçe, ama şunlar da hâlâ Türkçe:
- 15 komut satırı bayrağı açıklaması (`storebench`, `headquartersbench` ve diğerleri),
- 347 test hata iletisi,
- 55 yorum satırı.

Önceki çalışmada "hepsi çevrildi" denmişti; bu doğru değildi.

**G6 — Düşük: `staticcheck` ve `gosec`.**
- `staticcheck`: 2 kullanılmayan alan (`services/update/internal/release/updater.go:169` `lastPhase`, ilgili test), 2 kullanılmayan değer (testlerde), testte özdeş karşılaştırma (`pkg/login/signin_test.go:49`), 4 hata metni biçimi.
- `gosec` yüksek dereceli bulgular elle kontrol edildi, hepsi yanlış alarm:
  - Yol geçişi: `pkg/service/spa.go:18` yolu temizliyor, kanal adı izin listesinde.
  - SQL birleştirme: sütun adları sabit.
  - Çerez: `HttpOnly`, `SameSite=Strict`, HTTPS'te `Secure`.
- 78 adet "hata denetlenmemiş" (G104) bulgusu çoğunlukla kapanış ve yazma dönüşleri.

**G7 — Düşük: `pkg/pgtune/pgtune.go:123` yorumu yanlış.** "A register holds its own sales only until they are delivered" diyor, ama kasada satışlar silinmiyor (bkz. D1). Bu yorumu 08.11.2026'daki kasa geçişinde ben yazdım.

---

## 2. Veritabanı yöneticisi (DBA)

**Genel durum iyi.** Para `BIGINT` kuruş olarak tutuluyor. Fiş, Z, vardiya ve olay numaraları işlemle geri alınan sayaç satırından geliyor (testli). Ayarlar kasa ve mağaza sayısına göre hesaplanıyor. Aylık rapor için kapsayıcı indeks ölçülerek eklendi. Mağaza sunucusu ve merkezde günlük `pg_dump` yedeği var.

**D1 — Yüksek: Saklama süresi ve temizlik yok.** Kodda yaşa göre satır silen tek yer sepet ve adisyon tabloları; şu tablolar sınırsız büyüyor:
- **Kasada:**
  - `sales`, `sale_lines`, `sale_payments` (kasa başına yılda ~110 bin satış),
  - yayınlanmış `outbox` satırları (satış ve personel),
  - `delivery_received`,
  - personelde `completed_sales`, `voided_sales`, `cash_movements`, `drawer_opens`.

  `ABAROW_CLEAN_MAX_AGE` yalnızca NATS akışındaki iletileri siliyor, veritabanı satırlarını değil.
- **Mağaza sunucusu ve merkezde:**
  - satış defteri tabloları,
  - yayınlanmış `outbox` ve `receipt_outbox`,
  - `delivery_issued`,
  - merkezde her olayın ham içeriğini (`BYTEA`) tutan `events` tablosu. Aynı satış hem `events`'te hem normalleştirilmiş tablolarda duruyor; bu çift depolama demek.
- 50 mağazada bir yıl ≈ 25 GB satış verisi ([PERFORMANCE.md](PERFORMANCE.md)); `events` bunun üstüne eklenir. Disk, yedek süresi ve rapor süresi büyür.
- Öneri:
  - her tablo için saklama süresi (yasal saklama süresi ayrı değerlendirilmeli),
  - yayınlanmış olay kuyruğunun budanması,
  - merkezde satış tablolarının aylık bölümlenmesi (partition),
  - `events` için arşivleme.

**D2 — Orta: Anlık kurtarma yok; kasada yedek yok.**
- Mağaza sunucusu ve merkez günde bir `pg_dump` alıyor (`deploy/quadlet/backup/backup.sh:15`). WAL arşivi yok, bu yüzden merkezde bir günlük veri kaybı olası. Ayrıca büyüyen veride `pg_dump` uzar.
- Kasada hiç yedek yok. Mağazaya teslim edilmemiş olaylar tek kopya olarak kasa diskinde.

**D3 — Orta: İzleme ve bakım planı yok.**
- `pg_stat_statements` ya da yavaş sorgu günlüğü açık değil.
- Autovacuum ve şişkinlik izlenmiyor.
- Zamanlanmış `REINDEX` ya da `VACUUM` yok, her şey autovacuum'a bırakılmış (2 işçi, varsayılan eşikler).
- Ölçümler hep taze veride yapıldı; aylarca çalışmış bir veritabanının durumu bilinmiyor.

**D4 — Orta: Migration disiplini.**
- Kasa servislerinin `00001_init.sql` dosyaları PostgreSQL geçişinde yerinde değiştirildi; sahada kurulum olmadığı için bilinçli bir seçim.
- Sahaya çıktıktan sonra her değişiklik yeni dosya olmalı. Yayınlanmış bir migration dosyasının değiştirilmesini CI'da yakalayan bir denetim yok.

**D5 — Düşük: Yabancı anahtarda indeks eksikleri.** `staff` şemasında indeks olmayan yabancı anahtar sütunları:
- `cash_movements.cashier_id`,
- `approvals.manager_id`,
- `drawer_opens.opened_by`,
- `sessions.cashier_id` (`ON DELETE CASCADE`).

Tablolar küçük, ama kasiyer silinince tam tarama olur.

**D6 — Düşük: `plan_cache_mode=force_custom_plan` sunucu genelinde.** Rapor sorununu çözdü, ama her hazır sorgu (satış yazmaları dahil) her çalıştırmada yeniden planlanıyor. Yalnızca rapor işlemine özel (`SET LOCAL`) uygulanabilirdi. Yazma yoluna etkisi ölçülmedi.

**D7 — Düşük: Zaman tipi tutarsız.** `staff` şemasında zaman sütunları sabit genişlikli metin (`TEXT COLLATE "C"`), diğer şemalarda `TIMESTAMPTZ`.

**D8 — Düşük: Ana sürüm yükseltmesi.** PostgreSQL 18 sabit. Kasalar dahil yüzlerce kurulumda `pg_upgrade` yolu tanımlı değil.

---

## 3. Zayıf bağlı uygulama uzmanı

**Genel durum iyi.** Katmanlar arası akış olay tabanlı:
- olay kuyruğu (outbox),
- protobuf sözleşmeleri (`buf breaking` CI'da),
- sürümlü konu adları (`.v1`),
- kimlikle tekrar ayıklama,
- teslim makbuzu,
- sıra numarasıyla kayıp olay tespiti.

Bağlantı kopunca kasa ve mağaza çalışmayı sürdürüyor, olaylar birikip sonra gidiyor.

**B1 — Yüksek: Merkezde servisler veritabanı üzerinden birbirine bağlı.**
- `services/headquarters/reporting/internal/adapters/out/postgres/store.go` başka servislerin tablolarını okuyor:
  - `ingest`'in tabloları: `sales`, `store_links`, `store_stats`, `store_prices`, `cashiers`,
  - `catalog-admin`'in tabloları: `catalog_products`, `catalog_prices`.
- Satış defteri sorgu kodu (`pkg/ledger/ledgerdb`) bilinçli olarak ortak; ama tablo sahipliği belirsiz.
- Bu tablolardan birinde şema değişirse üç servis birlikte kırılır, ayrı ayrı güncellenemez. Bu, "her servis tek başına güncellenebilir" hedefiyle çelişiyor.
- Öneri: okuma modelini olaylarla `reporting`'e taşımak ya da tablo sahipliğini ve sözleşmesini açıkça yazıp şema değişikliğini sürüm kuralına bağlamak.

**B2 — Orta: Kasada senkron zincir.**
- Gateway her korumalı istekte `staff.Authorize` çağırıyor (`services/core/gateway/internal/httpapi/handler.go:160,482,845`). `staff` yeniden başlarken (güncelleme, çökme) bütün kasa ekranı durur.
- Satış kapanışı `sales → payment` ve `sales → fiscal` senkron (`services/core/sales/internal/app/service.go:138,262`).
- Zaman aşımları var, yeniden deneme ve devre kesici yok. Para işlemlerinde kör yeniden deneme tehlikeli olduğu için bu kısmen bilinçli.
- Öneri: oturum doğrulamasının gateway'de kısa süreli önbelleklenmesi ya da imzalı jeton; `staff` kesintisinde ne olacağının tasarlanması.

**B3 — Düşük: Tek ortak `abarow.com/pkg` modülü.** Servisler 10–25 alt paket kullanıyor (en çok `store` 25, `reporting` 20). `go.work` ile hepsi aynı sürümü görüyor. `pkg`'deki bir değişiklik bütün servislerin yeniden derlenip test edilmesini gerektiriyor; paketlerin ayrı sürümü yok.

**B4 — Düşük: Kasada ortak veritabanı.** PostgreSQL geçişinden sonra altı servis aynı container'a bağlı (ayrı şemalarla). Container durursa hepsi aynı anda "sağlıksız" olur; önceden her servisin kendi dosyası vardı.

---

## 4. Mikroservis mimarı

**Genel durum iyi.**
- Her servis kendi container'ında ve tek başına güncellenebiliyor.
- İmzalı güncelleme merkezi, onay ve kademeli yayın var.
- Sağlık uçları, katman başına ağ ayrımı, leaf NATS ile katmanlar arası TLS ve servis başına bağlantı havuzu sınırı (`pkg/pgpool`) mevcut.

**M1 — Orta: Gözlemlenebilirlik yok.**
- Prometheus metrikleri, OpenTelemetry izleri ya da istek/korelasyon kimliği yok.
- Yalnız stdout'a JSON günlük (`pkg/service/service.go:60`) ve `/healthz` var.
- Üç katmanda 20'den fazla servis var. Bir satışın kasadan merkeze yolculuğunu izlemek ya da "rapor neden yavaş" sorusunu sahada cevaplamak için veri yok.
- Ölçümler yalnızca test araçlarıyla (`storebench`, `headquartersbench`) yapılabiliyor.

**M2 — Düşük: Kasada 10 container.**
- Gateway, modül, catalog, sales, payment, staff, device, fiscal, NATS ve veritabanı.
- "Tek binary yok" kararıyla uyumlu; bellek zirvesi 141 MiB, sorun değil.
- Bedeli sürüm uyumluluğu. Protobuf `buf breaking` sözleşmeyi koruyor, ama farklı sürümdeki servislerin birlikte çalıştığını gösteren bir test yok; güncelleme merkezi tek tek servis güncelliyor.

**M3 — Düşük: Merkezde tek NATS düğümü.** JetStream akışları tek kopya, replika yok. Merkezin NATS'ı ya da diski giderse merkez olay kabul edemez. Mağazalar biriktirdiği için veri kaybolmaz ama merkez durur.

**M4 — Düşük: Sağlık ucu sürüm söylemiyor.** `/healthz` yalnızca "ok" dönüyor. Sahada hangi servisin hangi sürümde olduğu güncelleme ajanının raporundan biliniyor, servisten değil.

---

## 5. Performanslı, hafif ve ölçeklenebilir yazılım uzmanı

**Genel durum iyi.**
- İmajlar `FROM scratch`; servis başına `GOMEMLIMIT` ve bellek tavanı var.
- Kasa toplam zirvesi ≈141 MiB (55 MiB'ı veritabanı).
- Veritabanı ayarı ölçek kademeli (`pgtune`); paralel sorgu 10 kasadan itibaren açılıyor.
- Aylık rapor 50 mağazada tek kullanıcıyla 1,8 sn.

**P1 — Orta: Dönem raporları her istekte ham satırlardan hesaplanıyor.**
- 50 mağaza, 11 ekran ve kayıt yükü altında aylık rapor 5,9–6,8 sn.
- Veri büyüdükçe süre doğrusal artar (D1 ile birleşince yıllar içinde ağırlaşır).
- Önceden toplanmış günlük özet tablosu yok ([PERFORMANCE.md](PERFORMANCE.md)'de açık bırakılan konu).

**P2 — Orta: Merkez `ingest` tek işçi.**
- JetStream tüketicisi iletileri sırayla işliyor (`pkg/natsio/consumer.go:122`).
- Ölçülen kapasite 44–60 satış/sn. 100 kasalı yükte (20 satış/sn) yaklaşık %40 doluluk; yoğun saat ya da mağaza artışında sınır yakın.
- Yatay ölçek için birden fazla `ingest` örneği çalıştırma, sıra numarası takibi açısından denenmedi.

**P3 — Düşük: Çift depolama.** `events` tablosundaki ham olay içeriği disk, yedek süresi ve önbellek baskısı ekliyor (D1).

**P4 — Düşük: Ölçülmeyenler.**
- Chromium kiosk ve işletim sistemi belleği.
- Aylarca veri birikmiş kasa ve merkez (bellek, disk, rapor süresi).
- ARM64 düşük güçlü işlemcide süreler.
- Elektrik kesintisinden sonra PostgreSQL'in toparlanma süresi.

**P5 — Düşük: Paralel sorgu bellek payı.** `/dev/shm` en kötü duruma göre ayrıldığı için merkez 50 mağazada container tavanı 2560 MB'a çıktı. Pay ancak kullanıldıkça harcanıyor, ama makine boyutlandırması bu tavana göre yapılmalı.
