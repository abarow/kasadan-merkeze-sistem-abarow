# Tanıtım sayfası: uygulama planı

> Durum: plan (14.11.2026), sayfa henüz yazılmadı. Bu belge sayfayı yazacak kişi ya da model içindir; başka bir belge okumadan işi bitirebilecek kadar ayrıntılıdır. Sayfa bitince bu dosya silinir ya da "yapıldı" notuyla kısaltılır.

## 1. Amaç ve okur

- **Amaç:** abarow'u yazılım ve yazılım mimarisinden anlayan kişilere tanıtmak. Sayfa üç şeyi gösterir: mimari, veri akışı (hareketli çizimlerle) ve ölçülmüş performans ile kaynak kullanımı.
- **Okur:** yazılım mühendisi, mimar, teknik karar verici. Go, container, mesaj kuyruğu ve PostgreSQL kavramlarını bilir. POS alanını ve abarow'un iç adlarını bilmez.
- **Ton:** teknik ve sade bir açık kaynak proje sayfası. Satış broşürü değil.
- **Başarı ölçütü:** okur sayfayı bitirdiğinde şu soruları yanıtlayabilmeli:
  1. Bir satış kasadan merkeze hangi yoldan gider, bağlantı kesilince ne olur?
  2. Aynı satış neden iki kez sayılmaz, neden kaybolmaz?
  3. Katmanlar birbirine nasıl güvenir?
  4. Bir kasa ne kadar bellek ister, 50 mağazalı merkezde rapor kaç saniye sürer?

## 2. Çıktı ve teknik kurallar

- **Dosya:** yalnız `docs/site/index.html`. Başka dosya (resim, CSS, JS) yok; hepsi dosyanın içinde.
- **Bağımlılık yok:** dış kütüphane, CDN, web fontu, analitik ya da dış istek yok. Sayfa çift tıklayınca (`file://`) ve internetsiz açılır.
- **Dil:** görünen bütün metin Türkçe. Kod, `id`, `class`, değişken ve yorumlar İngilizce.
- **Başlık yorumu:** dosyanın ilk satırlarında HTML yorumu olarak telif başlığı:
  `Copyright 2026 Mehmet ERGULCU and the abarow contributors` ve `SPDX-License-Identifier: Apache-2.0`. `scripts/check-headers.sh` `.html` dosyalarını denetliyorsa geçmeli; denetlemiyorsa betiğe dokunma.
