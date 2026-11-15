# Uçtan uca testler

Gerçek servisleri Docker'da kurup ekranları Playwright'la, API'leri küçük HTTP istemcileriyle dener. Konu: bir kaydın kasadan operasyona ve merkeze **aktığı, hiç akmadığı, kısmen aktığı** durumlar ve [teslim makbuzu](DESIGN.md) kuralı (olay, üst katmanın PostgreSQL'ine işlendiği makbuzla kanıtlanmadan silinmez).

Mevcut `scripts/store-sim.sh` ve `tools/ui-smoke` mutlu yolu dener (23 kasa senaryosu, ekran gezintisi). Bunlar kopukluk ve silme kurallarını denemez; burası onları tamamlar.

## Çalıştırma

```sh
cd tools/e2e
npm install
npx playwright install chromium     # bir kez
npm test                            # dört dosya, sırayla (yaklaşık yarım saat; ilk seferde image derlemesi de var)
npm run test:standalone              # yalnızca birini
```

Her dosya kendi yığınını `scripts/e2e.sh` ile kurar, bitince siler. Hata ayıklarken:

| Değişken | Etkisi |
|---|---|
| `ABAROW_E2E_REUSE=1` | Yığını kurmaz ve silmez; elle kurulmuş yığını kullanır (`bash scripts/e2e.sh up headquarters`) |
| `ABAROW_E2E_KEEP=1` | Yığını kurar ama testten sonra silmez (ekranlara bakmak için) |
| `ABAROW_BASH` | Windows'ta Git Bash'in yolu (`C:\Program Files\Git\bin\bash.exe`); varsayılan `bash` |

Raporlar ve ekran görüntüleri `tools/e2e/out/` altına yazılır (HTML rapor: `out/report`). CI'da her senaryo ayrı çalıştırıcıda koşar ve `out/` yüklenir.

## Yığınlar

`scripts/e2e.sh up <ad>`, `store-sim.sh` ile aynı sırada kimlik ve anahtar üretir:

| Ad | İçerik |
|---|---|
| `standalone` | market kasası + operasyon; merkez yok |
| `headquarters` | market kasası + operasyon + merkez |
| `restaurant-standalone`, `restaurant-headquarters` | aynısı, kasada market yerine restoran modülü |
| `update` | `headquarters` + güncelleme merkezi (`deploy/compose/update-*.yaml`): yayıncının kayıt sunucusu, merkez, operasyon ve kasa ajanları, merkezin kayıt sunucusu. Düz HTTP (yalnızca deneme); yayıncı merkezi kurulumda onaylar |

Süreler testte kısaltılır: olay en az **6 sn** saklanır, temizlik **2 sn**'de bir çalışır (`ABAROW_CLEAN_MAX_AGE`, `ABAROW_CLEAN_INTERVAL`). Gerçek kurulumda 30/90 gün ve 1 dakikadır.

## Senaryolar

Kayıt tipleri: **tam akar** (hepsi gelir), **hiç akmaz** (bağlantı kopuk), **kısmen akar** (bir kısmı geçer, kalanı sonra gelir ya da bir katmanda takılır).

