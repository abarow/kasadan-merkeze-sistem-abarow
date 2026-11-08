# abarow — Kaynak kullanımı ölçümü (boşta ve yük altında)

> Tarih: 05.11.2026. Üç katmanlı simülasyonun (`scripts/store-sim.sh`) 28 container'ı ayaktayken ölçüldü. Bu bir **geliştirici bilgisayarı ölçümüdür**; gerçek mağaza donanımının değerini vermez, garanti de değildir (bkz. "Sınırlar").

## Güncelleme: HTTPS sonrası yeniden ölçüm (06.11.2026)

Operasyon ekranı HTTPS'e geçti (karar 06.11.2026) ve veritabanı migration'ları tek dosyada birleşti; bu yüzden ölçümler yeniden alındı. Aşağıdaki değerler güncel olandır; sonraki bölümler 05.11.2026 ölçümüdür ve tarihsel kayıt olarak kalır. Aynı makine ve aynı yük üreteci; **Dependency-Track container'ları koşumdan önce durduruldu**. Ana makinede abarow dışı birkaç boşta container (ve boşta bir `abarow-pgtest`) çalışıyordu, ölçüme alınmadı.

**Önemli:** Docker Compose simülasyonu operasyon ekranını hâlâ düz HTTP sunar (ekran testleri ve uçtan uca testler öyle yazılı). Bu yüzden tam yığın ölçümü HTTP'dir; HTTPS'in etkisi aşağıda ayrıca, aynı veritabanına bağlı iki report ile ölçüldü.

### Ortam ve yöntem

- Docker Desktop (Windows 11, WSL2): 16 mantıksal CPU, 7,9 GB bellek. 29 container (2 kasa × 9, operasyon 5, merkez 6), hepsi `healthy`.
- Yük 05.11 ile aynıdır (75 sn; her kasada bir kasiyer art arda tam satış, kasa başına 8 gateway okuyucusu, 16 operasyon okuyucusu, 16 merkez okuyucusu). `docker stats --no-stream` döngüsü, yük süresince; boşta 45 sn. CPU %, tek çekirdeğin yüzdesidir.
- **Taze yığındaki ilk koşum** raporlanır (aşağıya bakın: ardışık koşumlar yavaşladı).

### Toplam

| | Boşta | Yük altında |
|---|---|---|
| Tüm container'lar, CPU (ortalama) | %20 (yaklaşık 0,2 çekirdek) | %994 (yaklaşık 9,9 çekirdek) |
| Tüm container'lar, CPU (en yüksek örnek) | %43 | %1170 |
| Tüm container'lar, bellek (ortalama) | 445 MiB | 582 MiB |
| Tüm container'lar, bellek (en yüksek) | 449 MiB | 613 MiB |

| Katman | Boşta CPU / bellek | Yük CPU / bellek |
|---|---|---|
| İki kasa (18 container) | %12 / 142 MiB | %320 / 192 MiB |
| Operasyon (5) | %4,5 / 158 MiB | %360 / 220 MiB |
| Merkez (6) | %4,2 / 145 MiB | %314 / 170 MiB |

Bir kasa (9 container): boşta yaklaşık %6 CPU ve 68 MiB, yük altında yaklaşık %161 CPU ve 95 MiB (en çok 115 MiB).

### Container başına

CPU %, tek çekirdek; bellek MiB. register-2 register-1 ile aynı davranır, satırı çıkarıldı.

| Container | Boşta CPU | Boşta bellek | Yük CPU (ort / maks) | Yük bellek (ort / maks) |
|---|---|---|---|---|
| register-1 catalog | 0,6 | 7,3 | 4,0 / 10,0 | 9,3 / 10,1 |
| register-1 device | 0,6 | 3,7 | 1,9 / 7,2 | 6,4 / 9,3 |
| register-1 fiscal | 0,5 | 3,7 | 2,2 / 8,0 | 7,1 / 10,8 |
| register-1 gateway | 0,6 | 5,7 | 43,9 / 72,4 | 9,9 / 11,2 |
| register-1 nats | 1,1 | 16,4 | 3,4 / 7,0 | 18,7 / 21,3 |
| register-1 payment | 0,7 | 5,3 | 2,5 / 7,8 | 7,2 / 12,7 |
| register-1 retail | 0,5 | 6,7 | 8,2 / 14,8 | 10,2 / 11,2 |
| register-1 sales | 0,8 | 9,5 | 8,4 / 12,7 | 11,6 / 12,7 |
| register-1 staff | 0,8 | 9,6 | 86,4 / 105,8 | 14,1 / 16,0 |
| operasyon operasyon (NATS) | 1,5 | 25,7 | 15,4 / 21,2 | 51,0 / 55,8 |
| operasyon inventory | 0,8 | 20,8 | 26,1 / 40,0 | 23,8 / 25,0 |
| operasyon catalog-admin | 0,8 | 19,1 | 0,9 / 5,9 | 19,1 / 20,8 |
| operasyon PostgreSQL | 0,7 | 76,1 | 208,1 / 319,7 | 102,7 / 106,3 |
| operasyon report (ekran + API) | 0,8 | 16,8 | 109,4 / 157,0 | 23,1 / 28,8 |
| merkez catalog-admin | 0,6 | 7,5 | 1,4 / 7,1 | 7,3 / 7,5 |
| merkez ingest | 0,6 | 20,3 | 8,2 / 15,5 | 20,2 / 21,0 |
| merkez merkez (NATS) | 1,0 | 13,9 | 5,1 / 8,6 | 20,0 / 22,8 |
| merkez PostgreSQL | 0,8 | 71,6 | 196,6 / 294,2 | 87,8 / 91,8 |
| merkez inventory | 0,7 | 7,4 | 3,7 / 8,9 | 8,4 / 8,9 |
| merkez reporting (ekran + API) | 0,5 | 24,0 | 98,4 / 135,8 | 26,5 / 27,6 |

### Gecikme (taze yığın, ağır yük: okuyucu 8)

