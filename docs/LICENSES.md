# Lisanslar: abarow'un kendi lisansı ve içindeki üçüncü taraf kod

> Tarih: 14.11.2026. Bu belge bir hukuki görüş değildir; yazılımın içinde hangi kodun hangi lisansla durduğunu ve bunun için ne yapıldığını anlatır. Yayından önce avukata göstermek iyi olur.

## Kısaca

- **abarow'un kendisi** Apache-2.0'dır: `LICENSE` (resmî metin, değişmemiş), `NOTICE` (telif ve anılması gerekenler), `AUTHORS`, her kaynak dosyada başlık.
- **İçindeki başkalarının kodu** (Go modülleri, ekranlara giren npm paketleri, Patroni imajının bileşenleri) kendi lisanslarıyla durur. Bu lisanslar, kodu dağıtırken **telif bildirimini ve lisans metnini birlikte vermeyi** ister. Bunun için `THIRD_PARTY_NOTICES` dosyası üretilir ve her imajın içine konur.
- **İncelenen 25 Go modülünün ve ekranlardaki 4 npm paketinin hiçbiri copyleft değildir.** Hepsi izin verici lisanslıdır. Copyleft bileşen yalnızca Patroni imajındadır (aşağıda).

## Ne yapılıyor

| Ne | Nerede |
|---|---|
| Lisans metni, bildirimler | Her imajda `/licenses/LICENSE`, `/licenses/NOTICE`, `/licenses/THIRD_PARTY_NOTICES` (Go servisleri, ekranlı servisler, Patroni imajı) |
| Bağımlılıkların tam listesi ve metinleri | Depoda `THIRD_PARTY_NOTICES` (üreten betik: `scripts/third-party-notices.py`) |
| Apache bağımlılıklarının `NOTICE` metinleri | `NOTICE` dosyasında aynen (Apache-2.0 madde 4(d)): `go-oidc`, `grpc` |
| Inter yazı tipi (OFL) | `NOTICE`'te anılır; metni her ekranla birlikte `/licenses/Inter-OFL.txt` olarak sunulur |
| Patroni imajındaki LGPL ve GPL metinleri | İmajda `/licenses/LGPL-3.0.txt`, `/licenses/GPL-3.0.txt` (`deploy/container/patroni/licenses/`) |
| Liste güncel mi, yasak lisans girdi mi | CI'da `python3 scripts/third-party-notices.py --check` (`contracts` işi) |

Yeni bir bağımlılık eklenince `python3 scripts/third-party-notices.py` çalıştırılır ve dosya değişiklikle birlikte gönderilir; unutulursa CI durur. Kural [CONTRIBUTING.md](../CONTRIBUTING.md)'de.

## Denetim sonucu (14.11.2026)

**Go modülleri** (servislerin derlendiği 25 harici modül; `go list -deps`, `go.work`taki bütün modüller):

| Lisans | Modül sayısı | Örnek |
|---|---|---|
| Apache-2.0 | 9 | `nats.go`, `grpc`, `go-jose`, `go-oidc`, `klauspost/compress` |
| MIT | 7 | `pgx`, `goose`, `puddle`, `multierr` |
| BSD-3-Clause | 8 | `golang.org/x/*`, `protobuf`, `google/uuid` |
| BSD-2-Clause | 1 | `godbus/dbus` |

Go standart kütüphanesi de (BSD-3-Clause) servislerin içindedir ve listededir. İkisinin (`go-oidc`, `grpc`) ayrıca `NOTICE` dosyası vardır; `NOTICE`'e taşındı.

**Ekranlar** (Vite'ın tek pakete derlediği kod): `svelte`, `esm-env`, `clsx` ve Vite'ın küçük yardımcısı; hepsi MIT. Svelte ve Vite'ın diğer bağımlılıkları yalnızca derleme sırasında çalışır, ekrana girmez. Yazı tipi Inter: SIL Open Font License 1.1.

**Patroni imajı** (`abarow-patroni`; merkezin üç makineli kurulumu): Alpine tabanlı PostgreSQL imajına Python paketleri eklenir. Çoğu MIT, BSD, ISC, Apache. İki bileşen "ödünç lisans" taşır:

| Bileşen | Lisans | Ne demek |
|---|---|---|
| `psycopg2-binary` | LGPL-3.0 (OpenSSL istisnasıyla) | Ayrı bir Python paketidir, abarow'un kodu içine gömülü değildir. Başka bir sürümle değiştirilebilir (`pip`). Bu yüzden abarow'u Apache-2.0 olmaktan çıkarmaz. Metin imajda. |
| `certifi` | MPL-2.0 | Değiştirilmeden kullanılır; kaynağı herkese açık. |
| Alpine tabanı | `busybox` GPL-2.0, Python PSF, PostgreSQL | Taban imajın parçası. Kaynak kodlarının nereden alınacağı `THIRD_PARTY_NOTICES` bölüm 3'te yazılı. |

**Bizim dağıtmadığımız ama kurulumun yukarı akıştan çektiği imajlar** (değiştirilmeden, kendi sahiplerinden): Caddy (Apache-2.0), NATS (Apache-2.0), etcd (Apache-2.0), zot (Apache-2.0), PostgreSQL (PostgreSQL License), HAProxy (GPL-2.0; ayrı bir süreç olarak çalışır, abarow koduna bağlanmaz). Bu imajlar bu çalışmada taranmadı; lisansları projelerin kendi lisanslarıdır. abarow bunları dağıtmadığı için bu imajlara abarow'un bildirimi eklenmez.

## Bilinen sınırlar

- **Patroni imajının listesi imajı derlediğimiz günkü hâldir.** Python bağımlılıklarının sürümleri sabitlenmemiştir (yalnız Patroni ve `psycopg2-binary` sabit). Yeni bir derlemede değişebilirler; bölüm 3'ü `python3 scripts/third-party-notices.py --patroni-image <imaj>` yeniler. CI bunu denetlemez (Docker ve derleme ister).
- **Beko köprüsü yayınlanmıyor:** Token'ın SDK'sının lisansı belirsiz ([beko-x30tr-prep.md](decisions/beko-x30tr-prep.md)); o imaj ve SDK ne depoda ne yayında. Token'la netleşmeden bu değişmemeli.
- **Kasadaki işletim sistemi paketleri** (Chromium, sway, cage, keepalived) kurulumda kullanıcının dağıtımından gelir; abarow bunları dağıtmaz.
- **Marka:** Apache-2.0 `abarow` adını kullanma hakkı vermez (madde 6). Adın marka olarak tescili ayrı bir karardır.
- **Bu belge ve dosyalar bir hukuk incelemesinden geçmedi.**
