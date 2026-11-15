# Güvenlik taraması

> Tarih: 06.11.2026. Araçlarla yapılan otomatik tarama ve elle sınıflandırma. Bu bir sızma testi ya da güvenlik garantisi değildir; sızma testi öncesi hazırlığın ilk adımıdır.

## Yapılanlar

| Araç | Kapsam | Sonuç |
|---|---|---|
| `govulncheck` | 19 Go modülü | Yerel Go 1.27.0'da 13 standart kütüphane açığı (`net/http`, `crypto/tls`, `crypto/x509`, `html/template`, `mime/multipart`, `net/textproto`); hepsi Go 1.27.2/1.27.3'te kapalı. Container (`golang:1.27`) ve CI (`go-version: "1.27"`) en son yamayı çektiği için yayın imajları etkilenmez; **geliştirme makinesi Go 1.27.3'e yükseltilmeli**. 1.27.3 ile taramada kodun çağırdığı açık yok. |
| `govulncheck` (bağımlılıklar) | aynı | `golang.org/x/net` v0.61.0 → **v0.62.0'a yükseltildi** (18 modül). `golang.org/x/crypto` v0.58.0: `openpgp` paketi bakımsız (GO-2026-5932); kodumuz paketi içe aktarmıyor, düzeltme yalnızca sürüm etiketsiz bir commit'te, etiketli sürüm çıkınca yükseltilecek. |
| `gosec` (orta ve üstü) | 18 Go modülü (üretilen kod hariç) | 51 bulgu, aşağıda sınıflandırıldı. Gerçek açık yok; üç konu sızma testi öncesi gözden geçirilecek. |
| `npm audit` | 4 ekran uygulaması + `tools/e2e` | Ekranlarda 2 düşük şiddetli bulgu: `@sveltejs/vite-plugin-svelte` → `deepmerge` (prototype poisoning, düzeltme yok). Yalnızca derleme aracında; çalışan ekrana girmez. `tools/e2e`: 0. |

## gosec bulgularının sınıflandırması

| Kural | Adet | Değerlendirme |
|---|---|---|
| G304 / G703 (dosya yolu değişkenden) | 13 | Yollar işletmecinin kurulumda verdiği ortam değişkeni ya da bayraktan gelir (`catalog.json`, `nodes.json`, NATS dosyaları); kullanıcı girdisi değildir. `pkg/service/spa.go` (ekran dosyası sunucusu) yolu temizleyip ekran dizininde tutar. Sızma testinde yol geçişi denenmeli. |
| G115 (tam sayı dönüşümü) | 17 | Bench araçları, protobuf alanlarına sığan küçük değerler ve `pkg/money` (taşma `panic` ile reddedilir, testli). Gerçek risk görülmedi. |
| G404 (zayıf rastgelelik) | 4 | Yalnızca ölçüm ve simülasyon araçları (`operasyonbench`, `merkezbench`, `register-sim`); güvenlik amaçlı rastgelelik `crypto/rand` kullanıyor. |
| G202 (SQL birleştirme) | 4 | Birleştirilen parça sabit sütun adı, sabit `ORDER BY/LIMIT` metni ya da `%d` ile yazılan tam sayı; kullanıcı girdisi her yerde parametre (`?`, `$n`). Yanlış pozitif. |
| G124 (çerez bayrakları) | 3 | `HttpOnly` ve `SameSite` her çerezde var. `Secure`, isteğin HTTPS ile gelmesine bağlı (`login.OverHTTPS`): mağaza içi düz HTTP kurulumda çerez `Secure` olmaz. **İncelendi ([PENTEST-PREP.md](PENTEST-PREP.md)):** başlığı sahte yazan istemci yalnızca kendi çerezini `Secure` yaptırır, kazanç sağlamaz; operasyon ekranı HTTPS'e geçti (06.11.2026), düz HTTP yalnızca simülasyonda kalır. |
| G306 / G301 (dosya izni 0444 / 0755) | 6 | `tools/natsauth` NATS ve TLS dosyalarını konteynerlerin root olmayan kullanıcılarının okuması için herkese okunur yazıyor. **İncelendi:** kurulum betikleri dosyaları `0700` kipli dizine yazar ve container'lara Podman secret olarak (`mode=0400`) verir; `0444` fiilen yalnızca Compose simülasyon hacimlerinde etkilidir. Kod değişmedi; kurulu makinede izinlerin doğrulanması test ekibinden istenir. |
| G204 (alt süreç) | 1 | `services/fiscal/beko` köprü yolu yapılandırmadan gelir; kullanıcı girdisi değil. |
| G704 (SSRF) | 2 | `tools/register-sim` (simülasyon aracı). |
| G101 (sabit parola) | 1 | `services/store/internal/app/auth.go`: değişiklik kayıtlarında görünen ad ("mağaza parolası"), parola değil. Yanlış pozitif. |

