# abarow — Taslak Tasarım (v0.7)

> Başlangıç taslağı; servisler, olaylar ve teknoloji seçimleri eklenip çıkarılmak üzere yazıldı.
>
> Verilerin nerede tutulduğu, katmanlar arasında nasıl gidip geldiği ve raporların neyden çıktığı tek yerde: [DATA-FLOW.md](DATA-FLOW.md).
>
> **v0.69:** (1) operasyonda ürün ve fiyat yönetimi: tek ayar "merkez bağlı" (bağlıyken katalog merkezden gelir ve mağazada salt okunur; kapalıyken mağazada yönetilir, müdür ekrandan değiştirir); catalog-admin operasyonda ikinci örnek olarak kendi şemasında çalışır. (2) Kasa olaylarına sıra numarası (`pkg/sequence`): kasanın outbox'ı olayı gönderilecek hale getirirken verdiği ardışık numara `ce-sequence`/`ce-producer` başlıklarıyla gider; operasyon ve merkez numaraları aralık olarak saklar, arada kalan boşluk "eksik kayıt" olarak görünür. (3) Operasyon veritabanı ayarları kasa sayısına göre hesaplanır (`pkg/pgtune`, `report pgtune`) ve servis bağlantı havuzları sınırlıdır (`pkg/pgpool`); başarım ölçümleri [PERFORMANCE.md](PERFORMANCE.md).
>
> **v0.69:** operasyon ekranı HTTPS sunar (karar 06.11.2026, önceki düz HTTP kararlarını değiştirir): `pkg/service` isteğe bağlı TLS (`ABAROW_TLS_CERT`, `ABAROW_TLS_KEY`; sertifika her bağlantıda okunur), report mağaza CA'sının leaf sertifikasını kullanır; müdür cihazlarına `ca.pem` yüklenir (bölüm 14).
>
> **v0.68:** teslim makbuzu (karar 05.11.2026): bir katmanın olayı, üst katmanın PostgreSQL'ine işlendiğinin kanıtı olan UUIDv7 makbuz kimliği kendi kaydının yanına yazılmadan silinmez; kasadaki 30 ve operasyondaki 90 günlük kendiliğinden silme kalktı, silmeyi makbuz temizleyicisi yapar ve süreleri ortam değişkeniyle ayarlanır; üst katman yoksa (tek kasa, merkez kurulmamış) hiçbir olay silinmez (bölüm 14, `pkg/delivery`)
>
> **v0.67:** kurumsal giriş (OIDC, [LOGIN.md](decisions/LOGIN.md)): operasyon ve merkez ekranına isteğe bağlı OpenID Connect girişi (`pkg/login/oidc`: yetki kodu, PKCE, state, nonce, jeton denetimi); `ABAROW_LOGIN=yerel|oidc|yerel,oidc`. Kimliği IdP kanıtlar, rol ve mağaza yetkisi abarow kişi kaydında kalır; kişi `(issuer, sub)` ile ya da ilk girişte doğrulanmış e-posta adresiyle tanınır (`pkg/personnel`: `email`, `idp_issuer`, `idp_subject`). Kişi kendi kimliğini bağlar/kaldırır, yönetici adres tanımlar ve bağı kaldırır; kurulum betikleri `--login`/`--oidc-*`. Gerçek bir IdP'yle denenmedi.
>
> **v0.66:** ortak rapor çekirdeği ve giriş uçları: operasyonun `report` ve merkezin `reporting` servisleri aynı dönem özetini (`pkg/report`, tek uç `GET /api/reports/summary`, aynı JSON; rakamlar `pkg/ledger`'den) ve aynı giriş uçlarını (`pkg/login.Web`: giriş, çıkış, oturum, yöntemler, hata → HTTP durumu eşlemesi) kullanır; servislere yalnızca kendi adaptörleri kalır (operasyon: kasalar, kasiyerler, mağaza fiyatları, mesaj yolu, müdür PIN'i ve mağaza parolası; merkez: mağaza durumları, mağaza fiyatı raporu, kullanıcılar, kullanıcı parolası). Oturum yanıtı iki serviste aynı biçim; operasyonda da yazma istekleri yalnızca JSON kabul eder.
>
> **v0.65:** operasyon da PostgreSQL ([STORE-POSTGRES.md](decisions/STORE-POSTGRES.md)): mağaza başına tek `abarow-store-db` container'ı; report ve inventory (mağaza kapsamı) ona taşındı, oturumlar `sessions` tablosunda; günlük `pg_dump` yedeği ve `store-install.sh restore`. Kasa SQLite'ta kalır.
>
> **v0.64:** tek giriş ve tek kişi modeli ([LOGIN.md](decisions/LOGIN.md), [PEOPLE.md](decisions/PEOPLE.md)): oturum, kilit ve çerez `pkg/login` çekirdeğinde (kimlik adaptörleri: mağaza parolası, müdür PIN'i, kullanıcı parolası; OIDC v0.67'de eklendi); kasiyer, müdür ve merkez kullanıcısı aynı `User` modeli, kasiyer bir roldür; kişi yönetimi (`Manage`) ve SQL (`personeldb`) operasyon ve merkezde ortak, tek `Giris.svelte` ve `KisiBolumu.svelte` ekranları. Operasyon `cashiers` tabloları `users`'a, merkez `users` tabloları aynı şemaya taşındı. Kasa, ingest ve kasa mesajları değişmedi.
>
> **v0.63:** restoran modülü uygulandı (bölüm 20): katalogda menü alanları ve görsel, restaurant servisi, ortak kasa ekranı paketi (`ui/packages/core`), restoran ekranı, `register-install.sh --module restaurant`, restoran simülasyonu ve tarayıcı testi.
>
> **v0.62:** restoran modülünün tasarımı (bölüm 20): salon ve masa, adisyon, mutfak ve bar fişi, hesap bölme; katalogda kategori, istasyon, opsiyon ve ürün görseli; mobil siparişe hazır API (tekrar güvenli satır ekleme, adisyon sürümü, olay akışı).
>
> **v0.61:** portlar: operasyonun leaf portu 7421 (kasalar bağlanır; merkezinki 7422 kalır), merkez ekranı 9090.
>
> **v0.60:** envanter transferi (bölüm 19, adım 3): merkez deposu ↔ mağaza ve mağazalar arası; gönderen yerin stoğundan hemen düşer, alan yer teslim alınca (gelen miktarla) onun stoğuna girer; mağazaya gelen transfer merkezin transfer stream'inden iner.
>
> **v0.59:** container görünümü: kasa, operasyon ve merkezdeki container'lar ve aralarındaki veri yönleri tek şemada (bölüm 2).
>
> **v0.58:** envanter ve depo modülünün tasarımı: stok hareketlerden oluşur; mağaza stoğu operasyonda, merkez deposu merkezde; satışlar olaylardan düşer; mal kabul, sayım, transfer (bölüm 19).
>
> **v0.57:** yemek kartı/çeki ödeme türü: kart gibi POS'tan çekilir, para üstü verilmez; fişte "YEMEK KARTI", X/Z'de ve operasyon ile merkez raporlarında ayrı satır. Tanınmayan yöntem (daha yeni bir satış servisinden) tutarı kaybetmeden "Diğer" satırına gider (bölüm 5).
>
> **v0.56:** kararlar (bölüm 18): ilk ÖKC Beko X30TR (kart ve yemek kartı ÖKC'nin POS'undan, geliştirme simülatörle), yemek kartı ödeme türü, envanter ve depo yönetimi ayrı modül, eksik raporla Z yok, yedek şifreleme yok, gerçek donanım denemesi yok, restoran en sonda.
>
> **v0.55:** iki ekranlı kiosk: müşteri ekranı verilince kiosk sway ile kurulur, kasa ve müşteri pencereleri kendi çıkışlarında tam ekran (bölüm 6).
>
> **v0.54:** müşteri ekranı sayfası (`/musteri.html`): sepet, toplam, ödenen ve para üstü; kasiyer ekranından aynı tarayıcıdaki pencereye BroadcastChannel ile gelir (bölüm 6).
>
> **v0.53:** kasada seri port terazi: tartılı ürün terazi barkodu olmadan seçilince ağırlık `device`'taki teraziden okunur (Toledo 8217 ya da NCI ECR). Kasa kurulumunda `--scale` (bölüm 5, 6).
>
> **v0.52:** yazıcının durumu (kağıt bitti, kağıt azaldı, kapak açık, kapalı, hata) `device`'tan ekrana akar; sorun varsa kasa ekranının üstünde uyarı şeridi görünür (bölüm 5, 6).
>
> **v0.51:** gerçek fiş yazıcısı: ESC/POS, USB ya da ağda; Türkçe kod sayfası, kağıt kesme, yazıcıya bağlı çekmece. Kasa kurulumunda `--printer`, `--receipt-width`, `--code-page` (bölüm 5).
>
> **v0.50:** barkod okuyucu `device`'tan okunur (evdev; aygıt device'a ayrılır) ve kodlar ekrana akar (device gRPC akışı → gateway SSE → ekran); imleç nerede olursa olsun sepete gider. İlk sürüm v0.1.0 etiketlendi (bölüm 5, 6).
>
> **v0.49:** merkezde roller ve mağaza yetkisi (karar 29.10.2026): yönetici, katalog, rapor; kullanıcıya bütün ya da seçilen mağazalar. Kullanıcılar ekranın "Kullanıcılar" bölümünden yönetilir; süzgeç sunucuda uygulanır (bölüm 15).
>
> **v0.48:** operasyonda hesabı açık müdür varken mağaza parolası kapalıdır (karar 29.10.2026); PIN'ini unutan tek müdürün kurtarma yolu `store-install.sh pin`. Operasyon ekranı düz HTTP kalır (karar 29.10.2026, yeniden) (bölüm 14).
>
> **v0.47:** merkez yedekleri her yedekten sonra makine dışına kopyalanır (`headquarters-install.sh --backup-target`: harici disk/NAS ya da SSH ile başka sunucu; rrsync'le yalnızca yazabilen anahtar) (bölüm 15).
>
> **v0.46:** merkez kurulum betiği ekranı HTTPS'ten sunabilir (`headquarters-install.sh --domain`): Caddy sertifikayı Let's Encrypt'ten kendisi alır ve yeniler (bölüm 15).
>
> **v0.45:** operasyon ekranına müdürler kendi kodu ve PIN'iyle girer; değişiklik kayıtlarına müdürün adı yazılır. Hesabı kapanan, müdürlüğü kalkan ya da PIN'i değişen müdürün oturumu kapanır; art arda kilitler uzar. Mağaza parolası ilk kurulum için kalır (bölüm 14, 15).
>
> **v0.44:** kasiyerler operasyon ekranında yönetilir ("Kasiyerler": ekle, ad, rol, PIN, hesabı kapat; kasiyer dosyasını yükleme); kasalara PIN özetiyle iner, kapanan hesap kasada giriş yapamaz, açık oturumu kapanır; merkeze adı ve rolü bildirilir, raporlarda kasiyer adları. Ekranlar geçmiş bir anı "ileri tarihli fiyat" olarak kabul etmez (bölüm 5, 14, 15).
>
> **v0.43:** merkezde "mağaza fiyatları" raporu: mağazaların bildirdiği istisnalar merkez fiyatıyla, farkıyla ve ileri tarihli değişiklikleriyle; ürün sayfasında ürünün mağazalardaki fiyatları (bölüm 15).
>
> **v0.42:** mağaza fiyatı (istisna) operasyon ekranında ("Fiyatlar"): hemen ya da ileri tarihli, merkez fiyatına dönüş, değişiklik geçmişi; kasalara mağaza ayarları stream'iyle iner, değişiklik olay olarak merkeze gider; mağaza yönetimi report servisinde (bölüm 14 ve 15).
>
> **v0.41:** katalog merkezden kasalara iner: operasyon merkezin katalog stream'ini, kasa operasyonunkini kaynak alır (leaf izinleri yalnızca katalog için, ters yönde); kasadaki catalog ürünleri uygular, satıştan kaldırılan ürünü göstermez; merkezden gelen ürün başlangıç dosyasıyla değişmez (bölüm 15).
>
> **v0.40:** merkezde katalog yönetimi (`catalog-admin`): ürün, barkod, KDV, hızlı tuş, merkez fiyatı ve ileri tarihli fiyat; değişiklik geçmişi ve aynı anda düzenlemeye karşı sürüm denetimi; ürünün son hali outbox'la katalog stream'ine (`ABAROW_CATALOG`); merkez ekranında "Ürünler" (bölüm 15).
>
> **v0.39:** kasada fiyat takvimi: catalog merkez ve mağaza fiyatlarını yürürlük anlarıyla tutar, satış anında yürürlükteki fiyatı verir; ileri tarihli fiyat başlangıç dosyasından da verilebilir (bölüm 15).
>
> **v0.38:** katalog, fiyatlar ve kasiyerler için kararlar (28.10.2026) ve tasarım: merkez fiyatı + mağaza istisnası, ileri tarihli fiyat, kasiyerler mağazada (bölüm 15).
>
> **v0.37:** merkez kurulumu (Quadlet + `headquarters-install.sh`: mağaza ekleme/çıkarma, hesaplar), operasyonun merkeze bağlanması (`store-install.sh --headquarters`), günlük `pg_dump` yedeği ve yedekten geri yükleme (yedekten sonraki olaylar merkez stream'inden yeniden işlenir) (bölüm 15).
>
> **v0.36:** merkez ekranı ve rapor API'si (`reporting`): mağazaların durumu ve bugünkü satışları, dönem özeti (mağaza, gün, ödeme, KDV, kasiyer, ürün); kişiye özel hesaplar, oturumlar veritabanında (bölüm 15).
>
> **v0.35:** merkezin ilk servisi `ingest`: mağazaların olayları merkeze (NATS leaf, TLS) ve PostgreSQL'e; olayın mağazasını konu belirler, tekrarlar olay kimliğiyle ayıklanır. Üç katmanlı simülasyon (bölüm 6 ve 15).
>
> **v0.34:** operasyon vardiya olaylarını da toplar: kasa kartında vardiyadaki kasiyer ve son kapanan vardiyanın kasa farkı (bölüm 14).
>
> **v0.33:** staff vardiya olaylarını yayınlar (`shift.opened.v1`, `shift.closed.v1`; outbox ile, kayıtla aynı transaction'da); outbox relay'i ortak pakette (`pkg/outbox`) (bölüm 6).
>
> **v0.32:** fişi kesilemeyen satışlar operasyonda görünür: sales mali kayıt başarısız olunca `sale.charged.v1` yayınlar (bölüm 6, 8 ve 14).
>
> **v0.31:** operasyon kurulumu (Quadlet + `store-install.sh`: kurulum, kasa ekleme/çıkarma); kasa kurulumu register/mağaza kimliği ve operasyon bağlantısı alır (bölüm 12 ve 14).
>
> **v0.30:** sepet değişiklikleri sıralı: aynı anda gelen okutmalar kaybolmaz (bölüm 11).
>
> **v0.29:** operasyon ekranına giriş: mağaza parolası (bcrypt özeti), çerezle oturum, yanlış denemede kilit (bölüm 14).
>
> **v0.28:** operasyon ekranı (`ui/apps/store`, report'a gömülü): kasa durumu, eksik fişler, günlük mağaza raporu; ekranlar Node 24 LTS ile derlenir (bölüm 14).
>
> **v0.27:** operasyon `report`: satış olaylarını `report.db`'ye işler; günlük mağaza raporu ve eksik fiş tespiti (bölüm 14).
>
> **v0.26:** kasa → operasyon leaf bağlantısı TLS'li: mağazaya özel CA, kasa CA'yı sabitler (bölüm 14).
>
> **v0.25:** kasa → operasyon aktarımı uygulandı: NATS leaf + JetStream sourcing, kasa başına NKey, kasalar arası yalıtım; `store/report` iskeleti ve mağaza simülasyonu (bölüm 6 ve 14).
>
> **v0.24:** kasa kurulum betiği (Debian 13'te denendi); Quadlet birimleri dış ağ beklemez (bölüm 12).
>
> **v0.23:** satış olayları protobuf (`events.proto`), JSON'dan geçiş tüketicilerde içerik türüyle (bölüm 6).
>
> **v0.22:** kasada NATS kimlik ve yetkisi (bölüm 6): her servis kendi NKey'iyle bağlanır, yalnızca sales satış olayı yayınlar. Quadlet dosyaları ve Podman secret'ları (bölüm 12).
>
> **v0.21:** satışsız çekmece açılışı müdür yetkisiyle, nedeniyle ve vardiyaya kayıtlı (bölüm 5).
>
> **v0.20:** fişi basılamamış satışın ödemesi müdür yetkisiyle iade edilebilir; staff'ta genel yetki denetimi (`Authorize`) (bölüm 8).
>
> **v0.19:** fişi basılamamış (`charged`) satışlar kasada gösterilir; bunlar varken Z ve vardiya kapanışı yapılmaz (bölüm 8). Kasa içi gRPC bağlantıları en çok 2 sn'de yeniden bağlanır; servise ulaşılamazsa kasiyere teknik ayrıntı değil "... yanıt vermiyor" gösterilir.
>
> **v0.18:** sepetler kalıcı; ekran ya da modül yeniden başlarsa kasiyer sepetinden, ödeme sırasındaysa ödeme penceresinden devam eder (bölüm 11).
>
> **v0.17:** X, Z ve vardiya raporları `device` üzerinden fiş yazıcısına basılır (bölüm 5).
>
> **v0.16:** `device` servisi (bölüm 5): fiş yazıcısı ve çekmece, şimdilik simülatör. ÖKC simülatörü fişi device'a basar; çekmece olayla değil nakitli fiş basılırken açılır.
>
> **v0.15:** müdür onayı (bölüm 5): kasiyer oturumundayken müdürün PIN'iyle tek seferlik yetki (Z, kasadan para alma, başkasının vardiyasını kapatma); Z yetkisi gateway'den staff'a taşındı.
>
> **v0.14:** kasa hareketleri (bölüm 5): satış dışında kasaya para koyma ve kasadan para alma; beklenen nakde girer, para almayı yalnızca müdür yapar. Geçmiş vardiya raporları.
>
> **v0.13:** vardiya aç/kapat (bölüm 5): kasada tek açık vardiya, satış yalnızca kendi vardiyasında, kör kasa sayımı, açık vardiyayla Z alınmaz. Z ve vardiya kapanışı bekleyen satış olayları işlenene kadar bekler.
>
> **v0.12:** kasiyer PIN girişi ve roller (bölüm 5): oturumu gateway doğrular, satışa kasiyer yazılır, Z yalnızca müdürde.
>
> **v0.11:** gün sonu (X/Z) raporları: `services/core/staff` sales olaylarını NATS'tan okur (bölüm 5); CI (GitHub Actions).
>
> **v0.10:** market modülü (sepet, terazi barkodu), catalog, gateway ve kasa ekranı (Svelte); ekran dosyaları `go:embed` yerine image'da `/web` (bölüm 11); tarayıcı testi (bölüm 12).
>
> **v0.9:** outbox relay → NATS JetStream (bölüm 6); NATS simülasyonda, mesaj yolu kesintisi senaryosu (bölüm 12).
>
> **v0.8:** servisler gRPC ile bağlı (sales, payment, ÖKC simülatörü); Docker simülasyonu ve ölçülen bellek kullanımı (bölüm 12 ve 13).
>
> **v0.7:** `charged` durumu ve aynı ödemeyle eşleştirme (bölüm 8); KDV hesabı ve simülatör fişi GİB YN ÖKC kılavuzuna göre (bölüm 5 ve 9).
>
> **v0.6:** ilk kod: `pkg/money`, `proto/` sözleşmeleri (sales, fiscal), `services/core/sales` (domain, app, SQLite + outbox), `services/fiscal/simulator` iskeleti. Fiş numarası ve transaction kuralları (bölüm 8), KDV'nin oran bazında hesaplanması (bölüm 9), modüller arası `replace` (bölüm 17).
>
> **v0.5:** proje adı abarow; Go modül yolu `abarow.com`; tanımlayıcılar `abarow` önekiyle, isim alanı `com.abarow`; `NOTICE` ve `CONTRIBUTING.md` eklendi; yerel git + GitHub private depo.
>
> **v0.4:** merkez katmanının adı merkez oldu; merkez her işletmenin kendi sunucusunda çalışır (self-hosted, tenant yok).
>
> **v0.3:** operasyon (mağaza sunucusu: ara bellek + mağaza raporları), ÖKC simülatörü (mali değeri olmayan fiş); merkez katmanı Merkez olarak adlandırıldı.
>
> **v0.2:** açık kaynak (Türkiye) hedefi, market/restaurant dikey modülleri, katmanlı topoloji, veritabanı karşılaştırması, container (Podman) ile dağıtım.

## 1. Hedef ve kısıtlar

| Konu | Karar |
|---|---|
| Dağıtım | Açık kaynak, öncelikli pazar Türkiye |
| Sektör | Önce market; dikey modül değiştirilerek restorana geçilebilir |
| Donanım | Kasa: 4 GB RAM, ARM64 (ör. RK3566/RK3588, RPi 5) veya Intel i3 (amd64) |
| Ölçek | Tek kasa → birden fazla kasada operasyon (mağaza sunucusu) → merkez |
| Merkez | Her işletme kendi sunucusunda çalıştırır (self-hosted) |
| İşletim sistemi | Debian minimal (iki mimaride de aynı dağıtım) |
| Backend | Go, hexagonal mikroservisler, her servis ayrı container |
| Önyüz | Svelte, Chromium kiosk modunda (bkz. bölüm 11) |
| Çalışma şekli | Çevrimdışı öncelikli: internet yokken de satış yapılabilmeli |

### Temel ilkeler

- **Çevrimdışı öncelikli:** Tüm okumalar yerel veriden yapılır. Üst katmana (operasyon, merkez) yazma her zaman asenkrondur (outbox + NATS).
- **Her servis kendi verisinin sahibi:** Başka servisin veritabanına doğrudan erişilmez.
- **Para = `int64` kuruş** (bkz. bölüm 9).
- **Çekirdek sektörden bağımsız:** Markete veya restorana özgü her şey dikey modülde durur.
- **Donanım yalnızca `device` servisinde;** mali kayıt (ÖKC veya simülatör) ayrı bir fiscal adapter container'ında.
- **Servisler bağımsız güncellenir:** Sözleşmeler (gRPC, olaylar, şemalar) geriye uyumlu tutulur (bkz. bölüm 12).
- **CGO'suz derleme:** Tek komutla amd64 ve arm64 image'ları üretilir.

## 2. Topoloji

```
  REGISTER 1..N (4 GB)            STORE (mağaza sunucusu)        HEADQUARTERS
 ┌───────────────────┐        ┌──────────────────────────┐        ┌───────────────────────┐
 │ UI + çekirdek     │  leaf  │ NATS hub (ara bellek)    │  leaf  │ NATS                  │
 │ + dikey modül     │───────▶│ mağaza raporları         │───────▶│ ingest → PostgreSQL   │
 │ + PostgreSQL      │        │ catalog (mağaza kopyası) │        │ raporlama, ürün/price │
 └─────────┬─────────┘        │ restaurant (çok terminal)│        │ cihaz/sürüm yönetimi  │
           │                  └──────────────────────────┘        └───────────────────────┘
           │                                                                  ▲
           └────────────── tek kasalı yerlerde doğrudan merkeze ──────────────┘
```

- **Tek kasa:** Operasyon yoktur. Kasa X/Z ve gün sonu raporlarını kendisi gösterir; merkez kurulmuşsa doğrudan ona bağlanır.
- **Birden fazla kasa:** Mağazaya bir operasyon kurulur (bkz. bölüm 14). Kasalar merkeze değil operasyona bağlanır; operasyon satışları biriktirip merkeze iletir ve mağaza raporlarını sunar. Kasalar operasyon olmadan da satış yapmaya devam eder.
- **Çok terminal (restoran):** Masa durumu terminaller arasında paylaşılmak zorunda olduğu için `restaurant` modülü operasyonda çalışır.
- Her katmanda **aynı container image'ları** çalışır. Katmanları ayıran tek şey hangi container'ların açık olduğudur.

### Container görünümü

Her kutu ayrı bir container'dır ve tek başına güncellenir. Kasa ve operasyonda Podman/Quadlet, merkezde de Quadlet kullanılır (kurulum betikleri: `deploy/install/`).

```
╔═ REGISTER 1..N ════════════════════════════════════════════════════════════════╗
║  [host] cage + Chromium kiosk (Svelte UI)                                  ║
║        │ HTTP/WS 127.0.0.1:8080                                            ║
║        ▼                                                                   ║
║  gateway ──gRPC──▶ retail ─▶ catalog                                       ║
║     │              sales ─▶ payment        (her biri kendi şeması)         ║
║     │                │  └──▶ fiscal (simülatör / Beko X30TR)               ║
║     └──────────────▶ staff   device (yazıcı, çekmece, barkod, terazi)      ║
║                                                                            ║
║  nats (JetStream) + nats-auth (NKey)  ◀── outbox relay (sales, staff)      ║
╚════════════════════════════════╤═══════════════════════════════════════════╝
                                 │ NATS leaf + TLS (mağaza CA'sı)
╔═ STORE ═══════════════════▼═══════════════════════════════════════════╗
║  nats hub (ABAROW_STORE, kasa akışlarını source eder)                      ║
║  report ── PostgreSQL (mağaza raporu, eksik fiş, kasa durumu, ekran,       ║
║     │                  kasiyer ve fiyat yönetimi)                          ║
║     └─ /api/stock ──▶ inventory ── PostgreSQL  (mağaza stoğu)              ║
║  catalog (mağaza kopyası)                                                  ║
╚════════════════════════════════╤═══════════════════════════════════════════╝
                                 │ NATS leaf + TLS (merkez CA'sı, mağaza başına NKey)
╔═ HEADQUARTERS ──────────────────▼═══════════════════════════════════════════╗
║  Caddy (HTTPS) ─▶ reporting (API + ekran)                                  ║
║  nats (ABAROW_HEADQUARTERS) ─▶ ingest ─▶ PostgreSQL ◀─ reporting                     ║
║  catalog-admin ──▶ katalog stream'i (aşağı: mağazalara ve kasalara)        ║
║  inventory ──▶ transfer stream'i (aşağı: mağazalara; bölüm 19)             ║
╚════════════════════════════════════════════════════════════════════════════╝
```

- **Yukarı:** olaylar kasadan operasyona, oradan merkeze akar (outbox → JetStream → stream sourcing / leaf). Merkez olay kimliğiyle tekrarları ayıklar.
- **Aşağı:** katalog, mağaza fiyatları, kasiyerler ve mağazaya gönderilen transferler ters yönde iner.
- **Henüz yok:** `sync` kasa servisi (aktarımı şimdilik NATS leaf ve sourcing yapıyor), `restaurant`, `fleet`, `e-belge`.

## 3. Kasa mimarisi

```
                    ┌─────────────────────────────────────┐
                    │  Svelte UI · Chromium kiosk · cage  │   ← host üzerinde
                    └──────────────────┬──────────────────┘
                                       │  HTTP + WebSocket (127.0.0.1:8080)
                                       ▼
                                ┌─────────────┐
                                │   gateway   │  / → aktif dikey modülün UI'si
                                └──────┬──────┘
                                       │  gRPC (podman ağı: abarow)
   ┌───────────┬───────────┬───────────┼───────────┬───────────┬───────────┐
   ▼           ▼           ▼           ▼           ▼           ▼           ▼
retail      catalog      sales      payment      staff      device       sync
(veya                      │                                   │
restaurant)                ▼                                   ▼
                    fiscal adapter                     yazıcı, çekmece,
                    (simülatör/ÖKC)                     barkod, terazi

═══════════════ NATS JetStream: tüm servisler olay yayınlar / dinler ═══════════════
```

- `cage`, tek bir uygulamayı tam ekran çalıştıran minimal bir Wayland kiosk compositor'ıdır. cage ve Chromium host'ta, geri kalan her şey container'da çalışır.
- Operasyon olan kurulumda kasa aynı container'ları çalıştırır; tek fark `sync`'in üst katman olarak merkezi değil operasyonu görmesidir.

## 4. Modülerlik: çekirdek + dikey modül

İki kavram birbirinden ayrılır:

- **Sipariş (order):** Satırların nasıl toplandığı. Sektöre özgüdür, **dikey modülde** durur.
- **Satış (sale):** Fişin kendisi: toplamlar, KDV, ödeme, mali kayıt. Her sektörde aynıdır, **çekirdekte** (`sales`) durur.

Dikey modül satırları topladıktan sonra `sales.CreateSale` ile çekirdeğe devreder. Operasyona ve merkeze iki sektörden de aynı `sale.completed.v1` olayı gider; üst katmanlar hangi modülün kullanıldığını bilmek zorunda kalmaz.

| | Market (`retail`) | Restoran (`restaurant`) |
|---|---|---|
| Sipariş toplama | Sepet, barkod, terazi barkodu, hızlı tuşlar | Masa planı, adisyon, ürün opsiyonları, mutfak fişi/KDS, hesap bölme, garson |
| Çekirdek | catalog, sales, payment, staff, device, sync | aynı |
| Üst katmana giden | `sale.completed.v1` | `sale.completed.v1` |

- **Bir dikey modülün içeriği:** kendi backend'i (hexagonal Go servisi), kendi UI uygulaması (`ui/apps/<modül>`, container'a gömülü) ve kendi veritabanı. Gateway modüle özgü hiçbir şey bilmez; `/` ve `/api/module/*` isteklerini aktif modüle yönlendirir.
- **Uygulandı (market):** Gateway `/api/config` (aktif modül, fiscal modu) ve `/api/sales/*` (ödeme, iptal → sales) uçlarını kendisi karşılar, gerisini modüle iletir. Market modülünün HTTP API'si `/api/module/baskets` (sepet, barkod ve terazi barkodu, miktar, satır silme, checkout, reopen) ve `/api/module/products` (arama, hızlı tuşlar). Sepetler şimdilik bellekte tutulur.
- **Marketten restorana geçiş:** `abarow-retail` container'ı kapatılıp `abarow-restaurant` açılır, gateway'de `ABAROW_MODULE=restaurant` ayarlanır. Çekirdek ve veriler (katalog, kasiyerler, geçmiş satışlar) olduğu gibi kalır.
- **Katalog:** Tek bir genel model iki sektörü de karşılar: ürün, birim (adet/kg), 0..n barkod, 0..n opsiyon grubu. Market barkodları, restoran opsiyon gruplarını kullanır.

## 5. Servisler

| Servis | Tür | Sorumluluk | Veri | Driven port'lar |
|---|---|---|---|---|
| **gateway** | çekirdek | UI giriş noktası (REST + WebSocket), oturum, aktif modüle yönlendirme | – | Çekirdek servislerin gRPC client'ları |
| **catalog** | çekirdek | Ürün, barkod, fiyat, kategori, KDV oranı, opsiyon grupları | `catalog.db` | – (sync ile güncellenir) |
| **sales** | çekirdek | Fiş: toplamlar, KDV, indirim, ödeme orkestrasyonu, mali kayıt, iptal/iade, kasa bazında artan fiş numarası | `sales.db` + outbox | `PaymentGateway`, `FiscalDevice` |
| **payment** | çekirdek | Nakit, kart, yemek kartı, bölünmüş ödeme, para üstü | `payment.db` | `CardTerminal` |
| **staff** | çekirdek | Kasiyer PIN girişi, roller, vardiya aç/kapat, X/Z özeti | `staff.db` | – |
| **device** | çekirdek | Fiş ve mutfak yazıcıları (ESC/POS), çekmece, barkod okuyucu, müşteri ekranı, terazi | – | `Printer`, `Scanner`, `Drawer`, `Display`, `Scale` |
| **sync** | çekirdek | Olayları üst katmana (operasyon veya merkez) iletir, catalog/price değişikliklerini çeker, ilk kurulumda katalog anlık görüntüsünü indirir, kasa durumunu (heartbeat) yayınlar | `sync.db` | `Upstream` |
| **retail** | dikey modül | Sepet, barkod ve terazi barkodu çözümü, hızlı tuşlar | `retail.db` | `ProductLookup`, `Checkout` |
| **restaurant** | dikey modül | Masa, adisyon, opsiyon, mutfak yönlendirme, hesap bölme | `restaurant.db` | `ProductLookup`, `Checkout` |
| **fiscal-\<adapter\>** | adapter | Satışı mali olarak kaydeder ve fişi basar (bkz. aşağıda) | – | ÖKC protokolü/SDK'sı ya da `device` (simülatör) |

### Gün sonu (X/Z) raporları

`services/core/staff`, sales olaylarını (`sale.completed.v1`, `sale.voided.v1`) `ABAROW_SALES` stream'inden kalıcı bir tüketiciyle (`staff`) okur ve dönem (iki Z arası) bazında saklar. X raporu açık dönemin anlık özetidir; Z raporu dönemi kapatır ve yenisini açar.

- **Alanlar** GİB YN ÖKC Z raporu düzenini izler: KDV oranı bazında KDV dahil toplam ve KDV tutarı, toplam KDV, toplam satış, fiş sayısı ve numara aralığı, ödeme türleri (nakitte para üstü düşülür; ödemeler toplamı satış toplamına eşittir), ödemesi alınmadan iptal edilen satışlar.
- **KDV yeniden hesaplanmaz:** Fişlerde hesaplanmış KDV tutarları toplanır.
- **Resmi Z raporu:** Gerçek ÖKC bağlıyken resmi Z raporu ÖKC'nin kendi raporudur; bu rapor yönetim özetidir ve ÖKC'ninkiyle karşılaştırılabilir.
- **Basım:** X, Z (Z numarasıyla yeniden de basılabilir) ve kapanmış vardiya raporları `device` üzerinden fiş yazıcısına basılır. Rapor yazıcının sütun sayısına göre düzenlenir (`pkg/layout`; hiçbir satır genişliği aşmaz, sığmayan bölünür); saatler kasanın saat diliminde (`ABAROW_TZ`) basılır. Basılan raporun başlığında "YÖNETİM ÖZETİ", Z'de "Resmi Z raporu ÖKC raporudur", sonunda "MALİ DEĞERİ YOKTUR" yazar. Vardiya raporunu görme yetkisi olan basabilir; açık vardiyanın raporu basılmaz (kör sayım).
- **Olay sırası:** Olaylar staff'a vardıkları sırayla açık döneme yazılır. Mesaj yolu kesikken yapılan satışlar olaylar gelince rapora girer.
- **Kapanıştan önce bekleme:** Z ve vardiya kapanışı, o ana kadarki satış olayları işlenene kadar bekler. Önce sales'in henüz yayınlamadığı olaylara bakılır (`GetOutboxStatus`), sonra staff tüketicisinin işlemediği olaylara (JetStream tüketici bilgisi). Sıra önemlidir: sales'te sıfır, olayların mesaj yolunda olduğunu; tüketicide sıfır, işlendiklerini gösterir. Olaylar süresinde işlenmezse (`ABAROW_STAFF_EVENT_WAIT`, varsayılan 5 sn) işlem yapılmaz ve 503 döner: mesaj yolu kesikken Z alınmaz, vardiya kapanmaz, mesaj yolu gelince tekrar denenir. Böylece satış ertesi güne kaymaz, kasa sayımında yanlış fark görünmez.

### Kasiyer girişi ve yetkiler

Kasiyerler ve oturumlar `services/core/staff`'tadır; ekran yalnızca gateway'le konuşur.

- **PIN:** En az 4 haneli; `staff.db`'de yalnızca bcrypt özeti tutulur. 5 hatalı denemeden sonra kasiyer 5 dakika kilitlenir; ekran kalan hakkı gösterir. Doğru giriş sayacı sıfırlar.
- **Kasiyerlerin kaynağı:** Operasyona bağlı kasada kasiyerler operasyon ekranında yönetilir (bölüm 15) ve kasaya iner: staff kendi stream'ini (`ABAROW_CASHIERS`) mağaza ayarları stream'inin `config.cashier.>` konularından kaynak olarak kurar (`ABAROW_CASHIERS_SOURCE`, operasyonun mağaza kimliği) ve kasiyerin son halini uygular. Operasyondan gelen kasiyer kurulumdaki dosyayla (`cashiers.json`) değişmez; dosya yalnızca operasyonda yönetilmeyen kasiyerleri yükler (operasyonsuz kasa ve ilk kurulum). PIN değişince hatalı deneme sayacı ve kilit sıfırlanır.
- **Kapalı hesap:** İşten ayrılan kasiyerin hesabı kapatılır, kasiyer silinmez (satışlarda ve vardiyalarda kimliği kalır). Kapalı kasiyer giriş listesinde görünmez, giriş yapamaz (403), açık oturumu kapanır ve verdiği müdür onayı geçmez. Girişteki hatalı deneme sayacı ayrı yazılır: giriş sırasında operasyondan gelen değişiklik ezilmez.
- **Oturum:** Girişte 32 baytlık rastgele bir anahtar üretilir; veritabanında yalnızca SHA-256 özeti tutulur. Süresi varsayılan 12 saattir (`ABAROW_STAFF_SESSION_TTL`). Ekran anahtarı sekme belleğinde (`sessionStorage`) tutar; tarayıcı kapanınca yeniden giriş gerekir.
- **Gateway:** `/api/config`, kasiyer listesi ve giriş dışındaki her istek geçerli oturum ister (yoksa 401). Gateway kasiyer kimliğini modüle `X-Abarow-Cashier-Id` başlığıyla iletir; istemcinin gönderdiği bu başlık silinir, yani kasiyer kimliği yalnızca oturumdan gelir.
- **Roller:** `cashier` satış yapar ve X raporu alır; `manager` ayrıca Z alır, kasadan para alır ve başkasının vardiyasını kapatır. Yetkiyi staff denetler (müdür gerekirken 403); gateway yalnızca kimliği iletir.
- **Müdür onayı:** Müdür yetkisi gereken bir işlemde kasiyerin çıkış yapması gerekmez: müdür kasanın başında PIN'ini girer (`POST /api/auth/approve`, işlem adıyla). Dönen anahtar yalnızca o işlem için, bir dakika geçerli ve tek kullanımlıktır; istek `X-Abarow-Approval` başlığıyla gönderilir ve işlemi müdür yapmış sayılır. Anahtarın yalnızca SHA-256 özeti saklanır; kayıt kimin istediğini ve kimin onayladığını gösterir. Hatalı PIN müdürün deneme sayacına işler ve 403 döner; 401 dönseydi ekran kasiyerin oturumunu kapatırdı.
- **Satışta kasiyer:** sales her satışa kasiyeri yazar (`cashier_id`) ve `sale.completed.v1` ile `sale.voided.v1` olaylarında taşır; vardiya toplamları buna dayanır.
- **Kasiyer listesi:** Şimdilik açılışta bir JSON dosyasından yüklenir (`ABAROW_STAFF_SEED`; aynı kimlik yeniden yüklenirse ad, rol ve PIN güncellenir, kilit de kalkar). Kasiyer yönetimi merkezden gelecek (sync).

### Vardiya

Vardiya, bir kasiyerin kasadaki çalışma dönemidir; `services/core/staff` tutar.

- **Tek çekmece, tek vardiya:** Kasada aynı anda tek vardiya açık olabilir. Başkasının vardiyası açıkken yeni vardiya açılmaz ve satış yapılmaz. Kasiyer mola için çıkış yapabilir; vardiyası açık kalır.
- **Açılış:** Kasiyer çekmecedeki nakdi sayıp açılış nakdini girer.
- **Satış yalnızca kendi vardiyasında:** Gateway, satış isteklerinde (modül API'si ve `/api/sales/*`) kasiyerin kendi vardiyasının açık olduğunu denetler; değilse 409. Raporlar ve vardiya işlemleri vardiya istemez.
- **Satışın vardiyaya bağlanması:** staff, satış olayı geldiğinde satışı kasiyerine ve satış anına göre vardiyaya bağlar (`opened_at ≤ satış anı < closed_at`). Olay geç gelse de satış yapıldığı vardiyaya girer. Kasiyersiz satış hiçbir vardiyaya girmez.
- **Kapanış ve kör sayım:** Vardiyayı sahibi ya da müdür kapatır. Kasiyer çekmecedeki nakdi sayıp girer; beklenen tutarı saymadan önce görmez. Kapanıştan sonra rapor gösterilir. Raporda fiş sayısı, satış toplamı, ödeme türleri ve iptaller bulunur. Beklenen nakit, açılış nakdi ile para üstü düşülmüş nakit tahsilatın toplamıdır. Fark, sayılan ile beklenen arasındadır; eksi değer kasa açığı, artı değer kasa fazlasıdır. Kendi vardiyasını kapatan kasiyerin oturumu da kapanır.
- **Kasa hareketleri:** Açık vardiyada satış dışında kasaya para konabilir (ör. bozuk para) ya da kasadan para alınabilir (ör. ara parayı çelik kasaya götürme, masraf). Tutar artıdır, açıklama zorunludur. Para koymayı vardiyanın sahibi ya da müdür yapar. Para almayı yalnızca müdür yapar: kasiyer kendi adına çıkış kaydedebilseydi kör sayımda ortaya çıkacak açığı gizleyebilirdi. Beklenen nakit = açılış + nakit tahsilat + konan − alınan. Bunlar satış değildir, mali belge gerektirmez. Müşteriden alınan ön ödeme (GİB'in "avans fişi") ayrı bir konudur ve henüz desteklenmez.
- **Satışsız çekmece açılışı:** Çekmece satış olmadan yalnızca müdür yetkisiyle açılır (kasiyer oturumunda müdür onayıyla) ve neden zorunludur. Her açılış (açan müdür, kimin oturumunda, neden, ne zaman) vardiyaya kaydedilir; vardiya raporunda "Satışsız çekmece açılışı" sayısı görünür. Sık açılış kayıp önleme açısından göze batar. staff yetkiyi denetler, device çekmeceyi açar.
- **Geçmiş raporlar:** Kapanmış vardiyalar yeniden eskiye listelenir. Rapor, kayıtlı satış ve kasa hareketlerinden her açılışta yeniden hesaplanır. Müdür bütün vardiyaları, kasiyer yalnızca kendi vardiyalarını görür. Açık vardiyanın raporu verilmez (kör sayım).
- **Gün sonu:** Açık vardiya varken Z alınmaz (409); önce kasa sayılıp vardiya kapatılır.
- **Zamanlar:** staff veritabanında zamanlar sabit genişlikli UTC metindir (`2006-01-02T15:04:05.000000000Z`); böylece SQL'deki metin karşılaştırması zaman sırasıyla aynıdır. Önceki biçimdeki kayıtlar da okunur.

**İleride eklenebilecek modüller:** `inventory` (kasada stok; şimdilik stok merkezde düşülür), `promotion` (kampanya motoru), `loyalty` (sadakat).

### Fiscal adapter'ları

`sales`, `FiscalDevice` portu üzerinden aktif fiscal adapter'ı çağırır. Satış fişini adapter basar; böylece "fişi kim basar" sorusu tek bir yerde çözülür.

| Adapter | Ne yapar | Ne zaman |
|---|---|---|
| **simulator** | ÖKC yerine `device` üzerinden normal fiş yazıcısına **mali değeri olmayan fiş** basar, sahte bir mali numara döner (`SIM-000123`) | Geliştirme, demo, eğitim; ÖKC entegrasyonu hazır olana kadar varsayılan |
| **okc-\<marka\>** | Satışı sertifikalı ÖKC'ye kaydettirir; mali fişi ÖKC basar | Türkiye'de canlı kullanım |

- **Sözleşme:** `proto/com/abarow/fiscal/v1` (`FiscalService.Register`). Fiscal adapter'ları yalnızca bu pakete bağlıdır, sales'in iç yapısını bilmez. Fiş metni UTF-8 üretilir; yazıcının kod sayfasına çevirmek `device`'ın işidir.
- **Fiş düzeni:** Simülatör fişi GİB'in YN ÖKC fiş örneğini izler: işletme bilgileri, tarih ve SAAT, FİŞ NO, oranıyla kalemler (`*` önekli tutar), TOPKDV, TOPLAM, ödeme satırları. Mali sembol, EKÜ ve Z numarası gibi yalnızca gerçek mali fişe ait işaretler bilerek basılmaz.
- **Faturalı satış:** VUK Genel Tebliği (Resmi Gazete 8.5.2026) ile YN ÖKC'ler e-Arşiv ve e-Fatura düzenleyebiliyor. Fatura gerektiren tahsilatta bilgi fişi + ayrı fatura yerine e-belgenin kendisi ÖKC'den düzenlenecek. `fiscal.v1` sözleşmesine alıcı bilgisi (VKN/TCKN, unvan, adres) ve belge türü eklenecek; akış ilk gerçek ÖKC entegrasyonuyla tasarlanacak (bkz. TODO.md, Mevzuat).
- **Belgenin adı:** Mali değeri olmayan fiş. Restoranda ödeme öncesi basılan hesap dökümüne zaten "adisyon" denir; markette "satış fişi" ya da "sipariş fişi" denebilir. "Bilgi fişi" adı kullanılmamalı, çünkü bu ÖKC'nin bastığı yasal bir belgenin adıdır.
- **Çekmece:** Para çekmecesi gerçekte ÖKC'ye ya da fiş yazıcısına bağlıdır ve nakitli fiş basılırken açılır. Gerçek ÖKC kendi çekmecesini açar; simülatör, nakitli fişi bastıktan sonra `device`'a çekmeceyi açtırır. Çekmece satış olayıyla açılmaz: mesaj yolu kesikken hiç açılmaz, kesinti bitince eski olaylarla rastgele anlarda açılırdı. Çekmece açılamazsa fiş yine geçerlidir; hata günlüğe yazılır.
- **Fiş genişliği:** Simülatör fişi `device`'taki yazıcının sütun sayısıyla düzenler (`GetStatus`); genişlik tek yerde ayarlanır (`ABAROW_PRINTER_COLUMNS`: 58 mm için 32, 80 mm için 48). Yazıcıdan geniş satır kesilmez ya da kaydırılmaz, belge reddedilir: fişte tutar kayabilirdi.
- **Fiş yazıcısı (ESC/POS):** `device` belgeyi ESC/POS komutlarıyla basar: yazıcıyı sıfırlar (`ESC @`), kod sayfasını seçer (`ESC t`), satırları basar, kağıdı 4 satır ilerletip keser (`GS V 1`). Türkçe için PC857 ya da Windows-1254 kullanılır; `ESC t` numarası yazıcıdan yazıcıya değişebildiği için ayarlanabilir. Kod sayfasında karşılığı olmayan karakter `?`, denetim karakteri boşluk basılır: satır genişliği değişmez, metin yazıcıya komut olarak sızmaz. Yazıcı USB'de (`/dev/usb/lpN`) ya da ağda (TCP 9100) olabilir. Her belge tek bağlantıda gider ve belgeler sırayla basılır. Çekmece yazıcıya bağlıdır ve yazıcıya giden komutla açılır (`ESC p`). Bağlanılamazsa ya da veri 10 saniyede gitmezse hata döner ve kasiyer olası nedeni görür: yazıcı kapalı ya da bağlı değil; kağıt bitmiş ya da kapak açık. USB yazıcının aygıt dosyası yazıcı kapanınca silinir, açılınca yeniden yapılır. Kurulum aynı numarayla kalıcı bir aygıt dosyası yapıp container'a bağlar, böylece yazıcı kapatılıp açılınca `device` yeniden başlamadan basar. Yazıcının durumu 5 saniyede bir ve her basma hatasından sonra `DLE EOT 1, 2, 4` ile sorulur (yazıcı kağıdı bitmişken ya da kapağı açıkken de yanıtlar): kapak açık, kağıt bitti, hata, kağıt azaldı ya da hazır; birden çok sorun varsa en ağırı bildirilir. Bağlanılamayan yazıcı "kapalı ya da bağlı değil"dir; bağlanılıp yanıt alınamıyorsa (durum bildirmeyen ya da tek yönlü yazıcı) durum bilinmiyor kalır ve uyarı çıkmaz. Durum değişince `WatchPrinter` akışıyla ekrana gider; ekran sorun varken üstte uyarı şeridi gösterir ("Fiş yazıcısı: Kağıt bitti: fiş basılamaz."; kağıt azaldıysa sarı "yeni rulo hazırlayın"). Simülatörün durumu testlerde ayarlanır (`POST /api/device/printer`); kağıdı bitmiş simülatör basmaz. Durum operasyona gönderilmez (karar 29.10.2026: kağıdın bittiğini kasiyerin bilmesi yeter).
- **Simülatör fişinin üstünde ve altında "MALİ DEĞERİ YOKTUR" yazar,** UI'da da simülatör modu belirgin şekilde gösterilir. Türkiye'de tüketiciye satışta mali belge zorunlu olduğundan, bu modun canlı satışta tek başına kullanılmaması gerektiği dokümante edilir.

## 6. İletişim ve olaylar

- **UI ↔ gateway:** HTTP/JSON; anlık olaylar (okunan barkod: `scan`; yazıcının durumu: `printer`) için tek yönlü akış: Server-Sent Events (`GET /api/device/scans`). Eski ekran tanımadığı olayı atlar; yazıcı durumunu bilmeyen eski device'ta yalnızca okumalar akar (her servis tek başına güncellenir). WebSocket gerekmedi: olaylar yalnızca sunucudan ekrana gider. Ekran akışı kasiyer oturumunun anahtarıyla (Authorization başlığı) açar; EventSource başlık taşıyamadığı için fetch akışıyla okur.
- **Servisler arası senkron çağrı:** gRPC, podman ağı içinde servis adıyla (`sales:9000`). Kontratlar `proto/` altında.
- **Asenkron olaylar:** NATS JetStream (kendi container'ı, ~20–40 MB). Olaylar diske yazılır, servis yeniden başlasa da kaybolmaz.
- **Katmanlar arası:** NATS leaf node + JetStream sourcing ile kasa → operasyon → merkez (tek kasada kasa → merkez). Bağlantı yoksa olaylar bir alt katmanda birikir, bağlantı gelince iletilir. Kasa → operasyon uygulandı (bkz. bölüm 14, "Kasaların bağlantısı"); operasyon → merkez de (bölüm 15, "Olay alma").
- **Outbox:** Servis kendi verisini ve yayınlayacağı olayı aynı SQLite transaction'ında yazar, ayrı bir relay olayı NATS'a iletir. Böylece "satış kaydedildi ama olay gitmedi" durumu oluşmaz. İçerik protobuf'tur: olay mesajları `proto/com/abarow/sales/v1/events.proto`'dadır (`SaleCompleted` satışın tamamını taşır), `content-type: application/protobuf`. Sözleşme `buf breaking` ile korunur. Kodlamayı sales'te bir adapter yapar (`EventEncoder`); outbox içerik türünü de saklar. Tüketiciler içerik türüne bakar, stream'de kalmış eski JSON olayları da okur.
- **İdempotent tüketim:** Her olayın benzersiz bir ID'si vardır. Tüketiciler (operasyon, merkez) tekrarları bu ID ile ayıklar.
- **Kasada kimlik ve yetki:** NATS kimliksiz bağlantıyı kabul etmez. Her servis kendi NKey'iyle bağlanır (`ABAROW_NATS_NKEY`: gizli anahtar dosyası) ve yalnızca işinin gerektirdiği konulara erişir:
  - **sales:** `sale.>` yayınlar ve stream'i kurar.
  - **staff:** Yalnızca `staff` tüketicisini kurar, okur, onaylar.
  - **register-sim:** Salt okumadır.
  - **Diğer servisler:** NATS kullanmaz, bağlanamaz.

  Böylece örneğin market modülündeki bir açık, sahte bir satış olayıyla Z raporunu bozamaz. Anahtarları ve sunucu yapılandırmasını `tools/natsauth` üretir; yetki tablosu tek yerdedir. Gizli anahtarlar depoya girmez:
  - **Docker simülasyonu:** Açılışta bir volume'a üretilir; her servis yalnızca kendi anahtar klasörünü görür (`subpath`).
  - **Gerçek kurulum:** Podman secret'ı olarak verilir (`deploy/quadlet/README.md`).

  register-sim her çalıştırmada kimliksiz bağlantının ve yetkisiz yayının reddedildiğini denetler.
- **Vardiya olayları (staff):** Vardiya açılışı ve kapanışı, kayıtla aynı transaction'da staff'ın outbox'ına yazılır, relay `ABAROW_STAFF` stream'ine (`shift.>`) iletir. Kapanış olayı vardiya raporunu taşır: satışlar, beklenen ve sayılan nakit, kasa farkı. Rapor kapanıştan önce çıkarılır; çıkarılamazsa vardiya kapanmaz. Olaydaki mağaza ve kasa kimliği `ABAROW_STORE_ID` ve `ABAROW_REGISTER_ID`'den gelir. Kurulu kasada image kendiliğinden güncellenir ama birim dosyası güncellenmez; bu kimlikler yoksa staff çalışır, yalnızca olay yayınlamaz ve günlüğe uyarı yazar. Relay sales ile ortaktır (`pkg/outbox`).
- **Relay (services/core/sales'te uygulandı):** Outbox'ı 500 ms aralıklarla, yazılış sırasıyla `ABAROW_SALES` stream'ine (`sale.>`, disk; süre dolunca kendiliğinden silinmez, bkz. "Teslim makbuzu", bölüm 14) yayınlar. Olay ID'si `Nats-Msg-Id` olarak gider; JetStream 10 dakikalık pencerede tekrarları ayıklar. Olay özellikleri CloudEvents başlıklarıyla (`ce-id`, `ce-type`, `ce-source`, `ce-time`) taşınır. NATS erişilemezken her yayın 5 saniyede zaman aşımına uğrar, uyarı loglanır ve mesajlar outbox'ta bekler; bağlantı gelince sırayla gönderilir.

| Olay | Yayınlayan | Dinleyen |
|---|---|---|
| `sale.completed.v1` | sales | staff (vardiya toplamı), operasyon `report` |
| `sale.voided.v1`, `sale.refunded.v1` | sales | staff, operasyon `report` |
| `sale.charged.v1` (ödeme alındı, fiş kesilemedi) | sales | operasyon `report` (kasada bekleyen satışlar) |
| `catalog.updated.v1` | catalog | gateway, dikey modül (önbellek tazele) |
| `shift.opened.v1`, `shift.closed.v1` (`ABAROW_STAFF`; kapanış vardiya raporunu taşır) | staff | operasyon `report` (vardiyadaki kasiyer, kasa farkı) |
| `order.items_added.v1` | restaurant | device (mutfak yazıcısı/KDS) |
| `node.heartbeat.v1` | sync | operasyon `report` (kasa durumu, bekleyen olay sayısı) |
| (okunan barkod) | device | gateway → UI: kalıcı olay değil, gRPC akışı (`WatchScans`) ve SSE; dinlenmezken okunan kod kaybolur |
| (yazıcının durumu) | device | gateway → UI: kalıcı olay değil, gRPC akışı (`WatchPrinter`) ve SSE |

## 7. Örnek akışlar

### Market: barkod → ödeme → fiş

1. Kasiyer barkodu okutur. `device` okuyucuyu evdev'den okur (aygıt device'a ayrılır: tuşları kiosk tarayıcısına düşmez); kodu `WatchScans` akışıyla verir, gateway SSE ile ekrana iletir, ekran sepete ekler. İmleç nerede olursa olsun kod sepete gider; ödeme sürerken okunan kod eklenmez, ekran bunu söyler. Okutma NATS'a yazılmaz: anlık bir giriştir, kaydı sepettir.
2. UI → `POST /api/module/baskets/{id}/items {barcode}` → gateway → `retail.AddItem`. Terazi barkodunda ağırlık barkodun içinden çözülür; tartılı ürün terazi barkodu olmadan seçilirse (hızlı tuş, ürün barkodu) retail ağırlığı `device`'ın terazisinden okur (`ReadWeight`), terazi yoksa terazi barkodu ister; retail → `catalog.ByBarcode` ile fiyat ve KDV'yi alır.
3. Kasiyer "Öde"ye basar. retail sepeti satır listesine çevirip `sales.CreateSale`'e verir; UI ortak ödeme ekranına geçer.
4. Ödeme ekranı → `POST /api/sales/{id}/checkout {ödemeler}` → `sales.Checkout` → `payment.Charge` → `fiscal.Register`. Fiscal adapter fişi basar ve nakitte çekmeceyi açar: simülatörde `device` üzerinden normal yazıcıya mali değeri olmayan fiş, gerçek ÖKC'de mali fiş.
5. Hepsi başarılıysa satış `paid` olur; satış ve `sale.completed.v1` aynı transaction'da yazılır (outbox).
6. Relay olayı NATS'a yayınlar: staff vardiya ve gün toplamlarına işler, sync satışı operasyona (tek kasada merkeze) iletir.

### Restoran: masa → mutfak → hesap

1. Garson masayı açar ve ürün + opsiyon ekler → restaurant `order.items_added.v1` yayınlar → device ürün kategorisine göre ilgili mutfak yazıcısına basar.
2. Hesap istenince adisyon (mali değeri olmayan hesap dökümü) basılır. Ödeme sırasında restaurant adisyonu (veya bölünmüş bir parçasını) satır listesine çevirip `sales.CreateSale`'e verir.
3. Gerisi marketle aynıdır: checkout → payment → fiscal adapter → `sale.completed.v1`.

## 8. Hexagonal servis yapısı

Bağımlılık yönü: `adapters → app → domain`. `domain` hiçbir dış paketi import etmez. Her servis (çekirdek veya modül) aynı iskeleti kullanır:

```
services/modules/retail/
├── cmd/retail/main.go      # composition root: adapter'ları oluşturur, port'lara bağlar
├── internal/
│   ├── domain/             # Basket, Line, terazi barkodu çözümü (saf Go)
│   ├── app/                # use case'ler + port interface'leri
│   └── adapters/
│       ├── in/http/        # driving: gateway'den gelen istekler
│       └── out/
│           ├── sqlite/     # BasketRepository
│           ├── catalog/    # ProductLookup → catalog gRPC client
│           └── sales/      # Checkout → sales gRPC client
├── web/                    # ui/apps/retail build çıktısı (go:embed)
├── migrations/
└── Containerfile
```

Örnek port'lar ve bir use case:

```go
// internal/app/ports.go
type BasketRepository interface {
	Get(ctx context.Context, id domain.BasketID) (*domain.Basket, error)
	Save(ctx context.Context, b *domain.Basket) error
}

type ProductLookup interface { // catalog servisi
	ByBarcode(ctx context.Context, barcode string) (domain.ProductSnapshot, error)
}

type Checkout interface { // sales servisi
	CreateSale(ctx context.Context, lines []domain.SaleLine) (domain.SaleID, error)
}

// internal/app/add_item.go
func (s *Service) AddItem(ctx context.Context, id domain.BasketID, barcode string) (*domain.Basket, error) {
	b, err := s.baskets.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	code, qty := domain.ParseScaleBarcode(barcode) // terazi barkodu değilse miktar 1 adet
	p, err := s.products.ByBarcode(ctx, code)
	if err != nil {
		return nil, err
	}
	if err := b.AddLine(p, qty); err != nil { // iş kuralı domain'de
		return nil, err
	}
	return b, s.baskets.Save(ctx, b)
}
```

### services/core/sales: ödeme, mali kayıt ve fiş numarası

Satış durumları: `open` → `charged` (ödeme alındı, mali kayıt bekleniyor) → `paid`; ödemesi alınmamış satış `voided` olabilir. `services/core/sales`'in repository port'u bir unit of work'tür: `Repository.InTx(ctx, func(tx Tx) error)`.

Checkout akışı:

1. Satış okunur, ödemeler domain'de kontrol edilir (kart çekilmeden önce). Fazla ödeme yalnızca nakitle olur; fazlası para üstüdür.
2. `PaymentGateway.Charge` ödemeyi alır ve her ödemeyi referansıyla (`Ref`) döner. Ödemeler hemen satışa işlenir, satış `charged` olur.
3. `FiscalDevice.Register` mali kaydı yapar ve fişi basar.
4. Tek transaction'da `NextReceiptNo` ile fiş numarası ayrılır, satış `paid` olur; satış ve `sale.completed.v1` olayı outbox'a birlikte yazılır.

- **Aynı ödemeyle eşleştirme:** Mali kayıt başarısız olursa satış `charged` kalır. Checkout tekrar denendiğinde istekteki ödemeler yok sayılır ve kayıtlı ödemeyle devam edilir; müşteriden ikinci kez ödeme alınmaz. `charged` satış iptal edilemez.
- **Kaybolan cevap:** Ödeme alınıp cevap sales'e ulaşmazsa satış `open` kalır. Bu yüzden payment servisi `Charge`'ı satış kimliğine göre idempotent uygulamalıdır: aynı satış için yeni ödeme almaz, önceki sonucu döner.
- **Süre sınırları ve donmuş servis:** Her gRPC çağrısı kendi süre sınırını taşır; zincirin dış halkası iç halkaların toplamından uzundur: kapı (gateway) ödeme isteği 3 dakika, `payment` 90 sn (müşteri kartta şifre girer), `fiscal` 30 sn, sonucun yazılması en çok 30 sn. Dış işlem (kart çekme, fiş kesme, geri ödeme) bittikten sonra satışın sonucu istek bağlamından ayrı bir bağlamla yazılır: kasiyer sayfayı kapatsa ya da kapının süresi dolsa bile fiş kesilmiş satış açık kalmaz, ödeme kaydı kaybolmaz. Kapının süresi dolarsa kasiyere "ödeme alınmış olabilir, tekrar ödeme almadan önce satışın durumunu denetleyin" denir. Donmuş ama bağlantısı açık kalan bir servis (süreç durdurulmuş, ağ yarım kopmuş) için bağlantılar canlılık denetimiyle (`service.Dial`/`service.NewGRPCServer`: 10 sn'de bir ping, 5 sn'de yanıt yoksa kapanır) izlenir; bekleyen çağrılar UNAVAILABLE ile biter. `staff`'ın yazıcı, çekmece ve `sales` sorgularına da kendi süre sınırı konmuştur.
- Fiş numarası mali kayıt başarılı olduktan sonra ayrıldığı için başarısız denemeler numarada boşluk bırakmaz; operasyondaki eksik fiş tespiti yanlış alarm vermez.
- **Kasada görünür:** `charged` satışlar `ListSales` ile listelenir; ekran, sepet boşken "Ödemesi alınmış ama fişi basılamamış N satış var" uyarısını ve "Fişi tamamla" düğmesini gösterir (ödeme penceresi "Fişi tekrar dene" durumunda açılır). Pencere "Sonra tamamla" ile kapatılabilir; kasiyer yeni satışa geçer, uyarı kalır.
- **Operasyonda görünür:** Mali kayıt başarısız olunca sales, satış `charged` kalırken `sale.charged.v1` olayını (tutar, kasiyer, neden) outbox'a yazar. Mağaza müdürü bunu operasyonun kasa kartında görür (bkz. bölüm 14). Başarılı ödemede bu olay yayınlanmaz.
- **Kapanış engeli:** `charged` satış varken gün sonu (Z) ve vardiya kapanışı yapılmaz (409): mali belgesiz satış rapora girmez, çekmecedeki para fazla görünürdü.
- **Ödemenin iadesi:** Mali kayıt yapılamıyorsa (ör. ÖKC uzun süre arızalı, müşteri beklemiyor) `charged` satışın ödemesi geri verilir (`RefundCharged`): payment kart işlemlerini POS'tan geri alır, nakdi kasiyer çekmeceden verir; satış `voided` ve `refunded` olur, `sale.voided.v1` olayı `refunded: true` taşır. Önce ödeme geri verilir; başarısız olursa satış `charged` kalır ve tekrar denenebilir (geri alınan kart işlemi tekrar alınmaz). Fiş hiç kesilmediği için mali belge gerekmez; raporda ödemesi alınmadan iptal edilen satış gibi sayılır, ödeme hiç gün ya da vardiya toplamına girmediği için beklenen nakit değişmez. Para geri vermek müdür işidir: gateway önce staff'tan yetki alır (`Authorize`: müdür oturumu ya da müdür onayı), sonra sales'i çağırır. Ekran, nakdin ne kadarının verileceğini söyler (para üstü verildiyse alınan − para üstü).
- **Mal iadesi** (fişi kesilmiş satışın iadesi) ayrı bir akıştır ve belgelenmesi mevzuata bağlıdır; henüz yok (aşağıda "Açık sorular").
- **Hata mesajları:** Kasa içindeki servisler birbirine `service.Dial` ile bağlanır; yeniden bağlanma beklemesi en çok 2 saniyedir, servis geri gelince ilk "tekrar dene" tutar. Servisin adı her bağlanışta yeniden çözülür (gRPC'nin DNS çözücüsü adı en sık 30 saniyede bir sorar): yeniden başlayan container yeni IP alır, eski adreste aranırsa güncellemeden ya da yeniden kurulumdan sonra yarım dakikaya yakın "yanıt vermiyor" görünürdü. Servise hiç ulaşılamazsa (`service.RemoteErrorFrom`) kasiyere gRPC'nin teknik mesajı değil "yazıcı yanıt vermiyor", "ÖKC yanıt vermiyor" gibi bir mesaj gider; servisin kendi mesajları olduğu gibi kalır.

## 9. Para ve miktar

### Neden `int64` kuruş?

- `float64` ondalık sayıları tam tutamaz: `0.1 + 0.2 = 0.30000000000000004`. Yüzlerce satır, KDV ayırma ve indirim hesabında kuruş farkları birikir ve Z raporu ÖKC ile tutmaz.
- Kuruş cinsinden tam sayı (`12,99 TL → 1299`) hem tam hem hızlıdır. `int64` sınırı ~92 katrilyon TL'dir.
- UI'ye de tam sayı gider. JavaScript'te güvenli tam sayı sınırı 2^53 kuruş (~90 trilyon TL), yeterli. TL'ye çevirme yalnızca gösterimde yapılır: `Intl.NumberFormat('tr-TR', { style: 'currency', currency: 'TRY' })`.
- Veritabanında SQLite `INTEGER`, PostgreSQL `BIGINT` olarak saklanır.
- **Oranlar baz puan:** KDV %20 = `2000`. **Miktar binde bir birim:** 1 adet = `1000`, 1,250 kg = `1250`.
- **Yuvarlama kuralı tek yerde** (`pkg/money`): yarım, sıfırdan uzağa yuvarlanır. Çarpma ve bölme 128 bitlik ara sonuçla yapıldığı için taşma olmaz.
- **KDV oran bazında hesaplanır:** Her KDV oranının KDV dahil toplamı üzerinden hesaplanır; fişte toplamları TOPKDV olarak basılır. Bu, GİB'in YN ÖKC düzeniyle uyumludur: Z raporu KDV'yi oran bazında toplar; fiş örneğinde 12,00 TL (%1) + 22,00 TL (%10) + 15,00 TL (%20) için TOPKDV 4,62, TOPLAM 49,00'dır ve bu örnek testlerde birebir doğrulanır. Satır satır hesaplayıp toplamak kuruş farkı çıkarır (üç tane 0,10 TL'lik %20'lik üründe 6 yerine 5 kuruş).
  - Kaynak: GİB, *Yeni Nesil Ödeme Kaydedici Cihazlardan Düzenlenecek Fiş, e-Belge ve Diğer Belgeler ile Rapor Formatları Teknik Kılavuzu Taslağı*, Sürüm 1.0 (bölüm 2 fiş örnekleri, bölüm 5.3 Z raporu; öngörülen yürürlük 1.7.2026). Taslak olduğundan kesinleşmiş sürümle yeniden kontrol edilmeli.
  - Gerçek ÖKC entegre edildiğinde resmi KDV tutarlarını ÖKC hesaplar. ÖKC adapter'ı, ÖKC'nin döndürdüğü toplamları sales'in hesabıyla karşılaştırıp farkı bildirmelidir.
  - Kuruş yuvarlaması için resmi bir hüküm bulunamadı; yaygın uygulama olan "yarım yukarı" (sıfırdan uzağa) kullanılıyor.
- Alternatif olarak `shopspring/decimal` gibi decimal kütüphaneleri de tam sonuç verir, ama daha yavaştır, her işlemde bellek ayırır ve JSON/DB'de string taşımayı gerektirir. Ara hesaplarda (ör. zincirleme yüzde indirim) kullanılabilir; saklanan ve taşınan değer yine kuruştur.

```go
type Money int64 // kuruş
type Rate int64  // baz puan: %20 = 2000

// IncludedTax, KDV dahil fiyattan KDV tutarını ayırır: brüt × oran / (10000 + oran).
// 120,00 TL, %20 → 20,00 TL
func (m Money) IncludedTax(r Rate) Money {
	return Money(divRound(int64(m)*int64(r), 10000+int64(r)))
}

// divRound yarımı sıfırdan uzağa yuvarlar; iadelerdeki negatif tutarlarda da doğru çalışır.
func divRound(num, den int64) int64 {
	if (num < 0) != (den < 0) {
		return (num - den/2) / den
	}
	return (num + den/2) / den
}
```

## 10. Veritabanı

| # | Seçenek | Kasada RAM | Artıları | Eksileri | Karar |
|---|---|---|---|---|---|
| 1 | **SQLite** | ~0 (kütüphane, ayrı süreç yok) | Gömülü; servis başına bir dosya, veri sahipliğiyle birebir örtüşür; yedek = dosya kopyası; CGO'suz Go sürücüsü (`modernc.org/sqlite`) | Tek yazar (kasa için sorun değil); ağ üzerinden paylaşılamaz | **Kasa** |
| 2 | **PostgreSQL** | boşta ~50 MB, yükte 100–200 MB | En güçlü SQL, JSONB, raporlama; varsayılan ayarları zaten küçük makinelere göre | Ayrı container; sahadaki yüzlerce cihazda major sürüm yükseltmesi zahmetli | **Operasyon ve merkez** (karar 04.11.2026) |
| 3 | **MariaDB / MySQL** | 100–250 MB | Türkiye'de yaygın bilinir | PostgreSQL'e göre bir avantajı yok | Gerek yok |
| 4 | **MongoDB** | sınırlanmazsa ~1,5 GB (WiredTiger önbelleği RAM'in yarısını alır) | Esnek şema | Çoklu doküman transaction'ı için replica set şart; 5.0+ ARM'da ARMv8.2 (RPi 4'te çalışmaz), x86'da AVX ister; SSPL lisanslı (OSI onaylı değil, Debian depolarında yok); POS verisi zaten ilişkisel | **Önerilmez** |
| 5 | **Gömülü KV** (bbolt, Pebble) | çok düşük | Saf Go, çok hızlı | SQL yok; indeks, sorgu ve rapor elle yazılır | Yalnızca özel işler (kuyruk, önbellek); NATS KV zaten var |

**Karar: üç katmanda da PostgreSQL** (operasyon 04.11.2026'da, kasa 08.11.2026'da SQLite'tan geçti: tek veritabanı teknolojisi, ortak SQL, `pg_dump` yedeği; bedeli her kasada küçük bir container, zirve 55 MiB; bkz. [STORE-POSTGRES.md](decisions/STORE-POSTGRES.md), [REGISTER-POSTGRES.md](decisions/REGISTER-POSTGRES.md)). Kasada her servis kendi şemasında ve tek bağlantıyla çalışır; fiş, Z ve olay numaraları işlemle geri alınan sayaç satırından gelir.

- Kasada PostgreSQL'in asıl bedeli RAM değil, operasyondur: bir container daha, cihaz başına yedek/geri yükleme, sahada ana sürüm yükseltmesi.
- Kasa servisleri ile merkez servisleri zaten farklı kod (`sales` ≠ `reporting`), bu yüzden iki SQL lehçesi aynı servis içinde çakışmaz.
- Operasyonun veritabanı mağaza başına tek PostgreSQL container'ıdır (`abarow-store-db`); report ve inventory ayrı tablolarla aynı veritabanını kullanır. Hexagonal yapının getirisi budur: depolama adaptörü değişti, servislerin iş kodu değişmedi.

**PostgreSQL'i hafif çalıştırmak:** `postgres:18-alpine` image'ı yeterli. Varsayılan ayarlar (`shared_buffers=128MB`, `work_mem=4MB`) zaten küçük makinelere göredir. Asıl dikkat edilecek şey bağlantı sayısı: her bağlantı ayrı bir süreçtir (~5–10 MB). Bu yüzden her servisin pgx havuzu küçük tutulur (`MaxConns` 4–8), `max_connections` 50 civarında bırakılır. (PGlite adında gömülü bir PostgreSQL var ama WASM tabanlı ve JS/Node için; Go backend'e uygun değil.)

### Kütüphaneler

| İhtiyaç | Seçim | Neden |
|---|---|---|
| Sorgular | `sqlc` | SQL'den tip güvenli Go kodu üretir, ORM yükü yok (SQLite ve PostgreSQL destekli) |
| Migration | `goose` | Basit, binary'ye gömülebilir; servis açılışta kendi migration'larını çalıştırır |
| RPC | gRPC + protobuf (`buf` ile kod üretimi) | Tipli kontratlar, `buf breaking` ile uyumluluk kontrolü |
| Olaylar | NATS JetStream | Hafif, kalıcı stream, leaf node ile katmanlar arası köprü |
| Log | `log/slog` (standart kütüphane) | Ek bağımlılık yok, journald'a yazar |

## 11. Önyüz

### Öneri: Svelte 5 + Vite, cage üzerinde Chromium kiosk

- Svelte derleme zamanında sade JavaScript'e dönüşür. Sanal DOM yoktur ve runtime çok küçüktür, bu yüzden zayıf ARM işlemcide bile akıcı çalışır.
- Cihazda Node.js bulunmaz. `vite build` çıktısı modül image'ına `/web` olarak konur (`deploy/container/module-ui.Containerfile`) ve modül servisi bu dizini sunar. `go:embed` yerine dizin kullanılmasının nedeni, Go derlemesinin ve testlerinin ekran derlemesine bağımlı olmamasıdır; image yine tek parçadır.
- Aynı UI tablette, arka ofiste veya uzaktan destekte tarayıcıdan açılabilir.
- Chromium, ARM Linux'ta GPU hızlandırması en olgun web motorudur ve 4 GB'a rahat sığar (~300–500 MB).

UI da çekirdek + modül olarak bölünür:

```
ui/
├── packages/core/          # ortak: numpad, ödeme ekranı, giriş, ayarlar, fiş önizleme, i18n
├── packages/admin/       # yönetim ekranlarının ortağı (operasyon, merkez): biçim, stok girişi, transfer, rapor parçaları, görünüm
├── apps/retail/            # market ekranları → services/modules/retail container'ına gömülür
├── apps/restaurant/        # restoran ekranları → services/modules/restaurant container'ına gömülür
├── apps/store/         # mağaza raporları ve kasa durumu → operasyon container'ına gömülür
└── apps/headquarters/        # merkez arayüzü
```

Kiosk başlatma (host'ta, systemd ile otomatik):

```sh
cage -- chromium --kiosk --ozone-platform=wayland --noerrdialogs \
  --disable-pinch --overscroll-history-navigation=0 http://127.0.0.1:8080
```

### POS UI kuralları

- Dokunma hedefleri en az 48 px, ana butonlar 64 px ve üstü olmalı. Ağır animasyon, gölge ve blur kullanılmaz.
- Büyük ürün listelerinde sanal liste (virtual list) kullanılır.
- UI durum sahibi değildir: tek gerçek kaynak backend'dir, UI WebSocket olaylarıyla güncellenir.
- **Kaldığı yerden devam:** Sepetler modülün veritabanında kasiyere bağlı saklanır (`retail.db`). Ekran, tarayıcı ya da modül yeniden başlarsa kasiyer son sepetinden devam eder (`GET /api/module/baskets/current`); sepet ödemeye geçmiş ve satış ödenmemişse (`open`/`charged`) ödeme penceresi geri gelir, böylece müşteriden ikinci kez ödeme alınmaz. 12 saatten (oturum süresi) eski sepetten devam edilmez; 7 günden eski sepetler silinir. Satışın kalıcı kaydı sales'tedir; sepet kasiyerin çalışma kopyasıdır.
- **Sıralı değişiklik:** Sepet değişiklikleri (okutma, miktar, silme, ödemeye geçiş) sırayla uygulanır. Hızlı barkod okuyucuda okutmalar aynı anda gelir; sıra olmasa ikisi de sepeti eski haliyle okuyup kaydeder ve biri kaybolurdu (müşteriye eksik tutar yansırdı). Ödemeye geçerken gelen okutma, satışı oluşturulmuş sepete girmez, reddedilir. Bu hata CI'daki tarayıcı testinde arada bir görüldü (aynı barkod iki kez okutulunca sepette tek adet).
- Barkod okuyucu klavye gibi değil `device` servisi üzerinden okunur, böylece odak (focus) sorunu olmaz.
- Fontlar ve ikonlar yerel olarak gömülür; internete bağımlılık yoktur. Yazı tipi Inter'dir (SIL Open Font License 1.1; lisans metni ekranla birlikte `/licenses/Inter-OFL.txt`, NOTICE'ta da anılır). Yalnızca latin ve latin-ext alt kümeleri paketlenir (~130 KB): Türkçe harfler ve ₺ (U+20BA) bunlarda.

### Alternatifler

| Seçenek | Ne zaman | Dikkat |
|---|---|---|
| Flutter (Linux embedder, ARM'da flutter-pi gibi) | Daha native his ve daha düşük RAM (~100–150 MB) istenirse | Dart; UI web'de yeniden kullanılamaz |
| Fyne / Gio (saf Go) | Tek dil istenirse | Özel dokunmatik tasarım daha zahmetli, ekosistem küçük |
| Wails / Tauri | Masaüstü uygulaması gibi paketlemek istenirse | Linux'ta WebKitGTK kullanır; ARM'da GPU hızlandırması karta göre değişir |
| Electron | Önerilmez | Chromium ve Node birlikte gelir, gereksiz ağır |

Seçimi kesinleştirmeden önce hedef kartta basit bir ürün ızgarası + sepet ekranıyla açılış süresi ve kaydırma akıcılığı ölçülmeli.

## 12. Container ve dağıtım

### Öneri: Podman + Quadlet

- **Daemon yok:** Docker'ın arka plan servisleri (dockerd + containerd) ~60–100 MB tutar. Podman'da her container için yalnızca küçük bir `conmon` süreci (~1–2 MB) olur.
- **systemd ile yerel entegrasyon:** Quadlet'te her container bir `.container` dosyasıdır ve systemd onu servis olarak çalıştırır. Yeniden başlatma, bağımlılık sırası ve journald logları hazır gelir.
- **Servis bazında, onaylı güncelleme:** Her servis tek başına güncellenir, ama kendiliğinden değil (karar 06.11.2026): yeni sürüm merkez, operasyon ve kasa ekranında bildirilir ve her katmanda ayrı ayrı onaylanınca uygulanır ([UPDATE.md](UPDATE.md)). Otomatik güncelleme (`AutoUpdate=registry`, `podman-auto-update.timer`) kaldırıldı.
- Docker Compose tercih edilirse aynı image'lar çalışır. Geliştirmede, operasyonda ve merkezde Compose da kullanılabilir.

Kasanın Quadlet dosyaları `deploy/quadlet/` altındadır. Kurulum `deploy/quadlet/README.md`'de anlatılır: root olmadan, ayrı bir kullanıcının systemd'siyle çalışır. Örnek:

```ini
# ~/.config/containers/systemd/abarow-sales.container (kısaltılmış)
[Unit]
Wants=abarow-nats.service
After=abarow-nats.service

[Container]
Image=ghcr.io/abarow/abarow-sales:0.1
Network=abarow.network
Volume=abarow-sales-data:/data
Environment=GOMEMLIMIT=96MiB
HealthCmd=["/service", "healthcheck"]
Notify=healthy
PodmanArgs=--memory=128m

[Service]
Restart=always

[Install]
WantedBy=default.target
```

- **`Wants=`, `Requires=` değil:** `Requires=` ile bir servis yeniden başlarken (güncelleme) ona bağlı servisler de durur; sales güncellenirken gateway ve kasa ekranı kapanırdı. Servisler bağımlılık kesintisinde bağlantıyı kendileri yeniler (`service.Dial`). Açılış sırasını `After=` ve `Notify=healthy` sağlar: birim, container'ı sağlıklı olunca etkin sayılır.
- **Dış ağ beklenmez:** `[Quadlet] DefaultDependencies=false` Podman'ın "ağ çevrimiçi" beklemesini kaldırır. Bağlantısız kasada bu bekleme her birim için 90 sn sürerdi; servisler yalnızca yerel ağı kullanır.
- **Operasyon kurulumu:** `deploy/install/store-install.sh` operasyonu kurar (Quadlet birimleri `deploy/quadlet/store`): mağaza parolası, NATS anahtarları ve leaf TLS'i, servisler. `register-add` ve `register-remove` kasa listesini değiştirip NATS yapılandırmasını yeniler. Kasa kurulumu `--store` ve `--store-ca` ile operasyona bağlanır ve kasanın anahtarını yazdırır. İki betik ortak işlevleri `deploy/install/common.sh`'tan alır.
- **Kurulum betiği:** `deploy/install/register-install.sh`. Ön koşulları denetler: Podman 5, aardvark-dns, nftables. Sonra kullanıcıyı, Quadlet dosyalarını, başlangıç verisini ve NATS anahtarlarını (secret) kurar; servisleri başlatıp her birinin çalıştığını denetler; kiosku (cage + Chromium, tty1) kurar.
- **`HealthCmd` JSON dizisidir:** İmajlarda kabuk yoktur (`scratch`). Düz metin verilirse Podman komutu `/bin/sh -c` ile çalıştırır ve sağlık denetimi hep başarısız olur.
- **Denendi:** Dosyalar Podman 5.4'te systemd ile denendi (26.10.2026). Soğuk açılışta on birim ~10 sn'de sırayla sağlıklı açıldı, satış uçtan uca çalıştı, bir servisin yeniden başlaması ya da durması diğerlerini etkilemedi.

| Host'ta (Debian minimal) | Container'da |
|---|---|
| systemd, Podman, cage + Chromium (GPU ve ekrana doğrudan erişim için), udev kuralları | nats, gateway, catalog, sales, payment, staff, device, sync, dikey modül, fiscal adapter |

- **Çevre birimleri (karar 29.10.2026):** Fiş yazıcısı (raporlar, simülatör fişi, ileride mutfak fişi; satış fişini gerçek ÖKC basar) ESC/POS ile hem USB hem ağ (TCP 9100) üzerinden; genişlik kurulumda (58 mm 32, 80 mm 48 sütun). Barkod okuyucu USB klavye gibi çalışan okuyucudur; `device` onu evdev'den okur ve cihazı kendine ayırır (tuşları ekrana düşmez). Terazi iki yolla: reyonda barkod basan terazinin etiketi (bugünkü gibi) ve kasada seri port terazi (tartılı ürün seçilince ağırlık okunur). Müşteri ekranı HDMI ikinci ekrandır (sepet, tutar, para üstü).
- **Barkod okuyucu (uygulandı, gerçek okuyucuyla denenmedi):** `ABAROW_SCANNER=evdev:/dev/...`: device aygıtı açar, `EVIOCGRAB` ile kendine ayırır, tuş kodlarını ABD klavye düzeniyle karaktere çevirir (okuyucular kutudan böyle çıkar), kod Enter, sayısal takım Enter'ı ya da Tab'la biter. İki tuş arası 500 ms'yi geçerse yarım okuma, bilinmeyen tuş ya da 128 karakterden uzun kod atılır (ekrana bozuk kod gitmez). Aygıt yoksa ya da koparsa 2 saniyede bir yeniden açılır. `simulator` (varsayılan) okutmayı `POST /api/device/scans` ile taklit eder (testler); gerçek okuyucuda bu 409'dur. Okuyucu verilmemiş kasada klavye gibi barkod alanına yazan okuyucu da çalışır.
- **Terazi (uygulandı, gerçek teraziyle denenmedi):** Reyondaki terazinin bastığı barkod (ağırlık barkodda) sürer. Kasaya seri portla (RS-232 ya da USB-seri çevirici) bağlı terazi de olabilir: `ABAROW_SCALE=toledo8217:/dev/...` ya da `nci:/dev/...`, port ayarı `ABAROW_SCALE_SERIAL` (varsayılan `9600,7E1`). Kasa terazilerinin çoğu bu iki protokolden birini konuşur ya da ayarından birine geçirilir. Toledo 8217'de `W` sorulur, yanıt `<STX>01.250<CR>` ya da durum (`<STX>?durum<CR>`: hareket, kapasite, eksi, sıfır); NCI ECR'de `W<CR>` sorulur, yanıt `<LF>01.250KG<CR><LF>Shh<CR><ETX>` ya da yalnızca durum baytları. Ağırlık ondalık noktayla ve kilogram (NCI'de `KG` ya da `GM`) olmalıdır; noktasız ya da başka birimdeki yanıt reddedilir: birimi belli olmayan ağırlık tutarı yanlış yapardı. Tartılı ürün seçilince retail `device`'a sorar; terazi sallanıyorsa en çok 3 saniye durulması beklenir (200 ms'de bir okuma). Boş, eksi, kapasitesi aşılmış ya da hata bildiren terazide ürün eklenmez ve kasiyer nedeni görür. Port ilk tartımda açılır, iletişim hatasında kapatılıp sonraki tartımda yeniden açılır; USB-seri çevirici için kurulum yazıcıdaki gibi kalıcı aygıt dosyası yapar (`dialout` grubu). Tartım sepet kilidinin dışındadır: terazi durulurken okuyucudan gelen barkodlar beklemez. `simulator`'ın ağırlığı testlerde ayarlanır (`POST /api/device/weight`).
- **Müşteri ekranı (sayfa uygulandı; iki ekranlı kiosk henüz yok):** HDMI ikinci ekranda `/musteri.html` açılır: boş sepette "Hoş geldiniz", okutulan satırlar (miktar × birim fiyat, tutar; son satır kalın), büyük toplam; ödemede "Ödenecek tutar"; satış bitince toplam, ödenen, para üstü ve "Teşekkür ederiz"; kasiyer çıkınca "Bu kasa kapalı". Simülatörde müşteriye de "basılan fişlerin mali değeri yoktur" yazar. Durum kasiyer ekranından aynı tarayıcıdaki müşteri penceresine BroadcastChannel ile gider: sunucu, oturum ve olay gerekmez; müşteri ekranı kasiyerin gördüğünden fazlasını bilmez. Pencere sonradan açılırsa son durumu ister. Müşteri ekranı dokunmatik değildir. Kiosk tek ekranda cage'dir; cage tek pencere gösterdiği için müşteri ekranı verilince (`register-install.sh --register-screen --customer-screen`) sway kullanılır: tek Chromium süreci iki pencere açar, pencereler sayfa başlıklarıyla çıkışlarına yerleşir ve tam ekran olur (Chromium tam ekran değilken sekme ve adres çubuğunu gösterir; müşteri penceresi taşınınca kasa penceresi yeniden tam ekran yapılır). Dokunmatik kasa çıkışına eşlenir; müşteri penceresi odak almaz, klavye ve okuyucu kasa penceresine yazar. Kasa sayfaları makine çevirisine kapalıdır (`translate="no"`): sistem dili Türkçe değilken Chromium'un çeviri önerisi ekranı kapatıyor ve kasiyer çeviriyi açabiliyordu.
  - **Kurulum:** `register-install.sh --barcode-scanner /dev/input/by-id/...-event-kbd`. device `/dev/input`'u salt okunur bağlar (`AddDevice` değil): okuyucu çekilip takılınca yeni aygıt da görünür, okuyucu takılı değilken container yine açılır (yazıcı çalışır). `abarow` `input` grubuna eklenir, grup container'a geçer (`GroupAdd=keep-groups`); grup eklenince kullanıcının systemd oturumu yeniden başlatılır. Gerçek kurulum testinde sahte karakter aygıtıyla izin (ENXIO, EACCES değil) ve FIFO'yla okuma denendi; gerçek okuyucuyla denenmedi.
- **Donanım erişimi:** `device` container'ına cihazlar `AddDevice=` ile verilir (ör. `/dev/usb/lp0`, `/dev/ttyUSB0`, barkod okuyucunun `/dev/input` girişi). udev kurallarıyla sabit isimler verilir (`/dev/abarow-printer`), böylece USB portu değişse de yapılandırma bozulmaz.
- **Image'lar:** Go statik binary + `FROM scratch`, servis başına ~20–25 MB (SQLite sürücüsü binary'yi büyütür). Go çapraz derlediği için multi-arch (amd64 + arm64) image'lar emülasyon olmadan üretilir ve GHCR'de yayınlanır.
- **Sürüm:** `vX.Y.Z` etiketi push edilince `.github/workflows/release.yml` her servisin imajını amd64 ve arm64 için derler ve iki etiketle yayınlar: sabit sürüm (`X.Y.Z`) ve kanal (`X.Y`). Kasalardaki Quadlet birimleri kanal etiketine bağlıdır; yeni yama ekrandan onaylanınca servis servis uygulanır. Kırıcı değişiklik yeni kanal (`X.Y+1`) demektir; kasa kanalı bilerek değiştirilir.

### Bağımsız güncelleme kuralları

Servisleri tek tek güncelleyebilmenin şartı, sözleşmelerin geriye uyumlu kalmasıdır:

- **gRPC:** protobuf'a yalnızca alan eklenir; alan silinmez, numarası değiştirilmez. CI'da `buf breaking` kontrol eder.
- **Olaylar sürümlüdür** (`sale.completed.v1`). Kırıcı değişiklikte `v2` bir süre `v1` ile birlikte yayınlanır.
- **Migration'lar geriye uyumludur (expand/contract):** Yeni sürüm geri alınırsa eski sürüm yeni şemayla çalışabilmeli. Kolon önce eklenir, eski kolon ancak sonraki sürümlerde kaldırılır.
- **Bundle:** Birlikte test edilmiş sürümler bir manifest olarak yayınlanır (ör. `abarow-2026.11`). Kurulumcular bunu kullanır; tek servis güncellemesi yine serbesttir.

### Docker simülasyonu (geliştirme)

Bir kasa, geliştirme makinesinde Docker Compose ile taklit edilir (`deploy/compose/compose.yaml`): gateway, market modülü (ekranıyla), catalog (örnek veriyle), sales, payment (POS simülatörüyle), staff (kasiyer, vardiya, gün sonu), ÖKC simülatörü, device (yazıcı ve çekmece simülatörü; basılan belgeler `docker compose logs device`) ve NATS JetStream, kasadaki bellek sınırlarıyla çalışır. Kasa ekranı `http://localhost:8088` adresinden açılır. `tools/register-sim` kasiyerin yapacağı işlemleri sırayla dener ve sonuçları denetler.

```sh
docker compose -f deploy/compose/compose.yaml up --build -d
docker compose -f deploy/compose/compose.yaml run --rm register-sim
docker compose -f deploy/compose/compose.yaml logs fiscal   # basılan fişler
docker compose -f deploy/compose/compose.yaml --profile ui-test run --rm ui-smoke   # tarayıcı testi
```

- **Tarayıcı testi (`tools/ui-smoke`):** Playwright ile ekranı gerçek bir tarayıcıda kasiyer gibi kullanır: barkod, terazi barkodu, hızlı tuş, bilinmeyen barkod uyarısı, nakit ödeme ve para üstü. Her adımda `tools/ui-smoke/out/` altına ekran görüntüsü alır.

- **Senaryolar:** nakit satış ve para üstü, GİB örnek fişi (nakit + kart), ödenmemiş satışın iptali, eksik ödemenin reddi, limiti aşan kartın reddedilip satışın nakitle tamamlanması, gateway üzerinden market akışı, olayların NATS JetStream'e ulaşması, X raporunun çalıştırmanın satışlarını birebir tutması ve Z'nin günü kapatması.
- **Mesaj yolu kesintisi:** `docker compose stop nats` sonrası `ABAROW_SIM_SKIP_EVENTS=1 docker compose run --rm --no-deps register-sim` ile satış sürer; `docker compose start nats` sonrası bekleyen olaylar gönderilir ve `register-sim` bunu doğrular.
- **ARM kasa:** `DOCKER_DEFAULT_PLATFORM=linux/arm64` ile image'lar arm64 için çapraz derlenir ve emülasyonda çalışır.
- **Imaj tarifi:** Tüm Go servisleri tek bir tarif kullanır (`deploy/container/go-service.Containerfile`): `scratch`, root olmayan kullanıcı, `/service healthcheck` ile sağlık kontrolü.

## 13. RAM bütçesi (kasa, tahmini)

| Bileşen | RAM |
|---|---|
| Debian minimal + systemd | 150–250 MB |
| cage + Chromium (tek sayfa, kiosk) | 320–520 MB |
| Podman (daemon yok; container başına conmon) | 10–20 MB |
| NATS server (JetStream) | 20–40 MB |
| 8 Go servisi (her biri 10–30 MB) | 80–240 MB |
| SQLite sayfa önbellekleri | 10–50 MB |
| **Toplam** | **~0,6–1,1 GB** |
| *(Kasa operasyon görevini de üstlenirse)* | *+100–200 MB* |
| *(PostgreSQL kasada çalışsaydı)* | *+100–200 MB* |

Docker simülasyonunda ölçülen değerler (25.10.2026): NATS 8,1 MiB, sales 7,4 MiB, gateway 6,4 MiB, staff 4,6 MiB, retail 4,5 MiB, catalog 4,4 MiB, payment 3,7 MiB, ÖKC simülatörü 3,0 MiB; 8 container toplam ~42 MiB, tahminin çok altında. Sonradan eklenen device 3,2 MiB (26.10.2026). Kasa ekranı ~19 KB (gzip) JavaScript.

4 GB'ın yaklaşık 3 GB'ı boşta kalır. ARM'da asıl darboğaz RAM değil, CPU/GPU'nun çizim performansıdır; bu yüzden UI tarafında hafif framework ve sade tasarım önemlidir.

## 14. Operasyon (mağaza sunucusu)

Birden fazla kasası olan yerlerde mağazaya bir operasyon kurulur. Kasalar merkeze değil operasyona bağlanır; operasyon mağazanın ara belleği ve rapor noktasıdır.

> **Adlandırma:** Bu dokümanda mağaza katmanı **operasyon**, merkez katmanı **merkez** olarak geçer.

### Görevleri

1. **Ara bellek (store-and-forward):** Kasalardan gelen olaylar operasyondaki NATS JetStream'de diske yazılır ve sırayla merkeze iletilir. İnternet kesilirse olaylar birikir, bağlantı gelince gönderilir. Olay, merkezin PostgreSQL'ine işlendiği makbuzla kanıtlanmadan silinmez; makbuzdan sonra da en az 90 gün (ayarlanabilir) saklanır: yerel raporlar ve gerekirse yeniden gönderim için (bkz. "Teslim makbuzu").
2. **Mağaza raporları:** İnternet olmasa da mağaza müdürü anlık ciroyu, register/kasiyer/ürün bazında satışları, saatlik dağılımı ve birleşik gün sonu raporunu görebilir.
3. **Eksik fiş tespiti:** Her kasanın fiş numarası artarak gider. Operasyon numaralardaki boşlukları yakalar ("Kasa 3: 1042–1045 henüz gelmedi").
4. **Kasa durumu:** Hangi kasa çevrimiçi, kaç olay bekliyor, yazıcıda kağıt var mı.
5. **Katalog dağıtımı:** Merkezden gelen price/ürün değişikliğini bir kez indirir, kasalara dağıtır.
6. **Paylaşılan durum:** Çok terminalli restoranda `restaurant` modülü burada çalışır.
7. **Image önbelleği (opsiyonel):** Kasalar container güncellemelerini internetten değil operasyondan çeker.

### İki kademeli ara bellek

```
kasa (outbox → yerel JetStream) ──▶ operasyon (JetStream, makbuzla en az 90 gün) ──▶ merkez (PostgreSQL)
```

| Ne çökerse | Ne olur |
|---|---|
| İnternet | Mağaza raporlar dahil normal çalışır; olaylar operasyonda birikir |
| Operasyon | Market kasaları satışa devam eder, olaylar kasalarda birikir; mağaza raporları durur. Restoranda masa paylaşımı durur, bu yüzden UPS önerilir |
| Bir kasa | Diğer kasalar etkilenmez; o kasanın bekleyen olayları kasa açılınca gönderilir |

### Teslim makbuzu (karar 05.11.2026, `pkg/delivery`)

**Kural:** bir katmanın stream'indeki olay, üst katmanın **PostgreSQL'ine işlendiği makbuzla kanıtlanmadan silinmez**. Üst katman yoksa (tek kasa, operasyon ya da merkez kurulmamış), erişilemiyorsa ya da olayı henüz işlememişse olay kalır. Stream'lerdeki kendiliğinden silme (kasada 30, operasyonda 90 gün) kaldırıldı.

NATS'ın kendi teslim bilgisi bunu kanıtlamaz: kaynak tüketicisinin onayı yalnızca "alan stream yazdı" der, veritabanına işlendiğini söylemez ve göndericinin tarafında görünmez (denendi: kaynak tüketicisi üst stream'in tüketici listesinde yok). Bu yüzden düzen uygulama düzeyindedir:

1. **Alıcı** (operasyonda `report`, merkezde `ingest`) olayı veritabanına işler ve olay için bir **UUIDv7 makbuz kimliği** üretir. Makbuz kimliği alıcının veritabanında olayın yanına yazılır (`delivery_issued`) ve makbuz outbox'a konur (`receipt_outbox`). Aynı olay tekrar gelirse aynı makbuz kimliği kullanılır. Merkezde olay kaydı ve makbuz aynı işlemdedir. Operasyonda ayrı işlemlerdedir ama mesaj ikisi de bitmeden onaylanmaz: arada çökerse mesaj yeniden gelir, olay kaydı tekrara dayanıklıdır, makbuz tamamlanır.
2. Makbuz outbox'tan **makbuz stream'ine** (`ABAROW_RECEIPT`, konu `receipt.<gönderen>.<stream>`) yayınlanır ve katalog gibi aşağı iner: merkezden operasyona, operasyondan kasaya. Her katman yalnızca kendi makbuzlarını kaynak alır (`receipt.<kimlik>.>` süzgeci).
3. **Gönderen** makbuzu kendi veritabanında olayın yanına yazar (`delivery_received`) ve ancak ondan sonra onaylar.
4. **Temizleyici** (gönderende, dakikada bir) stream'deki eski bir olayı yalnızca üç koşul birlikteyse siler: olay en az `ABAROW_CLEAN_MAX_AGE` kadar eski, makbuz kimliği `delivery_received`'da kayıtlı ve stream'in kendi adlı tüketicileri (operasyonda `report`, `inventory`; kasada `staff`) olayı işlemiş. Olay kimliği (`ce-id`) olmayan mesaj silinmez.

Anlık olması gerekmez: makbuz dakikalar sonra gelebilir, olay o zamana kadar durur.

| | Makbuzu veren | Makbuzu alan ve silen | Silinen stream | Varsayılan süre |
|---|---|---|---|---|
| Kasa → operasyon | operasyon (`report`) | kasada `sales` ve `staff` | `ABAROW_SALES`, `ABAROW_STAFF` | 30 gün |
| Operasyon → merkez | merkez (`ingest`) | operasyon (`report`) | `ABAROW_STORE` | 90 gün |

**Ayarlar** (ortam değişkenleri; süre `90d`, `36h`, `1h30m` biçimindedir, geçersiz değerde servis açılmaz):

| Değişken | Anlamı | Varsayılan |
|---|---|---|
| `ABAROW_CLEAN_MAX_AGE` | Makbuzu gelmiş olsa da olayın en az saklanma süresi | kasada 30 gün, operasyonda 90 gün |
| `ABAROW_CLEAN_INTERVAL` | Temizlik turlarının aralığı | 1 dakika |
| `ABAROW_CLEAN_BUDGET` | Bir turda incelenen en çok mesaj | 20000 |
| `ABAROW_RECEIPT_MAX_AGE` | Makbuzun makbuz stream'inde durma süresi | 30 gün |
| `ABAROW_RECEIPT_SOURCE` | Kasada: makbuzu veren operasyonun JetStream alanı (mağaza kimliği); boşsa kasada hiçbir olay silinmez. `register-install.sh` operasyon verilince yazar | – |
| `ABAROW_CATALOG_SOURCE` | Operasyonda: makbuzu veren merkezin JetStream alanı (`--headquarters` ile gelir); boşsa mağaza stream'inden hiçbir olay silinmez | – |

**Sınırlar ve bilinenler:**
- **Üst katmansız kurulumda disk büyür:** tek kasada ve merkezsiz operasyonda olaylar hiç silinmez. Disk dolmasına karşı izleme ve yedekleme operatörün işidir.
- **Sürüm geçişinde eski olaylar:** bu sürümden önce işlenmiş olayların makbuzu yoktur, o olaylar silinmez. Alıcının stream'i baştan işlemesi (`headquarters-install.sh reprocess`, `store-install.sh reprocess`) eski olaylara makbuzlarını verir.
- **Makbuz stream'inin süresi:** gönderen `ABAROW_RECEIPT_MAX_AGE`'ten uzun süre kapalı kalırsa o dönemin makbuzlarını almaz ve o olaylar silinmez (güvenli taraf); alıcı yeniden işleme ile makbuzları tekrar verir.
- **Olay kimliği:** makbuz olay kimliğiyle eşleşir (`ce-id`, yoksa `Nats-Msg-Id`). Kasanın akışlarında bugün yayınlanan her konu (`sale.completed`, `sale.charged`, `sale.voided`, `shift.opened`, `shift.closed`) operasyonda işlenir; ileride işlenmeyen bir konu eklenirse o olayın makbuzu verilmez ve kasada silinmez.
- **Reddedilen olay:** alıcının okuyamayıp kalıcı reddettiği (bozuk) olay için makbuz verilmez; olay göndericide kalır.
- Makbuz konusunu başka bir cihaz kendi kaynak süzgeciyle okumaya çalışabilir: makbuzda yalnızca olay kimliği ve UUID vardır, satış içeriği yoktur.

### Servisler

| Servis | Sorumluluk | Veri |
|---|---|---|
| **nats** (hub) | Kasa akışlarını toplar (JetStream sourcing, `ABAROW_STORE`); merkez mağaza stream'ini leaf bağlantısı üzerinden kendisi çeker. Asıl ara bellek budur | JetStream dosyaları |
| **report** | Mağaza stream'ini kurar; olayları tüketip rapor veritabanına işler, eksik fişleri ve kasa durumunu izler; rapor API'si ve operasyon ekranı | PostgreSQL (operasyon) |
| **catalog** | Mağazanın katalog kopyası | `catalog.db` |
| **sync** | Merkez bağlantısı, hangi olayın merkeze ulaştığının takibi | `sync.db` |
| **restaurant** | Yalnızca çok terminalli restoranda | `restaurant.db` |

`report` iyi bir hexagonal örneğidir: aynı uygulama çekirdeğini iki driving adapter kullanır (NATS tüketicisi ve HTTP API).

### Mağaza raporları (report, uygulandı)

`report`, mağaza stream'ini kendi kalıcı tüketicisiyle (`report`) okur ve satışları PostgreSQL'e yazar: satış, satırlar, ödemeler, oran bazında KDV; iptaller ayrı. Satış tabloları, onları yazan kod ve gün raporunun hesabı merkezle ortaktır (satış defteri `pkg/ledger`; merkezde aynı tablolar `store_id` ile mağazaları ayırır). Olaylar en az bir kez gelir; satış kimliği tekrarı ayıklar. Raporlar NATS kapalıyken de veritabanından sunulur.

- **Günlük mağaza raporu** (`GET /api/reports/summary?from=2026-10-26`; tarih yoksa bugün; merkezin dönem özetiyle aynı uç ve biçim, `to` verilirse dönem): Gün mağaza saatiyle hesaplanır (`ABAROW_TZ`). Rapor şunları verir:
  - fiş sayısı, KDV dahil toplam, KDV, iptaller
  - ödeme türleri (para üstü nakitten düşülür; kasadaki X/Z ile aynı kural)
  - oran bazında KDV
  - kasa, kasiyer, saat (0–23) ve ürün bazında toplamlar

  Rapor tek tek fiş toplamlarından çıkarılır, her kasanın mali Z raporunun yerine geçmez; mali değeri yoktur. Tutarlar ortak `pkg/money` ile hesaplanır, testler ödemeler ve ürünler toplamının satış toplamına eşit olduğunu denetler.
- **Fişi kesilemeyen satışlar** (`GET /api/registers`, `unfinished_sales`): Ödeme alınıp mali kayıt (ÖKC/yazıcı) başarısız olunca sales `sale.charged.v1` yayınlar. Olayda tutar, kasiyer ve neden (ör. "yazıcı yanıt vermiyor") bulunur. Başarılı ödemede yayınlanmaz, her başarısız denemede yeniden yayınlanır. report satışı, tamamlanana (`sale.completed.v1`) ya da ödemesi geri verilip iptal edilene (`sale.voided.v1`) kadar kasa kartında gösterir. Müdür, hangi kasada müşterinin parası alınmış ama fişinin kesilmemiş olduğunu görür.
- **Eksik fiş tespiti** (`GET /api/registers`, `missing_receipts`): Fiş numarası kasa bazında hiç sıfırlanmadan artar ve yalnızca mali kaydı yapılan satış numara alır. Bu yüzden bir kasadan gelmiş en küçük ve en büyük numara arasındaki her boşluk, henüz gelmemiş ya da kaybolmuş bir fiştir ("Kasa 3: 1042–1045"). Kasa operasyona sonradan bağlandıysa ilk gelen fişten öncesi bilinmez.
- **Kasa durumu** (`GET /api/registers`): bağlantı (bağlı / bağlantı yok / hiç bağlanmadı), son haber zamanı, bekleyen olay sayısı, gelmiş fiş sayısı, son fiş numarası ve zamanı.
- **Vardiyalar** (`open_shift`, `last_closed_shift`): Mağaza stream'i her kasanın vardiya stream'ini (`ABAROW_STAFF`) de kaynak alır. Kasa kartında vardiyadaki kasiyer ve son kapanan vardiyanın kasa farkı (açık/fazla) görünür. Vardiya numarası kasa içinde artar; vardiya kasa kimliği ve numarasıyla tutulur. Kasanın bağlantı durumu yalnızca satış kaynağından okunur: vardiya stream'i kasada ilk vardiyaya kadar (ya da eski sürüm kasada) yoktur ve o kaynak hata verir.
- **Ekran** (`ui/apps/store`, report image'ına gömülü; simülasyonda http://localhost:8090):
  - kasa kartları: bağlantı, son haber, bekleyen olay, gelen fiş, eksik fiş uyarısı
  - günlük rapor: özet, ödemeler, KDV, kasalar, kasiyerler, saatlik dağılım, ürünler
  - "Bugün" mağaza saatiyle sunucuda belirlenir (tarayıcının saat dilimi farklı olabilir). Bugün gösterilirken ekran 15 saniyede bir tazelenir.
  - Kasaların durumu okunamazsa (ör. mesaj yolu kapalı) rapor yine gösterilir.
  - Ekranda raporun bilgi amaçlı olduğu, mali değeri olmadığı yazar.
- **Giriş:** Satış rakamları ticari sırdır; raporlar ve kasaların durumu giriş ister. Ekran dosyalarında veri olmadığı için onlar açıktır. İki giriş yolu var:
  - **Müdür kodu ve PIN'i:** Operasyonda yönetilen (bölüm 15), hesabı açık müdürler kasadaki kodu ve PIN'iyle girer. Değişiklik kayıtlarına müdürün adı yazılır; mağaza fiyatında bu ad merkeze de gider. Kasiyer (müdür olmayan) giremez. Bilinmeyen kod yanlış PIN gibi yanıtlanır; kodlar gizli değildir, kasanın giriş ekranında listelenir.
  - **Mağaza parolası:** Kurulumda belirlenir, en az 8 karakterdir. İlk kurulumda ve müdür tanımlanmamış mağazada giriş yolu budur; kayıtlara "mağaza parolası" yazılır. **Hesabı açık müdür varken kapalıdır** (karar 29.10.2026): parola karşılaştırılmaz (403), yanlış deneme sayılmaz; parolayla açılmış oturum ilk müdür kaydedilince kapanır (ekran bunu önceden yazar). Böylece müdürler tanımlandıktan sonra her değişiklikte kişi bellidir. Giriş ekranı, hesabı açık müdür varsa yalnızca müdür girişini, yoksa mağaza parolasını gösterir. Ekranın üstünde kimin girdiği yazar.
  - **Kurtarma:** PIN'ini unutan müdüre başka bir müdür Kasiyerler bölümünden yeni PIN verir. Başka müdür yoksa PIN operasyonda komut satırından değişir: `store-install.sh pin <kod>` (`report set-pin`; PIN sorulur, kayda "kurulum (komut satırı)" yazılır). Hesabı açık müdür kalmazsa (ör. dosyadan yüklemede hepsi kapalı) mağaza parolası yeniden açılır.
  - **Müdürün oturumu her istekte denetlenir:** Hesabı kapanan, müdürlüğü kalkan ya da kasiyer listesinden çıkan müdürün oturumu bir sonraki istekte kapanır. PIN'i değişince (ör. başkası öğrendi) o PIN'le açılmış oturumlar kapanır; kendi PIN'ini değiştiren müdürün o oturumu açık kalır. Adı değişirse yeni adı yazılır. Müdür kendi hesabını kapatamaz, kendi müdürlüğünü kaldıramaz; bunu başka bir müdür ya da mağaza parolasıyla giren yapar.
  - **Kurumsal giriş (isteğe bağlı, v0.67):** `ABAROW_LOGIN` ile müdürler kurumsal kimlik sağlayıcıyla (OpenID Connect) girer; kimliği IdP kanıtlar, müdürlük Kasiyerler kaydından okunur (kayıtsız, kasiyer ya da hesabı kapalı kişi girmez). Müdür ilk girişte doğrulanmış e-posta adresiyle tanınır (adres Kasiyerler'den ya da `store-install.sh email` ile verilir) ve kimliği bağlanır; oturum PIN özetine bağlıdır (PIN değişince kapanır). Geri dönüş adresi HTTPS olmak zorunda olduğundan ekranın önüne TLS'li ters vekil gerekir. Yalnızca `oidc` iken IdP erişilemezse ekrana girilemez (kasalar etkilenmez). Ayrıntı: [LOGIN.md](decisions/LOGIN.md).
  - **Saklama:** report parolanın kendisini değil bcrypt özetini görür (`ABAROW_REPORT_PASSWORD_HASH`; kurulumda Podman secret'ı). Özeti `report hash-password` üretir; parola standart girdiden verilir, komut satırına yazılmaz. Özet yoksa report açılmaz.
  - **Oturum:** Çerezle 12 saat. Çerez JavaScript'ten okunamaz (`HttpOnly`) ve başka sitelerden gelen isteklerle gönderilmez (`SameSite=Strict`). Oturumlar bellektedir, servis yeniden başlayınca yeniden giriş yapılır.
  - **Deneme sınırı:** 5 yanlış denemeden sonra giriş kilitlenir: ilk kilit 5 dakika, art arda gelen her kilit iki katı (en çok 1 saat). Doğru girişte süre başa döner. Mağaza parolası ve her müdür ayrı sayılır: birinin kilidi öbürlerini etkilemez. Kilitler de bellektedir. Uzayan kilit, 4 haneli PIN'in deneme yanılmayla bulunmasını aylara yayar.
  - **Simülasyon:** Deneme parolası kullanır (deneme PIN'leri gibi); gerçek kurulumda kullanılmaz.
- **Bilinen sınırlar:**
  - **HTTPS (karar, 06.11.2026):** Ekran mağaza ağında HTTPS sunar; parola, PIN ve raporlar şifreli gider. Sertifika operasyonun leaf bağlantısıyla aynıdır: mağaza CA'sının imzaladığı, kurulumdaki `--address` adlarına verilmiş sunucu sertifikası (`natsauth`). Tarayıcıların uyarı vermemesi için müdür cihazlarına CA sertifikası (`ca.pem`) yüklenir; ekrana yalnızca `--address` ile verilen ad ya da IP ile girilir. Önceki kararlar (26.10 ve 29.10.2026: düz HTTP) bu yüzden değişti; ekran yine internete açılmaz, uzaktan erişim için VPN kullanılır. Simülasyon (Compose) düz HTTP kalır.
  - **CA yükleme riski:** `ca.pem`'i cihaza yükleyen kişi o CA'nın imzaladığı her sertifikaya güvenir; CA yalnızca müdür cihazlarına yüklenir. Leaf sertifikasıyla aynı anahtar ekranda da kullanıldığından anahtar iki container'a (NATS ve report) bağlanır; ikisi de salt okunur secret olarak, `0400` kipiyle alır.
  - **Kilitlenen müdür:** Yanlış PIN kilidi bellektedir; müdürlerin hepsi kilitliyse kilidin bitmesi beklenir ya da report yeniden başlatılır (mağaza parolası müdür varken kapalı).
  - v0.45'ten önce mağaza parolasıyla yapılan değişikliklerin kaydında "mağaza" yazar.
  - Gün raporunda kasiyerin adı operasyonda yönetilen kasiyerlerdendir; yalnızca kasadaki dosyada tanımlı kasiyerin kimliği gösterilir.
  - v0.23'ten önceki JSON olaylar rapora alınmaz; günlüğe yazılıp atlanır (yalnızca geliştirme ortamlarında vardır).

### Kasaların bağlantısı (uygulandı)

Kasa olaylarını operasyona kendisi göndermez; NATS taşır. Her kasanın NATS'ı operasyona **leaf** olarak bağlanır. Operasyondaki `ABAROW_STORE` stream'i her kasanın `ABAROW_SALES` stream'ini **kaynak** (source) olarak ekler. NATS her kaynakta nerede kaldığını tutar: bağlantı kopunca olaylar kasada birikir, gelince kaldığı yerden aktarılır.

- **JetStream alanı (domain):** Her NATS sunucusunun bir alan adı vardır: kasada kasa kimliği (`register-1`), operasyonda mağaza kimliği (`store-1`). Operasyon kasanın stream'ine bu adla ulaşır. Kasadaki servisler yine yalnızca kendi kasalarının stream'ini görür.
- **Kasa listesi:** Operasyona bağlı kasalar `registers.json`'dadır (`pkg/registers`): kimlik, ekranda görünen ad ve kasanın açık anahtarı. NATS yapılandırması (`natsauth -role store`) ve `report`'un kaynak listesi aynı dosyadan üretilir.
- **TLS:** Leaf bağlantısı her zaman TLS'lidir; olaylar mağaza ağında açık gitmez.
  - Operasyon mağazaya özel bir sertifika otoritesi (CA) üretir ve saklar (`natsauth`, `<out>/tls`). CA'nın gizli anahtarı operasyondan çıkmaz.
  - CA'nın imzaladığı sunucu sertifikası, kasaların bağlandığı adları ve IP'leri içerir (`-leaf-hosts`). Adres değişince ya da süresi dolmaya yaklaşınca (2 yıl, 60 gün kala) yeniden üretilir; kasalar etkilenmez.
  - Kasa kurulumda CA sertifikasını alır (`-leaf-ca`; operasyonda `<out>/ca/ca.pem`) ve operasyonu onunla doğrular. Sahte bir operasyona bağlanmaz.
  - CA bir kez üretilir: gizli anahtarı kaybolursa `natsauth` yenisini kendiliğinden üretmez, çünkü kasalardaki CA geçersiz kalırdı.
- **Cihaz kimliği (NKey):** Her kasa kendi leaf anahtarını üretir (`natsauth -show-leaf-key`). Gizli kısmı kasadan çıkmaz; açık kısmı operasyonun listesine eklenir (`natsauth -add-register`). Kasa çalınırsa ya da kaldırılırsa listeden çıkarılır (`-remove-register`) ve bağlanamaz. JWT yerine NKey seçildi: ayrı bir operatör/calc altyapısı gerektirmez, iptal listeden çıkarmakla olur.
- **Kasalar arası yalıtım:** Leaf bağlantısında olay konuları (`sale.>` …) iki yönde de kapalıdır. Kapalı olmasaydı bir kasanın satışı operasyon üzerinden diğer kasanın stream'ine düşerdi ve gün sonu raporu başka kasanın satışını sayardı (prototipte görüldü). Kapatılan konular `natsauth`'un yetki tablosundan çıkarılır; yeni olay konusu kendiliğinden kapanır.
- **En az yetki (operasyon tarafı):**
  - Operasyon kasa bağlantısına izin listesiyle yetki verir: kasada yalnızca satış ve vardiya stream'lerinden kaynak tüketicisi açabilir. Kasa da yalnızca kaynak verisini ve cevaplarını gönderebilir.
  - Kasa operasyonun JetStream API'sine ve başka kasaların konularına erişemez.
  - Kasa anahtarı yalnızca leaf bağlantısı için geçerlidir (`allowed_connection_types: LEAFNODE`), servis anahtarları yalnızca istemci bağlantısı için.
- **Yetişme süresi:** NATS kaynağı koptuğunda artan aralıklarla yeniden dener (5 sn × deneme sayısı × 2, en çok 2 dk). Operasyon kesintiden sonra açılınca olaylar on saniyelerle dakika arasında gelir. Raporlama için yeterli; anlık değil.
- **Saklama:** Kasada en az 30 gün, operasyonda en az 90 gün ve yalnızca üst katmanın makbuzu varsa (bkz. "Teslim makbuzu"). Kasa sonradan operasyona bağlanırsa stream'deki bütün olaylar aktarılır.
- **Ağ:** Operasyonun istemci portu (4222) mağaza ağına açılmaz; yalnızca leaf portu (7421) açılır; merkezin leaf portu 7422'dir.
- **Test:**
  - `tools/natsauth` testi üretilen yapılandırmalarla süreç içinde bir operasyon ve iki kasa çalıştırır (TLS'le). Olaylar kayıpsız ve tekrarsız gelir, kasalar karışmaz, kasa anahtarı istemci olarak reddedilir, başka bir CA'ya güvenen kasa bağlanamaz, operasyon kapalıyken biriken olaylar açılınca gelir.
  - Mağaza simülasyonu (`scripts/store-sim.sh`) aynısını gerçek servislerle iki kasada yapar (26.10.2026: iki kasada 15'er senaryo geçti; report iki kasayı bağlı ve bekleyen olaysız gösterdi). Eski verisiyle çalışan bir kasa da yeni yapılandırmaya (JetStream alanı eklendi) sorunsuz geçti.

### Donanım

- Mini PC (ör. Intel N100 veya i3; 4 GB yeterli, 8 GB rahat), **SSD** ve **UPS**. Sürekli yazma yapıldığı için SD kart/eMMC yerine SSD tercih edilir.
- Küçük mağazada (2–3 kasa) ayrı makine yerine kasalardan biri operasyon container'larını da çalıştırabilir (kasada ~3 GB boş RAM var). Bu durumda o kasa kritik hale gelir.
- Tek kasalı yerde istenirse `report` kasanın kendisinde de çalıştırılabilir; böylece aynı rapor ekranı elde edilir.

## 15. Merkez

Merkez tüm mağazaların verisini toplar. Operasyonlardan (tek kasalı yerlerde doğrudan kasalardan) gelen olaylar en az bir kez ulaşır; merkez olay ID'siyle tekrarları ayıklar. Böylece kayıt ne eksik ne çift olur.

| Servis | Sorumluluk |
|---|---|
| **ingest** | Mağazalardan gelen olayları alır, PostgreSQL'e yazar, tekrarları ayıklar |
| **reporting** | Mağazalar arası raporlar: ciro, ürün, kasiyer, dönem karşılaştırma |
| **catalog-admin** | Ürün, fiyat (ileri tarihli dahil), kampanya yönetimi; katalog stream'iyle mağazalara iner (bkz. "Katalog, fiyatlar ve kasiyerler") |
| **fleet** | Mağaza, operasyon ve kasa kaydı; hangi cihazda hangi sürüm çalıştığı; uzaktan ayar |
| **merkez UI** | Aynı Svelte stack, tarayıcıdan |
| **e-belge** (opsiyonel) | e-Arşiv/e-Fatura için özel entegratör adapter'ı |

Veri PostgreSQL'de tutulur. Rapor hacmi büyürse ayrı bir analitik veritabanı (ClickHouse vb.) eklenebilir; başlangıçta gerek yok.

### Olay alma (ingest, uygulandı)

```
kasa (ABAROW_SALES, ABAROW_STAFF) ──▶ operasyon (ABAROW_STORE) ──▶ merkez (ABAROW_HEADQUARTERS) ──▶ ingest ──▶ PostgreSQL
```

- **Bağlantı:** Operasyonun NATS'ı merkeze leaf olarak, TLS'le bağlanır (`natsauth -role store -leaf tls://headquarters:7422 -leaf-ca ...`). Merkez kendi CA'sını üretir; operasyon kurulumda CA sertifikasını alır ve merkezi onunla doğrular. Her mağaza kendi NKey'iyle bağlanır; merkezde izin listesi vardır (`natsauth -role headquarters -add-store`), listeden çıkarılan mağaza bağlanamaz. Mağaza yalnızca kendi stream'ini sunabilir; mağazalar arası olay geçmez.
- **Merkez stream'i (`ABAROW_HEADQUARTERS`):** ingest, `stores.json`'daki her mağazanın stream'ini (`ABAROW_STORE`, mağazanın JetStream alanından) kaynak olarak ekler. Kaynakta konunun başına mağaza kimliği eklenir (`store-1.sale.completed.v1`): olayın hangi mağazadan geldiğini merkezin kendi ayarı belirler, olayın içinde yazan değil. Yanlış mağaza kimliğiyle ayarlanmış kasanın olayı geldiği mağazaya yazılır, günlüğe uyarı düşer. Farklı mağazalardaki aynı adlı kasalar (her mağazada `register-1`) karışmaz. Stream olayları 90 gün saklar: ingest uzun süre kapalı kalsa da olay kaybolmaz.
- **PostgreSQL:** Her olay önce ham haliyle (`events`: kimlik, tür, mağaza, kasa, zaman, protobuf içerik) yazılır; aynı transaction'da okuma modellerine işlenir: satışlar (satır, ödeme, KDV), iptaller, fişi kesilemeyen satışlar, vardiyalar, mağaza ve kasa özetleri. Olay kimliği (ce-id) birincil anahtardır; tekrar gelen olay hiçbir şey yazmaz. Okuma modeli yazılamazsa ham olay da yazılmaz ve olay tekrar gelir: kayıt yarım kalmaz. Bilinmeyen olay türleri yalnızca ham haliyle saklanır; ingest yeni türü öğrenince ham kayıttan yeniden işlenebilir. Tutarlar kuruş (`bigint`).
- **Durum:** `GET /api/stores`: her mağazanın bağlantısı (bağlı, haber yok, hiç bağlanmadı), mağazada bekleyen olay sayısı, gelen olay ve satış sayısı, kasa başına son olay. Yalnızca merkezin iç ağı içindir; raporlar ve giriş merkez ekranında olacak.
- **Kütüphaneler:** pgx (database/sql üzerinden) ve goose migration'ları; SQLite tarafıyla aynı yapı.
- **Bağlantı durumu:** ingest mağazaların bağlantı durumunu 15 saniyede bir `store_links` tablosuna da yazar; merkez ekranı NATS'a bağlanmadan durumu buradan okur. Yazma zamanı bir dakikadan eskiyse durum "bilinmiyor" gösterilir (ingest çalışmıyor).
- **Test:** PostgreSQL testleri gerçek sunucuyla çalışır (`ABAROW_TEST_POSTGRES`; CI'da servis container'ı). Üç katmanlı NATS testi (`tools/natsauth`): iki mağaza, her birinde `register-1`, olaylar merkeze mağaza konusuyla ayrı gelir. Docker simülasyonu (`scripts/store-sim.sh`) merkez, operasyon ve iki kasayı gerçek servislerle çalıştırır; register-sim her kasanın olay ve satış sayısının merkezde birebir aynı olduğunu denetler.

### Raporlar ve ekran (reporting, uygulandı)

- **Veri:** Satış tablolarını ingest yazar; reporting yalnızca okur. Kendi tabloları (kullanıcılar, oturumlar) ayrı bir migration sürüm tablosundadır (`reporting_goose_db_version`). İki servis ayrı container'dır, ayrı güncellenir; okuma modelinin şeması ikisi arasındaki sözleşmedir (reporting'in testleri şemayı ingest'in migration'larıyla kurar).
- **Mağazalar:** her mağazanın bağlantısı (bağlı, haber yok, hiç bağlanmadı, bilinmiyor), son haber, bekleyen olay ve bugünkü satışı.
- **Dönem özeti:** seçilen gün aralığında (en çok 366 gün) bütün mağazalar ya da bir mağaza: toplam, fiş, KDV, iptal; mağazalar, günler (satışsız günler de), ödemeler (nakit para üstü düşülmüş; kasadaki X/Z ile aynı kural), KDV oranları, kasiyerler (mağazayla birlikte; aynı kimlik başka mağazada başka kişi olabilir) ve en çok satan 50 ürün. Günler mağaza saatiyle (`ABAROW_TZ`). Özet tek bir tutarlı okumayla (aynı anlık görüntü) hesaplanır. Rapor bilgi amaçlıdır, mali değeri yoktur.
- **Giriş:** Kişiye özel hesaplar (operasyondaki ortak mağaza parolasından farklı): merkez internetten erişilebilir olabilir ve ileride kasiyer/müdür listesini de dağıtacak. Parolanın bcrypt özeti saklanır (en az 10 karakter); kullanıcı adı ASCII (I da İ de i olur). Oturum anahtarının yalnızca SHA-256 özeti veritabanında tutulur: servis yeniden başlayınca oturum düşmez; parola değişince ya da hesap kapatılınca kişinin bütün oturumları kapanır. Aynı kullanıcı adıyla 5 yanlış denemede giriş 5 dakika kilitlenir; olmayan ya da kapalı hesapta da aynı hata ve aynı yanıt süresi.
- **Kurumsal giriş (isteğe bağlı, v0.67):** `ABAROW_LOGIN=yerel,oidc` ile kullanıcılar kurumsal kimlik sağlayıcıyla (OpenID Connect) girebilir; kimliği IdP kanıtlar, rolü ve mağaza yetkisi kullanıcı kaydından okunur. Kullanıcı ilk girişte doğrulanmış e-posta adresiyle tanınır (adres Kullanıcılar'dan ya da `headquarters-install.sh hesap eposta` ile verilir), kimliği bağlanır; kişi "Kurumsal kimlik" bölümünden kendi bağını kurar/kaldırır, yönetici bağı kaldırır. Geri dönüş adresi `--domain` (HTTPS). Ayrıntı: [LOGIN.md](decisions/LOGIN.md).
- **Roller ve mağazalar (uygulandı; karar 29.10.2026):**
  - **Yönetici:** kullanıcıları yönetir, ürün ve merkez fiyatlarını değiştirir, bütün mağazaları görür.
  - **Katalog:** ürün ve merkez fiyatlarını değiştirir; raporlarda kendisine verilen mağazaları görür.
  - **Rapor:** raporlarda kendisine verilen mağazaları görür; kataloğu yalnızca okur.
  - Yönetici olmayan kullanıcıya bütün mağazalar ya da seçilen mağazalar verilir (`user_stores`). Süzgeç sunucuda uygulanır: mağaza listesi, dönem özeti (SQL'de `store_id = ANY(...)`) ve mağaza fiyatları raporu. Başka mağazanın özeti istenirse 403. Oturumu yalnızca reporting denetler; catalog-admin'e ve merkezin envanterine kişiyi (kullanıcı adı, ad, rol, mağazalar) başlıklarla iletir (`pkg/login`, operasyondaki yöntemin aynısı), istemciden gelen aynı adlı başlıkları siler. catalog-admin yazma isteğini yönetici ve katalog dışında reddeder (403). Rol ve mağaza değişikliği bir sonraki istekte geçerlidir (oturum her istekte veritabanından okunur).
  - **Kullanıcı yönetimi:** Ekranın "Kullanıcılar" bölümünde (yalnızca yönetici): ekle, ad, rol, mağazalar, parola (iki kez yazılır), hesabı kapat/aç. Her değişiklik kimin yaptığı ve okunur özetiyle kaydedilir (`user_changes`; parola yazılmaz). Yönetici kendi hesabını kapatamaz, kendi yöneticiliğini kaldıramaz; hesabı açık son yönetici kapatılamaz, rolü değiştirilemez (ekranı yönetecek kimse kalmazdı).
  - **Komut satırı:** İlk yönetici ve kurtarma için (`reporting user add|passwd|disable|enable|list`, `headquarters-install.sh hesap ...`; parola standart girdiden). Komut satırından eklenen kullanıcı yöneticidir; kayıtlara "kurulum (komut satırı)" yazılır. Rolden önce eklenmiş kullanıcılar migration'la yönetici olur.
  - **Kendi parolası (uygulandı):** Herkes "Parolam"dan kendi parolasını şu anki parolasıyla değiştirir. Yanlış şu anki parola girişle birlikte sayılır (5 denemede 5 dakika kilit): oturumu açık kalan cihazda parola deneme yanılmayla bulunamaz. Kişinin bütün oturumları kapanır, bu tarayıcıya yeni oturum verilir; kayda "parola değişti (kendisi)" yazılır.
- **Tarayıcı güvenliği:** Çerez HttpOnly ve SameSite=Strict; HTTPS ile (doğrudan ya da TLS'li ters vekilin `X-Forwarded-Proto` başlığıyla) gelince Secure. Giriş ve çıkış yalnızca JSON kabul eder (başka sitedeki form tetikleyemez). Sıkı içerik güvenliği politikası (CSP: yalnızca kendi dosyaları, satır içi script/stil yok, çerçeveye konamaz). Rapor yanıtları önbelleğe alınmaz.
- **Ekran (`ui/apps/headquarters`):** operasyon ekranıyla aynı yapı (Svelte 5, Inter); hazır dönemler (bugün, dün, son 7 gün, bu ay), mağaza seçimi, telefon genişliğinde çalışır. Bugünü içeren dönem 30 saniyede bir tazelenir. Tarayıcı testi üç katmanlı simülasyonda (`tools/ui-smoke/headquarters.mjs`).

### Katalog, fiyatlar ve kasiyerler (tasarım)

**Kararlar (28.10.2026, işletme sahibi):**

- **Fiyat:** Her ürünün merkezde bir fiyatı vardır; istenen mağazada o ürüne ayrı fiyat (mağaza istisnası) verilebilir.
- **Kim değiştirir:** Ürünü (ad, barkod, KDV, birim) ve merkez fiyatını merkez değiştirir. Mağaza müdürü kendi mağazasının fiyatını değiştirebilir. Mağazanın değişikliği merkeze bildirilir; merkezde farklı fiyatlı ürünler ve mağazalar raporlanır.
- **Kasiyerler ve PIN'ler** mağazada (operasyon ekranında) yönetilir; merkez yalnızca görür (raporlarda kasiyer adı).
- **Zamanlama:** Fiyat değişikliği hemen ya da ileri bir tarihte (ör. pazartesi 00:00, raf etiketleriyle birlikte) yürürlüğe girebilir.

**Tasarım:**

```
merkez (catalog-admin) ─▶ ABAROW_CATALOG ─▶ operasyon ─▶ kasa (catalog)
                                              │  ▲
              mağaza istisnası, kasiyerler ───┘  └── merkeze bildirim (ABAROW_STORE üzerinden)
```

- **Katalog yönetimi (catalog-admin, uygulandı):** Ayrı servis; merkez ekranındaki "Ürünler" bölümü. Ürün kodu (stok kodu) zorunludur ve değişmez; mesajın konusuna girdiği için harf, rakam, - ve _ içerir. Fiyat hemen ya da ileri bir tarih ve saatten itibaren verilir; henüz yürürlüğe girmemiş fiyat geri alınabilir, yürürlüğe girmiş olan alınamaz (yeni fiyat verilir). Satıştan kaldırılan ürün kasada bulunmaz. Her değişiklik kimin yaptığı ve okunur özetiyle kaydedilir (ürünün "Değişiklikler" listesi). Her değişiklik ürünün okunduğu sürümle yapılır; arada başkası değiştirdiyse reddedilir (409), sessizce ezilmez. Ürünün son hali aynı transaction'da outbox'a yazılır, relay sırayla yayınlar: NATS kapalıyken de değişiklik kaybolmaz. Ekran reporting'den sunulur; reporting `/api/catalog/` isteklerini oturumu denetleyip kişiyle birlikte catalog-admin'e iletir (catalog-admin'in portu dışarı açılmaz). Kasaların başlangıç dosyası (`catalog.json`) merkeze bir kez yüklenebilir (`headquarters-install.sh catalog-load`). Tablolar `catalog_` önekli, migration sürümü ayrı tabloda.
- **Katalog stream'i (uygulandı):** Merkezdeki katalog yönetimi her ürünün tam halini (ad, barkodlar, KDV, birim, hızlı tuş, fiyat takvimi) konu başına tek mesaj olarak yazar (`catalog.product.<ürün>`; konu başına yalnızca son değer saklanır). Stream yalnızca güncel kataloğu tutar: yeni kurulan kasa bütün kataloğu tarihçesiz alır. Bağlantı kesikken değişiklikler birikir, bağlantı gelince yalnızca son hali iner. Operasyon merkezin stream'ini, kasa operasyonunkini kaynak olarak alır (satışların tersi yönde; aynı NATS leaf bağlantıları). Leaf izinleri bu yönde yalnızca katalog stream'ine kaynak tüketicisi açmaya yeter: kasa operasyondan başka kasaların satışlarını alamaz (üç katmanlı NATS testi denetler). Mağazadaki kopyayı report kurar (`ABAROW_CATALOG_SOURCE=merkez`; mağaza yönetimi servisi gelince ona geçer). Kasadaki catalog stream'i kendi kalıcı tüketicisiyle okur ve her ürünün son halini uygular; satıştan kaldırılan ürün okutulunca bulunmaz, aramada çıkmaz. Merkezden gelen ürün kasanın başlangıç dosyasıyla değişmez; yalnızca dosyada olan ürünler dosyayla yönetilmeye devam eder (merkezsiz işletme). Merkez bağlantısı kesikken yeni kurulan kasa kataloğu operasyondan alır.
- **Fiyat takvimi (kasada uygulandı):** Fiyat değişikliği yürürlük anıyla (mutlak an; ekranda mağaza saatiyle seçilir) kasaya önceden iner. Sunucular geçmiş bir yürürlük anını "hemen" sayar (cihaz saatleri arasındaki küçük farklar yüzünden); ekranlar ise "İleri tarihte" seçiliyken geçmiş bir anı reddeder: yanlış girilen tarih fiyatı habersizce hemen değiştirmesin. Kasa, satış anında yürürlükteki fiyatı kendisi hesaplar: yürürlükteki en son mağaza fiyatı varsa o, yoksa yürürlükteki en son merkez fiyatı. Böylece ileri tarihli fiyat, internet kesikken de tam zamanında geçer. Kasanın saati doğru olmalıdır (NTP). catalog takvimi `prices` tablosunda tutar (eski kurulumlarda ürünün fiyatı baştan beri geçerli merkez fiyatına taşınır); geçmişte kalan kayıtlar atılır. Fiyatı henüz yürürlükte olmayan ürün (satışı ileri tarihte başlayacak) barkodla okutulunca "ürünün fiyatı henüz yürürlükte değil" der, aramada listelenmez. Merkez bağlanana kadar ileri tarihli fiyat başlangıç dosyasından da verilebilir (`catalog.json`'da `prices`).
- **Mağaza istisnası (operasyon ve kasa tarafı uygulandı):** Operasyon ekranında mağaza müdürü bir ürüne mağaza fiyatı verir (hemen ya da ileri tarihli) ya da istisnayı kaldırır. İstisna kasalara iner ve aynı değişiklik olay olarak merkeze çıkar (`store.price.changed.v1`). Merkez fiyatı sonradan değişse de istisna durur; istisna kaldırılınca mağaza merkez fiyatına döner. Merkezde "farklı fiyatlı ürünler" raporu: hangi mağazada hangi ürün, merkez fiyatından ne kadar farklı.
  - **Operasyon:** Mağaza yönetimi (mağaza fiyatları, sonra kasiyerler) ayrı bir servis yerine report'ta: operasyon ekranı ve girişi zaten oradadır, operasyon küçük bir makinedir (karar 29.10.2026, teknik). report, merkezin kataloğunun mağazadaki kopyasını kendi tüketicisiyle okur (ürün adları, barkodlar, merkez fiyatları). Takvim `report.db`'de; her değişiklik aynı transaction'da outbox'a iki mesaj yazar: kasalara `config.price.<ürün>` (mağaza ayarları stream'i `ABAROW_STORE_CONFIG`, ürün başına son hal) ve merkeze `store.price.changed.v1` (mağaza stream'i; operasyonun kendi olayları `store.>` konularıyla bu stream'e yazılır). Mesaj sözleşmesi `catalog.v1.StorePrices`. Değişiklik kaydına ekrana giren müdürün adı yazılır (mağaza parolasıyla girildiyse "mağaza parolası"); merkeze giden olayda da bu ad gider.
  - **Kasa:** Kasanın katalog stream'i mağaza ayarları stream'inin `config.price.>` konularını da kaynak alır; catalog takvimi mağaza fiyatı olarak uygular. Ürün kasaya henüz inmediyse fiyat sonra tekrar gelir.
  - **Merkez (uygulandı):** ingest `store.price.changed.v1` olaylarından mağaza ve ürün başına takvimin son halini tutar (`store_prices`; sırası karışan eski hal yenisini ezmez). Merkez ekranında "Ürünler" bölümünde "Mağaza fiyatları": şimdi ya da ileri tarihte farklı fiyatlı ürünler, merkez fiyatı, mağaza fiyatı, fark, ileri tarihli değişiklikler, son değişiklik; ürün sayfasında ürünün mağazalardaki fiyatları. Merkez fiyatı catalog-admin'in tablosundan okunur (reporting'in testleri iki servisin şemasını da kurar).
- **Satıştaki fiyat:** Fiyat kasada belirlenir ve satış olayında taşınır; raporlar gerçek satış fiyatını gösterir.
- **Kasiyerler (uygulandı):** Operasyon ekranının "Kasiyerler" bölümünde kasiyer ve müdür eklenir; adı, rolü ve PIN'i değişir; işten ayrılanın hesabı kapatılır (silinmez, yeniden açılabilir). Kasiyer kodu değişmez; mesaj konusuna girdiği için küçük harf (Türkçe karakter olmadan), rakam, - ve _ içerir. PIN 4–8 hanedir, ekranda iki kez yazılır ve hiç geri okunmaz; report yalnızca bcrypt özetini tutar. Her değişiklik kimin yaptığı ve okunur özetiyle kaydedilir (PIN'in kendisi yazılmaz). Mağaza fiyatları gibi aynı transaction'da outbox'a iki mesaj yazılır: kasalara `config.cashier.<kasiyer>` (mağaza ayarları stream'i, kasiyer başına son hal, PIN özetiyle) ve merkeze `store.cashier.changed.v1` (PIN özeti olmadan). Mesaj sözleşmesi `staff.v1.CashierState`. Kasaların kasiyer dosyası operasyona yüklenebilir (`store-install.sh cashiers-load cashiers.json`, `report import-cashiers`): olmayan kasiyer eklenir, olanın adı, rolü ve PIN'i dosyadakiyle değişir; dosyada hata varsa hiçbiri yüklenmez. Kasiyer yönetimi operasyonun mağaza kimliğini ister (`ABAROW_STORE_ID`; kurulum betiği yazar), merkez bağlantısı istemez. Mağaza ayarları stream'ini report açılırken kurar: kasalar onu ilk değişiklikten önce de kaynak alabilir. Kurulum betikleri birim ek dosyaları (drop-in) değişince ilgili servisleri yeniden başlatır; eski bir kurulumda betik aynı seçeneklerle yeniden çalıştırılınca ayar devreye girer (gerçek kurulum testinde denendi). Kasadaki uygulama bölüm 5'te.
- **Mağazada ürün ve fiyat yönetimi (uygulandı, karar 05.11.2026):** Merkez olmayan (ya da merkezden ayrılan) mağazada ürün, barkod, KDV ve fiyat operasyon ekranının "Ürünler" bölümünden yönetilir. Merkezin catalog-admin'iyle aynı imaj ve kod operasyonda ikinci örnek olarak çalışır (`abarow-store-catalog-admin`; mağazanın veritabanında kendi `katalog` şemasında, çünkü report'un katalog aynası aynı adlı tabloyu kullanır). Yazma yetkisi mağaza müdürünündür (`ABAROW_CATALOG_WRITERS=manager`). **Sahiplik tek bir ayarla belirlenir: "merkez bağlı".** Bağlıysa mağazanın katalog stream'i merkezden beslenir ve ekran yazmaya kapalıdır (report yazma isteklerini 403 ile reddeder); kapatılınca stream merkezden ayrılır, ürünler mağazada yönetilir ve kasalara aynı stream'den iner. Ayar anlık ağ durumuna değil, mağazanın tercihine bağlıdır (bağlantı geçici kopsa da sahiplik değişmez); merkez kurulu değilse (`ABAROW_CATALOG_SOURCE` yok) mağaza her zaman kopuktur ve ayar açılamaz. Ayar yalnızca katalog sahipliğini belirler; mağazanın merkeze olay göndermesi (leaf bağlantısı, makbuzlar) kurulum parametresidir ve değişmez. Yeniden bağlanınca mağazada eklenen ürünler stream'de kalır; merkezden gelenle aynı koda sahipse merkezinki geçerli olur.
- **Kasa olaylarına sıra numarası (uygulandı, `pkg/sequence`):** Kasanın her outbox'ı (satış, vardiya) olayı gönderilecek hale getirirken ardışık bir numara verir (SQLite `AUTOINCREMENT`: geri alınan işlemde numara tüketmez, silinen numara yeniden verilmez). Numara ve outbox'un adı olayın `ce-sequence` ve `ce-producer` başlıklarıyla yolculuk eder. Operasyon ve merkez (ingest) her (mağaza, kasa, gönderen) için gelen numaraları aralık olarak saklar (`event_ranges`); arada kalan boşluk, hiç gelmemiş kayıttır ve operasyon kasa durumunda (`missing_events`) ile merkez Mağazalar ekranında "Eksik kayıt" olarak görünür. Eksik olay sonradan gelirse boşluk kapanır. İşlenemeyen olayın numarası kaydedilmez (olay tekrar gelir); reddedilen (bozuk) olayınki kaydedilir (gelmiştir, sorun günlüğe yazılır). Sınır: numaralamadan önceki (başlıksız) eski olaylar izlenmez ve ilk görülen numaradan öncesi boşluk sayılmaz. Fiş numarası boşluğundan (yalnızca satış) farkı: her tür olayı kapsar. Teslim makbuzu olayın işlendiğini kanıtlar; sıra numarası hiç gelmeyen olayı fark ettirir.
- **Veritabanı ayarı kasa sayısına göre (uygulandı, `pkg/pgtune`):** Operasyon PostgreSQL ayarları `report pgtune --registers N` ile hesaplanır; `store-install.sh` kasa eklenince/çıkarılınca ek dosyayı (`abarow-store-db.container.d/tune.conf`) yeniden yazar ve değiştiyse veritabanını yeniden başlatır. 4 kasaya kadar `shared_buffers` 64 MB, `max_connections` 20, `work_mem` 4 MB; gerisi bunlardan türer (formüller ve ölçümler [PERFORMANCE.md](PERFORMANCE.md)). Servislerin bağlantı havuzları sınırlıdır (`pkg/pgpool`: report 8, envanter 4, catalog-admin 4). Paralel sorgu kapalıdır.
  - **Merkez:** ingest `store.cashier.changed.v1` olaylarından mağaza ve kasiyer başına son hali tutar (`cashiers`; sırası karışan eski hal yenisini ezmez). Dönem özetinin kasiyer tablosunda ad görünür; kasiyer kimliği mağazaya özeldir (aynı kimlik başka mağazada başka kişidir).
  - **Operasyon girişi (uygulandı):** Müdürler operasyon ekranına kendi kodu ve PIN'iyle girer; kişi kasiyer tablosundan doğrulanır (bölüm 14).
- **Merkezsiz kurulum:** Merkezi olmayan işletmede katalog bugünkü gibi kasada başlangıç dosyasından ya da operasyondan yönetilir (sonra).
- **Açık konu:** Gerçek ÖKC'lerin bir kısmı ürün/price tanımı (PLU) isteyebilir; gerçek ÖKC entegrasyonunda değerlendirilecek.

### Kurulum: her işletme kendi sunucusunda

- **Tenant yok:** Her işletme kendi merkezini çalıştırır, şemalarda `tenant_id` bulunmaz. Bir işletmenin birden fazla mağazası olabileceği için tüm olaylar `store_id` ve `register_id` taşır.
- **Nerede çalışır:** İşletmenin kendi sunucusunda, kiralık bir VPS'te ya da tek mağazalı işletmede operasyon ile aynı makinede (aynı image'lar, Compose ile). Tek kasalı küçük işletmede merkez opsiyoneldir; kasanın X/Z ve gün sonu raporları yetebilir.
- **Bağlantı yönü:** Mağazalar merkeze dışarı doğru bağlanır (NATS leaf, TLS). Bu yüzden yalnızca merkezin erişilebilir bir adresi olmalıdır (sabit IP, DDNS veya VPS); mağazalarda port açmak gerekmez.
- **Cihaz kimliği:** Her operasyon ve kasa kendi NATS kimlik bilgisiyle (nkey/JWT) bağlanır. Bir cihaz çalınırsa yalnızca onun erişimi iptal edilir.
- **Uzaktan erişim:** İşletme sahibi raporlara dışarıdan bakmak isterse ekran HTTPS'ten sunulur (`headquarters-install.sh --domain`) ya da merkez WireGuard VPN'in arkasında kalır.
- **HTTPS (uygulandı):** `--domain` verilince kurulum ekranın önüne Caddy'yi ayrı bir container olarak kurar (`deploy/quadlet/headquarters/https`). Caddy alan adı için sertifikayı Let's Encrypt'ten kendisi alır ve yeniler (HTTP'den HTTPS'e yönlendirme, HSTS); reporting'e merkez ağı içinden bağlanır. reporting HTTPS'le geldiğini `X-Forwarded-Proto`'dan anlar, çerezi Secure yapar. Root olmayan Podman'ın 80 ve 443'ü açabilmesi için kurulum `net.ipv4.ip_unprivileged_port_start=80` yapar: merkez makinesi başka işler için kullanılmamalı. İnternete açık olduğu için Caddy imajı küçük sürümün (2.11) yamalarını kendiliğinden alır. Betik `--domain` olmadan yeniden çalıştırılınca Caddy kaldırılır; sertifikalar birimde kalır. Gerçek kurulum testinde Caddy'nin yerel CA'sıyla denendi (kurulum, HTTPS, yönlendirme, Secure çerez, yeniden kurulum, kaldırma, makine yeniden başlatma); Let's Encrypt'ten gerçek sertifika alma henüz gerçek bir alan adıyla denenmedi.
- **Kurulum (uygulandı):** `deploy/install/headquarters-install.sh` + `deploy/quadlet/headquarters` (NATS, PostgreSQL 18, ingest, reporting). Veritabanı parolası kurulumda rastgele üretilir, dışarı açılmaz. Ekran varsayılan olarak yalnızca `127.0.0.1:9090`; uzaktan erişim `--domain` (HTTPS) ya da VPN ile (bkz. `deploy/quadlet/README.md`). Operasyon `store-install.sh --headquarters` ile bağlanır, merkezde `headquarters-install.sh store-add` ile eklenir.
- **Yedekleme (uygulandı):** Günlük `pg_dump` (systemd zamanlayıcısı, 03:30, 14 gün saklanır; ilk yedek kurulumda).
- **Makine dışına kopya (uygulandı):** `headquarters-install.sh --backup-target` verilince her başarılı yedekten sonra (`OnSuccess=`, yedek biriminin ek dosyası) yedekler rsync ile kopyalanır: harici disk/NAS (bağlanmış klasör) ya da SSH ile başka sunucu. Hedefte olmayan yedekler gider (önce kopyalanamayanlar da); hedefteki dosya ezilmez, yerelde silinen hedefte silinmez. Harici disk takılı değilse hedef yedeklerle aynı diskte kalır: betik bunu görür ve kopyalamaz. Sunucu için ayrı bir SSH anahtarı üretilir; önerilen yetkilendirme `rrsync -wo -no-del -no-overwrite <klasör>`: anahtar yalnızca o klasöre yazabilir, okuyamaz, silemez, ezemez (merkez ele geçirilse de eski kopyalar korunur). rrsync istemcinin yolunu kendi klasörüne göre yorumladığı için bu yolda hedef yolsuz verilir (`kullanici@sunucu`); rrsync olmayan sunucuda (NAS) yollu hedef ve kısıtsız anahtar. Kopyalama makinenin rsync ve ssh'ıyla, abarow kullanıcısının systemd birimiyle yapılır (container'da değil); aynı anda iki kopya çalışmaz (flock). Gerçek kurulum testinde iki makineyle denendi (harici disk tmpfs ile taklit: aynı disk, disk çıkarılınca/takılınca; SSH: kısıtsız ve rrsync'li, anahtarın sınırları). Yedekler şifrelenmez (sonra: şifreleme anahtarının saklanması ve geri yükleme birlikte tasarlanmalı). `headquarters-install.sh restore <dosya>` önce o anki hali yedekler, sonra yedeği yükler ve yedekten sonraki olayları merkez stream'inden (90 gün) yeniden işletir (`ingest replay`: ingest'in tüketicisi silinir; çalışan ingest onu yeniden kurar ve stream'i baştan okur, var olan olaylar olay kimliğiyle atlanır).

## 16. Açık kaynak notları

- **Lisans: Apache-2.0** (onaylandı; `LICENSE`, `NOTICE`, `AUTHORS` ve `CONTRIBUTING.md` repo kökünde). İstenenler: kod serbestçe kullanılsın ve değiştirilsin, her kaynak dosyada yazarın adı kalsın, yazara dava açılamasın.
  - Kod serbestçe kullanılabilir, değiştirilebilir, satılabilir.
  - Her kaynak dosyanın başında telif satırı bulunur ve değiştirilmiş sürümlerde de silinemez (md. 4c). `NOTICE` dosyasındaki atıf her dağıtımla birlikte gider (md. 4d).
  - Yazılım garantisiz sunulur, sorumluluk sınırlıdır (md. 7–8). Proje hakkında patent davası açanın patent izni düşer (md. 3).
  - Katkılar otomatik olarak Apache-2.0 ile gelir (md. 5); katkıcılar commit'lerini DCO (`Signed-off-by`) ile imzalar.
  - **Katkıcıların isim hakkı:** Katkı yapan herkes adını `AUTHORS` dosyasına ekleyebilir. Bir dosyada kayda değer değişiklik yapan kişi başlığa kendi telif satırını da ekleyebilir (Apache-2.0 md. 4 buna zaten izin verir). Mevcut satırlar silinmez veya değiştirilmez; yeni satır altına eklenir.
  - CI'da `scripts/check-headers.sh` başlığı olmayan ya da kurucu satırı silinmiş dosyayı reddeder.
  - Lisans proje adını kapsamaz (md. 6). Değiştirilmiş sürümlerin projenin adıyla satılmasını engellemek için ad TÜRKPATENT'te marka olarak tescil ettirilebilir.

  ```go
  // Copyright 2026 Mehmet ERGULCU and the abarow contributors
  // Copyright 2027 Ayşe Yılmaz          ← isteğe bağlı: dosyayı önemli ölçüde değiştiren katkıcı
  // SPDX-License-Identifier: Apache-2.0
  ```
- **ÖKC adapter'ları ayrı container:** Üretici SDK'ları çoğu zaman kapalı kaynak veya NDA'lıdır. Adapter ayrı bir süreçte çalışıp gRPC sözleşmesiyle konuştuğu için kapalı kaynak bir adapter de projeye dokunmadan takılabilir. Repo'da varsayılan olarak `services/fiscal/simulator` gelir (bkz. bölüm 5).
- **i18n baştan:** Türkçe varsayılan; metinler çeviri dosyalarında, para birimi ve KDV oranları ayardan gelir.
- **Kurulum:** Multi-arch image'lar GHCR'de yayınlanır; tek bir script Debian'a Podman'ı, kiosk'u ve Quadlet dosyalarını kurar. Script kurulum tipini sorar: tek kasa, kasa (operasyona bağlı), operasyon, merkez.

## 17. Repo yapısı

```
pointofsale/
├── LICENSE                 # Apache-2.0
├── NOTICE                  # atıf
├── CONTRIBUTING.md         # katkı kuralları
├── AUTHORS                 # kurucu + katkıcılar
├── README.md               # proje tanıtımı, ABAROW açılımı
├── go.work                 # tüm Go modüllerini tek workspace'te toplar
├── proto/                  # gRPC servisleri + olay şemaları (com.abarow.sales.v1 gibi sürümlü)
├── gen/                    # proto'dan üretilen Go kodu (abarow.com/gen; scripts/gen.sh)
├── pkg/                    # ortak küçük paketler: money, service (az tutulmalı)
├── scripts/                # check.sh (tüm kontroller), gen.sh (kod üretimi)
├── tools/                  # geliştirme araçları: register-sim (senaryolar), ui-smoke (tarayıcı testi)
├── services/              # Go servisleri (her modül ayrı container); modül yolu dizin yoluna eşit: abarow.com/services/core/sales
│   ├── core/               # her kasada olan servisler
│   │   ├── gateway/
│   │   ├── catalog/
│   │   ├── sales/
│   │   ├── payment/
│   │   ├── staff/
│   │   └── device/
│   ├── modules/            # dikey modüller (kurulumda biri seçilir)
│   │   ├── retail/
│   │   └── restaurant/
│   ├── fiscal/             # fiscal adapter'ları (her biri ayrı container)
│   │   ├── simulator/      # mali değeri olmayan fiş, varsayılan
│   │   └── beko/           # Beko X30TR adapter'ı
│   ├── store/              # mağaza sunucusu: report (catalog, sync, nats core'dan gelir)
│   ├── headquarters/       # merkez: ingest, reporting, catalog-admin
│   ├── inventory/          # stok
│   └── update/             # güncelleme merkezi: agent, server, auth
├── ui/                     # Svelte: packages/{core,admin} + apps/{retail,restaurant,store,headquarters}
├── deploy/
│   ├── quadlet/            # kasa ve operasyon: Podman + systemd birimleri
│   ├── container/          # image tarifleri: Go servisi, ekranlı modül
│   ├── compose/            # Docker simülasyonu: kasa, operasyon, merkez
│   └── kiosk/              # cage + Chromium başlatma
└── docs/
```

Depo şimdilik yerel git (`main` dalı) üzerinde; ara ara GitHub'daki özel (private) depoya ([abarow/abarow](https://github.com/abarow/abarow)) gönderilir. Açık kaynak olarak yayınlanana kadar private kalır.

Her Go modülü workspace'teki diğer modüllere `replace` ile bağlanır (ör. `replace abarow.com/pkg => ../../pkg`). Neden: `go mod tidy` `go.work`'ü dikkate almaz ve depo private olduğu için `abarow.com` modülleri indirilemez. Kök dizin bir modül olmadığından `go test ./...` çalışmaz; tüm modüller `scripts/check.sh` ile kontrol edilir (gofmt, build, vet, test).

### İsim alanı (namespace)

Ortak önek `com.abarow`. Go modül yolu ise projenin kendi alan adı `abarow.com`. Go'da paketler ters domainle adlandırılmadığı için önek her teknolojide kendi geleneğine göre uygulanır:

| Yer | Biçim | Örnek |
|---|---|---|
| Go modül yolu | Alan adı normal sırada | `abarow.com/services/core/sales` |
| Go paket adı | Tek kısa kelime (Go kuralı) | `package sales` |
| Protobuf paketi | Ters domain, sürümlü | `package com.abarow.sales.v1;` |
| Olay tipi (CloudEvents `type`) | Ters domain | `com.abarow.sale.completed.v1` |
| NATS subject | Kısa | `sale.completed.v1` |
| Container image etiketleri | Ters domain | `com.abarow.module=retail` |

- Go modül yolu `com.abarow/...` olamaz: `go get` ilk parçayı gerçek bir alan adı olarak çözmeye çalışır. `com.abarow` çözülemediği için dışarıdan kimse paketi indiremez.
- Depo private olduğu sürece geliştirme makinelerinde `GOPRIVATE=abarow.com` ayarlanır; Go araçları paketi herkese açık proxy'de aramaz.
- Depo herkese açılınca `abarow.com`'a küçük bir yönlendirme sayfası (`<meta name="go-import" content="abarow.com git https://github.com/abarow/abarow">`) konur; böylece `go get abarow.com/...` GitHub'daki depoyu bulur.
- Alan adının süresi dolarsa import yolları kırılır; alan adı otomatik yenilemede tutulmalı.

## 18. Açık sorular ve kararlar

Açık: **mal iadesi** belgelemesi; mali müşavirin cevabı bekleniyor ([goods-return-note.md](decisions/goods-return-note.md)).

Kararlar (29.10.2026):

- **Çevre birimleri:** bölüm 12'de. Gerçek donanımla deneme yapılmayacak; parçalar yayımlanmış belgelere göre yazıldı, simülatörle denendi.
- **İlk gerçek ÖKC:** Beko X30TR. Kart ve yemek kartı ödemesi ÖKC'nin içindeki POS'tan alınır, ayrı banka POS'u yazılmaz. Geliştirme ÖKC simülatörüyle sürer; cihaza geçiş [beko-x30tr-prep.md](decisions/beko-x30tr-prep.md).
- **Ödeme türleri:** nakit, kredi kartı, yemek kartı/çeki.
- **Gün sonu:** mesaj yolu kesikken eksik raporla Z alınmaz; hat düzelince ya da ertesi gün alınan Z son Z'den bu yana olanı tam kapsar.
- **Yedekler** şifrelenmez.
- **Envanter ve depo yönetimi** ayrı modüldür: stok takibi, mal kabul, sayım, transfer; stok mağazalarda ve merkez deposunda, merkez hepsini görür.
- **Restoran modülü** en son yazılır.

## 19. Envanter ve depo (karar 29.10.2026)

Envanter ayrı bir modüldür: satış servislerine dokunmaz, onların olaylarını okur. Kasada çalışmaz; kasa stok bilmeden satar.

**Yerler:** her mağazanın stoğu kendi operasyonundadır; merkez deposunun stoğu merkezdedir. Merkez bütün yerleri görür. İkisi **aynı servistir** (`inventory`): operasyonda mağaza kapsamında (SQLite, tek yer), merkezde merkez kapsamında (PostgreSQL, bütün yerler); kapsam yapılandırmadan (`ABAROW_DATABASE_URL` varsa merkez) ve depolama adaptöründen gelir, kurallar, use case'ler, HTTP API'si ve ekran bileşenleri ortaktır.

**Model:** stok, hareketlerin toplamıdır. Her hareket tek bir yerin tek bir ürünündedir: miktar (işaretli, binde bir birim: 1000 = 1 adet ya da 1 kg), tür, dayanak, zaman ve kişi. Hareket türleri:

| Tür | Miktar | Dayanak |
| --- | --- | --- |
| satış | − | satış kimliği ve satır (kasadan `sale.completed.v1`) |
| mal kabul | + | tedarikçi ve irsaliye no |
| sayım farkı | ± | sayım no; sayılan − sistemdeki |
| transfer çıkışı | − | transfer kimliği |
| transfer girişi | + | transfer kimliği |

Hareketler değişmez; yanlış girilen mal kabul ters hareketle düzeltilir. Stok eksiye düşebilir: satış durmaz (malı rafta olan kasiyer satar), eksi stok ekranda göze batar ve sayımla düzeltilir.

**Mağaza (operasyon):**
- Satışlar: envanter mağaza stream'indeki `sale.completed.v1` olaylarını kendi kalıcı tüketicisiyle okur, her satırı satış hareketi yapar. Aynı satış iki kez gelse de bir kez düşer (satış kimliği ve satır no). İptal edilen satış hiç tamamlanmadığı için stoğa dokunmaz; mal iadesi (henüz yok) gelince giriş hareketi olur.
- Mal kabul, sayım ve stok listesi operasyon ekranındadır ("Stok"). Ekran report'a bağlanır; report stok isteklerini oturumdaki kişinin kimliğiyle envantere iletir (envanterin portu dışarı açılmaz).
- Her hareket `store.stock.moved.v1` olarak mağaza stream'ine yazılır ve merkeze satışlarla birlikte gider.

**Merkez:**
- Merkez deposunun mal kabulü ve sayımı merkez ekranındadır.
- Mağazaların stoğu, mağazalardan gelen `store.stock.moved.v1` olaylarından tutulur. Merkez ekranı ürün başına her yerin stoğunu gösterir.

**Transfer** (merkez deposu ↔ mağaza, mağaza ↔ mağaza): gönderen yer çıkış hareketini yapar, alan yer malı teslim alınca giriş hareketini yapar; arada transfer yoldadır ve ekranda görünür. Mağazaya gelen transferler merkezden aşağı inen bir stream'le gider (katalogdaki gibi); mağazanın gönderdiği transfer mağaza stream'iyle merkeze çıkar. Gönderilen ile alınan farklıysa fark alan yerin kaydında görünür.

**Kurulum sırası:** (1) mağaza stoğu: satışla düşme, mal kabul, sayım; (2) merkez deposu ve bütün yerlerin stoğu; (3) transfer.

**Uygulandı (1):** `inventory`, mağaza kapsamı (PostgreSQL; ilk sürümde SQLite `inventory.db` idi). Satışları mağaza stream'inden `inventory` kalıcı tüketicisiyle okur; merkeze bağlı mağazada ürünleri katalog stream'inden alır, bağlı olmayanda satış satırından adıyla öğrenir. Hareket, stok ve outbox aynı işlemde yazılır; mal kabul, sayım ve satış tek kilitten geçer (sayımın okuduğu stokla yazdığı fark arasına satış girmez). Miktar birimi denetlenir: adetli ürüne kesirli miktar girilmez. Arama Türkçe harflerde büyük/küçük ayırmaz (ad, Türkçe kurallarla küçük harfe çevrilmiş ayrı bir sütunda aranır). Ekran: operasyon "Stok" (liste, mal kabul, sayım, ürünün hareketleri); report `/api/stock` isteklerini oturumdaki kişiyle (`pkg/login` başlıkları, merkezdeki yöntemin aynısı) envantere iletir, ekrandan gelen aynı adlı başlıkları siler. NATS yetkisi: mağaza stream'inde ve katalogda yalnızca kendi tüketicisi, yayında yalnızca `store.stock.>`.

**Uygulandı (2):** `inventory`, merkez kapsamı (merkez veritabanında `stock_` tabloları; imaj `abarow-inventory-headquarters`). Mağazaların hareketlerini merkez stream'inden (`<mağaza>.store.stock.moved.v1`) kendi kalıcı tüketicisiyle okur: yer konudan alınır, olaydaki yer konuyla tutmazsa olay reddedilir (mağaza başka mağaza adına stok yazamaz). Aynı hareket bir kez sayılır. Ürünleri merkezin katalog stream'inden alır. Merkez deposunun mal kabulü ve sayımı aynı kurallarla (`pkg/stock`, mağazayla ortak). Ekran: merkez "Stok" (yer başına sütunlar, toplam, hareketler; mal kabul ve sayım merkez deposuna). Oturumu reporting denetler, kişiyi başlıklarla iletir (catalog-admin gibi); merkez deposuna yönetici ve katalog kullanıcısı girer, rapor kullanıcısı yalnızca görür ve yalnızca kendi mağazalarının stoğunu görür. NATS yetkisi: merkez stream'inde ve katalogda yalnızca kendi tüketicisi; yayın yok.

**Uygulandı (3), transfer:** Gönderen yer çıkış hareketini yapar (stoğundan hemen düşer, karşı yer harekette yazılı); transfer alan yer teslim alana kadar yoldadır. Teslim alan kişi gelen miktarı girer: eksik gelen az, hiç gelmeyen sıfır; gönderilen ve alınan ayrı kaydedilir, ikisi de iki ekranda görünür. Bir transfer bir kez ve yalnızca alan yerde teslim alınır. Kurallar `pkg/stock`'tadır (iki taraf ortak).
- *Mağazanın gönderdiği:* çıkış hareketi her hareket gibi mağaza stream'iyle merkeze çıkar; merkez transferi açar. Alan yer başka mağazaysa merkez transferi ona iletir, merkez deposuysa depo merkez ekranından teslim alır.
- *Mağazaya gelen:* merkez transfer stream'ine (`ABAROW_TRANSFERS`, merkezin envanteri kurar ve yayınlar) `transfer.<mağaza>.<transfer>.sent.v1` yazar; her mağazanın envanteri bu stream'den yalnızca kendi konularını kaynak alır (kendi NATS'ında aynı adlı stream). Merkezle bağlantı kesikken gelmiş transferler bağlantı gelince iner. Teslim alma giriş hareketi olarak merkeze çıkar; merkez transferi kapatır.
- *Yer listesi:* merkez deposu ve mağazalar (merkezin mağaza listesi, `stores.json`) `transfer.locations.v1` olarak aynı stream'den iner; operasyon transfer ekranında bunlardan seçer. Merkez envanteri açılışta yayınlar; mağaza eklenince kurulum betiği servisi yeniden başlatır.
- *Ekran:* operasyon "Stok" → "Transfer gönder", "Transferler" (yoldaki gelenler önce, teslim al); merkez "Stok" → depodan "Transfer gönder", "Transferler" (görülen yerlerinkiler; depoya geleni teslim al). Ürünün hareketlerinde karşı yer görünür.
- *Bilinen sınır:* mağaza, gönderdiği transferin karşıda teslim alındığını kendi ekranında görmez ("gönderildi" yazar); teslim durumu merkez ekranındadır.
- *NATS yetkisi:* merkez envanteri transfer stream'ini kurar ve `transfer.>` yayınlar; mağaza envanteri yalnızca kendi transfer stream'ini kurar ve okur; mağazanın merkeze leaf bağlantısı transfer stream'inde yalnızca kaynak tüketicisi açabilir.

## 20. Restoran (karar 30.10.2026)

Restoran dikey modülü (`services/modules/restaurant`, ekranı `ui/apps/restaurant`) marketin yerine geçer: gateway'de `ABAROW_MODULE=restaurant`. Çekirdek (katalog, satış, ödeme, vardiya, cihaz) ve üst katmana giden olaylar (`sale.completed.v1`) aynıdır.

**İlk sürümün kapsamı:**
- **Salon ve masa:** salonlar (ör. "İç salon", "Bahçe") ve masalar; müdür kasadan tanımlar. Ekranda salon sekmeleri, masa kutuları (boş, dolu, hesap istendi; açık tutar ve süre).
- **Adisyon (sipariş):** masaya açılır. Satır: ürün, adet, seçilen opsiyonlar, not ("soğansız"), ekleyen garson. Fiyat ve KDV satır eklenince katalogdan alınır, sonra değişmez.
- **Mutfağa gönder:** bekleyen satırlar ürünün istasyonuna (mutfak, bar) göre ayrı fişlere basılır. Gönderilmemiş satır serbestçe değişir ve silinir; gönderilmiş satırın iptali müdür onayı ister, nedeni kaydedilir ve istasyona iptal fişi gider.
- **Adisyon dökümü:** hesap istenince basılır; simülatörde "MALİ DEĞERİ YOKTUR". Gerçek ÖKC'de e-Adisyon (kılavuz bölüm 3.8) ÖKC entegrasyonuyla yapılacak (bkz. TODO).
- **Ödeme ve hesap bölme:** adisyonun tamamı ya da seçilen satırlar ve adetler ödenir. Seçilenler `sales.CreateSale` ile satışa çevrilir, ödeme ekranı marketle ortaktır. Satış ödenince o satırlar "ödendi" olur, hepsi ödenince masa kapanır. Ödeme iptal edilirse satırlar adisyona döner.
- **Masa taşıma ve birleştirme:** adisyon başka boş masaya taşınır; iki masanın adisyonu birleştirilir.
- **Ürün seçimi:** kategori sekmeleri, görselli ürün kartları (görsel yoksa adın baş harfleri), opsiyon penceresi (zorunlu ve isteğe bağlı gruplar, fiyat farkıyla).

**Katalogda yeni alanlar (iki sektör ortak):**
- *Kategori ve menü sırası:* ürünün menü kategorisi (ad) ve sırası. Kategoriler en küçük sıralı ürünlerine göre, ürünler sıraya ve ada göre dizilir.
- *İstasyon:* mutfak fişinin gideceği yer (`kitchen`, `bar`; boşsa mutfak).
- *Opsiyon grupları:* ürünün içinde: grup adı, en az ve en çok seçim, seçenekler (ad, KDV dahil fiyat farkı). Satırın fiyatı ürün fiyatı + seçilen fiyat farklarıdır; satışa ad ve seçimlerle tek satır olarak gider ("Köfte (az pişmiş, + kaşar)").
- *Görsel:* merkez ekranından yüklenir. Tarayıcı küçültür (en çok 640 px), sunucu türü (JPEG, PNG, WebP) ve boyutu (en çok 512 KB) denetler. Görsel katalog stream'inde ürünün yanında ayrı konudadır (`catalog.image.<ürün>`, konu başına son hal): operasyona ve kasalara katalogla aynı yoldan, yeni akış ve yetki gerektirmeden iner. Kasada katalog veritabanında saklanır; ekran özetiyle (`?h=<sha256>`) ister, tarayıcı süresiz önbellekler. Tek kasalı kurulumda başlangıç dosyasından da yüklenir.

**Mobil siparişe hazırlık (şimdi yazılmayacak; tasarım buna göre):**
- *Tek giriş noktası:* restoranın bütün sipariş işleri modülün HTTP API'sidir (`/api/module/...`: menü, görseller, masalar, adisyonlar, satır ekleme, mutfağa gönderme). Kasa ekranı bu API'nin bir istemcisidir; garson telefonu ya da tableti başka bir istemcisi olur. Ekranda iş kuralı yoktur.
- *Tekrar güvenliği:* satırı istemci kendi ürettiği kimlikle ekler; zayıf Wi-Fi'de tekrar gönderilen istek ikinci satır açmaz. Mutfağa gönderme de aynı satırı iki kez basmaz.
- *Eşzamanlılık:* her adisyonun sürümü vardır; değişiklikler sunucuda sırayla uygulanır, istemci eski sürümle masa taşıma, birleştirme ya da ödeme yapamaz (409). Değişiklikler bir olay akışıyla (`GET /api/module/events`, SSE) bütün istemcilere bildirilir: bir terminalde eklenen sipariş ötekinde hemen görünür.
- *Kim:* her satırda ve olayda ekleyen kişi (garson) vardır; kimlik bugün kasanın oturumundan (kasiyer PIN'i), mobilde garsonun kendi PIN'iyle açtığı oturumdan gelir.
- *Ödeme yalnızca kasada:* telefon sipariş alır, ödeme ve mali fiş ÖKC'li kasadadır.
- *Çok terminal ve mobil kurulum (sonra):* aynı restaurant image'ı operasyonda "sunucu" olarak çalışır (masalar ve adisyonlar orada, telefonlar ve kasalar ona bağlanır, mutfak yazıcıları ağdan oradan basılır). Kasadaki restaurant adisyonları yerel veritabanı yerine operasyondan okur (aynı port, HTTP adapter'ı); ödemeyi kendi `sales`'ıyla alır. İlk sürüm tek kasalıdır: masalar ve adisyonlar kasanın `restaurant.db`'sindedir.

**Uygulandı (1), katalog:** Kurallar `pkg/menu`'dedir (opsiyon grupları ve seçimi, istasyon, görsel denetimi); merkez kataloğu, kasa kataloğu ve restoran aynısını kullanır. Merkez: "Ürünler" ekranında menü kategorisi, sırası, istasyon, opsiyon grupları ve görsel (tarayıcıda 640 px JPEG'e küçültülür; sunucu türü içerikten tanır, SVG kabul etmez). Görsel ürünün sürümüyle yazılır: yükleme ve kaldırma eski sürümle reddedilir, değişiklik kaydına girer; önce görselin sonra ürünün mesajı outbox'a yazılır. `catalog-admin image <kod>` ve `headquarters-install.sh catalog-load` dosyadaki görselleri yükler. Kasa: menü (`ListMenu`), tek ürün (`GetProduct`) ve görsel (`GetImage`, yalnızca özeti ürününkini tutan) gRPC'de; başlangıç dosyasında `"image"`. Katalog stream'ini okuyan öteki servisler (operasyon report ve stok, merkez stok) yalnızca `catalog.product.>`'u okur. Deneme menüsü: `deploy/compose/seed/restaurant/` (12 ürün, düz çizim görseller).

**Uygulandı (2), restaurant servisi:** `services/modules/restaurant` (hexagonal; `restaurant.db`). Adisyon JSON belge olarak saklanır; bir masada tek açık adisyonu veritabanı da garanti eder. Bütün değişiklikler tek kilitten geçer.
- *API* (`/api/module/...`): `menu`, `images/{ürün}?h=`, `floor` (PUT: müdür onayı), `tables/{masa}/open` (masa doluysa açık adisyonu döner), `orders/{id}`, `lines` (istemci kimlikli), `lines/{satır}` (PATCH), `lines/{satır}/void` (müdür onayı), `send`, `bill`, `pay` (sürüm ve hesap bölme), `settle`, `move`, `merge`, `close` (boş adisyon), `events` (SSE).
- *Mutfağa gönderme:* istasyon başına ayrı fiş; bir istasyonun yazıcısı basamazsa onun satırları bekler, ötekininki basılır (tekrar gönderilince yalnızca bekleyenler). `device`'a istasyon yazıcıları eklendi (`ABAROW_KITCHEN_PRINTER`, `ABAROW_BAR_PRINTER`; yoksa fiş yazıcısı).
- *İptal:* gönderilmiş satır nedeniyle ve müdür onayıyla (staff `ACTION_VOID_ORDER_LINE`; salon düzeni `ACTION_EDIT_FLOOR`) iptal edilir; kısmi iptal ayrı iptal satırı olur, istasyona iptal fişi basılır, basılamazsa iptal yapılmaz.
- *Ödeme:* seçilen adetler satışa çevrilir (satır adı opsiyonlarla: "Köfte (az pişmiş)"), satış sürerken o adetler ödemededir. Sonuç satıştan okunur (`settle`; masa listesi ve adisyon açılırken de): ödendiyse işlenir, iptal edildiyse adetler adisyona döner. Ödeme ekranı kapanmadan kasa kapansa da kayıp olmaz. Gönderilmemiş satır ödenmez.
- *Kim:* gateway kasiyerin adını da iletir (`X-Abarow-Cashier-Name`); satırda ve fişte garsonun adı yazar. Kapanan adisyonlar 30 gün saklanır (satışlar sales'te kalıcıdır).

**Uygulandı (3), ekran:** Kasa ekranlarının ortak parçaları `ui/packages/core`'a taşındı (giriş, vardiya, kasa işlemleri, müdür onayı, ödeme, raporlar, ortak görünüm `register.css`, ortak API); market ve restoran ekranı onu `@abarow/core` adıyla kullanır (Vite takma adı; container derlemesi paketi depodaki yerine koyar). Restoran ekranı (`ui/apps/restaurant`): salon sekmeleri ve masa kutuları (boş, dolu, hesap istendi, ödemede; tutar, süre, gönderilmemiş satır), adisyon (satır durumları, adet, not, iptal), görselli menü (kategori sekmeleri; görsel yoksa baş harfler), opsiyon penceresi (zorunlu seçim yapılmadan "Ekle" kapalı), hesap bölme, taşı/birleştir, salon düzeni (müdür onayı). Satır kimliği ekranda üretilir (tekrar güvenli). Değişiklik akışı (SSE) başka terminaldeki değişikliği tazeler. Ürün görselleri gateway'den oturumsuz sunulur (`<img>` başlık taşıyamaz; görseller gizli değil, yalnızca okunur); geri kalan restoran API'si oturum ve açık vardiya ister.

**Uygulandı (4), kurulum ve deneme:** `register-install.sh --module restaurant [--kitchen-printer adres] [--bar-printer adres]` (market birimi kaldırılır, gateway restorana yönelir, seed'in `images/` klasörü kopyalanır); Quadlet `deploy/quadlet/restaurant/`. Simülasyon: `scripts/restaurant-sim.sh` (compose.yaml + `restaurant.yaml`; deneme menüsü, mutfak yazıcısı ayrı simülatör) ve tarayıcı testi `tools/ui-smoke/restaurant.mjs`: salon düzeni (müdür onayı), görselli menü, opsiyon, mutfak/bar fişi, müdür onaylı iptal, adisyon, hesap bölme (nakit + kart), masa taşıma. CI'da çalışır.

**Kurulum sırası:** (1) katalog: kategori, istasyon, opsiyon, görsel (merkez ekranı, olaylar, kasa); (2) restaurant servisi: masa, adisyon, mutfak ve adisyon fişi, ödeme ve hesap bölme, olay akışı; (3) restoran ekranı ve ortak kasa ekranı paketi (`ui/packages/core`: giriş, vardiya, ödeme, raporlar); (4) kurulum (`register-install.sh --module restaurant`), simülasyon ve tarayıcı testi.
