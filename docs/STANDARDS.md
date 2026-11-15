# Yazılım standartlarını yukarı çekmek

Bu belge, abarow'un yazılım kalitesi ve güvenliği standartlarının bugün nerede
olduğunu ve nasıl yükseltilebileceğini yazılı olarak değerlendirir. Bir karar
belgesidir: hangi önerinin yapılacağını proje sahibi seçer; seçilenler
o zaman GitHub Issues'a girer. Tarama sonuçları için bkz.
[SECURITY-SCAN.md](SECURITY-SCAN.md). Bu belge hiçbir uyum ya da
sertifikasyon iddiası taşımaz; anılan standartlar yalnızca ölçüt olarak yol gösterir.

## Bugün elimizde olanlar

| Alan | Durum |
|---|---|
| Biçim, derleme, vet, test | `scripts/check.sh`: gofmt, build, vet, tüm modüllerde test; her modül `GOWORK=off` ile bağımsız da vet'lenir |
| Test | 400 Go dosyasının 157'si test; gerçek PostgreSQL'e karşı testler; Playwright ile uçtan uca 3 senaryo; Docker simülasyonları (kasa, mağaza, restoran) |
| Sözleşmeler | `buf lint`, `buf breaking`, üretilen kodun güncelliği CI'da zorunlu |
| Lisans | Her dosyada telif başlığı denetimi, DCO imzası |
| Para ve KDV | `int64` kuruş; bu koda dokunan değişiklik test içerir (CONTRIBUTING) |
| İmaj | `scratch` tabanı, root olmayan kullanıcı (65532), `CGO_ENABLED=0`, `-trimpath`; tek servis tek imaj |
| Sır | NATS kimliği Podman secret, kip 0400 |
| Bağımlılık | Sürümler `go.mod`/`package-lock.json`'da sabit; imajda `npm ci` |
| Taramalar | `govulncheck`, `gosec`, `npm audit` elle bir kez çalıştırıldı (CI'da değil) |

## Boşluklar ve öneriler

Etki ve emek sırasına göre. "Emek" kabaca bir geliştiricinin süresidir.

### 1. Tarama ve denetimleri CI'ya almak (etki yüksek, emek düşük)

Bugünkü taramalar bir kez elle yapıldı; bir sonraki bağımlılık açığı kimsenin
dikkatini çekmeden girebilir.

- `govulncheck` her push'ta ve haftalık zamanlayıcıyla (yeni açık, kod değişmese de çıkar).
- `gosec` ve `staticcheck` (ya da ikisini toplayan `golangci-lint` ile bir `.golangci.yml`). İlk koşuda bilinen yanlış pozitifler yazılı gerekçeyle dışlanır; sonra yeni bulgu CI'yı kırar.
- `go test -race`: kasada relay, tüketiciler ve HTTP birlikte çalışan çok sayıda goroutine var. Yarış denetimi CI'da Linux'ta çalışır (Windows'ta cgo gerektirir).
- `go mod tidy` farkı denetimi (go.mod/go.sum güncel mi).

### 2. Bağımlılık ve tedarik zinciri (etki yüksek, emek düşük-orta)

- **Dependabot ya da Renovate:** `gomod`, `npm`, `github-actions` ve Docker taban imajları için haftalık güncelleme PR'ları. `x/net` yükseltmesi gibi işler elle değil kendiliğinden gelir.
- **GitHub Actions'ı commit SHA'sına sabitlemek:** CI ve sürüm iş akışı bugün `actions/checkout@v7` gibi etiket kullanıyor; etiket sonradan başka koda taşınabilir.
- **Taban imajlarını özetle (digest) sabitlemek:** `golang:1.27` ve `node:24-alpine` kayan etiket. Kayan etiket yamayı otomatik alır ama yeniden üretilebilirliği bozar; Dependabot digest'i güncel tutarak ikisini birleştirir.
- **Sürüm imajlarına SBOM ve provenance:** `docker/build-push-action` `sbom: true` ve `provenance: mode=max` ile imajla birlikte yazılım malzeme listesi ve derleme kanıtı basar. `scratch` imajda içerik tek binary olduğundan SBOM küçük ve anlamlı çıkar. **Yapıldı (07.11.2026):** `release.yml` ikisini de basıyor ve terfi kapısı (`scripts/promotion-gate.py`, `docs/UPDATE.md` "Terfi kapısı") bunlar olmadan terfi ettirmiyor; GitHub'da henüz çalıştırılmadı.
- **İmaj imzalama (cosign, anahtarsız/OIDC):** Kasalar imajı kayıt defterinden çekiyor (güncelleme ekrandan onaylanınca); imza doğrulaması olmadan kayıt defterine erişen biri onaylanan güncellemeyle kasalara kod gönderebilir. Kasada `policy.json` ile imza zorunlu kılınabilir. Kasa filosu için en önemli tek tedarik zinciri önlemi budur.
- **İmaj taraması:** Trivy ya da Grype, sürüm iş akışında imaj yayınlanmadan önce (TODO'daki "image/bağımlılık taraması" ile aynı iştir).
- **OpenSSF Scorecard** iş akışı: yukarıdakilerin ölçüsünü verir.

### 3. Çalışma zamanı sertleştirme (etki orta-yüksek, emek düşük)

Quadlet birimlerinde `ReadOnly`, `DropCapability`, `NoNewPrivileges` yok. İmaj
zaten `scratch` ve root dışı olduğundan kazanç ucuz:

- `ReadOnly=true`; yazılan tek yer `/data` zaten volume. Geçici dosya gerekirse `Tmpfs=/tmp`.
- `DropCapability=ALL`, `NoNewPrivileges=true`.
- Compose dosyalarında da aynısı (`read_only: true`, `cap_drop: [ALL]`, `security_opt: [no-new-privileges:true]`); böylece simülasyon üretimle aynı kısıtlarda koşar ve sertleştirmenin kırdığı yer erkenden görünür.
- Ayarlar CI'daki Quadlet doğrulamasına ve e2e senaryolarına bindirilerek sınanır.

Bu başlığın ölçütü CIS Docker/Podman kıyas listeleridir.

### 4. Uygulama güvenliği standardı: OWASP ASVS (etki yüksek, emek orta-yüksek)

Bugünkü ölçüt "gosec temiz" gibi araç çıktısıdır; araçlar mantık hatalarını
(yetki atlama, durum makinesi hataları) bulmaz. OWASP ASVS (Application Security
Verification Standard) bir kontrol listesi verir:

- Hedef: **Seviye 1** tüm HTTP yüzeyleri için (kasa ekranı, operasyon, merkez); **Seviye 2** kimlik, oturum, yetki ve para akışları için.
- Çıktı: her madde için "karşılıyor / karşılamıyor / uygulanmaz" ve kanıt (dosya, test) tablosu. Bu tablo hem sızma testi kapsamını belirler hem testçiye verilecek belgedir.
- Yetki kontrolü için tablo tabanlı test: "her uç nokta × her rol" matrisi; yetkisiz rol 403 almalı. Rol modeli (kasiyer, yönetici, merkez kullanıcısı) küçük olduğundan matris ucuz ve değerlidir.
- Girdi ayrıştıran paketlere (barkod, tutar, JSON gövdeleri, görsel yükleme) **fuzz testi** (`go test -fuzz`): bugün hiç `Fuzz` fonksiyonu yok.

### 5. Güvenlik olaylarının kaydı (etki orta, emek orta)

Para ve vardiya olayları outbox'ta izleniyor; ancak güvenlik olayları (başarısız
giriş, yetkisiz erişim denemesi, ayar değişikliği) için ortak bir kayıt biçimi
yok. Öneri: `slog` JSON günlüğünde sabit bir "güvenlik olayı" biçimi ve gizli
verinin (PIN, parola, belirteç) günlüğe girmediğini sınayan bir test.

### 6. Ekran (Svelte) kalitesi (etki orta, emek düşük-orta)

`ui/` altındaki dört uygulamanın `package.json` dosyasında yalnızca
`dev/build/preview` var: tip denetimi, lint ya da birim test yok. Ekran
davranışı yalnızca Playwright e2e ile sınanıyor.

- `svelte-check` ve ESLint'i CI'ya.
- Kasa ekranı için erişilebilirlik (WCAG 2.2 AA) denetimi: Playwright'a `axe-core` eklenerek mevcut e2e senaryolarının içinde.
- İçerik Güvenliği Politikası (CSP) ve güvenlik başlıkları operasyon ve merkez ekranında var (`pkg/report.Secure`), kasa ekranında (gateway, modüller) yok; yerel erişim varsayımı yazılı, ama ortak bir ara katmanla her ekrana konup testle doğrulanabilir.

### 7. Test derinliği (etki orta, emek yüksek)

- **Kapsam ölçümü** (`go test -cover`) eşik koymadan raporlanır; hangi paketlerin testsiz olduğu görünür (TODO'daki "kalan birim testleri" listesi kendiliğinden çıkar).
- **Mutasyon testi** yalnızca para, KDV, fiş numarası ve stok hesabı gibi küçük çekirdeğe: "test var" ile "test hatayı yakalıyor"u ayırır.
- **Özellik tabanlı testler** (`testing/quick` ya da `rapid`): "toplam = satırların toplamı", "KDV yuvarlaması kuruş kaybetmez" gibi değişmezler.
- **Kopukluk testleri:** e2e senaryoları kopukluğu zaten dener; NATS ve PostgreSQL'i rastgele durduran bir tur eklenebilir.

### 8. Süreç (etki orta, emek düşük)

- **SECURITY.md:** açığın nasıl ve kime bildirileceği (GitHub private vulnerability reporting). Depo herkese açılmadan önce şart.
- **Dal koruması ve `CODEOWNERS`:** `main`'e doğrudan push yok, CI yeşil olmadan birleştirme yok. Tek geliştirici için hafif tutulabilir.
- **`CHANGELOG.md`:** kırıcı değişiklik ve migration notları ayrı başlıkta.
- **Süreç ölçütü:** OWASP SAMM ve OpenSSF Best Practices rozeti.

## Önerilen sıra

1. CI'ya `govulncheck` + `gosec`/`staticcheck` + `-race` + Dependabot + SHA sabitleme (bölüm 1 ve 2'nin ilk maddeleri).
2. Quadlet ve Compose sertleştirme (bölüm 3).
3. SBOM + provenance + cosign imzası + imaj taraması (bölüm 2'nin kalanı): kasa filosuna güvenli güncelleme için.
4. ASVS tablosu ve yetki matrisi testi (bölüm 4): sızma testi hazırlığıyla birlikte yapılır, çünkü tablo test kapsamının girdisidir.
5. Geri kalanı (ekran, kapsam, mutasyon, güvenlik olayı kaydı, süreç) sırayla.

## Kapsam dışı olanlar

- **Resmî sertifikasyonlar** (ISO 27001, PCI DSS, SOC 2): bir kuruluşun işletme sürecidir, yazılımla tek başına kazanılmaz. Kart verisi işleyen ödeme bileşeninin PCI DSS kapsamına girip girmediği, ödeme sağlayıcı entegrasyonu netleşince ayrıca değerlendirilmelidir.
- **KVKK:** kişisel veri (kasiyer ve kullanıcı bilgileri, e-posta) tutulduğu için veri envanteri, saklama süreleri ve silme kuralları ayrı bir hukuki inceleme gerektirir; bu belge hukuki görüş değildir.
- **Mali mevzuat (GİB, ÖKC):** abarow şu an simülatörle çalışır; mali değer taşıyan kullanımın gereklilikleri [beko-x30tr-prep.md](decisions/beko-x30tr-prep.md)'de izlenir.