## OWASP Dependency-Track (06.11.2026)

Dependency-Track 4.14.6 Docker'da kuruldu (`tools/dependency-track/compose.yaml`, kurulum ve tarama `scripts/dt-scan.sh`); kaynaklar OSV (Go ve npm), NVD ve OSS Index. 19 Go modülü ve 5 npm uygulaması için CycloneDX SBOM üretilip yüklendi (Go: modülün gerçekten içe aldığı bileşenler; npm: kilit dosyası, geliştirme bağımlılıkları dahil).

| Sonuç | |
|---|---|
| Go modülleri (19) | `github.com/klauspost/compress` v1.20.1 (GO-2026-6865, `fse`/`huff0`'da iki bloklu çerçevede panik; 1.20.2'de düzeltildi) **v1.20.3'e yükseltildi** (10 modül, hepsi dolaylı bağımlılık). Kalan: `golang.org/x/crypto` v0.58.0 GO-2026-5932 (`openpgp` paketi bakımsız; 9 modülde): paket içe aktarılmıyor, düzeltme etiketli bir sürümde değil (yalnızca 0.58.1 öncesi bir sürüm numarası), bu yüzden bekletiliyor. Önem derecesi Go açık veritabanında atanmamış (UNASSIGNED). |
| npm uygulamaları (4 ekran + `tools/e2e`) | Her ekran uygulamasında 1 düşük bulgu: `deepmerge` 4.3.1 (GHSA-gcqx-w48m-w2h4, CVE-2026-93753); `npm audit` bulgusuyla aynı, yalnızca derleme aracında. `tools/e2e` temiz. |

**Düzeltme (06.11.2026):** Bu bölümün ilk sürümü "Go: 0 bulgu" diyordu; bu yanlıştı. DT'nin proje özeti (metrikler) analizden dakikalar sonra güncellendiği için `dt-scan.sh durum` ilk okumada her projeyi 0 gösterdi. Komut artık sayıları doğrudan bulgu API'sinden okur. Doğrulama: bilinen açıklı sürümlerle (x/net v0.17.0, grpc v1.50.0, x/crypto v0.30.0) hazırlanan deneme SBOM'u 78 bulgu verdi (`v` önekli ve öneksiz sürümde aynı); `x/crypto` v0.58.0 tek başına GO-2026-5932 verdi. Yani Go açık verisi indi, SBOM'daki sürümler ve `pkg:golang` kimlikleri eşleşiyor.

Sınırlar: SBOM'lar yalnızca modül düzeyinde eşleştirir (kodun açık fonksiyonu çağırıp çağırmadığına bakmaz; bunu `govulncheck` yapar). Go standart kütüphanesi SBOM'da bileşen değildir; o açıklar yukarıda `govulncheck` ile izlenir. SBOM ve açık verisi tarama günüdür. Lisans ve politika denetimi kurulmadı. İmaj (Trivy gibi) taraması yapılmadı.

### İmaj taraması (Dependency-Track, 06.11.2026)

