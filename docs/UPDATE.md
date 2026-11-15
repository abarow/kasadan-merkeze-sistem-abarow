# Güncelleme merkezi ve imaj dağıtımı

> **Durum:** uygulandı, kısmen sınandı (06.11.2026). Otomatik güncellemenin kapatılması; cihaz kaydı ve onayı; imzalı kanal bildirimi, bildirim, onay, geri alma ve önceden indirme; üç ekran; imaj aynasının yetkilendirmesi ve isteğe bağlı senkronu; Quadlet birimleri, kurulum betikleri ve terfi iş akışı yazıldı. Kayıt, bildirim, onay ve geri alma akışı gerçek süreçlerle, gerçek Docker yığınında ve Playwright'la sınandı; ancak **imajları gerçekten indirip birimleri yeniden başlatan adaptör (Podman API + systemd D-Bus) ve kurulum betikleri gerçek Linux/Podman makinede çalıştırılmadı** (e2e deneme çalışma zamanıyla koşar), terfi iş akışı GitHub'da çalıştırılmadı. Bu belge güvenlik garantisi vermez.

## Karar: güncelleme kendiliğinden olmaz

Yeni sürüm bir makineye **geldiğinde bildirilir, ekrandan onaylanınca uygulanır**; merkez, operasyon ve kasa için ayrı ayrı. Gerekçe: kendiliğinden güncelleme, satış sırasında kasayı, mağaza kapanışını ya da mali işlemi kesebilir ve yanlış bir sürümü tüm makinelere kimse görmeden yayabilir.

Bugün uygulanan: kurulum betikleri `podman-auto-update.timer`'ı açmaz ve Quadlet birimlerinde `AutoUpdate=registry` yoktur. Ekran onayı gelene kadar güncelleme elle yapılır ([deploy/quadlet/README.md](../deploy/quadlet/README.md) "Güncelleme").

**Bedeli:** yamalar onay beklediği için gecikir. İnternete açık Caddy gibi bileşenlerde bu bir risktir. Ekran sürümün aciliyetini (güvenlik yaması mı) ve ne kadar süredir beklediğini yalnızca **bilgi** olarak gösterir. Acil yamalar için davranış farklı değildir: onay istenir, *Sonra hatırlat* ve *Bu sürümü atla* aynen geçerlidir, hatırlatma sıklığı değişmez. Onay verilmezse hiçbir şey zorla uygulanmaz.

## Kanallar

| Kanal | Kim izler | Nasıl bu kanala girer |
|---|---|---|
| `test` | Güncelleme seviyesi `test` olan makineler (geliştirme, simülasyon, e2e) | Her başarılı `master` derlemesi |
| `pilot` | Güncelleme seviyesi `pilot` olan makineler | Elle terfi: bir kişinin onayı, **pilot anahtarı** |
| `prod` | Güncelleme seviyesi `prod` olan makineler (**varsayılan**) | Elle terfi: iki kişinin onayı, **prod anahtarı** |

**Güncelleme seviyesi** her makinenin kurulumda seçilen bir özelliğidir (`--level`); belirtilmezse `prod`. Seviyesi `pilot` olan makineler **her zaman pilot kanalından beslenir**. Bekleme süresi ya da pilottan prod'a kendiliğinden geçiş yoktur: pilot seviyesi, o makinenin prod adayını önce denemeyi kabul ettiği anlamına gelir. Pilotta çalışan bayt, prod'a giden bayttır. Kanal yalnızca *hangi sürümün bildirileceğini* belirler; uygulamak yine ekran onayıyla olur.

## Terfi: aynı digest'i imzalamak

- CI her imajı bir kez derler ve sürüm numarası + digest ile yayınlar. Yeniden derleme yok.
- Kanala terfi, o digest'e **kanalın anahtarıyla ikinci bir imza** eklemektir. Etiketler (`pilot`, `prod`) kolaylıktır; güven imzadadır.

| Anahtar | Ne imzalar | Nerede durur | Onay |
|---|---|---|---|
| **Derleme kimliği** | CI'nın derlediği her imaj | Anahtarsız (GitHub OIDC kimliği) | Otomatik |
| **Pilot anahtarı** | Pilot kanalına terfi eden digest | CI ortamı `pilot` (gizli anahtar ya da KMS) | 1 onaylayıcı |
| **Prod anahtarı** | Prod kanalına terfi eden digest | Çevrimdışı ya da donanım anahtarı | 2 onaylayıcı, pilottan farklı kişiler |

Ayrı anahtarın nedeni hasarı sınırlamaktır: pilot anahtarı çalınırsa en fazla küçük pilot grup etkilenir.

**Terfi kapısı.** `promotion.yml`, hiçbir şey kopyalamadan, imzalamadan ya da bildirim üretmeden önce `scripts/promotion-gate.py`yi çalıştırır; aşağıdakilerden biri eksikse iş akışı orada durur. Atlatma seçeneği yoktur: kapıyı geçemeyen sürüm düzeltilir ve yeni etiketle yayınlanır.

| Denetim | Nasıl |
|---|---|
| Sürüm çalıştırması başarılı ve etiketten | GitHub API: `release.yml` çalıştırması `completed/success`, olay `push`, etiket `v<sürüm>`, aynı depo |
| O commit'in CI'ı başarılı | `ci.yml`nin o commit için **en son** çalışması başarılı; `Uçtan uca (merkezsiz)`, `(merkezli)`, `(restoran)` ve `(guncelleme)` işlerinin her biri `success` (atlanmış ya da hiç yok sayılmaz). Senaryo listesi `ci.yml` matrisiyle aynı olmak zorundadır; `scripts/promotion_gate_test.py` bunu denetler |
| SBOM | Terfi eden her imaj digest'inin her platformunda (`linux/amd64`, `linux/arm64`) SPDX belgesi ve boş olmayan paket listesi (`docker buildx imagetools inspect --format '{{json .SBOM}}'`) |
| Provenance | Her platformda SLSA belgesi; belgedeki kaynak commit'i (`release.yml`nin bastığı `org.opencontainers.image.revision` etiketi ya da `vcs:revision`; bulunanların hepsi) sürümün commit'i, kaynak deposu bu depo olmak zorundadır |
| Kanıtların imaja bağlılığı | İndeksteki her platform imajının, ona başvuran bir kanıt (attestation) bildirimi vardır |

