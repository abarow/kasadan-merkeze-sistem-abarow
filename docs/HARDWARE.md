# abarow: donanım ihtiyacı ve ölçümler

Bu belge README'deki özetin ayrıntısıdır. Ölçümler geliştirici bilgisayarında (Docker Desktop, 16 CPU, 7,9 GB), tek çalıştırmayla alındı; mağaza donanımı için garanti değildir. Yöntem ve ham sonuçlar: [PERFORMANCE.md](PERFORMANCE.md).

## Hız ve ölçeklenme
Bir satış (sepet aç, 3 ürün okut, sepeti kapat, nakit öde: 5 çağrı) hafif yükte **p50 119 ms**, yukarıdaki ağır yükte p50 211 ms / p95 325 ms sürdü. 682 satış ve ≈113 bin okuma isteğinde **hata 0**.

Mağazadaki operasyon, kasa sayısı büyüdükçe (her kasada 30 günlük, kasa başına 9 bin satışlık veriyle):

| Kasa | Satış sayısı | Kasa durumu ekranı | Günlük rapor | Aylık rapor | Kayıt işleme (p50 / p95) | Veritabanı |
|---|---|---|---|---|---|---|
| 1 | 9.000 | **9 ms** | 10 ms | 107 ms | 14 / 20 ms | 26 MB |
| 2 | 18.000 | 15 ms | 16 ms | 216 ms | 14 / 22 ms | 42 MB |
| 5 | 45.000 | 29 ms | 40 ms | 0,4 sn | 14 / 18 ms | 91 MB |
| 10 | 90.000 | 52 ms | 79 ms | 0,8 sn | 14 / 29 ms | 172 MB |
| 50 | 450.000 | 0,4 sn | **0,8 sn** | 7,5 sn | 31 / 397 ms | 830 MB |

Her kasa saniyede 0,5 satış göndererek (gerçek yoğun saatin 5–15 katı; 50 kasada 25 satış/sn) ölçüldü. Kayıtlar hiçbir kasa sayısında geride kalmadı. Merkezde (merkez) mağaza sayısıyla (mağaza başına 2 kasa):

| Mağaza | Satış sayısı | Mağaza durumu | Günlük rapor (hepsi) | Aylık rapor | Kayıt işleme (p50 / p95) | Veritabanı |
|---|---|---|---|---|---|---|
| 1 | 18.000 | 3 ms | 8 ms | 133 ms | 21 / 25 ms | 49 MB |
| 2 | 36.000 | 3 ms | 9 ms | 256 ms | 18 / 34 ms | 88 MB |
| 5 | 90.000 | 3 ms | 17 ms | 0,8 sn | 18 / 28 ms | 206 MB |
| 10 | 180.000 | 4 ms | 18 ms | 1,7 sn | 18 / 34 ms | 404 MB |
| 50 | 900.000 | 35 ms | 79 ms | 16,9 sn | 86 ms / 1,0 sn | 2,0 GB |

Aylık raporlar her istekte 30 günün tüm satırını topladığı için veriyle doğrusal büyür; kasa durumu ve tek mağazanın günlük özeti ise 50 mağazada bile 20–35 ms'dir. Bunu hızlandırmak için önceden toplanmış özet tabloları gerekir (henüz yapılmadı).

## Veritabanı ayarı kasa sayısına göre kendiliğinden yapılır

Kasa eklenince ya da çıkarılınca kurulum betiği PostgreSQL ayarlarını yeniden hesaplar (`report pgtune`, `reporting pgtune`). 4 kasaya kadar `shared_buffers` 64 MB, `max_connections` 20, `work_mem` 4 MB'tır; daha büyük mağazada büyür:

| Kasa (operasyon) | Container belleği | shared_buffers | work_mem |
|---|---|---|---|
| 1–4 | 256 MB | 64 MB | 4 MB |
| 10 | 576 MB | 112 MB | 8 MB |
| 30 ve üstü | 1024 MB | 256 MB | 16 MB |

10 kasadan itibaren dönem raporları paralel sorgu kullanır (10–49 kasa 2 işçi, 50 ve üstü 4 işçi); container belleğine `/dev/shm` payı (24 × `work_mem`) eklenmiştir.