`bash scripts/dt-scan.sh image`: sürüm iş akışındaki (`release.yml`) 15 imaj yerelde `deploy/container/*.Containerfile` ile derlenir, Syft (`anchore/syft:v1.18.1`, Docker imajı olarak, imaj tar dosyasını okur) ile CycloneDX SBOM'a çevrilir ve DT'ye `abarow-image/<ad>` projesi olarak yüklenir. Üçüncü taraf `nats:2.12-alpine` ve `postgres:18-alpine` de yerelde varsa eklenir. Restoran imajı sürüm iş akışında henüz olmadığından taranmadı (Quadlet birimi bu imajı ister; bkz. ISIMLENDIRME.md).

| Sonuç | |
|---|---|
| abarow imajları (15) | Go standart kütüphanesi **go1.27.3**: bulgu yok. Tek bulgu `golang.org/x/crypto` v0.58.0 GO-2026-5932 (`openpgp`, kullanılmıyor; yukarıda). |
| `nats:2.12-alpine` (üçüncü taraf) | 26 bulgu: Go standart kütüphanesi go1.25.12 (21), `x/crypto` v0.55.0 (3), `musl` 1.2.5-r12 CVE-2025-26519 (yüksek), `klauspost/compress` v1.19.2 (1). İmajı biz yapmıyoruz; yeni bir `nats:2.12` yaması çıkınca güncelleme onaylanarak düzelir (kendiliğinden güncellenmez). |
| `postgres:18-alpine` (üçüncü taraf) | 101 bulgu (2 kritik, 21 yüksek): 100'ü Go standart kütüphanesi go1.24.6 (imajdaki bir Go ikilisinden; büyük olasılıkla ayrıcalık düşürme aracı), 1'i `x/sys`. Doğrulanmadı: hangi ikili olduğu ve bulguların PostgreSQL'e erişilebilir olup olmadığı. Taban imajın yeni sürümü gerekir. |