| Dosya | Senaryo | Doğrulanan |
|---|---|---|
| `standalone` | akış tam | 3 satış operasyonda 3 fiş; eksik fiş yok; kasa dökümü günlük toplama eşit; çift sayım yok |
| | makbuz geldi | kasadaki satış ve vardiya olayları operasyon işleyince silinir |
| | kısmen akar | operasyon kapalıyken yapılan 2 satış kasada kalır ve makbuzsuz silinmez; açılınca gelir, sonra silinir; ekranda "Bağlantı yok" → "Bağlı" |
| | hiç akmaz | kasanın NATS'ı kopar; satış sürer; ekranda "Bağlantı yok" ve "satışlar kasada birikir"; bağlantı gelince akar |
| | merkez yok | operasyondaki mağaza stream'inden hiçbir olay silinmez |
| | ekran | kasiyer market ekranında satar, operasyon kartında "Bağlı" ve fiş sayısı |
| `headquarters` | akış tam | satışlar operasyona ve merkeze ulaşır; iki katmanın günlük özeti aynı |
| | makbuz zinciri | kasadaki olay operasyonun, operasyondaki olay merkezin makbuzuyla silinir |
| | kısmen: merkez kapalı | operasyon raporlar, merkez makbuzu yok → operasyon silmez; merkez açılınca akar ve siler |
| | kısmen: merkezin veritabanı kapalı | olay merkezin NATS stream'ine gelir ama PostgreSQL'e işlenmez; makbuz yok, silinmez; veritabanı gelince işlenir, makbuz gelir, silinir |
| | hiç akmaz | operasyon kopuk; merkez mağazayı kopuk görür; açılınca kasadan merkeze akar |
| | ekran | operasyon ve merkez ekranları satışı gösterir; merkez kapalıyken operasyon ekranı çalışır |
| `restaurant` | her iki kipte: akış tam, kısmen (operasyon kapalı), hiç akmaz (kasa NATS'ı kopuk); merkezli kipte merkez kapalı; merkezsiz kipte mağaza stream'i silinmez | adisyon ekranla ödenir; aynı kurallar |

Güncelleme senaryosu (`update`, gerçek yığında 24 test; yeni sürüm adımları gerçek container'a dokunmayan deneme çalışma zamanıyla koşar):

- *Kayıt ve onay:* merkezin kendi ajanı yayıncıda onaylı görünür; operasyon ve kasa kayıt olup ekranlarında onay kodunu gösterir (kasa merkezi bilmez, kaydı yalnızca operasyonun vekili taşır); yönetici yanlış kodu (başka birimin kodu dahil) reddeder, doğru kodla onaylar; onaylı kayıt iptal edilir; yeni kimlikle gelen kasa reddedilir; rapor rolü sekmeleri görmez ve yönetim uçları 403 verir; oturumsuz istekler 401 alır; kayıt sunucusu kapanınca merkez ekranı çalışır; kasa satışı sürer.
- *Yeni sürüm:* yanlış anahtarla (pilot) imzalı bildirim görünmez; prod imzalı bildirim üç ekranda aciliyet ve notlarla bildirilir, imajlar önceden indirilir, beklerken kendiliğinden kurulmaz; kasada açık vardiya varken uygulanamaz; merkez yöneticisi, operasyon müdürü ve kasada müdür PIN'i (yanlış PIN reddedilir) onaylayınca sürüm uygulanır ve onaylayan kayda yazılır; sağlıksız açılan imaj geri alınır, ekran nedenini yazar, sürüm yeniden bildirilmez, düzeltilmiş yeni sürüm bildirilir; ertele ve atla; eski sıra numaralı ve süresi dolmuş bildirim reddedilir.
- *Çalışan sürüm:* merkez ekranı onaylı birimlerin çalışan sürümünü, bildirilmiş yeni sürümü (hazır, indiriliyor) ve imaj digest'lerini gösterir; uygulama ve geri almadan sonra güncellenir.
- *Anahtar döndürme:* yeni anahtarla imzalı bildirim güven dosyasında yokken reddedilir; dosyaya eklenince (ajanlar yeniden başlatılmadan) kabul edilir; geçiş penceresinde eski anahtarla imzalısı da kabul edilir; eski anahtarın `not_after`ı dolunca reddedilir. Güven dosyası testin sonunda eski haline döner.
- *İmaj imzası döndürme:* iki cosign anahtarıyla (eski, yeni) imzalı imajlar gerçek Podman'la çekilir: geçiş penceresinde ikisi de çekilir, eski anahtar düşünce yalnızca yenisinin imzası geçer, geçerli anahtar kalmayınca politika üretilmez.
- *Yayıncının kayıt defteri:* CI yazma jetonuyla `test/` yoluna yazar; `scripts/image-copy.sh` aynı digest'i `prod/` yoluna kopyalar; birim jetonu yalnızca kendi kanalını çeker, test yolunu, kimliksiz ve sahte jetonlu isteği reddeder, yazamaz.
- *İmaj imzası:* `scripts/image-sign.sh` (cosign, gerçek container) prod anahtarıyla imzalar; `server generate-policy` ile üretilen politikayla gerçek Podman yalnızca prod anahtarıyla imzalı imajı çeker, imzasızı ve pilot anahtarıyla imzalıyı reddeder.
- Bildirimleri testin `lib/channel.mjs` yardımcısı üretir ve `server sign-channel` ile imzalar; anahtarlar `tools/e2e/out/trust` altındadır (`scripts/e2e.sh up guncelleme` üretir).

`standalone` ve `headquarters` senaryoları ayrıca bütün sekmeleri gezip sayfa/konsol hatası ve 5xx arar, güncelleme kurulu değilken ekranların "kurulu değil" dediğini ve uçların 404 verdiğini denetler.

## Sınırlar

- Testler gerçek servisleri ve gerçek NATS leaf bağlantılarını kullanır; bu yüzden yavaştır. Kaynak bağlantısı kopunca NATS yeniden denemesi yüzünden yetişme birkaç on saniye sürebilir; beklemeler buna göre geniştir.
- Temizleyicinin gerçek 30/90 günlük süreleri denenmez; kısaltılmış süreyle kural denenir (hangi olay hangi koşulda silinir). Süre ayarının ortam değişkeninden okunması `pkg/delivery` birim testlerindedir.
- Yük, çok kasa ve çok mağaza denenmez (`store-sim.sh` iki kasa dener).
- Operasyonda kasa ve restoran dışı modüller (ör. çok terminalli masa paylaşımı) henüz yoktur.