| İş | Tamamlanan | Hata | İstek/sn | p50 | p95 | p99 |
|---|---|---|---|---|---|---|
| Kasa satışı (tam akış, 5 çağrı) | 682 | 0 | 9,1 (iki kasa toplam) | 211 ms | 325 ms | 371 ms |
| Kasa gateway okuma | 57 339 | 0 | 769 | 17 ms | 50 ms | 71 ms |
| Operasyon okuma | 28 620 | 0 | 384 | 38 ms | 83 ms | 109 ms |
| Merkez okuma | 27 240 | 0 | 365 | 40 ms | 84 ms | 115 ms |

Hafif yük (okuyucu 2, 60 sn, ayrı taze yığın): tam satış 904 adet, p50 119 ms, p95 228 ms, p99 281 ms; kasa gateway okuma p50 6 ms; operasyon okuma p50 11 ms; merkez okuma p50 11 ms; hata 0.

Bu değerler 05.11 değerlerinden (ağır yükte satış p50 406 ms, hafif yükte 213 ms) belirgin biçimde iyidir. **Bu farkı kodun hızlanmasına bağlamayın:** 05.11 ölçümü ana makinede başka işler çalışırken alınmıştı, bugünkü koşum daha az yüklü bir makinede; mutlak sayılar koşumdan koşuma değişir. Karşılaştırma için aynı oturumda alınan sayılara bakın.

### HTTPS'in etkisi (operasyon ekranı)