`release.yml` kanıtları `docker/build-push-action` ile üretir (`sbom: true`, `provenance: mode=max`); kanıtlar imaj indeksine eklenir, dolayısıyla terfi eden digest (indeksin digest'i) kanıtları da kapsar ve `skopeo copy --all --preserve-digests` kanıtları aynı baytlarla taşır. Kapının denetim mantığı `scripts/promotion_gate_test.py` ile CI'da denenir; girdi biçimleri gerçek `buildx` çıktısından alındı (yerel kayıt defterine iki platformlu SBOM ve provenance'lı imaj basılıp `imagetools inspect` çıktısı incelendi: çok platformlu imajda çıktı platform anahtarlıdır, tek platformlu imajda anahtarsızdır; provenance SLSA v1 biçimindedir). GitHub Actions'ta derlenmiş gerçek açık imajların (`ghcr.io/astral-sh/uv`, `ghcr.io/traefik/whoami`, `moby/buildkit`) provenance'ı da incelendi: `vcs:revision` hepsinde yoktu, bu yüzden kapı commit'i imaja `--label` ile basılan `org.opencontainers.image.revision` etiketinden de okur (`mode=max` provenance'ı etiketleri `label:` anahtarlarıyla taşır; yerelde SLSA v1 ile doğrulandı, uv'nin v0.2 biçiminde de aynı anahtar vardı). **Denenmeyen:** kapının kendi depomuzun GitHub'daki gerçek koşusu. Dependency-Track taraması kapının parçası değildir.

| Güncelleme seviyesi | Kanal | Kabul ettiği imzalar |
|---|---|---|
| test | `test` | derleme kimliği |
| pilot | `pilot` | pilot anahtarı ya da prod anahtarı |
| prod | `prod` | **yalnızca prod anahtarı** |

Pilot makineler prod anahtarını da kabul eder; böylece prod'a terfi eden sürümü kesintisiz izlerler. Güven iki yerde uygulanır: bildirimi seviyenin kabul ettiği anahtarlar imzalamış olmalı (ajan doğrular) ve imaj bildirimdeki digest'le çekilmeli. İmaj imzası da seviyeye göre doğrulanır (aşağıda "İmaj imzası doğrulaması"): ajan, seviyenin kabul ettiği cosign anahtarlarıyla `policy.json`u üretir. Seviye kurulumda `--level` ile seçilir.

## Güncelleme akışı

```
yayın (imzalı digest) ──▶ kanal bildirimi (imzalı) ──▶ ajan yoklar, imzayı doğrular
                                                          │
                          ekranda "Yeni sürüm hazır" ◀────┘
                                   │  yetkili kişi onaylar (katman başına ayrı)
                                   ▼
                  ajan çeker ▸ doğrular ▸ uygular ▸ sağlığı bekler ▸ kayıt
```

1. **Gelir:** ajan, geçerli jetonuyla, bağlı olduğu üst katmanın aynasından (kasa → operasyon → merkez → yayıncının güncelleme merkezi) imzalı kanal bildirimini yoklar. İmzası ya da kanalı politikaya uymayan bildirim hiç gösterilmez.
2. **Bildirilir:** katmanın kendi ekranında bir uyarı çıkar: sürüm, kanal, değişiklik özeti, hangi servislerin yenileneceği, boyut, imza doğrulandı bilgisi, aciliyet ve ne kadar süredir beklediği. Seçenekler: *Şimdi uygula*, *Sonra hatırlat*, *Bu sürümü atla*.
3. **Onaylanır:** onay yetkisi katmana göre mevcut rol modeline bağlıdır.
   | Katman | Ekran | Kim onaylar |
   |---|---|---|
   | Merkez | merkez ekranı (`reporting`) | yönetici |
   | Operasyon | operasyon ekranı (`report`) | müdür (müdür kodu ve PIN) |
   | Kasa | kasa ekranı (`gateway`) | müdür onayı (kasadaki mevcut PIN onayı) |
4. **Uygulanır:** yalnızca güvenli anda. Kasada açık vardiya, açık sepet ya da bekleyen tahsilat varken *Şimdi uygula* engellenir ve nedeni yazılır. Operasyonda bağlı kasaların durumu gösterilir. Her servis tek başına yenilenir.
5. **Sağlık ve geri alma:** onaylanan güncelleme başarısız olursa (yeni sürüm süre içinde sağlıklı açılmazsa) ajan **kendiliğinden önceki digest'e geri alır** ve ekran "geri alındı" der (karar 06.11.2026). Geri alma, onaylanmış işlemin parçasıdır; yeni bir güncelleme değildir ve ayrı onay istemez. Önceki imaj, güncelleme başarıyla bitip doğrulanana kadar makinede tutulur. Geri alınan sürüm tekrar bildirilmez; yetkili kişi isterse yeniden onaylar.
6. **Kayıt:** her onay denetim kaydına yazılır: kim, ne zaman, hangi katman, hangi digest, sonuç. Mali değişiklikler için izlenebilirlik sağlar.

**Sıra:** merkez → operasyon → kasa önerilir (sözleşmeler geriye uyumlu: protobuf'a yalnızca alan eklenir, migration'lar expand/contract). Zorunlu değildir; ekran, üst katman bu sürümden eskiyse uyarı verir ("operasyon henüz bu sürümde değil").

## Cihaz kaydı ve imaj çekme jetonu

**Karar:** imajları yalnızca yayıncıya güvenli biçimde bağlı birimler alabilir. Her birim cihaza özel üretilen kimlikle kayıt olur, üst katman kaydı onaylar ve onaylı birim imaj çekme jetonu alır. Jetonu olmayan ya da iptal edilen birim imaj çekemez.

### Güven zinciri

```
yayıncı kökü ──imzalar──▶ merkez sertifikası ──imzalar──▶ birim jetonu (operasyon, kasa)
      └────────imzalar──▶ merkez jetonu
```

- **Yayıncı kökü:** yayıncının kayıt anahtarı; her ajana ve aynaya önceden bilinir (sabitlenir). Çevrimdışı ya da donanım anahtarında tutulur.
- **Merkez sertifikası:** merkez yayıncıya kayıt olunca yayıncı, merkezin jeton imzalama anahtarını sertifikalar. Bu sertifika yalnızca **operasyon ve kasa** jetonu vermeye yetkilidir; merkez jetonu verilemez.
- **Birim jetonu:** merkez, onayladığı operasyona ve kasaya imaj çekme jetonu verir. Jeton cihaz anahtarının parmak iziyle (cnf) bağlıdır.
- Bir ayna, jetonu **yalnızca kökle** doğrular; yayıncıya ulaşması gerekmez, internet kopukken de çalışır.

### Cihaz kimliği

- Ajan ilk açılışta cihazda bir Ed25519 anahtar çifti üretir. Özel anahtar cihazdan çıkmaz, yalnızca imza üretir (dosya: yalnızca sahibi okur; TPM varsa onunla korunması sonraki iyileştirmedir).
- **Donanım parmak izi:** `machine-id`, anakart, ürün ve kasa seri numaralarının özeti (okunabilenler).
- **Cihaz kimliği** (`ABRW-XXXX-XXXX-XXXX-XXXX`) genel anahtardan ve parmak izinden türetilir. Disk başka bir makineye kopyalanırsa parmak izi uyuşmaz, ajan kayıtlı kimliği kullanmaz ve ekranda uyarır.

### Kayıt ve jeton alma

1. **İstek:** ajan, kimliğini, katmanını, seviyesini ve adını imzalayarak üst katmana gönderir (kasa ve operasyon merkeze, merkez yayıncıya). İstek bekleyen durumda kalır.
2. **Onay kodu:** birimin kendi ekranında kısa bir kod görünür (`XXXX-XXXX`, cihaz kimliğinden türetilir).
3. **Onay:** yetkili kişi (merkezde yönetici, merkezin kaydında yayıncının operatörü) bekleyen listesinde birimi bulur ve **birimin ekranındaki kodu girer**. Böylece yanlış cihaz onaylanamaz. Beş yanlış kodda kayıt reddedilir.
4. **Jeton:** onaydan sonra birim tek kullanımlık bir meydan okumayı cihaz anahtarıyla imzalayarak jeton alır. Aynı imza yeniden oynatılamaz; meydan okumanın ömrü 2 dakikadır.
5. **Yenileme:** jetonun ömrü sınırlıdır (varsayılan 7 gün); yenilemek için aynı meydan okuma yapılır. Cihaz anahtarı olmayan, jetonu çalan kişi yenileyemez.
6. **İptal:** onaylayan kaydı iptal eder; yeni jeton verilmez. Mevcut jeton süresi dolana kadar geçerlidir (ömür bu yüzden kısa tutulur; ayna iptal listesi sonraki iyileştirmedir). İptal edilmiş cihaz yeniden istek göndererek kendini onaylatamaz.

### Aynada yetki

Kayıt defterinin yetkisi **depo adındaki kanal önekiyle** verilir (etiketler Docker token protokolünde yetki kapsamına girmez, bu yüzden etiketle kanal ayrımı güvenli olmaz). İmajlar `<kanal>/<ad>` depolarında durur (`prod/abarow-sales`, `pilot/abarow-sales`, `test/abarow-sales`); terfi, aynı digest'in bir sonraki kanalın deposuna kopyalanması ve o kanalın anahtarıyla imzalanmasıdır. Seviyeye göre çekilebilen depolar:

| Güncelleme seviyesi | Çekebildiği depolar |
|---|---|
| `test` | `test/` |
| `pilot` | `pilot/` ve `prod/` |
| `prod` | `prod/` |

Yetkiyi `abarow-update-auth` hizmeti (`cmd/auth`) verir: istemci (Podman, Docker, skopeo) parola olarak abarow jetonunu sunar; hizmet jetonu yayıncı kökü ile **çevrimdışı** doğrular, seviyeye göre depolar için 5 dakikalık bir kayıt defteri belirteci (ES256) imzalar. Yalnızca `pull` verilir. Ayna (Zot) belirteci hizmetin sertifikasıyla (`mirror.crt`) doğrular. Hizmet ve Zot, aynı katmanda (merkezde ve operasyonda) yan yana çalışır.

**Sınama (gerçek Zot 2.1.23 ve skopeo, Podman'ın kullandığı istemci yığını):** kimliksiz istek 401 aldı; prod seviyesi yalnızca `prod/`, pilot seviyesi `pilot/` ve `prod/`, test seviyesi yalnızca `test/` imajını çekti, diğerleri reddedildi; prod jetonuyla katmanlar dahil tam indirme çalıştı; bozulmuş jeton reddedildi. Denenmeyenler: Zot'lar arası senkron (operasyon aynasının merkezden çekmesi), imaj yazma (yayıncının CI'sı için ayrı bir kimlik doğrulama yolu gerekir; token hizmeti yazma yetkisi vermez), TLS ile bu yığın.

### Riskler

| Risk | Davranış |
|---|---|
| Jeton çalındı | Süresi dolana kadar imaj çekilebilir; imajlar imzalı olduğu için çekmek tek başına kod çalıştırmaz; yenilenemez |
| Merkez ele geçirildi | Yalnızca kendi operasyon ve kasaları için jeton verebilir; yayıncı merkez sertifikasını iptal eder (sertifika ömrü kısa tutulmalı, öneri 90 gün) |
| Yanlış cihaz onaylandı | Onay kodu cihazın kendi ekranından okunur; iptal mümkündür |
| Klonlanmış disk | Parmak izi uyuşmaz, kimlik kullanılmaz |

### Durum

`update/` modülünde kayıt ve jeton katmanı yazıldı ve sınandı:

| Parça | Ne yapar |
|---|---|
| `kimlik`, `jeton`, `kayit` | Cihaz kimliği, güven zinciri, kayıt/onay/iptal/meydan okuma/token (karşılaştır-ve-yaz ile yarışa dayanıklı, bekleyen kayıt sınırı) |
| `adapters/in/httpapi` | **Cihaz API'si** (ağa açık, cihaz imzasıyla): `POST /v1/registrations`, `GET /v1/registrations/{cihaz}`, `POST /v1/challenge`, `POST /v1/token`. **Yönetim API'si** (yalnızca ekran servisinin ulaşacağı iç port, kişi başlıklarıyla, yalnızca `admin` rolü): bekleyenleri listele, onayla (birimin ekranındaki kodla), reddet, iptal et. **Ajan durum API'si** (yerel): `GET /api/update/status` |
| `adapters/out/postgres` | Kayıtlar `update_registrations` tablosunda (goose migration) |
| `adapters/in/httprelay` | **Operasyondaki vekil** (kasalar için): kasa yalnızca operasyonla konuşabildiğinden dört cihaz ucunu (kayıt, durum, meydan okuma, jeton) merkeze iletir. İstekler cihaz imzalı, onay merkezdedir; vekil karar vermez, yalnızca geciktirebilir ya da düşürebilir, jeton sahtesi üretemez. Yönetim uçları iletilmez; kişi başlıkları, çerezler ve yetki başlıkları atılır; gövde 16 KiB, yanıt 64 KiB, eşzamanlı istek 16 ile sınırlı. Yalnızca operasyon ajanında açılır (`ABAROW_UPDATE_RELAY_ADDR`, TLS zorunlu) |
| `adapters/out/httpclient` | Ajanın istemcisi; sunucu sertifikası verilen CA'ya sabitlenir, düz HTTP yalnızca açıkça izin verilirse |
| `ajan` | Ajan durum makinesi: kimlik üret/yükle, kayıt ol, onayı bekle, jeton al, ömrün son üçte birinde yenile, sunucu kapalıyken eldeki jetonu koru ve geri çekilerek dene, klonlanmış diskte kendini devre dışı bırak |
| `cmd/server` (`abarow-update-server`) | Kayıt sunucusu: merkez kipi (operasyon ve kasa onaylar, jetonu merkez anahtarı ve sertifikasıyla imzalar) ya da yayıncı kipi (merkez onaylar, kök anahtarla imzalar). Alt komutlar: `generate-key`, `verify-token` |
| `cmd/agent` (`abarow-update`) | Birimdeki ajan |
| `kanal` | İmzalı kanal bildirimi: sürüm, kanal, artan sıra numarası, geçerlilik süresi, aciliyet, notlar ve imajların digest'leri. Seviyeye göre kabul edilen anahtarlar (test: derleme, pilot: pilot ya da prod, prod: yalnızca prod), geri alma ve bayatlama koruması, bilinmeyen alan reddi |
| `surum` | Yeni sürüm akışı: bildirimi yoklar ve doğrular, değişen imajı bildirir, kendiliğinden uygulamaz; onayda güvenli anı denetler, imajları tek tek değiştirir, sağlığı bekler, başarısız olursa hepsini önceki digest'e geri alır; erteleme, atlama, önceden indirme, denetim kaydı, ajan uygulama sırasında ölürse açılışta bitirme ya da geri alma |
| `adapters/out/podman` | `release.Runtime`: Podman API soketiyle imaj çeker (digest doğrular), `<birim>.container.d/zz-release.conf` drop-in'iyle digest'e sabitler, systemd D-Bus ile yeniden başlatır; yalnızca `abarow-` önekli ve kurulu birimlere dokunur; ayna verilirse imajı `<ayna>/<kanal>/<imaj>@digest` adıyla çeker |
| `adapters/out/dryrun` | Gerçek container'a dokunmayan deneme çalışma zamanı (e2e) |
| `adapters/out/httpgate` | Güvenli an sorusu: kasada gateway açık vardiyada "uygulanamaz" der |
| `channelsync` | Yayıncıda kanal dizinini, terfi iş akışının yüklediği bildirimlerle besler (`ABAROW_UPDATE_CHANNEL_SOURCE`); kaynağa güvenmez: her bildirim güvenilen anahtarlarla doğrulanır, eski sıra numarası ve süresi dolmuş bildirim alınmaz, dosya atomik değişir |
| `kayit` (durum bildirimi) | Onaylı birim çalışan sürüm durumunu kendi cihaz anahtarıyla imzalayıp üst katmana bildirir (`POST /v1/report`): birimlerin çalışan imaj digest'leri, son uygulanan sürüm etiketi, akış fazı, bildirilmiş sürüm. Sunucu yalnızca onaylı birimden, kayıtlı anahtarla doğrulanan ve zaman damgası ±5 dakika içinde olan bildirimi kabul eder, bildirimin geldiği anı kendisi yazar, içeriği (birim adı, digest, etiket biçimi, boyut) doğrular. Ajan durum değişince hemen (`Nudge`), değişmezse 5 dakikada bir bildirir; kasanın bildirimi operasyonun vekilinden geçer |
| `policy` | Podman imaj imzası denetimini (`policy.json`, `registries.d`) seviyeye göre üretir ve `~/.config/containers`a yazar (başkasının policy.json'unu ezmez). Test: derleme, pilot: pilot ya da prod, prod: yalnızca prod cosign anahtarı; aynadan gelen başka yol reddedilir. İmza ayna adresine değil sabit kimliğe (`abarow.invalid/<imaj>@<digest>`) bağlıdır |
| `besleme` | Jeton yenilenince yerel aynayı (Zot) üst katmana bağlayan kimlik dosyasını yazar ve Zot'a yeniden yüklettirir |
| `cmd/server sign-channel`, `countersign-channel`, `verify-channel`, `check-trust` | Terfi araçları: bildirimi kanal anahtarıyla imzalar, ikinci imza ekler, bir seviyenin gözüyle doğrular; `check-trust` güven dosyasındaki anahtarların bugünkü durumunu listeler |

**Sınama.** Birim ve entegrasyon testleri geçti (vekil dahil) (gerçek HTTP/TLS sunucusuna karşı ajan; gerçek PostgreSQL'e karşı depo). Ayrıca iki program gerçek süreç olarak, tek makinede şu sırayla çalıştırıldı: yayıncı sunucusu → merkez ajanı kayıt olur, yayıncı onaylar, ajan jeton ve sertifika alır → merkez sunucusu bu anahtar ve sertifikayla açılır → operasyon ajanı merkeze kayıt olur ve kasalar için vekil açar → kasa ajanı **merkezin adresini bilmeden, yalnızca operasyonun vekili üzerinden** kayıt olur, merkez yöneticisi onaylar → merkez, operasyon ve kasanın üç jetonu da **yalnızca yayıncının genel anahtarıyla** doğrulandı (`server verify-token`). Vekilden yönetim ucu istenince 404 döndü. Yanlış onay kodu reddedildi, oturumsuz yönetim isteği 401 aldı. Her iki imaj da sürüm Containerfile'ıyla derlendi (5 ve 7 MB).

**Ekranlar.** Merkez ekranında yönetici için "Güncelleme" bölümü: bu merkezin ajan durumu ve onay kodu, birim kayıtları (Bekleyen / Onaylı / Reddedilen / İptal edilen), bekleyen kayıt için birimin ekranındaki kodla onay, reddetme, onaylıyı iptal. Operasyon ekranında "Güncelleme" bölümü ve kasada Raporlar penceresinde "Güncelleme" düğmesi: ajanın durumu, cihaz kimliği, erişim süresi ve onay beklerken büyük puntolu kod. Ekran servisleri (`gateway`, `report`, `reporting`) kişiyi doğrulayıp ajana ya da kayıt sunucusuna iletir (`ABAROW_UPDATE_AGENT_URL`, `ABAROW_UPDATE_ADMIN_URL`); güncelleme kurulu değilse 404 döner ve ekran "kurulu değil" yazar. Yönetim uçları yalnızca `admin` rolüne açıktır. Go tarafı (vekil yolları) testlidir; Svelte bileşenleri derlenir ama tarayıcıda otomatik sınanmadı.

**Kurulum.** Quadlet birimleri: `deploy/quadlet/update` (kasa), `store/update`, `headquarters/update`; ajan ayrı container'dır. Kurulum betikleri: `register-install.sh --update-url https://store:8443 [--level test|pilot|prod]`, `store-install.sh --update-url https://headquarters:8443 [--level ...] [--publisher-key kok.pub]`, `headquarters-install.sh --publisher-url ... --publisher-ca ... [--level ...] [--publisher-key kok.pub]`. Seçenek verilmezse ajan ve ekran bağlantısı kaldırılır. **Vekil sertifikası:** operasyondaki vekil, operasyon ekranının mağaza CA'sının imzaladığı sertifikayı kullanır; kasa kurulumda `--store-ca` ile o CA'ya sabitlenir. **İmaj aynası:** `--publisher-key` (yayıncının kök genel anahtarı, 43 karakter base64url) verilirse merkezde ve operasyonda `abarow-<katman>-mirror` (Zot v2.1.23) ile `abarow-<katman>-mirror-auth` kurulur; Zot ayarı `deploy/quadlet/mirror/zot.json.template`'dan yazılır, Zot TLS'lidir (5443), yetki hizmeti 5444'te açılır. Ayna, üst katmandan (merkezin aynası yayıncının kayıt defterinden, operasyonunki merkezden) isteğe bağlı imaj getirir; kayıt defteri adresi üst katmanın adresiyle aynı ana makine ve `:5443` kabul edilir. Kasa imajı operasyonun aynasından çeker. **Yeni sürüm uygulama yetkisi** `--channel-keys channel.json` ile verilir (yayıncının build/pilot/prod genel anahtarları); verilmezse ajan yalnızca kayıt olur ve jeton alır. Verilince kurulum betiği ajana abarow kullanıcısının Podman soketini, D-Bus soketini ve Quadlet dizinini bağlar, ekran servisleriyle paylaşılan onay gizli anahtarını (`abarow-update-secret`) üretir ve kasada gateway'in güvenli an sorusunu ayarlar. `policy.json` yazılmadı.

**Cihaz parmak izi sınırı.** Rootless container yalnızca `/etc/machine-id`'yi (ve herkesçe okunabiliyorsa DMI seri numaralarını) görür. Diskin tamamen kopyalanması bu durumda fark edilmeyebilir; kurulum betiği DMI okunamıyorsa bunu uyarı olarak yazar.

**Yeni sürüm akışı (uygulama).** Ajan `ABAROW_UPDATE_TRUST_FILE` verilince kanalı yoklar (varsayılan 10 dk): yayıncı bildirimi `GET /v1/channels/{kanal}` ile sunar (kasa için operasyonun vekili üzerinden); merkez ajanı doğruladığı bildirimi kendi dizinine koyar, merkezin kayıt sunucusu onu operasyon ve kasaya sunar. Bildirimin imzası yalnızca güvenilen kanal anahtarlarıyla, sıra numarası artarak ve süresi dolmadan kabul edilir; imajlar bildirimdeki **digest** ile çekilir, etiketle değil. Bildirim herkese açıktır (bütünlüğü imza korur, gizli değildir); imajların kendisi ise kayıt defterinde jetonla korunur. Onay API'si ajanın iç portundadır ve paylaşılan gizli anahtar ister; ekran servisleri kişiyi doğrulayıp iletir: merkezde yalnızca `admin`, operasyonda müdür (oturum), kasada müdür oturumu ya da müdür PIN'i (staff `ACTION_APPLY_UPDATE`). Onaylayanın adı denetim kaydına (`denetim.jsonl`) yazılır. Kasada açık vardiya varken uygulanmaz. Geri alınan ya da atlanan sürüm tekrar bildirilmez, daha yenisi bildirilir.

**Sınama (06.11.2026, gerçek Docker yığını + Playwright, `update` senaryosu, 19 test).** Deneme çalışma zamanıyla: yanlış anahtarla imzalı bildirim görünmez; imzalı bildirim üç ekranda aciliyet, notlar ve "imajlar önceden indirildi" ile bildirilir ve beklerken kendiliğinden kurulmaz; kasada açık vardiya varken "Şimdi uygula" kapalıdır; merkez yöneticisi, operasyon müdürü ve kasada müdür PIN'i (yanlış PIN reddedilir) onaylayınca sürüm uygulanır ve onaylayan kayda yazılır; sağlıksız açılan imaj operasyonda geri alınır, ekran nedenini yazar ve sürüm yeniden bildirilmez, düzeltilmiş yeni sürüm bildirilir; ertele ve atla; eski sıra numaralı ve süresi dolmuş bildirim reddedilir; rapor rolü onaylayamaz (403). Birim testleri: `kanal`, `surum` (geri alma, ajanın yarım kalan işlemi, kalıcılık), `podman` (sahte Podman API'si), `httpapi`, `besleme`, ekran servislerinin rol ve gizli anahtar denetimleri.

**Merkez ekranında çalışan sürüm (06.11.2026).** Merkez ekranının "Güncelleme" bölümünde onaylı her birimin kartı çalışan sürümü ("bilinmiyor" hiç uygulanmadıysa), bildirilmiş yeni sürümü ve fazını (hazır, indiriliyor, uygulanıyor), son bildirim anını (15 dakikadan eskiyse "eski") ve imajların kısaltılmış digest'lerini gösterir. Gerçek yığında sınandı: bildirimden önce "bilinmiyor · yeni sürüm 0.4.0 hazır", uygulamadan sonra "0.4.0" ve imaj digest'leri, geri almadan sonra eski sürüm ve yeni bildirilen sürüm. Yalnızca bir katman altını gösterir: merkez operasyon ve kasaları, yayıncı merkezleri bilir.

**Yayıncının kayıt defterine imaj yazma yolu.** Yayıncının kayıt defteri (Zot, `deploy/quadlet/publisher`) tek kimlik doğrulama kullanır, yetki hizmetinin belirteci: CI, yazma jetonuyla (kullanıcı `ci`, sunucuda yalnızca SHA-256 özeti, `ABAROW_UPDATE_CI_SHA256`) `test/`, `pilot/` ve `prod/` yollarına okur ve yazar; birimler abarow jetonuyla yalnızca kendi seviyesinin yolunu çeker (birim jetonu yalnızca çekme yetkisi verir). Zot'a htpasswd eklemek denendi: istemciler Zot'un yönlendirdiği belirteç hizmetine gidip CI kimliğiyle reddedildi, bu yüzden yazma kimliği yetki hizmetine eklendi. Yazma hattı: sürüm iş akışı imajı `ghcr.io`ya yayınlar ve (`PUBLISHER_REGISTRY` tanımlıysa) `test/` yoluna kopyalar; terfi iş akışı aynı digest'i pilot ve prod yoluna `scripts/image-copy.sh` ile kopyalar (`skopeo copy --all --preserve-digests`, kopyadan sonra hedefteki digest'in aynı olduğu doğrulanır). Yeniden derleme yoktur. Gerçek yığında sınandı: CI test yoluna yazdı, betik prod yoluna kopyaladı ve digest değişmedi, birim jetonu prod yolunu çekti, test yolunu çekemedi; kimliksiz ve sahte jetonlu istek ile jetonla yazma reddedildi. Denenmeyen: GitHub'daki iş akışlarının kendisi (yalnızca `actionlint` ve betiklerin yerel koşusu), TLS.

**İmaj imzası doğrulaması (cosign, `policy.json`).** Terfi iş akışı, kopyalanan her imajı kanalın cosign anahtarıyla imzalar (`scripts/image-sign.sh`; derleme, pilot, prod anahtarları); her birim çekerken imzayı kendi seviyesinin kabul ettiği anahtarla doğrular. Güven dosyasındaki `cosign` bölümünden ajan politikayı üretir ve Podman'ın yapılandırma dizinine yazar (`ABAROW_UPDATE_POLICY_DIR`; istendiği halde yazılamazsa ajan açılmaz, imzasız çekmeye düşülmez). Politika elle de üretilir: `server generate-policy <seviye> <ayna> <güven.json> <dizin>`. Tasarım kararları gerçek Podman v5.4, Zot ve cosign v2.4 ile denenerek verildi: (1) cosign imzası varsayılan olarak kayıt defteri adresine bağlanır; kasa, operasyon ve merkezin aynaları farklı adreste olduğundan imza `--sign-container-identity abarow.invalid/<imaj>@<digest>` ile sabit kimliğe bağlanır ve politika `remapIdentity` ile eşler (kimlik digest taşımazsa Podman digest ile çekilen imajı reddetti); (2) Zot'un isteğe bağlı senkronu imza eklerini (`sha256-<digest>.sig`) alt aynaya da taşır; (3) `keyDatas` ile çok anahtarlı (pilot ya da prod) doğrulama çalışır, yanlış anahtar, imzasız imaj ve kapsam dışı yol reddedilir. Gerçek yığında sınandı: prod anahtarıyla imzalı imaj, bearer jetonuyla Zot'tan çekildi; imzasız imaj ve pilot anahtarıyla imzalı imaj prod politikasında reddedildi. Anahtar yönetimi: üç cosign çifti (`cosign generate-key-pair`); özel anahtarlar CI ortamında (`COSIGN_KEY`) ya da çevrimdışıdır, genel anahtarlar güven dosyasındadır. Anahtar döndürme için aşağıdaki "Anahtar döndürme" bölümüne bakın. Denenmeyen: Rekor şeffaflık günlüğü (kapalı: `--tlog-upload=false`) ve GitHub OIDC ile anahtarsız imza.

**Aynalar arası senkron (Zot `sync`), gerçek Zot ile denendi.** Alt katmandaki Zot, üst katmandan isteğe bağlı (`onDemand`) imaj getirir; kimlik bilgisi `credentialsFile`'dadır. Deneyde **kimlik dosyasının tek başına değişmesi yetmedi**: Zot onu yalnızca açılışta ve yapılandırma dosyası değişince okuyor. Bu yüzden ajan jeton yenilenince (`besleme`) kimlik dosyasını yazar ve Zot'un yapılandırma dosyasının sonundaki satır sonunu değiştirir; bu, Zot'a yapılandırmayı yeniden yüklettirdi (yeni kimlikle çekme çalıştı). Denenen: htpasswd'li üst Zot + isteğe bağlı senkronlu alt Zot, `latest` sürüm. Denenmeyen: bearer (abarow jetonu) doğrulamalı üst Zot'tan senkron, TLS, v2.1.23 (kurulumdaki sürüm), jeton yenilemenin uzun süreli koşusu.

**Gerçek Podman ile `podman` adaptörü (06.11.2026).** `quay.io/podman/stable:v5.4` içinde ayrıcalıksız `podman system service` ve düz HTTP'li bir Zot, yalnızca Docker ağında: adaptör imajı `<ayna>/prod/<imaj>@digest` adıyla gerçekten çekti (`X-Registry-Auth` ile), indirilen imajın digest'ini API'den doğruladı, digest'e sabitleyen drop-in'i yazıp `Current` ile okudu, olmayan digest'i reddetti. Container'ın `ImageDigest` alanı API'de bulundu. Test `ABAROW_TEST_PODMAN_*` verilince çalışır (`real_test.go`). Denenmeyen: çalışan container'ın digest'ini okuma (iç içe Podman container çalıştıramadı), systemd D-Bus (`Reload`, `Restart`, `Active`) ve ajanın kendi birimini güncellemesi.

**Yapılacak:**
- Yayıncının kayıt defteri, cosign imzası ve iş akışlarının (terfi kapısı dahil) TLS'li, gerçek GitHub ortamında çalıştırılması; derleme kimliği (GitHub OIDC, anahtarsız) imzası ve Dependency-Track'in terfi kapısına eklenmesi.
- Kurulum betiklerinin gerçek Linux/Podman makinede çalıştırılması ve ekranların uçtan uca (Playwright) sınanması; bugüne kadar yalnızca derleme, birim testleri, Quadlet üreticisi (`quadlet -dryrun`, 35/35 birim) ve betik işlevlerinin yalıtılmış denemesi yapıldı.
- Gerçek systemd üzerinde `podman` adaptörünün D-Bus yarısının (daemon-reload, yeniden başlatma işi, etkinlik denetimi), ajanın kendi birimini güncellemesinin ve `Recover`'ın denenmesi; SELinux'lu makinede soket erişimi.
- Terfi iş akışının (`promotion.yml`) ve `release.yml`'deki digest kaydının GitHub'da çalıştırılması. İş akışının araçları (`scripts/channel-notice.py`, `server sign-channel`, `countersign-channel`, `verify-channel`) `scripts/channel-test.sh` ile CI'da uçtan uca denenir, iş akışı `actionlint`ten geçer; yayıncının kanal dizinini bildirimlerden beslemesi `channelsync` ile yazıldı (kaynak: `kanal` sürümünün varlıkları).
- İptal listesi ve jeton ömrü ayarları, TPM ile anahtar koruması, hız sınırı (bekleyen kayıt sayısı sınırı var).


## Önceden indirme (seçenekli, varsayılan açık)

Ayar açıkken ajan, imzalı bildirim gelince imajı arka planda çeker ve imzasını doğrular, **uygulamaz**; onaydan sonra yalnızca yeniden başlatma kalır, kesinti kısalır. Ayar kapalıysa indirme onaydan sonra başlar. Ayar katmanın kendi ekranından açılıp kapatılır (merkezde yönetici; operasyonda ve kasada müdür). Kasa imajı operasyonun aynasından, mağaza içi ağdan çeker. İndirmeden önce boş disk yeri denetlenir; yetersizse indirilmez ve ekranda yazılır. İndirilen ama onaylanmayan imaj, sürüm atlanınca ya da daha yenisi gelince silinir.

## Kim işletir: imajı yayınlayan

Güncelleme merkezini (kayıt defteri, kanal bildirimi, imza anahtarları, terfi) **imajları yayınlayan taraf** işletir: proje kendi imajlarını yayınlıyorsa proje, bir çatal (fork) ya da dağıtıcı kendi imajlarını yayınlıyorsa o. Müşterinin merkezi ve operasyonu yalnızca ayna (önbellek) görevi görür; sürüm kararını ve imzayı yayıncı verir. Kendi anahtarlarıyla imzalamak isteyen, kendi imajlarını yayınlayan bir yayıncı olur ve kendi güncelleme merkezini işletir.

**Mali modüller:** ÖKC ve mali bağlayıcı imajlarını yayınlayan taraf, kendi mevzuat onayından ve onay kaydından sorumludur; güncelleme merkezi bu onayı yönetmez. Bu belge hukuki görüş değildir.

## Ajan

Her makinede (merkez, operasyon, kasa) ayrı bir container olarak çalışır: **`abarow-update`** (karar 06.11.2026). Kayıt sunucusu merkezde ve yayıncıda ayrı bir imajdır: `abarow-update-server`. İmajı diğer servislerle aynı yayın hattından, aynı imzayla gelir; Quadlet birimi `abarow-update.container`. Küçük bir Go programı (hexagonal, diğer servisler gibi).

Görevleri: yoklama, imza ve kanal doğrulama, onay API'si, indirme, uygulama, sağlık bekleme, geri alma, durumu üst katmana bildirme. Ekran servisleri onay isteğini kendi oturum ve yetki denetimlerinden geçirip ajana iletir; ajan kimlik doğrulamasını kendi yapmaz.

**Yetkiler.** Ajan iki soket kullanır: kullanıcının rootless Podman API soketi (imaj çekmek, container durumunu okumak) ve kullanıcı D-Bus soketi (`systemd --user` birimlerini yeniden başlatmak, `daemon-reload`). Ayrıca Quadlet drop-in dizinine yazar (`Image=...@sha256:...` sabitlemesi). Bu, `abarow` kullanıcısının **tüm container ve birimlerini yönetme yetkisidir**; soket düzeyinde daraltılamaz. Önlemler:

- Yalnızca `abarow-` önekli birimlere dokunur (kodda zorunlu allow-list).
- Ağa dinleyen tek yüzü onay API'sidir: yalnızca iç `abarow` ağında, ana makineye açılmaz, ekran servislerine verilen bir gizli anahtarla (Podman secret) çağrılır.
- Dışarıya yalnızca bağlı olduğu üst katmanın aynasına bağlanır (kasa operasyona, operasyon merkeze, merkez yayıncıya).
- `scratch` imaj, root olmayan kullanıcı, salt okunur kök dosya sistemi, tüm yetenekler (capabilities) kapalı, `no-new-privileges`. SELinux'lu makinelerde soket erişimi için etiket gevşetmesi gerekebilir; uygulamada denenmeli.
- Her Podman ve D-Bus çağrısı denetim kaydına yazılır.
- Kod küçük tutulur ve her sürümü imzalıdır; ajanın kendisi de aynı onaylı güncelleme akışından geçer.

**Kendini güncelleme.** Ajan kendi birimini yeniden başlatınca süreç kendi kendini sonlandırır; yeniden başlatma işini `systemd` yürüttüğü için istek tamamlanır. İşlem durumu kalıcı bir hacimde tutulur: yeni ajan açılınca sağlığı doğrular ve işlemi bitirir. Yeni ajan açılamazsa önceki digest'e dönüşü bir `OnFailure=` birimi yapar. Ayrıntı uygulamada netleştirilir.

## Anahtar döndürme

Güven dosyası (`trust.json`) her rol için tek bir anahtar yerine **geçerlilik penceresi olan anahtarlar** taşıyabilir. Bu, hem kanal bildirimini imzalayan Ed25519 anahtarları (`build`, `pilot`, `prod`) hem de imaj imzasını doğrulayan cosign anahtarları (`cosign.build|pilot|prod`) için geçerlidir. Bir anahtar düz dize, `{"key", "not_before", "not_after"}` nesnesi ya da bunların dizisi olabilir; eski biçim aynen çalışır:

```json
{
  "prod": [
    {"key": "<eski>", "not_after": "2026-12-15T00:00:00Z"},
    {"key": "<yeni>", "not_before": "2026-12-01T00:00:00Z"}
  ],
  "cosign": {
    "prod": [
      {"key": "-----BEGIN PUBLIC KEY-----\n...", "not_after": "2026-12-15T00:00:00Z"},
      {"key": "-----BEGIN PUBLIC KEY-----\n...", "not_before": "2026-12-01T00:00:00Z"}
    ]
  }
}
```

`not_before` dahil, `not_after` dahil değildir. Pencere dışındaki anahtar yok sayılır; süresi dolan anahtar kendiliğinden düşer, ayrıca bir işlem gerekmez. Yazım hatası (bilinmeyen alan ya da rol, bozuk anahtar, ters pencere) süresi dolmuş anahtarda bile hata verir: yanlış yazılan anahtar sessizce düşmez.

**Sıcak yenileme.** Ajanlar ve yayıncının kanal eşitleyicisi güven dosyasını her yoklamada yeniden okur; ajan yeniden başlatılmaz. Cosign anahtarları değişince ajan `policy.json`u yeniden üretir ve yazar (Podman her çekmede okur). Dosya okunamaz ya da bozuksa son bilinen anahtarlar kullanılır ve hata yoklama hatası olarak ekranda görünür; ajan açılırken ise dosya kusursuz olmak zorundadır. Kurulum betikleri güven dosyasını `~abarow/abarow/update/trust/` dizinine yazar ve container'a dosyayı değil dizini bağlar; bu yüzden dosya yerinde yazılsa da (`tee`) yeniden adlandırılarak (`mv`) değiştirilse de ajan yenisini görür (kurulum betiğini yeni dosyayla yeniden çalıştırmak da yeterlidir). Tek dosya olarak bağlanan kurulumlarda (e2e yığını) dosya yerinde yazılmalıdır.

**Süre dolunca kilit.** Bir seviyenin kabul ettiği cosign rollerinden birinin geçerli anahtarı kalmazsa (örneğin yeni anahtar hiç eklenmeden eskisi doldu), ajan eski politikayı bırakmaz: aynadan çekmeyi tümden reddeden kilitli bir `policy.json` yazar ve bunu günlüğe hata olarak basar. İmzasız çekmeye düşülmez; güven dosyasına geçerli anahtar eklenince bir sonraki yoklamada kilit kalkar. Aynı durumda manifest anahtarı kalmazsa bildirimler doğrulanamaz (yeni sürüm bildirilmez), çalışan sistem etkilenmez.

**Yordam (prod anahtarı örneği).**
1. Yeni çifti üretin (manifest: `server generate-key`, cosign: `cosign generate-key-pair`). Özel anahtarı CI ortamına/çevrimdışı yere koyun.
2. Güven dosyasına yeni genel anahtarı `not_before` = geçiş başlangıcı ile ekleyin; eskisine `not_after` = geçişin sonu koyun. Dosyayı bütün katmanlara dağıtın (kurulum betiği güven dosyasını makinelere kopyalar).
3. `server check-trust trust.json` ile durumu görün (hangi anahtar geçerli, henüz geçerli değil, süresi dolmuş); her rolün bugün geçerli anahtarı yoksa komut hata verir. Ajan, bir anahtarın süresi 14 gün içinde dolacaksa günlüğe uyarı yazar.
4. Geçiş başlayınca terfi iş akışının gizli anahtarını (`CHANNEL_KEY`, `COSIGN_KEY`) yenisiyle değiştirin. Geçiş penceresinde hem eski hem yeni anahtarla imzalı bildirim ve imaj kabul edilir; böylece çevrimdışı olan makineler yeni imzaya geçene kadar eski imzayı kabul eder.
5. Eski anahtar `not_after`da düşer. Pencereyi, en uzun çevrimdışı kalabilecek makinenin güven dosyasını alabileceği süreden uzun tutun: güven dosyasını almamış çevrimdışı bir makine yalnızca eski anahtarı bilir ve yeni imzalı sürümü bildirmez (yanlış kabul etmez, kapalı kalır).

Anahtar **çalınmışsa** geçiş penceresi tanınmaz: eski anahtarı güven dosyasından hemen çıkarın (ya da `not_after`ı geçmişe alın). Çalınan anahtarla imzalanmış, hâlâ geçerli süreli bildirimler `seq` ile aşılır: yeni anahtarla daha yüksek `seq`li bildirim yayınlayın.

**Sınandı:** birim testleri (pencere sınırları, üç JSON biçimi, bozuk girdi, süresi dolmuş bozuk anahtar, ajan yenilemesi ve hata halinde son bilinenin korunması, kanal eşitleyicisinin yenilemesi, politikada iki anahtar, kilitli politika ve başkasının `policy.json`unun ezilmemesi) ve gerçek yığında `update` senaryosu: iki cosign anahtarıyla gerçek Podman (geçiş penceresinde eski ve yeni anahtarla imzalı imaj çekilir; eski anahtar düşünce eskisiyle imzalı reddedilir, yenisiyle imzalı çekilir; hiç geçerli anahtar kalmayınca politika üretilmez) ve ajanlar yeniden başlatılmadan yeni anahtarla imzalı bildirim önce reddedildi, güven dosyasına eklenince kabul edildi, geçiş penceresinde eski anahtarla imzalı bildirim de kabul edildi, eski anahtar `not_after`la düşünce reddedildi. **Denenmeyen:** ajanın çalışan bir Podman makinesinde politikayı kendisinin yeniden yazması (yazma mantığı birim testinde; Podman tarafı CLI ile üretilen politikayla denendi), uzun süreli (günler) pencere ve kurulum betiğindeki dizin bağlamasının gerçek makinede çalıştırılması.

## Güvenlik değerlendirmesi

| Tehdit | Önlem |
|---|---|
| Kayıt defterine yazan biri etiketi başka imaja taşır | Etiket güven kaynağı değil; kanal imzası yoksa bildirim çıkmaz, çalışmaz. Yetki ayrıca depo önekiyle verilir |
| Pilot anahtarı çalındı | Yalnızca pilot grup etkilenir; prod makineleri kabul etmez |
| Prod anahtarı çalındı | Çevrimdışı/donanım anahtarı, iki onay; çalınırsa `policy.json` yeni anahtara geçirilir |
| Yanlış ya da aceleyle onay | İmzasız sürüm onaylansa bile uygulanmaz; kasada güvenli an kuralı; hepsi kayda geçer |
| Eski, açıklı sürümün yeniden yayını | İmzalı sürüm numarası ve TUF (`go-tuf`) ile ajan eski sürümü reddeder |
| Zararlı imaj | Terfi kapısı: sürüm ve CI/e2e başarısı, her imajda SBOM ve commit'e bağlı provenance (yazıldı, GitHub'da henüz çalıştırılmadı); Dependency-Track taraması kapıda yok |
| Güncelleme ajanı ele geçirildi | Ağı yalnızca üst katmanın aynasıyla sınırlı, küçük ve imzalı kod, allow-list, denetim kaydı. **Kalan risk:** ele geçirilirse `abarow` kullanıcısının tüm container'larını yönetebilir (kabul edilen risk) |
| İnternet kopukken | Sistem çalışır; bildirim gelmez; hiçbir şey zorla uygulanmaz |

## Aşamalar

**Aşama 1: imzalı yayın ve terfi.** Yazıldı, çalıştırılmadı: `release.yml` her imajın digest'ini kaydeder, `promotion.yml` (elle başlatılır, GitHub ortamı `pilot`/`prod` onay kurallarıyla) digest'lerden kanal bildirimi üretir (`scripts/channel-notice.py`) ve kanal anahtarıyla imzalar (`server sign-channel`); prod terfisi pilot kanalına da prod anahtarıyla imzalı bildirim koyar. Anahtarı ortamda tutmak istemeyen imzasız bildirimi alıp çevrimdışı imzalar. İmajlar kanalın cosign anahtarıyla da imzalanır (`scripts/image-sign.sh`), kayıt defterine yazma `scripts/image-copy.sh` ile yapılır. Kalanlar: SBOM, provenance ve derleme kimliği (GitHub OIDC) imzası, e2e ve Dependency-Track'in terfi kapısı olması.

**Aşama 2: ajan ve ekran onayı.** Yazıldı ve sınandı (yukarıdaki "Durum"). TUF yerine kendi imzalı bildirim biçimimiz var (artan sıra numarası, geçerlilik süresi, seviyeye göre anahtarlar); TUF'un tam anahtar döndürme ve eşik imza modeli uygulanmadı. Merkez ekranında tüm makinelerin çalışan sürüm görünümü yok.

## Ajan çalışma yeri: değerlendirilen seçenekler

Üç seçenek değerlendirildi; **3 seçildi**.

| | 1. Ana makinede `systemd --user` servisi | 2. Tetiklemeli kısa ömürlü uygulayıcı | **3. Ayrı container (seçildi)** |
|---|---|---|---|
| Yetki yüzeyi | `abarow` kullanıcısı; ek soket yok | En küçük | En geniş: Podman ve D-Bus soketi |
| Mevcut düzene uyum | Ana makine bileşeni | Ana makine bileşeni | **Tam uyumlu**: ayrı container, aynı yayın ve imza hattı |
| Kendini güncelleme | Ayrı yol gerekir | Kolay | Zor; yukarıdaki "Kendini güncelleme" ile çözülür |
| Eksi | Düzenin dışında | İlerleme ve iptal kaba | Ele geçirilirse etki büyük |

Seçimin gerekçesi düzen bütünlüğüdür: her şey bir container, tek bir yayın hattı. Yetki yüzeyi bilinen ve kabul edilen risktir; "Ajan" bölümündeki önlemlerle sınırlandırılır.

## Alınan kararlar (06.11.2026)

- Güncelleme kendiliğinden olmaz; her katmanda (merkez, operasyon, kasa) ekrandan ayrı ayrı onaylanır.
- Pilot ve prod ayrı anahtarla imzalanır; pilot seviyesindeki makineler prod adayını önce dener.
- Onaylanan ama başarısız olan güncelleme kendiliğinden önceki sürüme geri alınır.
- Acil güvenlik yamalarında davranış farklı değildir (aynı onay, aynı *Sonra* ve *Atla* seçenekleri, aynı hatırlatma sıklığı).
- Önceden indirme seçenekli ve varsayılan açık (yalnızca indirme ve doğrulama; uygulama onayda).
- Her makinenin güncelleme seviyesi vardır, varsayılan `prod`. Seviyesi `pilot` olan makineler her zaman pilot kanalından beslenir; pilot için bekleme süresi ya da grup büyüklüğü kuralı yoktur.
- Güncelleme merkezini imajı yayınlayan taraf işletir; müşterinin merkezi ve operasyonu yalnızca aynadır.
- Mali modülleri yayınlayan taraf kendi mevzuat onayından sorumludur; güncelleme merkezi bu onayı yönetmez.
- Ajan her makinede ayrı bir container olarak çalışır (`abarow-update`); Podman ve D-Bus soketi kullanır, yetki yüzeyi önlemlerle sınırlanır.
- İmajları yalnızca yayıncıya güvenli bağlı birimler alabilir: cihaza özel kimlik, üst katmanın kodlu onayı ve cihaza bağlı, süreli jeton ("Cihaz kaydı ve imaj çekme jetonu").