Merkez için ayrı formül vardır (veri mağaza sayısıyla büyür): 10 mağazada 896 MB / 192 MB, 50 mağazada 2560 MB / 832 MB. Ölçümde bu ayarlar 50 kasada kayıt işleme gecikmesini 680 → 397 ms'ye, 50 mağazada 2,5 sn → 1,0 sn'ye indirdi. Tüm tablolar: [docs/PERFORMANCE.md](PERFORMANCE.md).

## Donanım ihtiyacı

Aşağıdaki sayılar abarow'un **kendi servislerinin** (container'ların) ihtiyacıdır; işletim sistemi ve kasadaki Chromium kiosk ayrıca gerekir (ikisi ölçülmedi; kasa için hedeflenen 4 GB bu yüzden geniştir).

- **Asgari:** ölçülen en yüksek bellek kullanımı (yük altında) + %40 pay, 64 MB'ın katına yuvarlanır. Altına inilirse bellek baskısı yaşanır.
- **Optimum:** container bellek tavanlarının toplamı; hiçbir servis sınırına dayansa bile makinede yer kalır. Veritabanı için `pgtune`'un hesapladığı bellek kullanılır.
- **CPU:** çekirdek sayısıdır. Ölçümler x86-64 geliştirici bilgisayarında alındı; düşük güçlü ARM64 çekirdekte süreler uzar (ölçülmedi). Asgari, ölçülen yükün çok altındaki gerçek kasa kullanımına göre; optimum ise yoğun saatte rahat çalışması içindir.

**Rol başına ihtiyaç**

| Rol | Bellek asgari | Bellek optimum | CPU asgari | CPU optimum |
|---|---|---|---|---|
| Kasa (9 container) | **192 MB** | **544 MB** | 1 | 2 |
| Operasyon, 4 kasaya kadar | 384 MB | 544 MB | 1 | 2 |
| Operasyon, 5 kasa | 448 MB | 608 MB | 1 | 2 |
| Operasyon, 50 kasa | 1152 MB | 1312 MB | 4 | 8 |

(Kasa: ölçülen en yüksek ≈137 MiB. Operasyon: veritabanı + diğer servislerin ≈110 MB zirvesi; optimumda NATS 128, report 64, envanter 48, catalog-admin 48 MB tavanı. Diskte veritabanı kasa başına ayda ≈17 MB, yılda ≈200 MB büyür.)

### Senaryo 1: tek makinede 1 kasa + operasyon

Küçük mağaza; kasa ve mağaza sunucusu aynı makinede.

| | Bellek asgari | Bellek optimum | CPU asgari | CPU optimum |
|---|---|---|---|---|
| Kasa servisleri | 192 MB | 544 MB | 1 | 2 |
| Operasyon (≤4 kasa) | 384 MB | 544 MB | 1 | 2 |
| **Makine toplamı** | **576 MB** | **1088 MB** | **2** | **4** |

### Senaryo 2: 4 kasa ayrı makinede + 1 kasa operasyon ile aynı makinede

Toplam 5 kasa.

| Makine | Adet | Bellek asgari | Bellek optimum | CPU asgari | CPU optimum |
|---|---|---|---|---|---|
| Yalnız kasa | 4 (her biri) | **192 MB** | **544 MB** | 1 | 2 |
| Operasyon (5 kasa) + 1 kasa | 1 | 640 MB | 1152 MB | 2 | 4 |
| **Mağaza toplamı** | 5 makine | **1408 MB** | **3328 MB** | 6 | 12 |

Operasyon makinesi: operasyon 448 / 608 MB + kasa 192 / 544 MB.

### Senaryo 3: ayrı bir operasyon makinesi + 50 kasa

| Makine | Adet | Bellek asgari | Bellek optimum | CPU asgari | CPU optimum |
|---|---|---|---|---|---|
| Yalnız kasa | 50 (her biri) | **192 MB** | **544 MB** | 1 | 2 |
| Operasyon (yalnız) | 1 | 1152 MB | 1312 MB | 4 | 8 |

Operasyon makinesi disk: veritabanı 50 kasada ≈830 MB / 30 gün, yılda ≈10 GB. Ölçülmeyenler ve riskler: operasyon NATS'ının 50 leaf bağlantısındaki belleği ve CPU'su (tavan 128 MB; kasa sayısı arttıkça izlenmeli), operasyondaki catalog-admin örneğinin kendi ölçümü (merkezdeki örneğe göre ≈10 MB varsayıldı).