Aynı veritabanına bağlı iki report (biri düz HTTP, biri HTTPS; sertifika mağaza CA'sının leaf sertifikası), yalnızca operasyon okuması (16 eşzamanlı okuyucu: gün özeti, kasa durumu, stok araması), 30 sn, sırayla 3 tur; her ölçümde yalnızca ilgili report çalışır (ikisi birlikte `max_connections = 20` sınırını aşar ve HTTPS tarafında 503 verir; ilk deneme böyle bozuldu ve tekrarlandı).

| Tur | HTTP: istek/sn · p50 · p95 | HTTPS: istek/sn · p50 · p95 |
|---|---|---|
| 1 | 688 · 18,5 ms · 47,9 ms | 695 · 18,6 ms · 46,8 ms |
| 2 | 673 · 19,0 ms · 49,0 ms | 699 · 18,4 ms · 46,7 ms |
| 3 | 698 · 18,3 ms · 47,0 ms | 662 · 19,4 ms · 50,2 ms |

HTTPS ile HTTP arasındaki fark tur içi gürültü sınırındadır (istek/sn %5, p50 1 ms içinde, iki yönde de); hata 0. Sınırlar: yük üreteci bağlantıları yeniden kullanır (keep-alive), yani TLS el sıkışması az sayıda yapılır; her istekte yeni bağlantı açan bir istemci daha pahalı olur ve ölçülmedi. Yük üreteci kendi imzalı sertifikayı doğrulamadan kabul etti (yerel deneme), sertifika doğrulaması ölçülmedi.

Aynı yığında 3 ağır koşum arka arkaya yapıldığında (ikincisinde ve üçüncüsünde operasyon ekranı HTTPS idi) satış sayısı 682 → 504 → 385'e, kasa gateway okuma 769 → 448 → 344 istek/sn'ye düştü. Gateway ve merkez TLS'ten etkilenmediği için bu düşüş HTTPS'ten değildir. Nedeni araştırılmadı (açık vardiyanın X raporu tek başına 8–11 ms ölçüldü; birikmiş veri tek başına açıklama değil). Bu yüzden tablolar taze yığındaki ilk koşumdur.

### Veritabanı ölçümü yeniden (10 kasa)

`scripts/bench-store.sh 10`, 05.11 ile aynı ayarlar ve veri (90 bin satış). Migration birleşimi şemayı değiştirmedi (önce/sonra şema karşılaştırması 0 fark); sonuç beklenen aralıkta:

| 10 kasa | 05.11 | 06.11 |
|---|---|---|
| Kasa durumu p50 | 52 ms | 59 ms |
| Günlük özet p50 | 79 ms | 65 ms |
| Aylık özet p50 | 837 ms | 819 ms |
| Ürün sıralaması p50 | 1,0 sn | 951 ms |
| Kayıt işleme p50 / p95 | 14 / 29 ms | 14 / 25 ms |
| En çok kuyruk | 3 | 2 |
| DB CPU ort. | %47 | %50 |
| Veritabanı boyutu | 172 MB | 172 MB |

Diğer kasa ve mağaza sayıları (1, 2, 5, 50; merkez bench) yeniden koşulmadı: HTTPS bu araçların yolunda değildir (doğrudan veritabanına yazarlar), şema aynıdır ve 10 kasa kontrolü değişmediğini gösteriyor. Tam yenileme için `bench-store.sh` ve `bench-headquarters.sh` aynen çalıştırılabilir.

---

Aşağıdaki bölümler **05.11.2026** ölçümüdür.

## Ortam

- Docker Desktop (Windows 11, WSL2): 16 mantıksal CPU, 7,9 GB bellek. Ölçüm ana makinede başka işler çalışırken alındı.
- 28 container, hepsi `healthy`: 2 kasa × 9 (gateway, sales, retail, catalog, payment, fiscal, device, staff, nats), operasyon 5 (store/NATS, inventory, report, PostgreSQL, nats-auth çıkışlı), merkez 6 (headquarters/NATS, ingest, reporting, inventory, catalog-admin, PostgreSQL).
- Her container'ın `mem_limit` değeri compose dosyalarında tanımlıdır (32–256 MiB).
- Örnekleme: `docker stats --no-stream`, container başına ~8 örnek (boşta) ve ~14 örnek (yük). **CPU %, tek çekirdeğin yüzdesidir** (100 = bir çekirdek tam dolu).

## Yük

75 saniye, aynı anda:

| Yük | Ayrıntı |
|---|---|
| Kasa satışı | Her kasada bir kasiyer, art arda tam satış: sepet aç, 3 ürün (iki barkod, bir terazi), sepeti kapat, nakit öde (gateway üzerinden) |
| Kasa gateway okuma | Kasa başına 8 eşzamanlı okuyucu: kasiyer listesi, X raporu, vardiya |
| Operasyon ekranı | 16 eşzamanlı okuyucu: gün özeti, kasa durumu, stok araması |
| Merkez ekranı | 16 eşzamanlı okuyucu: özet rapor, mağaza listesi |

## Sonuç: toplam

| | Boşta | Yük altında |
|---|---|---|
| Tüm container'lar, CPU (ortalama) | %38 (yaklaşık 0,4 çekirdek) | %922 (yaklaşık 9,2 çekirdek) |
| Tüm container'lar, CPU (en yüksek örnek) | %78 | %1213 |
| Tüm container'lar, bellek (ortalama) | 392 MiB | 540 MiB |
| Tüm container'lar, bellek (en yüksek) | 395 MiB | 579 MiB |

Katmana göre (ortalamaların toplamı):

| Katman | Boşta CPU / bellek | Yük CPU / bellek |
|---|---|---|
| İki kasa (18 container) | %22 / 167 MiB | %229 / 218 MiB |
| Operasyon (5) | %7,5 / 105 MiB | %383 / 159 MiB |
| Merkez (6) | %8,3 / 120 MiB | %310 / 163 MiB |

Bir kasa (9 container) boşta yaklaşık %11 CPU ve 85 MiB, yük altında yaklaşık %115 CPU ve 110 MiB kullandı.

## Sonuç: container başına

CPU %, tek çekirdek; bellek MiB. "register-1" ve "register-2" iki kasadır, aynı davranır; register-2 satırı çıkarıldı.

| Container | Boşta CPU | Boşta bellek | Yük CPU (ort / maks) | Yük bellek (ort / maks) |
|---|---|---|---|---|
| register-1 gateway | 1,4 | 16,7 | 30,2 / 54,6 | 21,0 / 24,0 |
| register-1 staff | 2,3 | 10,0 | 56,6 / 85,0 | 13,8 / 15,4 |
| register-1 sales | 0,8 | 8,9 | 7,3 / 13,6 | 11,3 / 12,3 |
| register-1 retail | 1,1 | 7,1 | 6,2 / 11,4 | 9,8 / 10,4 |
| register-1 catalog | 0,9 | 7,9 | 3,4 / 9,3 | 10,3 / 10,8 |
| register-1 payment | 1,0 | 20,3 | 2,5 / 11,1 | 21,5 / 22,2 |
| register-1 fiscal | 0,8 | 4,1 | 2,0 / 7,9 | 7,4 / 8,2 |
| register-1 device | 1,2 | 7,1 | 2,5 / 8,4 | 9,7 / 10,6 |
| register-1 nats | 1,7 | 19,6 | 3,0 / 8,6 | 21,6 / 22,6 |
| operasyon operasyon (NATS) | 2,0 | 19,3 | 10,3 / 18,0 | 43,4 / 55,0 |
| operasyon inventory | 2,0 | 8,1 | 13,2 / 22,6 | 9,7 / 10,4 |
| operasyon PostgreSQL | 1,1 | 63,5 | 289,8 / 401,8 | 87,6 / 98,1 |
| operasyon report (ekran + API) | 2,4 | 13,8 | 69,4 / 99,5 | 18,4 / 20,0 |
| merkez merkez (NATS) | 2,0 | 13,3 | 6,1 / 9,6 | 21,4 / 25,1 |
| merkez ingest | 1,3 | 8,9 | 6,7 / 13,4 | 9,3 / 9,9 |
| merkez inventory | 0,8 | 7,6 | 3,5 / 9,0 | 8,4 / 9,4 |
| merkez PostgreSQL | 1,4 | 73,5 | 228,1 / 338,8 | 101,5 / 112,7 |
| merkez reporting (ekran + API) | 1,2 | 8,7 | 64,9 / 101,3 | 15,4 / 18,0 |
| merkez catalog-admin | 1,7 | 8,1 | 1,0 / 7,0 | 7,4 / 7,6 |

## Yük altında gecikme (istemci tarafı)

| İş | Tamamlanan | Hata | İstek/sn | p50 | p95 | p99 |
|---|---|---|---|---|---|---|
| Kasa satışı (tam akış, 5 çağrı) | 361 | 0 | 4,9 (iki kasa toplam) | 406 ms | 583 ms | 637 ms |
| Kasa gateway okuma | 28 747 | 0 | 386 | 35 ms | 95 ms | 133 ms |
| Operasyon okuma | 5 691 | 0 | 77 | 176 ms | 510 ms | 677 ms |
| Merkez okuma | 7 441 | 0 | 100 | 114 ms | 462 ms | 639 ms |

Aynı yükün hafif hâlinde (okuyucu başına 2) tam satış p50 213 ms, p95 316 ms idi: okuma yükü eklenince satış gecikmesi yaklaşık iki katına çıktı.

## Gözlemler

- **Boşta** bütün sistem yaklaşık 0,4 çekirdek ve 390 MiB; iki PostgreSQL toplam bellek payının üçte birini alır (64 ve 74 MiB).
- **Yük altında en pahalı işler:** iki PostgreSQL (ekran okumalarının sorguları) ve her kasadaki `staff` servisi. `staff` her gateway isteğinde oturumu doğrular; okuma yükünde CPU'su (%57–%85) gateway'inkinden (%30–%55) yüksek çıktı. Bu bir iyileştirme adayıdır; bu çalışmada değiştirilmedi.
- Hiçbir container `mem_limit` değerine yaklaşmadı (en yüksek kullanım: merkez PostgreSQL 113 / 256 MiB, operasyon operasyon 55 / 128 MiB).
- Yük süresince hata ve düşen istek olmadı (sıfır hata).

## Sınırlar

- Docker Desktop/WSL2 üzerinde, geliştirici bilgisayarında, tek çalıştırma; mağaza donanımı (ör. düşük güçlü Linux kutusu) için sonuç çıkarılamaz. Gerçek donanımda aynı ölçüm ayrıca alınmalıdır.
- Kasada bir kasiyer olduğundan satış hattı kasa başına tek ve art arda; "eşzamanlı kasiyer" yükü değildir. Satışlar nakit, tek tür sepettir.
- Yük üreteci depoda değildir (tek seferlik betik); okuma uçları ve istek karışımı yukarıdaki tablodadır.
- CPU yüzdeleri `docker stats` örneklemesidir; kısa tepeler görünmeyebilir.

## Kasa sayısına göre operasyon veritabanı (1, 2, 5, 10, 50 kasa)

> Tarih: 05.11.2026. Operasyon PostgreSQL'inin kasa sayısıyla nasıl ölçeklendiğini ölçer. **Geliştirici bilgisayarında, tek çalıştırma**; sayılar sıralama ve oran için güvenilirdir, mağaza donanımı için garanti değildir.

### Yöntem

- Araç: `services/store/cmd/storebench` (gerçek `Store` kodu: `SaveSale`, `IssueReceipt`, `Activity`, `Report`) ve `scripts/bench-store.sh`. Kasalar ve NATS yoktur; kasaların gönderdiği satışlar doğrudan veritabanına yazılır. Veritabanı boş bir `postgres:18-alpine` container'ıdır; ayarları ve bellek sınırı `report pgtune --registers N` çıktısından gelir.
- **Veri kasa sayısıyla katlanır:** kasa başına 30 gün × 300 satış = 9 000 satış (satış başına 4 satır, ödeme, 2 KDV satırı, bir teslim makbuzu; yüzde biri iptal; günde bir vardiya). 1 kasa: 9 bin satış / 26 MB; 50 kasa: 450 bin satış / 830 MB.
- **Yük (60 sn):** her kasa saniyede 0,5 satış gönderir (gerçek yoğun saatin 5–15 katı; 50 kasada 25 satış/sn) ve satışlar tek işçide sırayla işlenir (operasyon tüketicisi gibi); gecikme geliş anından işlenmeye kadardır. Ekranlar: kasa durumu 5 sn'de bir, günlük/aylık özet ve ürün sıralaması 10 sn'de bir (ekran sayısı = kasa sayısı/10 + 1).
- Bağlantı havuzu sınırlıdır (rapor 8, envanter 4, catalog-admin 4); `max_connections` 20.

### Sonuç (son ayarlar, son indeksler)

| Kasa | Satış | Veritabanı | Kasa durumu p50 | Günlük özet p50 | Aylık özet p50 | Ürün sıralaması p50 | Kayıt işleme p50 / p95 | Kuyruk | DB CPU ort. | DB bellek en çok |
|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 9.000 | 26 MB | 9 ms | 10 ms | 107 ms | 89 ms | 14 ms / 20 ms | 1 | %7 | 72 MiB |
| 2 | 18.000 | 43 MB | 15 ms | 16 ms | 216 ms | 174 ms | 14 ms / 22 ms | 1 | %12 | 84 MiB |
| 5 | 45.000 | 91 MB | 29 ms | 40 ms | 388 ms | 437 ms | 14 ms / 18 ms | 1 | %25 | 135 MiB |
| 10 | 90.000 | 172 MB | 52 ms | 79 ms | 837 ms | 1,0 sn | 14 ms / 29 ms | 3 | %47 | 245 MiB |
| 50 | 450.000 | 830 MB | 437 ms | 816 ms | 7,5 sn | 8,5 sn | 31 ms / 397 ms | 27 | %220 | 640 MiB |

Hata: bu beş ölçümde toplam **0**. Kayıt işleme kapasitesi (tek işçi, kuyruksuz): 1 kasa 73/sn, 2 kasa 77/sn, 5 kasa 79/sn, 10 kasa 74/sn, 50 kasa 60/sn. En çok kullanılan bağlantı 9 (sınır 20).

### İndeks etkisi (indekssiz ve eski ayarlı temel ile karşılaştırma)

Her kasa sayısı için aynı yük, bu indeksler olmadan ("temel") ve ile ("indeksli"; ayarlar aynı, eski pgtune). Kasa durumu ekranının sorgusu:

| Kasa | Kasa durumu p50: temel → indeksli | Kayıt işleme p95: temel → indeksli |
|---|---|---|
| 1 | 10 ms → 10 ms | 24 ms → 19 ms |
| 2 | 24 ms → 14 ms | 22 ms → 25 ms |
| 5 | 61 ms → 28 ms | 18 ms → 20 ms |
| 10 | 86 ms → 52 ms | 22 ms → 21 ms |
| 50 | 1,1 sn → 669 ms | 450 ms → 680 ms |

Kasa durumu 2 kasadan itibaren %40–55 hızlandı. Aylık özet ve ürün sıralaması indekslerden etkilenmez (tüm aralığı tarar). 50 kasada kayıt işleme gecikmesi indeksli turlarda daha yüksek çıktı (p95 450 → 680 ms; ilk indeks sürümüyle 903 ms): ek indeksler yazma maliyeti getirir, ama sonraki ayar turunda (aşağıda) 397 ms'ye indi. Tek çalıştırma olduğundan bu fark gürültüden ayrılamaz.

Eklenen indeksler (operasyon `migrations/00001_init.sql`): `sales (register_id, receipt_no) INCLUDE (paid_at)` (eski indeksin ilk sütunu `store_id` olduğundan fiş boşluğu sorgusu onu kullanamıyor ve 450 bin satırı diske taşan bir sıralamayla işliyordu: 335 → 209 ms, yalnızca indeks taraması), `shifts (register_id, opened_at)` ve `shifts (register_id, closed_at)` (kasa başına son vardiya sorgusu), `outbox (seq) WHERE published_at IS NULL` (yayınlanmayı bekleyenler; diğer outbox tablolarında zaten vardı).

> **Not (08.11.2026):** Bu bölümün tabloları kapsayıcı `sale_lines` anahtarı ve `random_page_cost = 1,1` öncesi ölçülmüştür ve yenilenmemiştir. Yalnızca 10 kasa, gerçek `storebench` ile art arda karşılaştırıldı: günlük özet 51 → 27 ms, kasa durumu 59 → 58 ms, aylık özet 957 → 1117 ms ve ürün sıralaması 958 → 1166 ms (30 günlük veride pencere tablonun tamamı olduğu için hafif yavaşlama beklenir), kayıt işleme gecikmesinde fark yok.

### Ayar değişiklikleri (pgtune) ve gerekçeleri

İlk tur ayarlarıyla (shared_buffers = 64 MB + her kasa için 8 MB, bellek sınırı shared_buffers + 144 MB) görülenler ve yapılanlar:

| Bulgu | Değişiklik | Sonuç |
|---|---|---|
| 10 ve 50 kasada container belleği tavana dayandı; sayfa önbelleğine yer kalmıyordu | Bellek sınırı = 2 × shared_buffers + 128 MB (64'ün katı); 50 kasada 448 → 640 MB | 50 kasa, kayıt işleme p95 680 → 295 ms, günlük özet 1,6 → 1,0 sn, kasa durumu 669 → 492 ms |
| `effective_cache_size` (3 × shared_buffers = 768 MB) container'dan büyüktü | Container belleği - 144 MB (50 kasada 496 MB) | Planlayıcıya gerçek önbellek söylenir |
| `work_mem` 4 MB, 50 kasada karma birleştirmeleri 4 parçaya bölüp ~100 MB diske taşıyordu | 4 kasaya kadar 4 MB, 16 kasaya kadar 8 MB, daha çoğunda 16 MB | 50 kasa: günlük özet 1,0 → 0,8 sn, aylık 9,0 → 7,5 sn, ürün sıralaması 10,8 → 8,5 sn. 10 kasada ölçülebilir fark yok |
| **Hata:** 50 kasada sorgu başına 2 paralel işçi açılınca "could not resize shared memory segment … No space left on device" (container'ın 64 MB /dev/shm'i); aylık ve ürün özetleri 19'ar kez hata verdi, kayıt işleme p95 680 → 1109 ms | Paralel sorgu kapatıldı (hiçbir kasa sayısında açılmaz) | Hata yok; kayıt işleme p95 397 ms |

50 kasada ayar varyantları (aynı veri ve yük):

| Varyant | Bellek | work_mem | Kayıt işleme p95 | Kasa durumu p50 | Günlük p50 | Aylık p50 | Ürün sıralaması p50 |
|---|---|---|---|---|---|---|---|
| İlk ayarlar | 448 MB | 4 MB | 680 ms | 669 ms | 1,6 sn | 11,6 sn | 10,6 sn |
| Yeni bellek, eski work_mem | 640 MB | 4 MB | 295 ms | 492 ms | 1,0 sn | 9,0 sn | 10,8 sn |
| Son ayarlar | 640 MB | 16 MB | 397 ms | 437 ms | 816 ms | 7,5 sn | 8,5 sn |

Değişen ayarlar yalnızca 10 kasadan sonra ya da bellek sınırında kendini gösterir; 1, 2, 5 ve 10 kasa son ayarlarla yeniden ölçüldü (yukarıdaki tablo). İlk tura göre 1–10 kasada fark ölçüm gürültüsü içindedir (örneğin 10 kasa: aylık özet 884 → 837 ms).

### Bulgular ve sınırlar

- **Özet sorguları veriyle doğrusal büyür:** aylık özet ve ürün sıralaması 1 kasada ~0,1 sn, 10 kasada ~0,9–1,0 sn, 50 kasada 7,5–8,5 sn. Her istekte 30 günlük satırları baştan topladıkları için indeks ya da ayar bunu kökten çözmez. 50 kasalı bir mağazada bu iki ekranın birkaç saniye sürmesi beklenir; çözüm sorgu tasarımındadır (önceden toplanmış günlük özet tabloları) ve bu çalışmada yapılmadı.
- Kasa durumu ekranı 50 kasada ~0,4–0,6 sn (5 sn'de bir yoklanır); 10 kasaya kadar 50 ms altındadır.
- Kayıt işleme hiçbir kasa sayısında geride kalmadı (kuyruk 50 kasada en çok 27 satış, kapasite ~60–79 satış/sn; yük 25 satış/sn). Uzun özet sorguları çalışırken kayıt gecikmesi yükselir (50 kasada p95 yaklaşık 0,4 sn).
- `memory.peak` (cgroup) önbelleği de içerir; veri belleğe sığmadığında her zaman sınıra dayanır. Bellek yetmezliği (OOM) hiçbir ölçümde olmadı; tablodaki "DB bellek en çok" `docker stats` değeridir.
- Ölçüm tek çalıştırmadır, geliştirici bilgisayarında ve Docker Desktop (WSL2) üzerindedir; istemci (araç) aynı makinede çalıştı, ağ gecikmesi ve disk gerçek mağaza sunucusundan farklıdır. Gerçek donanımda yeniden ölçülmelidir.
- Ölçülen kasa sayıları 1, 2, 5, 10 ve 50'dir; aradaki sayılar (3, 20, 25 ...) pgtune formülüyle ara değer alır, ayrıca ölçülmedi.
- Ölçülmeyenler: kasalardan NATS ile gelen gerçek akış, envanter ve catalog-admin yükü (aynı veritabanını kullanırlar), yedek (`pg_dump`) sırasındaki gecikme.

## Merkez veritabanı: mağaza sayısına göre (1, 2, 5, 10, 50 mağaza)

> Tarih: 06.11.2026. Operasyon ölçümüyle aynı yöntem; ölçek boyutu mağaza sayısıdır. **Geliştirici bilgisayarında, tek çalıştırma.**

### Yöntem

- Araç: `services/headquarters/ingest/cmd/headquartersbench` ve `scripts/bench-headquarters.sh`. Gerçek kod yolları: ingest'in `Store.Record`'u (olay + satış defteri + sayaçlar + makbuz + sıra numarası) ve reporting'in kullandığı satış defteri raporu (`pkg/ledger`) ile mağaza durumu sorgusu. Mağazalar ve NATS yoktur; olaylar doğrudan yazılır. Veritabanı boş bir `postgres:18-alpine` container'ıdır; ayarları `reporting pgtune --stores N` verir (`pkg/pgtune.ForHeadquarters`).
- **Veri mağaza sayısıyla katlanır:** mağaza başına 2 kasa × 30 gün × 300 satış = 18 000 satış (satış başına bir ham olay, 4 satır, ödeme, 2 KDV satırı, makbuz; yüzde biri iptal; günde bir vardiya). 1 mağaza: 18 bin satış / 49 MB; 50 mağaza: 900 bin satış / 2,1 GB.
- **Yük (60 sn):** her kasa saniyede 0,2 satış gönderir (50 mağazada 100 kasa = 20 satış/sn); olaylar tek işçide sırayla işlenir. Ekranlar (mağaza sayısı/5 + 1): her 10 sn'de mağaza durumu, ingest etkinliği, tüm mağazaların günlük özeti ve bir mağazanın günlük özeti; her 30 sn'de aylık özet ve ürün sıralaması.
- Bağlantı havuzu sınırlıdır (reporting 8, ingest 4, catalog-admin 4, envanter 4); `max_connections` 30.

### Sonuç (son ayarlar; yenilendi 08.11.2026)

> Bu tablo, `00002_report_index.sql` (kapsayıcı `sale_lines` anahtarı) ve `random_page_cost = 1,1` eklendikten sonra aynı yöntemle yeniden ölçüldü. Veri hâlâ 30 günlüktür; aylık pencere tablonun tamamını kapsar (bu yüzden aylık rakamlar indeksten kazanç görmez, aşağıdaki nota bakın).

| Mağaza | Kasa | Satış | Veritabanı | Mağaza durumu p50 | Günlük özet (hepsi) p50 | Bir mağazanın günlüğü p50 | Aylık özet p50 | Ürün sıralaması p50 | Kayıt işleme p50 / p95 | Kuyruk | DB CPU ort. | DB bellek en çok |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 2 | 18.000 | 54 MB | 3 ms | 13 ms | 17 ms | 190 ms | 130 ms | 18 ms / 21 ms | 1 | %11 | 99 MiB |
| 2 | 4 | 36.000 | 97 MB | 3 ms | 17 ms | 13 ms | 254 ms | 242 ms | 19 ms / 44 ms | 1 | %19 | 128 MiB |
| 5 | 10 | 90.000 | 229 MB | 4 ms | 31 ms | 14 ms | 750 ms | 754 ms | 19 ms / 50 ms | 1 | %38 | 262 MiB |
| 10 | 20 | 180.000 | 450 MB | 5 ms | 54 ms | 14 ms | 1,7 sn | 1,6 sn | 19 ms / 46 ms | 2 | %61 | 380 MiB |
| 50 | 100 | 900.000 | 2,3 GB | 68 ms | 953 ms | 112 ms | 16,4 sn | 18,7 sn | 142 ms / 1,2 sn | 32 | %210 | 1612 MiB |

Hata: bu beş ölçümde toplam **0**. Kayıt işleme kapasitesi (tek işçi, kuyruksuz): 1 mağaza 60/sn, 2 mağaza 52/sn, 5 mağaza 61/sn, 10 mağaza 61/sn, 50 mağaza 44/sn. En çok kullanılan bağlantı: 1 mağaza 3, 2 mağaza 4, 5 mağaza 6, 10 mağaza 7, 50 mağaza 11 (sınır 30).

**Önceki (05–06.11.2026) tabloyla karşılaştırma uyarısı.** Yeni rakamlar eskisinden bazı yerlerde yüksektir (örn. günlük özet, 10 mağaza: 18 → 54 ms; 50 mağaza: 79 → 953 ms). Bu, değişikliğin yavaşlatması değildir: aynı gün, aynı makinede, eski kodla (indekssiz, `random_page_cost` 4) ve yeni kodla art arda ölçüldü ve **eski kod bugün de yüksek çıktı**:

| Ölçüm (aynı makine, art arda) | Günlük (hepsi) p50 | Bir mağazanın günlüğü p50 | Aylık p50 | Ürün sıralaması p50 | Kayıt işleme p95 |
|---|---|---|---|---|---|
| 10 mağaza, eski, tur 1 / 2 | 89 / 90 ms | 15 / 14 ms | 1,6 / 2,0 sn | 1,6 / 1,8 sn | 51 / 53 ms |
| 10 mağaza, **yeni**, tur 1 / 2 | 59 / 60 ms | 16 / 14 ms | 2,0 / 1,8 sn | 1,7 / 1,6 sn | 48 / 49 ms |
| 50 mağaza, eski | 1231 ms | 110 ms | 16,3 sn | 18,9 sn | 2,1 sn |
| 50 mağaza, **yeni** (iki ayrı ölçüm) | 933 / 953 ms | 63 / 112 ms | 15,7 / 16,4 sn | 17,2 / 18,7 sn | 1,2 sn / 1,2 sn |

Yani günlük özet yeni hâlde eskiden hızlı ya da aynı; aylık ve ürün sıralaması ölçüm gürültüsü içinde aynı (bu veride pencere tablonun tamamı; kazanç, ay verinin küçük bir kısmı olunca görülür, bkz. "Aylık rapor için indeks çalışması"). Eski tablodaki düşük günlük rakamların nedenini doğrulayamadım; olası neden, ölçüm aracının "bugün" penceresinin ölçümün yapıldığı saate göre farklı sayıda satır kapsamasıdır (aynı gün art arda yapılan karşılaştırmalar bundan etkilenmez).

### Ayar değişikliği: merkez için ayrı formül (`pgtune.ForHeadquarters`)

İlk tur operasyon formülüyle (`ForStore`; tampon en çok 256 MB, bellek en çok 640 MB) alındı. Merkezde veritabanı mağaza sayısıyla büyür (50 mağazada 2 GB) ve bu bellekte çalışma kümesi sığmadı:

| Mağaza | Ayar | Bellek | shared_buffers | work_mem | Mağaza durumu p50 / p95 | Günlük (hepsi) p50 | Aylık p50 | Ürün sıralaması p50 | Kayıt işleme p95 | Kuyruk |
|---|---|---|---|---|---|---|---|---|---|---|
| 10 | ilk (operasyon formülü) | 512 MB | 192 MB | 16 MB | 6 ms / 19 ms | 207 ms | 1,8 sn | 1,7 sn | 40 ms | 2 |
| 10 | büyük bellek | 768 MB | 256 MB | 16 MB | 3 ms / 15 ms | 11 ms | 1,7 sn | 1,6 sn | 30 ms | 1 |
| 10 | son (ForHeadquarters) | 512 MB | 192 MB | 16 MB | 4 ms / 19 ms | 18 ms | 1,7 sn | 1,6 sn | 34 ms | 1 |
| 50 | ilk (operasyon formülü) | 640 MB | 256 MB | 16 MB | 27 ms / 19,2 sn | 40 ms | 27,6 sn | bitmedi | 2,5 sn | 62 |
| 50 | büyük bellek | 2048 MB | 512 MB | 32 MB | 44 ms / 7,1 sn | 110 ms | 15,2 sn | 16,4 sn | 745 ms | 23 |
| 50 | son (ForHeadquarters) | 1792 MB | 832 MB | 32 MB | 35 ms / 7,8 sn | 79 ms | 16,9 sn | 16,8 sn | 1,0 sn | 29 |
Gerekçe ve okuma:

- **50 mağazada** ilk ayar yetmedi: 2 GB veri 640 MB bellekte çalışma kümesini tutamadı; aylık özet 27,6 sn sürdü, ürün sıralaması 60 sn'lik yük süresinde hiç bitmedi, mağaza durumu sorgusu p95 19 sn'ye çıktı, kayıt işleme gecikmesi p95 2,5 sn ve kuyruk 62 olayı buldu. Bellek sınırı 2 × shared_buffers + 128 MB, tampon 1 GB'a kadar (kasa başına 8 MB), work_mem 40 kasadan sonra 32 MB olunca: aylık özet 16,9 sn, ürün sıralaması 16,8 sn (tamamlanıyor), kayıt işleme p95 1,0 sn, kuyruk 29.
- **10 mağazada** ilk ve son ayar neredeyse aynıdır (192 MB tampon, 512 MB, work_mem 16 MB; yalnızca `max_connections` 20 → 30 ve `effective_cache_size`). Günlük özetteki 207 → 18 ms fark ayardan değil, koşular arası farktan (soğuk önbellek) gelir; aynı veriyle büyük bellek varyantı 11 ms verdi. Bu yüzden 10 mağaza için ayar değişikliği kazanç olarak yazılmaz.
- "Büyük bellek" varyantı (50 mağaza, 2048 MB, 512 MB tampon) son ayarla aynı düzeydedir (aylık 15,2 sn, kayıt işleme p95 0,75 sn); formül ikisinin arasında kalır.

> **Not (08.11.2026):** Bu tablo 05–06.11.2026'da, kapsayıcı `sale_lines` anahtarı ve `random_page_cost = 1,1` öncesinde ölçülmüştür; "son" satırının güncel ölçümü yukarıdaki "Sonuç (son ayarlar; yenilendi 08.11.2026)" tablosundadır. Satırların birbirine göre sırası geçerlidir, mutlak değerler farklı gün/makine durumunda ölçüldüğü için güncel tabloyla birebir karşılaştırılmamalıdır.

### Bulgular ve sınırlar

- **Aylık özet ve ürün sıralaması veriyle doğrusal büyür** (1 mağazada ~0,13 sn, 10 mağazada 1,6–1,7 sn, 50 mağazada ~17 sn). Her istekte 30 günlük tüm satırları toplarlar; ayar bunu kökten çözmez. Çözüm sorgu tasarımındadır (önceden toplanmış günlük özet tabloları) ve bu çalışmada yapılmadı.
- Mağaza durumu ve tek mağazanın günlük özeti 50 mağazada bile 20–35 ms'dir; asıl yük merkezin tüm mağazaları kapsayan dönem özetleridir.
- Kayıt işleme 50 mağazada (100 kasa, 20 satış/sn) geride kalmadı; uzun özet sorguları sürerken gecikme p95 ~1 sn'ye çıkar. Tek işçinin kapasitesi 49 satış/sn; bunun üstüne çıkılırsa tüketici paralelleştirilmeli.
- Merkezin veritabanı sınırsız büyür (kasa başına yılda ~110 bin satış). 50 mağazada bir yılın verisi ~25 GB olur; bu ölçüm yalnızca son 30 günü kapsar. Eski verinin arşivlenmesi/bölümlenmesi ayrı bir konudur.
- Ölçülen mağaza başına kasa sayısı 2'dir; çok kasalı mağazalarda veri orantılı büyür. Merkez ayarı toplam kasa sayısına (mağaza × kasa) göre hesaplanır; kasa sayısı bilinmiyorsa mağaza başına 2 varsayılır.
- Ölçülmeyenler: gerçek NATS akışı, catalog-admin ve envanter yükü, yedek sırasındaki gecikme, mağaza başına 2'den çok kasa. Tek çalıştırma, geliştirici bilgisayarı.

### Merkez ayar tablosu (`pgtune.ForHeadquarters`; mağaza × 2 kasa)

| Mağaza | Container belleği | shared_buffers | work_mem | effective_cache_size | max_wal_size | maintenance_work_mem | max_connections |
|---|---|---|---|---|---|---|---|
| 1 | 256 MB | 64 MB | 4 MB | 88 MB | 256 MB | 16 MB | 30 |
| 2 | 256 MB | 64 MB | 4 MB | 88 MB | 256 MB | 16 MB | 30 |
| 5 | 384 MB | 112 MB | 8 MB | 216 MB | 256 MB | 28 MB | 30 |
| 10 | 512 MB | 192 MB | 16 MB | 344 MB | 320 MB | 48 MB | 30 |
| 25 | 1024 MB | 432 MB | 32 MB | 856 MB | 800 MB | 108 MB | 30 |
| 50 | 1792 MB | 832 MB | 32 MB | 1624 MB | 1600 MB | 128 MB | 30 |
| 100 | 2176 MB | 1024 MB | 32 MB | 2008 MB | 3200 MB | 128 MB | 30 |

Ölçülenler 1, 2, 5, 10 ve 50 mağazadır; 25 ve 100 formülün ara/üst değeridir. Paralel sorgu kapalıdır (operasyon ölçümündeki `/dev/shm` hatası nedeniyle).


## Aylık rapor için indeks çalışması (07–08.11.2026)

> Geliştirici bilgisayarında, tek çalıştırma; sayılar sıralama ve oran için güvenilirdir, mağaza donanımı için garanti değildir.

Önceki ölçümler aylık özetin "veriyle doğrusal büyüdüğünü" gösteriyordu, ama ölçüm verisi yalnızca 30 günlüktü: aylık pencere tablonun tamamını kapsıyordu ve indeks bir şey değiştiremezdi. Gerçek bir mağazada veri yıllarca birikir ve ay, verinin küçük bir kısmıdır. Bu çalışma o durumu ölçer.

### Yöntem

- **Veri:** 1 mağaza, 10 kasa, 395 gün, kasa başına günde 300 satış = **1.185.000 satış** (4,74 milyon satır, 1,19 milyon ödeme, 2,37 milyon KDV satırı, 11.850 iptal), zaman sırasıyla eklenmiş. Bir ayın dilimi ~90 bin satış. `postgres:18-alpine`, `pgtune --registers 10` ayarı (384 MB container, `shared_buffers` 112 MB).
- **Sorgular:** `pkg/ledger/ledgerdb` içindeki `Report`'un beş sorgusu (satışlar, iptaller, ödemeler, KDV, ürün sıralaması). İki pencere: son 30 gün ve 6 ay önceki 30 gün. Her değer 3 çalıştırmanın medyanı (`EXPLAIN (ANALYZE, BUFFERS)` yürütme süresi), milisaniye.
- Her varyant aynı veri üstünde, yalnızca ilgili indeks/ayar değiştirilerek ölçüldü.

### Sonuç (son 30 gün, ms)

| Varyant | Satışlar | Ödemeler | KDV | Ürün sıralaması | Toplam | 6 ay önceki ay (toplam) |
|---|---|---|---|---|---|---|
| Temel (mevcut indeksler, `random_page_cost` 4) | 313 | 352 | 709 | 2809 | **4184** | 3572 |
| `random_page_cost` 1,1 | 166 | 332 | 600 | 1770 | 2868 | 2929 |
| `sales (paid_at)` kapsayıcı indeks | 295 | 320 | 553 | 1941 | 3109 | 3187 |
| Üç alt tablonun anahtarı kapsayıcı | 311 | 381 | 592 | 2755 | 4039 | 3218 |
| Üç alt tablo kapsayıcı + `random_page_cost` 1,1 | 158 | 343 | 560 | 578 | **1640** | 1750 |
| Aynısı, iç içe döngü zorlanmış (`enable_hashjoin=off`) | 159 | 241 | 266 | 479 | 1144 | 1147 |
| Alt tablolara `paid_at` kopyası + tarih indeksi | 285 | 16 | 36 | 390 | 728 | 731 |

Yalnızca `sale_lines` anahtarının kapsayıcı olduğu, taze veride doğrulama (eklenen hâl): temel 3391 ms (6 ay önce 3385), yalnızca ayar 3211 (2850), anahtar adsız 3115 (2950; ürün sıralaması 1860), anahtar adlı **1820 ms (1871); ürün sıralaması 685 ms**.

### Ne yapıldı

1. **`sale_lines` anahtarı kapsayıcı yapıldı** (`00002_report_index.sql`, mağaza ve merkez `ingest`): `PRIMARY KEY (store_id, sale_id, line_no) INCLUDE (product_id, name, qty, total)`. Ürün sıralaması 1996 → 685 ms (2,9 kat), toplam ~1,9 kat. Satır sayısı en büyük tablo olduğu için planlayıcı ancak bu tabloda tüm tabloyu taramak yerine indeksten okumayı seçiyor.
2. **`random_page_cost = 1,1`** (`pgtune` ve dört dağıtım dosyası: iki compose, iki Quadlet). Varsayılan 4,0 dönen disk varsayar; tek başına 4,2 → 2,9 sn'ye indiriyor, ama kapsayıcı anahtarın kullanılması için de gerekli (ayar olmadan kapsayıcı anahtar hiçbir şeyi değiştirmedi: 4039 ms). **Mağaza makinesi SSD/NVMe değilse bu ayar geri alınmalıdır.**
3. **Satır adı 512 bayta kısaltılıyor** (`ledgerdb.MaxLineNameBytes`). Kapsayıcı anahtar adı da taşır ve PostgreSQL, sayfanın üçte birinden büyük bir indeks girdisini kabul etmez (~2,7 KB): ölçümde 3200 karakterlik sıkıştırılamaz bir ad satış kaydını **hata ile** reddetti. Üründe ad uzunluğu sınırı olmadığı için kayıt anında defterdeki kopya kısaltılır; kasadaki fiş tam adı korur.

### Maliyet

- **Boyut:** 1,19 milyon satışta `sale_lines` indeksi 229 → 387 MB (+158 MB); veritabanı yaklaşık %11 büyür.
- **Yazma:** 100 bin satış + satırları toplu eklemede 19,3 → 20,4 sn (+%6); `storebench` kayıt işleme gecikmesinde fark ölçülmedi (p50 17,6 → 13,8 ms, ölçüm gürültüsü içinde).
- **Pencere tablonun tamamıysa hafif yavaşlama:** 30 günlük `storebench` verisinde (aylık pencere = tüm tablo) aylık özet 957 → 1117 ms, ürün sıralaması 958 → 1166 ms (+%17–22). Bu, bir mağazanın ilk ayıdır; ikinci aydan sonra pencere verinin yarısından azdır ve kazanç başlar.
- İndeks-yalnız taramalar görünürlük haritasına dayanır; toplu yüklemeden sonra `VACUUM` önerilir (günlük işte autovacuum yeter).

### Yapılmayanlar ve nedenleri

- **`sales (paid_at)` kapsayıcı indeks:** kazanç yok. Satışlar sorgusunun maliyeti, 15 dakikalık dilim × kasa × kasiyer kırılımının ~86 bin grubunu sıralamasındadır (diske taşar); indeks bunu değiştirmez.
- **Ödemeler ve KDV anahtarlarını kapsayıcı yapmak:** kazanç yok. Bu tablolar küçük olduğundan planlayıcı karma birleştirme + tam tarama seçiyor (iç içe döngü zorlanınca 352 → 241 ve 709 → 266 ms, yani toplam ~0,55 sn kazanç var), ama bunu ayar ya da sorgu yeniden yazımı olmadan sağlamak mümkün olmadı.
- **Alt tablolara `paid_at` kopyası** en iyi sonucu verir (toplam 728 ms; ödemeler 16, KDV 36 ms), ama yazma yolunu (`SaveSale`), iki şemayı ve geçmiş veri için bir doldurma `UPDATE`'ini değiştirir: 1,19 milyon satışta doldurma ~6 dakika sürdü ve tabloları iki katına şişirdi. Ayrı bir karar olarak bırakıldı.
- **50 kasa / çok yıllık veri ölçülmedi:** bu çalışma 10 kasa ve 13 ay ile sınırlıdır; büyüme doğrusal beklenir.