**Sahte bulgu düzeltmeleri.** Syft çıktısı olduğu gibi yüklendiğinde 15 abarow imajı başına 4–9 bulgu çıktı ve hepsi sahteydi; betik bunları SBOM'dan temizler:
- **Sürümlü modül yolları:** Syft `/v5`, `/v4`, `/v2` gibi son ekleri `pkg:golang/github.com/jackc/pgx@v5.11.0#v5` biçiminde (alt yol olarak) yazar. DT bunu eski `github.com/jackc/pgx` modülü sanıp pgx, go-jose ve nats-server için düzeltilmiş açıkları (GO-2024-2605, GO-2025-3485, GHSA-52jh-2xxh-pwh6 gibi) yanlışlıkla bildirdi. Betik biçimi `pkg:golang/github.com/jackc/pgx/v5@v5.11.0` yapar.
- **CPE:** Syft Go modüllerine CPE ekler; `google.golang.org/protobuf` için C++, Java ve Python protobuf'un NVD kayıtları (CVE-2015-5237, CVE-2021-22570, CVE-2024-7254, CVE-2026-0994) eşleşti. Betik Go modüllerinin CPE'sini atar (modüller purl ile OSV'den eşleşir); `stdlib` CPE'si kalır.

Bu sahte bulguların sahte olduğu OSV kayıtlarıyla doğrulandı (düzeltme sürümleri bizdekinden eski); modül SBOM'larında (üstte) bu bulgular hiç çıkmamıştı.

## OWASP ZAP (14.11.2026)

Çalışan ekranlara ve API'lere dinamik tarama. Tarama, merkezli uçtan uca test yığınında yapıldı (`bash scripts/e2e.sh up headquarters`). Bu yığın düz HTTP kullanıyor ve yalnız tohum verisi içeriyor. Komut: `python3 scripts/zap-scan.py`. ZAP sürümü 2.17.0 (`ghcr.io/zaproxy/zaproxy:stable`). Gerçek bir kuruluma çalıştırılmaz: etkin tarama saldırı dener ve çöp veri yazar.

**Nasıl taranıyor:**

- **Oturum:** betik her ekrana yığının deneme hesaplarıyla girer. Kasaya kasiyer PIN'iyle girip jeton alır; mağaza sunucusuna ve merkeze parolayla girip oturum çerezi alır. ZAP oturumu her isteğe ekler. Önce yalnız oturumla 200 dönen bir deneme isteği gönderilir; oturum çalışmazsa tarama durur.
- **İstekler:** ekranlar tek sayfalık uygulama olduğu için örümcek API'yi bulamaz. Bu yüzden her ekranın arkasındaki servislerin API yolları koddan alınıp istenir: kasada 38, mağaza sunucusunda 27, merkezde 31 yol.
- **Gövdeler:** serbest metin alan yazma istekleri gerçek gövdelerle bir kez daha gönderilir, etkin taramanın saldıracağı alan olsun diye. Bunlar kasiyer, kullanıcı, ürün, fiyat, mal kabul ve sayım istekleri.
- **Tarama dışı:** giriş, çıkış ve parola değiştirme. Tarama oturumu bitirir, deneme hesaplarını kilitlerdi.
- **Kapsam:** her ekranda pasif ve etkin tarama.

**Sonuç:** gerçek bir açık bulunmadı.

| Bulgu | Nerede | Değerlendirme |
|---|---|---|
| Path Traversal (High, güven düşük) | Mağaza sunucusunda `POST /api/cashiers` (`id`), merkezde `POST /api/users` (`username`) | **Sahte bulgu.** ZAP alana yolun son parçasını ("cashiers", "users") yazdı ve yanıt rastgele değerdekinden farklı çıktı. Fark, taramanın kendisinin daha önce aynı kodla kişi oluşturmuş olmasından (409). Gerçek yol geçişi değeri (`../../etc/passwd`) doğrulamada reddediliyor (422). Bu alanlar veritabanına ve NATS konusuna gidiyor, dosya yolu olmuyor; ilgili paketlerde dosya açan kod yok. |
| CSP, tıklama korsanlığı (X-Frame-Options), X-Content-Type-Options başlıkları yok (Medium/Low) | Yalnız kasa ekranı (gateway) | **Bilinen ve kabul edilmiş** ([PENTEST-PREP.md](PENTEST-PREP.md), madde 2): kasa ekranı yalnız kasanın kendisinde (`127.0.0.1`) sunuluyor. Mağaza sunucusu ve merkez ekranında bu başlıklar var ve tarama eksik bulmadı. |
| HTTP Only Site (Medium) | Üç ekran | **Test yığınından.** Compose yığını düz HTTP. Gerçek kurulumda mağaza sunucusu ekranı HTTPS, merkez Caddy arkasında HTTPS ve HSTS'li, kasa yalnız yerel. |
| Modern Web Application, User Agent Fuzzer, Authentication Request Identified (bilgi) | | Bilgi amaçlı; açık değil. |

**Sınırlar:**

- **Kimlikli tarama:** her ekranda tek bir rolle yapıldı: kasada kasiyer, mağaza sunucusunda mağaza parolası, merkezde yönetici. Rol başına yetki atlama (ör. kasiyerin müdür işlemi yapması, bir mağaza kullanıcısının başka mağazayı görmesi) ZAP'ın denetlemediği bir konu. Bu, sızma testinin kapsamında ([PENTEST-PREP.md](PENTEST-PREP.md)).
- **Var olmayan kayıtlar:** yol parametreli isteklerin çoğu var olmayan bir kimlikle gitti (`zap1`). Bu yüzden 404 döndüler ve gövdeleri işlenmedi. Gerçek gövdeyle gönderilenler yukarıdaki yazma istekleri.
- **Süre:** etkin tarama ekran başına 2–3 dakika sürdü. Tavan 20 dakikaydı, ama taranacak alan az olduğu için erken bitti.
- **Dışarıda kalanlar:** NATS (leaf TLS ve NKey yetkileri) ve kurulum betikleri ZAP'ın kapsamında değil.

## Yapılmayanlar (açık)

- **SonarQube for IDE:** IDE eklentisi olduğundan komut satırından çalıştırılamadı; IDE'de açılıp bulgular elle gözden geçirilmeli. Alternatif: SonarQube Community container'ı + `sonar-scanner`.
- Bu belgedeki gosec ve npm bulguları yalnızca sınıflandırıldı; incelenen üç konuda ([PENTEST-PREP.md](PENTEST-PREP.md)) kod değişikliği gerekmedi.