- **Çizimler:** satır içi SVG. Canvas yalnız gerekirse. Çizimlerdeki yazılar gerçek metin olur (`<text>`), resim olmaz.
- **Hareket:** CSS animasyonu ve küçük, bağımsız JavaScript. `prefers-reduced-motion: reduce` ayarında animasyonlar durur ve çizim son durumunu gösterir. Her hareketli çizimde "durdur / sürdür" düğmesi bulunur. Görünür alanın dışındaki çizim çalışmaz (`IntersectionObserver`).
- **Tema:** açık ve koyu. Renkler `:root` üzerinde CSS değişkenleri; `prefers-color-scheme` ile koyu tema; sağ üstte elle değiştirme düğmesi (seçim `localStorage`'da, `try/catch` içinde).
- **Düzen:** 360 piksel genişlikte de okunur. Yatay kaydırma yok; geniş tablolar kendi kutusunda kayar. Büyük çizimler dar ekranda dikey düzene geçer ya da ölçeklenir.
- **Erişilebilirlik:** başlık sırası düzgün (`h1` bir tane), her SVG'de `role="img"` ve `<title>`/`<desc>`, düğmeler klavyeyle kullanılır, renk tek başına bilgi taşımaz (akış yönü okla, durum yazıyla da belirtilir), kontrast en az 4.5:1.
- **Boyut:** dosya 300 KB'ı geçmemeli.
- **Yayın yok:** GitHub Pages, alan adı ya da başka yayın adımı bu işin parçası değil. Proje sahibi yapacak.
- **Depo kuralları:** artifact açma; `README.md` ve `docs/images/` proje sahibinindir, dokunma; commit ve push kuralları hafızadaki kurallardır (hafta sonu tarihi, `birlesik` → `master`, force-push yok).

## 3. İçerik kuralları (hukuki)

- **Yalnız ölçülmüş sayı:** bu plandaki tablolardaki sayıları kullan. Yeni sayı uydurma, yuvarlarken anlamı değiştirme. Bir sayıdan emin değilsen kaynağına bak: `docs/PERFORMANCE.md`, `docs/HARDWARE.md`.
- **Ölçüm notu:** her grafik ve sayı kutusunun altında şu anlamda bir not durur: "Geliştirici bilgisayarında (Docker Desktop, 16 mantıksal CPU) ölçüldü. Mağaza donanımının değerini vermez, garanti değildir."
- **Vaat yok:** "en hızlı", "kesintisiz", "kayıpsız", "güvenli", "%100" gibi garanti anlamına gelen ifade kullanma. Ne yapıldığını ve nasıl yapıldığını anlat, sonucu vaat etme. Örnek: "kaybolmaz" yerine "üst katman veritabanına işlediğini makbuzla bildirmeden silinmez".
- **Simülatör uyarısı:** mali yazıcı (ÖKC) bugün simülatördür ve mali değeri yoktur; gerçek satışta tek başına kullanılmaz. Bu, "Sınırlar" bölümünde açıkça yazar.
- **Denenmemişleri gizleme:** "Sınırlar" bölümü plandaki maddelerin hepsini içerir.
- **Adlar:** mağaza katmanına "mağaza sunucusu" de (belgelerde "operasyon" da geçer; sayfada tek ad kullan). Kişi adı olarak yalnız telif satırındaki ad geçer.

## 4. Görsel dil

- **Renkler (öneri; kontrastı ölç):**
  - katmanlar: kasa turuncu/kehribar, mağaza sunucusu camgöbeği, merkez mor;
  - akışlar: yukarı giden olay (satış, vardiya) yeşil nokta, aşağı inen veri (katalog, fiyat, kasiyer) mavi nokta, makbuz ince kesikli gri nokta;
  - durum: kesik bağlantı kırmızı kesikli çizgi ve "bağlantı yok" yazısı, bekleyen olay sayacı kehribar.
- **Yazı:** sistem fontları (`system-ui`, kod için `ui-monospace`). Gövde 16–18 px, satır aralığı 1.6, metin sütunu en çok 72 karakter.
- **Bileşenler:** sayı kutusu (büyük sayı, birim, kısa açıklama), kart, etiket, kod parçası, adımlı çizim (numaralı adımlar ve altında açıklama), çubuk grafik, çizgi grafik.
- **Grafikler:** elle yazılmış SVG. Eksen etiketleri ve birimler yazılı; değerler çubukların üstünde ya da yanında okunur; logaritmik eksen kullanırsan açıkça yaz. Her grafiğin altında aynı veriyi veren, açılır kapanır bir tablo (`<details>`) bulunur.
- **Simge yok:** emoji ve dekoratif simge kullanma.

## 5. Sayfa yapısı

Üstte yapışkan, ince bir gezinme çubuğu: bölüm bağlantıları ve tema düğmesi. Bölümler sırayla:

### 5.1 Açılış

- **Başlık:** "abarow" ve tek cümle: Türkiye'deki perakende ve restoran için Linux üzerinde çalışan, açık kaynak (Apache-2.0) bir satış noktası sistemi; kasa, mağaza sunucusu ve merkez olmak üzere üç katmanda, her katman kendi başına çalışabilecek şekilde tasarlandı.
- **Dört sayı kutusu:**
  - "≈95 MiB": bir kasanın 9 servisinin yük altındaki belleği (boşta ≈68 MiB);
  - "119 ms": bir satışın tamamı (5 çağrı), hafif yükte p50;
  - "0 hata": 682 satış ve ≈113 bin okuma isteğinde;
  - "2 satış": gerçekçi yükte merkezde bekleyen en uzun kuyruk (50 mağaza, 100 kasa).
- Altında ölçüm notu ve "nasıl ölçüldü" bağlantısı (5.7'ye).

### 5.2 Üç katman: canlı akış (ana görsel)

Sayfanın en önemli çizimi. Tam genişlikte bir SVG.

- **Düzen:** solda iki kasa, ortada mağaza sunucusu, sağda merkez. Dar ekranda yukarıdan aşağıya.
- **Her kutunun içinde görünenler:**
  - kasa: "PostgreSQL" ve "NATS JetStream" küçük kutuları, "outbox" etiketi, bekleyen olay sayacı;
  - mağaza sunucusu: "NATS JetStream (mağaza akışı)", "PostgreSQL", "rapor ekranı", bekleyen olay sayacı;
  - merkez: "NATS", "ingest", "PostgreSQL", "rapor ekranı", işlenen satış sayacı.
- **Akışlar:**
  1. Kasada bir satış oluşur (kasa kutusu kısa parlar), yeşil nokta kasanın NATS'ından mağaza sunucusuna, oradan merkeze gider. Merkezde sayaç artar.
  2. Merkezden mavi noktalar (katalog, fiyat) mağaza sunucusuna ve kasalara iner; seyrek.
  3. Merkez olayı veritabanına işleyince gri kesikli bir makbuz noktası geri iner: merkez → mağaza sunucusu. Aynı şekilde mağaza sunucusu → kasa.
- **Hız:** gerçek zamanlı değil. Kasa başına yaklaşık 2 saniyede bir satış gösterilir; çizimin altında "gösterim hızlandırılmıştır; ölçümlerde kasa başına 20 saniyede 1 satış kullanıldı" yazar.
- **Bağlantı etiketleri (çizgilerin üstünde):** kasa ↔ mağaza sunucusu: "NATS leaf, TLS, NKey"; mağaza sunucusu ↔ merkez: "NATS leaf, TLS, NKey, izin listesi".
- **Altında üç kısa madde:** her katmanın kendi PostgreSQL'i ve kendi NATS'ı vardır; olaylar yukarı, katalog ve makbuz aşağı akar; katmanlar birbirini beklemez.

### 5.3 Bağlantı kesilince

5.2'deki çizimin aynısı, iki düğmeyle: "mağaza sunucusu bağlantısını kes" ve "merkez (internet) bağlantısını kes". İkisi de aç/kapa çalışır.

- **Mağaza sunucusu bağlantısı kesikken:** kasa ↔ mağaza sunucusu çizgisi kırmızı kesikli olur. Kasalar satmaya devam eder; yeşil noktalar kasanın içinde durur ve kasanın sayacı artar. Mağaza sunucusunun rapor ekranı "kasalardan veri gelmiyor" durumunu gösterir.
- **Merkez bağlantısı kesikken:** mağaza sunucusu ↔ merkez çizgisi kırmızı kesikli olur. Mağaza normal çalışır; olaylar mağaza sunucusunda birikir, onun sayacı artar.
- **Bağlantı gelince:** biriken noktalar sırayla, hızlıca akar; sayaç sıfıra iner; ardından makbuzlar geri iner.
- **Yanında küçük tablo ("ne çökerse ne olur"):**

  | Ne çökerse | Ne olur |
  |---|---|
  | İnternet | Mağaza, raporlar dahil normal çalışır; olaylar mağaza sunucusunda birikir. |
  | Mağaza sunucusu | Market kasaları satmaya devam eder, olaylar kasalarda birikir; mağaza raporları durur. Restoranda masa paylaşımı durur. |
  | Bir kasa | Diğer kasalar etkilenmez; o kasanın bekleyen olayları kasa açılınca gönderilir. |

- **Altında "neden kaybolmaz, neden iki kez sayılmaz" açıklaması (adımlı küçük çizimle):**
  1. **Outbox:** servis satışı ve yayınlayacağı olayı aynı veritabanı işleminde yazar. "Satış kaydedildi ama olay gitmedi" durumu oluşmaz.
  2. **Relay:** ayrı bir döngü outbox'ı yazılış sırasıyla yerel JetStream'e yayınlar (500 ms aralıkla). NATS yoksa olay outbox'ta bekler.
  3. **Tekrar ayıklama:** her olayın benzersiz kimliği vardır. JetStream kimliği 10 dakikalık pencerede, alıcılar veritabanında ayıklar; aynı olay iki kez gelse bir kez işlenir.
  4. **Teslim makbuzu:** üst katman olayı kendi PostgreSQL'ine işleyince bir makbuz (UUIDv7) üretir ve aşağı gönderir. Alt katman, makbuzu gelmemiş olayı silmez. NATS'ın kendi onayı yalnız "alan akış yazdı" der; veritabanına işlendiğini makbuz kanıtlar.
  5. **Saklama:** makbuzu gelmiş olay da en az kasada 30, mağaza sunucusunda 90 gün durur.

### 5.4 Kasanın içi: bir satışın yolu

Adımlı, hareketli bir çizim. Kutular: ekran (Chromium kiosk) → gateway → retail (market modülü) → catalog, sales → payment, fiscal → device; yanında PostgreSQL ve NATS. "İleri / geri / oynat" düğmeleri; her adımda ilgili ok parlar ve altında bir cümle görünür.

Adımlar:

1. Kasiyer barkodu okutur. `device` okuyucudan okur; kod gateway üzerinden ekrana akar (Server-Sent Events).
2. Ekran `POST /api/module/baskets/{id}/items` çağırır; gateway isteği `retail`'e verir. `retail` fiyatı ve KDV'yi `catalog`'dan alır (gRPC).
3. Kasiyer "Öde"ye basar. `retail` sepeti satır listesine çevirip `sales.CreateSale`'e verir.
4. `sales.Checkout`: önce `payment.Charge` (ödeme alınır, satış `charged` olur), sonra `fiscal.Register` (mali kayıt ve fiş).
5. Tek veritabanı işleminde: fiş numarası ayrılır, satış `paid` olur, `sale.completed.v1` olayı outbox'a yazılır.
6. Relay olayı NATS'a yayınlar: `staff` vardiya toplamına işler, olay mağaza sunucusuna gider.

Çizimin yanında dört kısa not:

- **Çağrı türleri:** ekran ↔ gateway HTTP/JSON; servisler arası gRPC; olaylar NATS JetStream, içerik protobuf.
- **Durum makinesi:** `open → charged → paid`. Küçük bir durum çizimi; `charged` satış iptal edilemez, ödemesi geri verilebilir (müdür yetkisiyle).
- **Para:** tutarlar `int64` kuruş, miktarlar binde bir birim; kayan noktalı sayı yok.
- **Modüller:** çekirdek servisler ortaktır; market (`retail`) ve restoran (`restaurant`) aynı çekirdeğin üstünde dikey modüllerdir.

### 5.5 Tasarım kararları

Altı kart; her kart bir karar, bir cümle gerekçe ve bir cümle bedel içerir.

| Karar | Gerekçe | Bedel |
|---|---|---|
| Her servis ayrı container (tek binary yok) | Her servis tek başına güncellenir ve geri alınır; bir servisin hatası diğerini düşürmez. | Daha çok süreç ve servisler arası çağrı. |
| Hexagonal servis yapısı | İş kuralı (domain) veritabanını, gRPC'yi ve NATS'ı bilmez; adaptörler değişir, kural kalır. Mali yazıcı adaptörü buna örnektir: simülatör ya da gerçek cihaz. | Daha çok arayüz ve dosya. |
| Her katmanda PostgreSQL 18 | Tek veritabanı bilgisi, tek yedek ve bakım yolu; ayarlar kasa ve mağaza sayısından hesaplanır (`pkg/pgtune`). | Kasada da bir veritabanı süreci çalışır (boşta ≈55 MiB). |
| NATS JetStream ve leaf node | Katmanlar arası kalıcı, sıralı olay akışı; bağlantı kesilince alt katmanda birikir. | İşletilecek bir bileşen daha; teslim makbuzu uygulama düzeyinde yazıldı. |
| Podman ve Quadlet (systemd) | Arka plan sürecine (daemon) gerek yok; her container bir systemd birimidir, bellek tavanı ve sağlık denetimi birim dosyasındadır. | Kurulum betikleri Linux ve Podman 5 ister. |
| Go servisleri, `scratch` imajlar | Küçük imaj, kök olmayan kullanıcı, kabuk yok. | Hata ayıklama için imajın içine girilemez. |

Altında repo yapısını gösteren kısa bir ağaç: `services/core`, `services/modules`, `services/store`, `services/headquarters`, `pkg`, `proto`, `ui`, `deploy`.

### 5.6 Kaynak kullanımı

- **Grafik 1, yatay çubuklar:** bir kasanın 9 container'ı, boşta ve yük altında bellek (MiB).

  | Container | Boşta | Yük (ort.) |
  |---|---|---|
  | nats | 16,4 | 18,7 |
  | staff | 9,6 | 14,1 |
  | sales | 9,5 | 11,6 |
  | catalog | 7,3 | 9,3 |
  | retail | 6,7 | 10,2 |
  | gateway | 5,7 | 9,9 |
  | payment | 5,3 | 7,2 |
  | device | 3,7 | 6,4 |
  | fiscal | 3,7 | 7,1 |

  Toplam: boşta ≈68 MiB, yük altında ≈95 MiB (en çok 115 MiB). Not: bu ölçüm kasada PostgreSQL'e geçilmeden önceki 9 container'ı kapsar; kasanın veritabanı ayrıca ≈55 MiB kullanır (10 container, ölçülen en yüksek ≈141 MiB).

- **Grafik 2, yığılmış çubuklar:** katman başına bellek ve CPU, boşta ve yük altında.

  | Katman | Boşta CPU / bellek | Yük CPU / bellek |
  |---|---|---|
  | İki kasa (18 container) | %12 / 142 MiB | %320 / 192 MiB |
  | Mağaza sunucusu (5) | %4,5 / 158 MiB | %360 / 220 MiB |
  | Merkez (6) | %4,2 / 145 MiB | %314 / 170 MiB |

  CPU yüzdesi tek çekirdeğin yüzdesidir. Bütün yığın (29 container): boşta 445 MiB, yük altında 582 MiB.

- **Tablo, rol başına bellek ihtiyacı:**

  | Rol | Asgari | Optimum (tavanların toplamı) |
  |---|---|---|
  | Kasa (10 container) | 256 MB | 736 MB |
  | Mağaza sunucusu, 4 kasaya kadar | 384 MB | 544 MB |
  | Mağaza sunucusu, 50 kasa | 1152 MB | 1312 MB |

  Not: bunlar abarow'un kendi servisleridir; işletim sistemi (≈256 MB) ve kasadaki Chromium (≈512 MB, tahmin; ölçülmedi) ayrıca gerekir. Kurulum makinenin belleğini bu toplamla karşılaştırır ve yetmiyorsa uyarır.

- **Kısa madde:** her container'ın bir bellek tavanı vardır; veritabanı ayarları kasa ve mağaza sayısından hesaplanır (4 kasaya kadar `shared_buffers` 64 MB, bağlantı sınırı 20).

### 5.7 Performans

Başında yöntem kutusu: gerçekçi yük, kasa başına 20 saniyede 1 satış; 120 saniye; kasa başına 30 gün × 300 satışlık veri; veritabanı ayarları `pgtune`'dan; 14.11.2026; on ölçümde hata 0.

- **Grafik 3, çizgi:** mağaza sunucusunda kasa sayısına göre ekran süreleri (p50). Yatay eksen 1, 2, 5, 10, 50 kasa; dikey eksen ms, logaritmik.

  | Kasa | Satış (veri) | Veritabanı | Kasa durumu | Günlük özet | Aylık özet | Ürün sıralaması | Kayıt işleme p95 | Bellek / sınır |
  |---|---|---|---|---|---|---|---|---|
  | 1 | 9.000 | 29 MB | 11 ms | 12 ms | 56 ms | 55 ms | 20,0 ms | 74 / 256 MiB |
  | 2 | 18.000 | 47 MB | 16 ms | 15 ms | 95 ms | 90 ms | 15,6 ms | 110 / 256 MiB |
  | 5 | 45.000 | 102 MB | 30 ms | 24 ms | 205 ms | 184 ms | 16,4 ms | 149 / 320 MiB |
  | 10 | 90.000 | 193 MB | 59 ms | 40 ms | 364 ms | 348 ms | 19,7 ms | 261 / 576 MiB |
  | 50 | 450.000 | 944 MB | 306 ms | 179 ms | 2,26 sn | 1,79 sn | 40,6 ms | 912 / 1024 MiB |

- **Grafik 4, çizgi:** merkezde mağaza sayısına göre ekran süreleri (p50), mağaza başına 2 kasa.

  | Mağaza | Satış (veri) | Veritabanı | Mağaza durumu | Günlük özet (hepsi) | Aylık özet | Ürün sıralaması | Kayıt işleme p95 | Bellek / sınır |
  |---|---|---|---|---|---|---|---|---|
  | 1 | 18.000 | 55 MB | 3 ms | 15 ms | 88 ms | 90 ms | 33,7 ms | 118 / 256 MiB |
  | 2 | 36.000 | 98 MB | 3 ms | 21 ms | 157 ms | 152 ms | 24,9 ms | 141 / 256 MiB |
  | 5 | 90.000 | 230 MB | 3 ms | 36 ms | 385 ms | 347 ms | 26,6 ms | 258 / 576 MiB |
  | 10 | 180.000 | 452 MB | 4 ms | 81 ms | 723 ms | 680 ms | 40,5 ms | 475 / 896 MiB |
  | 50 | 900.000 | 2,28 GB | 24 ms | 0,56 sn | 6,92 sn | 6,94 sn | 55,8 ms | 2302 / 2560 MiB |

  Grafiğin altında zorunlu not: 50 mağazadaki aylık özet 11 rapor ekranı aynı anda açıkken ölçüldü. Süreyi satış hızı değil, aynı anda rapor isteyen ekran sayısı belirliyor.

- **Grafik 5, çubuk:** 50 mağazada rapor ekranı sayısına göre aylık rapor (p50): 1 ekran 1,79 sn; 3 ekran 2,53 sn; 11 ekran 6,75–6,87 sn.

- **Grafik 6, çubuk:** kasadaki satış ve okuma gecikmeleri (iki kasa, üç katmanlı yığın).

  | İş | p50 | p95 | p99 |
  |---|---|---|---|
  | Satışın tamamı (5 çağrı), hafif yük | 119 ms | 228 ms | 281 ms |
  | Satışın tamamı, ağır yük | 211 ms | 325 ms | 371 ms |
  | Kasa gateway okuma, ağır yük | 17 ms | 50 ms | 71 ms |
  | Mağaza sunucusu okuma, ağır yük | 38 ms | 83 ms | 109 ms |
  | Merkez okuma, ağır yük | 40 ms | 84 ms | 115 ms |

  Ağır yük: 75 saniye, kasa başına art arda satış yapan bir kasiyer ve 8 okuyucu, mağaza sunucusunda ve merkezde 16'şar okuyucu; 682 satış ve ≈113 bin okuma, hata 0.

- **Üç kısa bulgu:**
  - Kayıt işleme geride kalmıyor: iki katmanda da kuyruk 1–2 satışta kaldı; merkezin toplu yazma kapasitesi saniyede 260 satış (ölçülen yük saniyede 5).
  - 10 kasa ve 10 mağazaya kadar her ekran 1 saniyenin altında.
  - En büyük ölçeklerde veritabanı bellek sınırının yaklaşık %90'ını kullandı; hata ya da bellek yetersizliği olmadı. Aylarca birikmiş veride ölçüm yapılmadı.

### 5.8 Merkez üç makinede

Küçük, hareketli bir çizim: üç makine, her birinde etcd, Patroni (PostgreSQL) ve HAProxy.

- **Normal durum:** bir birincil, iki replika; yazma, en az bir replikaya ulaşmadan tamamlanmaz (senkron çoğaltma).
- **"Birincili durdur" düğmesi:** birincil kararır, etcd çoğunluğu yeni birincili seçer, HAProxy bağlantıları ona yöneltir.
- **İki replika da yokken:** yazma bilerek bekler. Bu bir tasarım kararıdır; veri tek makinede kalmasın diye.
- **Dürüstlük notu (zorunlu):** bu düzen Docker'da test edildi (`scripts/ha-db-test.sh`); kurulum betiği gerçek üç makinede denenmedi.

### 5.9 Güvenli iletişim ve güvenli işlem

Bu bölüm tarama ve test listesi **değildir**. İki soruyu yanıtlar: katmanlar ve servisler birbirleriyle nasıl güvenli konuşuyor, ve bir işlem (satış, ödeme, güncelleme) nasıl güvenli yapılıyor. İki alt başlık, her biri kısa kartlar ve bir çizim.

**A. Güvenli iletişim** (çizim: 5.2'deki üç katmanın sade hâli; her bağlantının üstünde kilit yerine yazılı etiket: protokol, şifreleme, kimlik)

| Bağlantı | Nasıl korunuyor |
|---|---|
| Kasa ekranı ↔ gateway | Yalnız kasanın kendisinde (`127.0.0.1`) sunulur; dışarıya açılmaz. Kasiyer PIN'iyle girer, oturum anahtarı her istekte gider. |
| Kasa içi servisler | Yalnız kasanın container ağında, servis adıyla gRPC konuşur. Dışarıya yalnız gateway açıktır. |
| Kasadaki NATS | Kimliksiz bağlantı kabul edilmez. Her servis kendi NKey'iyle (Ed25519 anahtar çifti) bağlanır ve yalnız işinin gerektirdiği konulara erişir: `sales` yalnız `sale.>` yayınlar, `staff` yalnız kendi tüketicisini okur, diğer servisler NATS'a bağlanamaz. Örnek: market modülündeki bir açık sahte satış olayı yayınlayıp gün sonu raporunu bozamaz. |
| Kasa ↔ mağaza sunucusu | NATS leaf bağlantısı, TLS (mağazanın kendi CA'sı) ve kasa başına NKey. Listeden çıkarılan kasa bağlanamaz. Her kasa yalnız kendi akışını sunar ve yalnız kendi makbuzlarını alır. |
| Mağaza sunucusu ↔ merkez | NATS leaf bağlantısı, TLS, mağaza başına NKey ve merkezdeki izin listesi. Merkez, olayın mağaza kimliğini bağlantıdan alır; bir mağaza başka mağaza adına yazamaz. |
| Mağaza sunucusu ekranı | HTTPS (mağaza CA'sının sertifikası), mağaza ağında. |
| Merkez ekranı | HTTPS ve HSTS (Caddy); servisin kendisi yalnız yerel adreste dinler. |
| Gizli anahtarlar | Depoya girmez. Kurulumda Podman secret'ı olarak verilir; her servis yalnız kendi anahtarını görür. |

**B. Güvenli işlem** (çizim: bir satışın durum makinesi ve yanında "ne ters giderse ne olur" okları)

| Konu | Nasıl yapılıyor |
|---|---|
| Satış ve olay birlikte | Satış, fiş numarası ve yayınlanacak olay tek veritabanı işleminde yazılır (outbox). Biri yazılıp diğeri yazılmamış olamaz. |
| Müşteriden iki kez ödeme alınmaması | Ödeme alınıp mali kayıt başarısız olursa satış `charged` kalır. Yeniden denemede istekteki ödeme yok sayılır, kayıtlı ödemeyle devam edilir. Ödeme servisi aynı satış için ikinci kez ödeme almaz, önceki sonucu döner. |
| Fiş numarasında boşluk olmaması | Numara, mali kayıt başarılı olduktan sonra, satışla aynı işlemde ayrılır. Başarısız deneme numara harcamaz; mağaza sunucusu eksik fişi boşluktan fark eder. |
| Yarım kalan satışın gizlenmemesi | Ödemesi alınmış ama fişi kesilememiş satış kasada ve mağaza sunucusunun ekranında görünür. Böyle bir satış varken vardiya ve gün sonu kapatılamaz. |
| Süre sınırları | Her çağrının süre sınırı vardır ve dış halka iç halkalardan uzundur (gateway 3 dk, ödeme 90 sn, mali kayıt 30 sn). Kart çekildikten sonra sonuç, istek kapansa bile yazılır. |
| Aynı olayın iki kez işlenmemesi | Her olay benzersiz kimlik taşır; alıcı veritabanında aynı kimliği ikinci kez işlemez. |
| Olayın erken silinmemesi | Üst katman veritabanına işlediğini makbuzla bildirmeden alt katman olayı silmez. |
| Para hesabı | Tutarlar tam sayı kuruştur; yuvarlama ve KDV kuralları testlidir. |
| Yetki isteyen işlemler | İade ve iptal müdür yetkisi ister: müdür oturumu ya da müdür onayı. PIN ve parolalar bcrypt özetiyle saklanır; 5 yanlış denemede hesap 5 dakika kilitlenir. Her değişiklik kimin yaptığıyla kaydedilir. |
| Tarayıcı tarafı | Oturum çerezi `HttpOnly` ve `SameSite=Strict`; yazma istekleri JSON gövde ister (başka siteden form ile gönderilemez); mağaza sunucusu ve merkez ekranında CSP ve çerçeve koruması başlıkları. |
| Güncelleme | İmajlar digest'iyle çekilir ve imzalıdır; kanala (pilot, prod) terfi aynı digest'e ikinci imzadır. Ajan imzayı doğrular, güncelleme ekrandan onay ister, yeni sürüm sağlıklı açılmazsa önceki digest'e kendiliğinden geri döner. |

Bölümün altında tek satır: "Bu bölüm nasıl yapıldığını anlatır; güvenlik garantisi değildir. Bağımsız sızma testi henüz yapılmadı."

### 5.10 Sınırlar ve durum

Düz, kısa bir liste. Hepsi yer alır:

- Mali yazıcı (ÖKC) simülatördür; fişin mali değeri yoktur. Gerçek cihaz adaptörü (Beko X30TR) sahte SDK ile sınandı, gerçek cihazla denenmedi.
- Ölçümler geliştirici bilgisayarında yapıldı; gerçek mağaza donanımında, ARM işlemcide ve aylarca birikmiş veride ölçüm yok.
- Kasadaki Chromium ve işletim sistemi belleği ölçülmedi.
- Merkezin üç makineli kurulumu gerçek makinelerde denenmedi.
- Güncelleme ajanının imaj indirip servisleri yeniden başlatan kısmı gerçek Linux/Podman makinede çalıştırılmadı.
- Kurumsal giriş (OIDC) gerçek bir kimlik sağlayıcıyla denenmedi.
- Mal iadesi akışı yok (belgelenmesi mevzuat yorumu bekliyor).
- Bağımsız sızma testi yapılmadı.
- Lisans Apache-2.0'dır; yazılım "olduğu gibi" sunulur, garanti verilmez.

### 5.11 Alt bilgi

Telif satırı, lisans adı, "Ölçümler: 05–14.11.2026" ve sayfanın tarihi. Dış bağlantı yok (depo henüz herkese açık değil).

## 6. Hareketli çizimlerin teknik tarifi

- **Tek akış motoru:** 5.2, 5.3 ve 5.9-A aynı SVG şablonunu ve aynı küçük JavaScript modülünü kullanır. Modül: düğümler, kenarlar (SVG `path`), her kenarda hareket eden noktalar (`getPointAtLength` ile ya da `offset-path` ile), düğüm başına kuyruk sayacı, kenar başına "açık/kesik" durumu.
- **Durum:** her çizimin kendi durumu vardır (`{linkStoreUp, linkHqUp, queues, counters}`); genel değişken yok. Bir IIFE ya da `<script type="module">` içinde.
- **Zamanlama:** `requestAnimationFrame`; sekme gizliyken ve çizim görünür değilken durur. Sabit hız, sahte rastgelelik için küçük bir tohumlu üreteç (her açılışta aynı görüntü).
- **Kesinti mantığı:** kenar kesikken o kenara girecek nokta kaynağın kuyruğuna eklenir. Kenar açılınca kuyruk sırayla, normalden hızlı boşalır. Makbuz noktası, olay hedefe vardıktan kısa süre sonra ters yönde çıkar.
- **Hareketi azalt:** nokta çizilmez; kenarlarda yön okları ve sayaçlarda örnek değerler gösterilir; kesinti düğmeleri yine çalışır ve durumu yazıyla gösterir.
- **Adımlı çizimler (5.4, 5.8):** durum bir adım numarasıdır; her adım hangi öğelerin vurgulanacağını ve alt yazıyı belirler. "Oynat" 2,5 saniyede bir ilerler.

## 7. Çalışma sırası

1. İskelet: gezinme, tema, bölüm başlıkları, bileşen stilleri. Tarayıcıda aç, açık ve koyu temada bak.
2. 5.2 ve 5.3 (akış motoru). **Burada dur ve proje sahibine göster;** yön onaylanınca devam et.
3. 5.4 (kasanın içi) ve 5.9 (güvenli iletişim ve işlem).
4. 5.6 ve 5.7 (grafikler). Her grafiği tablodaki sayılarla tek tek karşılaştır.
5. 5.1, 5.5, 5.8, 5.10, 5.11.
6. Denetim (aşağıda), sonra commit ve push.

## 8. Bitirmeden önce denetim

- [ ] Sayfa `file://` ile açılıyor; tarayıcı konsolunda hata ve ağ sekmesinde dış istek yok.
- [ ] Sayfadaki her sayı bu plandaki bir tabloda ya da `docs/PERFORMANCE.md` / `docs/HARDWARE.md`'de var. Rastgele beş sayıyı kaynağından doğrula.
- [ ] Her grafik ve sayı kutusunun altında ölçüm notu var.
- [ ] "Sınırlar" bölümündeki dokuz maddenin hepsi sayfada.
- [ ] Bölüm 3'teki yasak ifadeler sayfada geçmiyor (ara: "kesintisiz", "kayıpsız", "en hızlı", "garanti" yalnız "garanti değildir" bağlamında).
- [ ] Açık ve koyu temada, 360 ve 1440 piksel genişlikte ekran görüntüsü alıp bakıldı; yatay kaydırma yok.
- [ ] `prefers-reduced-motion` açıkken sayfa hareketsiz ve anlaşılır.
- [ ] Klavyeyle bütün düğmelere ulaşılıyor; her SVG'nin başlığı ve açıklaması var.
- [ ] Dosya 300 KB'ın altında; `bash scripts/check-headers.sh` geçiyor.
- [ ] `README.md` ve `docs/images/` değişmedi.

## 9. Kaynaklar

Sayılar ve olgular şu belgelerden alındı; çelişki görürsen belge geçerlidir ve proje sahibine söylenir:

- `docs/PERFORMANCE.md`: "Yenileme (14.11.2026)", "HTTPS sonrası yeniden ölçüm (06.11.2026)", "Gerçekçi yük (13.11.2026)".
- `docs/HARDWARE.md`: rol başına bellek.
- `docs/DESIGN.md`: bölüm 2 (topoloji), 6 (iletişim ve olaylar), 7 (örnek akışlar), 8 (hexagonal yapı ve checkout), 14 (iki kademeli ara bellek, teslim makbuzu).
- `docs/PENTEST-PREP.md`: güven sınırları, kimlik ve oturum denetimleri.
- `docs/UPDATE.md`: imzalı güncelleme ve geri alma.
- `docs/decisions/HA-HEADQUARTERS.md`: üç makineli merkez.
