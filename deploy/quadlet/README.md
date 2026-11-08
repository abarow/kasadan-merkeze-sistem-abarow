# Kasa, operasyon ve merkez kurulumu: Podman + Quadlet

Bu klasördeki dosyalar bir kasayı, `store/` alt klasöründekiler mağazanın operasyonunu (mağaza sunucusu), `headquarters/` alt klasöründekiler işletmenin merkezini (merkez) systemd ile çalıştırır. Her servis ayrı bir container ve ayrı bir systemd birimidir; her biri tek başına güncellenir (bkz. [DESIGN.md](../../docs/DESIGN.md) bölüm 12).

| Dosya | Birim |
|---|---|
| `abarow.network` | Ortak ağ; container'lar birbirine adıyla ulaşır (`abarow-sales:9000`) |
| `abarow-nats.container` | Mesaj yolu (NATS JetStream) |
| `abarow-device.container` | Fiş yazıcısı, çekmece ve barkod okuyucu (kurulumda verilmezse simülatör) |
| `abarow-fiscal.container` | ÖKC simülatörü (**mali değeri olmayan fiş**) |
| `abarow-payment.container` | Ödeme (POS simülatörü) |
| `abarow-catalog.container` | Katalog |
| `abarow-sales.container` | Satış |
| `abarow-staff.container` | Kasiyer, vardiya, gün sonu |
| `abarow-retail.container` | Market modülü ve ekranı |
| `abarow-gateway.container` | Ekranın girişi; yalnızca `127.0.0.1:8080` |

## Gereksinimler

- Linux, systemd, **Podman 5** (`Notify=healthy` için). cgroup v2'de kullanıcıya bellek denetimi devredilmiş olmalı (güncel dağıtımlarda varsayılan); bellek sınırları compose'dakilerle aynıdır.
- İmajlar `ghcr.io/abarow/abarow-*:0.1` kanalından gelir (amd64 ve arm64; sürüm etiketinde CI yayınlar). Depo özel olduğu sürece imajlar da özeldir: kurulum betiklerine GitHub kullanıcı adı ve `read:packages` yetkili bir token verilir (klasik "personal access token"; token bir dosyada, komut satırında değil):

  ```sh
  sudo deploy/install/register-install.sh ... --ghcr-user kullanici-adi --ghcr-token-file /root/ghcr-token
  ```

  Giriş `abarow` kullanıcısının `~/.config/containers/auth.json` dosyasına kaydedilir (yalnızca o okur); servisler ve güncellemeler bununla çeker. Token'ın süresi dolunca betik yeni token'la yeniden çalıştırılır. Token dosyası kurulumdan sonra silinebilir.

## Kurulum betiği

Debian 13'te paketler kurulduktan sonra tek komutla kurulur. Betik tekrar çalıştırılabilir:

```sh
sudo apt install podman aardvark-dns nftables cage chromium
sudo deploy/install/register-install.sh --seed /yol/seed --register register-1 --store-id store-1   # catalog.json, cashiers.json
```

`--register` ve `--store` satış olaylarına yazılan kasa ve mağaza kimlikleridir; birden fazla kasalı mağazada her kasanın kimliği ayrıdır (varsayılan `register-1`, `store-1`).

Betiğin yaptıkları:
- Podman 5'i, `aardvark-dns`'i (container'ların birbirini adıyla bulması) ve `nftables`'ı (Podman ağı) denetler.
- `abarow` kullanıcısını oluşturur ve linger'ı açar.
- Quadlet dosyalarını ve başlangıç verisini kopyalar.
- NATS anahtarlarını üretip Podman secret'ı yapar.
- Bütün servisleri başlatır ve **her birinin gerçekten çalıştığını denetler**.
- tty1'de cage + Chromium kiosk ekranını kurar (`--no-kiosk` ile atlanır).

Deneme verisiyle (`deploy/compose/seed`) kurulmaz.

**Restoran kasası:** `--module restaurant` market modülünün yerine restoran modülünü kurar (`deploy/quadlet/restaurant/abarow-restaurant.container`): salon ve masalar, adisyon, görselli menü, mutfak ve bar fişi, hesap bölme. Menü `catalog.json`'daki kategorili ürünlerdir; ürün görselleri seed klasörünün `images/` altından (dosyada `"image": "images/kofte.jpg"`) ya da merkezden gelir. Örnek: `deploy/compose/seed/restaurant/`. Mutfak ve bar yazıcıları ağdaki ESC/POS yazıcılardır; verilmezse o istasyonun fişi fiş yazıcısına basılır:

```sh
sudo deploy/install/register-install.sh --seed /yol/seed --module restaurant --printer /dev/usb/lp0 --kitchen-printer 192.168.1.51 --bar-printer 192.168.1.52
```

İlk açılışta "Salon düzeni"nden salonlar ve masalar tanımlanır (müdür onayı). Gönderilmiş siparişin iptali de müdür onayı ister ve istasyona iptal fişi basılır. Hesap istenince basılan adisyon dökümünün mali değeri yoktur; ÖKC'li e-Adisyon gerçek ÖKC entegrasyonuyla gelecek. Betik `--module market` ile yeniden çalıştırılınca market modülüne dönülür; satışlar, katalog ve kasiyerler kalır.

**Barkod okuyucu:** USB'de klavye gibi çalışan okuyucunun kalıcı aygıt adı verilir (`ls -l /dev/input/by-id/`; adı `-event-kbd` ile biter):

```sh
sudo deploy/install/register-install.sh --seed /yol/seed --barcode-scanner /dev/input/by-id/usb-Okuyucu_Markasi-event-kbd
```

device okuyucuyu kendine ayırır: tuşları ekrana düşmez, kod imleç nerede olursa olsun sepete gider. Okuyucu çekilip takılınca 2 saniye içinde yeniden okunur; takılı değilken de yazıcı çalışır. `abarow` kullanıcısı `input` grubuna eklenir. Bu yüzden `abarow` bütün giriş aygıtlarını (klavye dahil) okuyabilir; kasa başka işler için kullanılmamalı. Okuyucu ABD klavye düzeninde çalışmalıdır (kutudan böyle çıkar). Seçenek verilmezse okuyucu klavye gibi barkod alanına yazar.

**Fiş yazıcısı:** ESC/POS konuşan termal yazıcı (çoğu fiş yazıcısı) USB'de ya da ağda olabilir. Para çekmecesi yazıcıya bağlıdır ve yazıcıdan açılır.

```sh
# USB (yazıcı açıkken: ls -l /dev/usb/), 80 mm kağıt
sudo deploy/install/register-install.sh --seed /yol/seed --printer /dev/usb/lp0 --receipt-width 80
# Ağ (port verilmezse 9100), 58 mm kağıt
sudo deploy/install/register-install.sh --seed /yol/seed --printer 192.168.1.50
```

- `--receipt-width`: 58 mm (32 sütun, varsayılan) ya da 80 mm (48 sütun).
- `--code-page`: Türkçe karakter tablosu, `pc857` (varsayılan) ya da `wpc1254`. Yazıcı bu tabloyu başka bir numarayla seçiyorsa numara da verilir (ör. `pc857:61`; yazıcının kılavuzunda "ESC t" tablosu). Kurulumdan sonra X raporu basıp Türkçe harflere bakın.
- USB'de `abarow` kullanıcısı `lp` grubuna eklenir. Kurulum yazıcının aygıt numarasıyla kalıcı bir aygıt dosyası yapar (`~abarow/abarow/aygit/yazici`): yazıcı kapatılıp açılınca ya da çekilip takılınca device yeniden başlamadan basar. Yazıcı kapalıyken fiş basılamaz ve ekranda hata görünür. Tek USB yazıcı içindir: ikinci bir USB yazıcı takılırsa numaralar değişebilir.
- Ağda yazıcıya sabit IP verilmelidir (yazıcının ayarından ya da modemde DHCP rezervasyonuyla).
- Yazıcının durumu (kağıt bitti, kağıt azaldı, kapak açık, kapalı) 5 saniyede bir sorulur ve kasa ekranının üstünde uyarı olarak görünür. Durumunu bildirmeyen yazıcıda (tek yönlü USB yazıcı, `DLE EOT`'u tanımayan yazıcı) uyarı çıkmaz: USB yazıcı veriyi almazsa basma 10 saniye sonra hata verir; ağdaki yazıcı veriyi belleğine alıp kağıt takılınca basabilir ya da hiç basmayabilir. Fiş çıkmadıysa kasiyer bunu görüp bildirmelidir.
- Seçenek verilmezse yazıcı simülatördür: belgeler `journalctl --user -u abarow-device` günlüğüne basılır.

**Terazi (kasada):** Kasaya seri portla (RS-232 ya da USB-seri çevirici) bağlı terazide, tartılı ürün seçilince ağırlık teraziden okunur. Reyondaki terazinin bastığı barkod her durumda okutulabilir.

```sh
# USB-seri çeviricinin kalıcı adı: ls -l /dev/serial/by-id/
sudo deploy/install/register-install.sh --seed /yol/seed --scale /dev/serial/by-id/usb-...-port0 --scale-protocol nci --scale-settings 9600,7E1
```

- `--scale-protocol`: terazinin ayarındaki iletişim protokolü, `toledo8217` (varsayılan) ya da `nci` (Weigh-Tronix/NCI ECR). Terazinin kılavuzunda "Toledo 8217", "ECR" ya da "NCI" diye geçer; terazi bu protokollerden birine ayarlanmalıdır.
- `--scale-settings`: hız ve çerçeve, terazininkiyle aynı olmalı (varsayılan `9600,7E1`: 9600 baud, 7 veri biti, çift parite, 1 durma biti).
- Terazi kilogram göstermelidir. Ağırlık ondalık noktasız ya da başka birimde gelirse ürün eklenmez.
- `abarow` kullanıcısı `dialout` grubuna eklenir. Çevirici yazıcıdaki gibi kalıcı aygıt dosyasıyla bağlanır (`~abarow/abarow/aygit/terazi`): çekilip aynı porta takılınca yeniden okunur. Kurulumda çevirici takılı olmalıdır.
- Terazi kapalıysa ya da yanıt vermezse tartılı ürün eklenmez, kasiyer nedeni görür ("terazi kapalı ya da USB-seri çevirici takılı değil", "yanıt vermiyor"). Seçenek verilmezse tartılı ürün yalnızca terazi barkoduyla satılır.
- Gerçek bir teraziyle henüz denenmedi; protokoller yayımlanmış belgelere göre yazıldı.

**Müşteri ekranı (HDMI ikinci ekran):** Müşteriye dönük ikinci ekranda sepet, toplam, ödenen ve para üstü görünür. Kiosk bu durumda cage yerine sway ile kurulur (cage tek pencere gösterir):

```sh
sudo apt install sway
ls /sys/class/drm/        # çıkış adları: card0-eDP-1 → eDP-1, card0-HDMI-A-1 → HDMI-A-1
sudo deploy/install/register-install.sh --seed /yol/seed --register-screen eDP-1 --customer-screen HDMI-A-1
```

- Kasa ekranı ve dokunmatik birinci çıkıştadır; klavye ve klavye gibi okuyucu hep kasa penceresine yazar.
- İki pencere aynı Chromium sürecindedir (müşteri ekranı durumu kasiyer ekranından alır). Chromium kapanırsa ikisi birlikte yeniden açılır.
- Dosyalar: `/etc/abarow/kiosk-sway.conf`, `/etc/abarow/kiosk-tarayici.sh`. Seçenekler verilmeden yeniden kurulunca kiosk tek ekranlı cage'e döner.
- Başsız sway'de (iki sanal çıkış) denendi: pencereler doğru çıkışta, ikisi de tam ekran, odak kasada. Gerçek iki ekranlı donanımda henüz denenmedi.

## Elle kurulum (root olmadan, ayrı kullanıcıyla)

```sh
sudo useradd -m abarow
sudo loginctl enable-linger abarow          # oturum açılmadan servisler çalışsın
sudo -iu abarow
mkdir -p ~/.config/containers/systemd ~/abarow/seed
cp deploy/quadlet/abarow* ~/.config/containers/systemd/
cp /yol/catalog.json /yol/cashiers.json ~/abarow/seed/
systemctl --user daemon-reload
systemctl --user start abarow-gateway.service   # bağımlılıklar sırayla başlar
```

Ekran `http://127.0.0.1:8080` adresindedir; kiosk tarayıcısı (cage + Chromium) buraya bağlanır.

## NATS anahtarları

NATS kimliksiz bağlantıyı kabul etmez. Her servis kendi anahtarıyla bağlanır ve yalnızca kendi işine yetkilidir (bkz. TASARIM bölüm 6). Anahtarlar Podman secret'ı yapılır.

Kurulum betiği anahtarları `~abarow/abarow/nats-auth`'ta (yalnızca `abarow` okur) saklar ve her çalıştırmada aynılarını kullanır. Böylece kasanın operasyondaki kaydı yeniden kurulumda geçersiz kalmaz. Secret'lar zaten aynı kullanıcının diskindedir; bu, yeni bir açık oluşturmaz.

Elle kurulumda:

```sh
mkdir -p ~/abarow/nats-auth
podman run --rm --user 0 -v ~/abarow/nats-auth:/auth:Z ghcr.io/abarow/abarow-natsauth:0.1 \
  -out /auth -users sales,staff -server-name register-1
podman secret create abarow-nats-conf  ~/abarow/nats-auth/nats.conf
podman secret create abarow-nats-sales ~/abarow/nats-auth/sales/nats.nk
podman secret create abarow-nats-staff ~/abarow/nats-auth/staff/nats.nk
rm -rf ~/abarow/nats-auth
```

Anahtar yenilemek için secret'ları silip (`podman secret rm`) aynı adımları tekrarlayın, sonra servisleri yeniden başlatın.

## Operasyon (mağaza sunucusu)

Birden fazla kasalı mağazada kasalar operasyona bağlanır; operasyon satışları toplar, mağaza raporlarını ve kasa durumunu gösterir (bkz. TASARIM bölüm 14). Operasyon ayrı bir makinede ya da küçük mağazada kasalardan birinde çalışabilir; birim ve ağ adları kasanınkilerle çakışmaz.

| Dosya (`store/`) | Birim |
|---|---|
| `abarow-store.network` | Operasyon ağı (`abarow-store`) |
| `abarow-store.container` | NATS: kasaların leaf bağlantıları (TLS), mağaza stream'i. Mağaza ağına yalnızca 7421 açılır |
| `abarow-store-db.container` | Operasyon veritabanı (PostgreSQL 18; karar 04.11.2026): report ve envanter aynı veritabanını kullanır, oturumlar da burada. Port açılmaz; parola kurulumda rastgele üretilir |
| `abarow-report.container` | Mağaza raporları ve operasyon ekranı; mağaza ağına 8090 açılır (HTTPS, parolayla) |
| `abarow-inventory.container` | Mağaza stoğu (envanter): satışlar stoktan düşer, mal kabul, sayım ve transfer ekrandan; merkezin gönderdiği transferler iner; port açılmaz |
| `backup/abarow-store-backup.container`, `backup/abarow-store-backup.timer` | Günlük veritabanı yedeği (03:45, pg_dump, 14 gün) |
| `backup/abarow-store-backup-copy.service` | Yalnızca `--backup-target` ile: her yedekten sonra makine dışına kopya |

### 1. Operasyonu kurun

```sh
sudo apt install podman aardvark-dns nftables
sudo deploy/install/store-install.sh --store-id store-1 --address 192.168.1.10
```

- `--address`: kasaların operasyona bağlanacağı IP ya da ad. Sunucu sertifikasına yazılır. Birden fazlası virgülle verilir (`192.168.1.10,store.local`). Adres değişince betik `--address` ile yeniden çalıştırılır; yalnızca sunucu sertifikası yenilenir, kasalar etkilenmez.
- **Mağaza parolası:** Betik ekranın parolasını sorar (en az 8 karakter); ilk kurulumda ekrana bununla girilir, müdür tanımlanınca kapanır (bkz. Kasiyerler). Yalnızca bcrypt özeti saklanır. Etkileşimsiz kurulumda `--password-file` verilir. Parolayı değiştirmek için betik `--password-file` ile yeniden çalıştırılır.
- **Yazdırılanlar:** ekran adresi (`https://192.168.1.10:8090`), kasalara kopyalanacak CA sertifikası (`~abarow/abarow/store/ca.pem`) ve özeti, kasada çalıştırılacak komut.
- Güvenlik duvarı varsa mağaza ağına 7421 ve 8090 açılır. Ekran HTTPS sunar (karar 06.11.2026): mağaza CA'sının imzaladığı, kurulumdaki `--address` adlarına verilmiş sertifika kullanılır. Tarayıcıların uyarı vermemesi için müdür cihazlarına `ca.pem` güvenilen kök olarak yüklenir; ekrana yalnızca `--address` ile verilen ad ya da IP ile girilir. Ekranı internete açmayın; uzaktan erişim için VPN kullanın. Sertifika yenilenince (natsauth yeniden çalışınca) servis yeniden başlamadan yeni sertifikayı kullanır.

### 2. Kasayı operasyona bağlayın

CA sertifikasını kasaya kopyalayın (ör. USB bellekle). Özetinin operasyonun yazdırdığıyla aynı olduğunu `sha256sum ca.pem` ile denetleyin. Sonra kasada:

```sh
sudo deploy/install/register-install.sh --seed /yol/seed --register register-2 --store-id store-1 \
  --store-url tls://192.168.1.10:7421 --store-ca ca.pem
```

Betik sonunda kasanın açık anahtarını ve operasyonda çalıştırılacak komutu yazdırır.

Operasyonun leaf portu 30.10.2026'da 7422'den 7421'e alındı (7422 merkezin portudur). Daha önce `--store-url tls://...:7422` ile kurulmuş kasalar, operasyon güncellenince bağlanamaz; kasa kurulum betiği aynı ayarlarla, adresi `:7421` yapılarak yeniden çalıştırılır (anahtarlar değişmez, kasa operasyonda yeniden eklenmez).

### 3. Kasayı operasyona ekleyin

Operasyonda, kasanın yazdırdığı komutla:

```sh
sudo deploy/install/store-install.sh register-add register-2 UABC... "Kasa 2"
```

Kasa birkaç saniye içinde bağlanır; eklenene kadar yaptığı satışlar kasada birikir ve kayıpsız aktarılır.

Kasa çalınır ya da kaldırılırsa `store-install.sh register-remove register-2` ile çıkarılır: artık bağlanamaz, gelmiş satışları raporlarda kalır.

### 4. Kasiyerler

**Stok:** Operasyon ekranının "Stok" bölümü ürün başına eldeki miktarı gösterir. Kasalardaki satışlar stoktan kendiliğinden düşer. Tedarikçiden gelen mal "Mal kabul" ile girilir: tedarikçi, irsaliye no ve ürünler; ürün barkodla okutulur ya da adıyla aranır. Aynı tedarikçinin aynı irsaliyesi ikinci kez girilemez. "Sayım"da rafta sayılan miktar girilir; sistemdekiyle fark sayım farkı olarak kaydedilir. Stok eksiye düşebilir: satış durmaz, eksi stok sayımla düzeltilir. Her hareketin kaydı ürünün satırına tıklanınca görünür (kim, ne zaman, hangi irsaliye). Ürünler merkezin kataloğundan gelir; merkeze bağlı olmayan mağazada satılan ürünler adıyla öğrenilir. Hareketler merkeze de gider.

Kasiyerler operasyon ekranının "Kasiyerler" bölümünden yönetilir: ekleme, ad, rol (kasiyer ya da müdür), PIN ve işten ayrılanın hesabını kapatma. Değişiklik bağlı kasalara birkaç saniyede iner; hesabı kapanan kasiyer kasada giriş yapamaz, açık oturumu kapanır. Kasalarda kullanılan kasiyer dosyası operasyona bir kez yüklenebilir:

```sh
sudo deploy/install/store-install.sh cashiers-load /yol/seed/cashiers.json
```

Müdürler operasyon ekranına kendi kodu ve PIN'iyle girer; değişiklik kayıtlarında adları görünür. Mağaza parolası yalnızca müdür tanımlanmamışken (ilk kurulum) kullanılır: hesabı açık müdür olunca kapanır. PIN'ini unutan müdüre başka bir müdür ekrandan yeni PIN verir; başka müdür yoksa:

```sh
sudo deploy/install/store-install.sh pin ayse    # yeni PIN sorulur
```

Ekran HTTPS olduğundan müdür PIN'i (kasadaki müdür onayının da PIN'i) mağaza ağında şifreli gider. Yine de mağaza ağına yabancı cihaz bağlanmamalı, misafir Wi-Fi'si ayrı olmalı; `ca.pem`'i cihaza yükleyen kişi o CA'nın imzaladığı her sertifikaya güvenir, bu yüzden CA yalnızca müdür cihazlarına yüklenir.

Operasyonda yönetilen kasiyeri kasadaki dosya artık değiştirmez; dosya yalnızca operasyonda olmayan kasiyerler için kullanılır. Eski bir sürümle kurulmuş operasyonda önce `store-install.sh` kurulum seçenekleriyle yeniden çalıştırılır (mağaza kimliğini report'a yazar). Bağlı kasalarda da `register-install.sh` aynı seçeneklerle yeniden çalıştırılır (kasiyerleri operasyondan alma ayarı).

Ayarlar `/etc/abarow/store.env`'de (root), anahtarlar ve CA `~abarow/abarow/store/auth`'ta (yalnızca `abarow`), kasa listesi `~abarow/abarow/store/config/registers.json`'dadır. Veritabanı parolası `auth/db-password`'tadır (yeniden kurulumda korunur).

**Yedekler (operasyon):** veritabanının yedeği her gün 03:45'te `~abarow/abarow/store/backups` klasörüne alınır (`abarow-2026-11-04-034512.dump`); 14 günden eskiler silinir; kurulumda ilk yedek hemen alınır. Elle yedek: `systemctl --user start abarow-store-backup` (`abarow` kullanıcısıyla). Geri yükleme (önce şimdiki hal yedeklenir; yedekten sonra gelen satışlar mağaza stream'inden otomatik yeniden işlenir, rapor ve stok eksiğini kapatır):

```
sudo deploy/install/store-install.sh restore ~abarow/abarow/store/backups/abarow-2026-11-04-034512.dump
```

Yeniden işleme yalnızca satış olaylarını geri getirir: yedekten sonra girilen mal kabul ve sayım, mağaza fiyatı ve kasiyer değişiklikleri yalnızca veritabanında durduğundan geri gelmez (kasiyerler ve fiyatlar kasalarda ve merkezde durur; operasyona yeniden girilir). Elle yeniden işleme: `sudo deploy/install/store-install.sh reprocess`. Belirli bir ana geri dönmek için (WAL arşivi; o ana kadar girilen mal kabul, sayım ve fiyat değişiklikleri de korunur): `sudo deploy/install/store-install.sh restore-to "2026-11-12 14:30:00+03"`. Ayrıntı merkezin "Belirli bir ana geri dönme" bölümündedir.

Yedekler aynı diskte durur: disk bozulursa birlikte gider. `--backup-target` merkezdekiyle aynıdır (aynı betikler): harici disk ya da NAS (`--backup-target /mnt/backup/abarow`) veya SSH ile başka bir sunucu (`--backup-target yedek@backup.ornek.com`; kurulum kopyalama için ayrı bir anahtar üretir ve sunucuda nasıl yetkilendirileceğini yazar). Her başarılı yedekten sonra yedekler hedefe kopyalanır; hemen kopyalamak için `sudo deploy/install/store-install.sh backup-copy`. Ayrıntı için merkezin "Yedekler" bölümüne bakın.

## Merkez

Merkez işletmenin bütün mağazalarının satışlarını toplar ve merkez raporlarını gösterir (bkz. TASARIM bölüm 15). İşletmenin kendi sunucusunda ya da kiralık bir VPS'te çalışır; mağazalar ona dışarı doğru bağlanır, mağazalarda port açmak gerekmez.

| Dosya (`headquarters/`) | Birim |
|---|---|
| `abarow-headquarters.network` | Merkez ağı (`abarow-headquarters`) |
| `abarow-headquarters.container` | NATS: mağazaların leaf bağlantıları (TLS), merkez stream'i. Dışarıya yalnızca 7422 açılır |
| `abarow-db.container` | PostgreSQL 18; dışarı açılmaz. Parola kurulumda rastgele üretilir |
| `abarow-ingest.container` | Mağazaların olaylarını PostgreSQL'e yazar |
| `abarow-catalog-admin.container` | Katalog yönetimi (ürünler, merkez fiyatları); değişiklikleri mağazalara gönderir. Dışarı açılmaz |
| `abarow-inventory-headquarters.container` | Stok: merkez deposu ve bütün yerlerin stoğu (mağazaların hareketlerinden); transferleri mağazalara iletir (yer listesi `stores.json`'dan). Dışarı açılmaz |
| `abarow-reporting.container` | Merkez raporları ve merkez ekranı; varsayılan olarak yalnızca `127.0.0.1:9090` |
| `backup/abarow-backup.container`, `backup/abarow-backup.timer` | Günlük veritabanı yedeği (03:30) |
| `backup/abarow-backup-copy.service`; ortak `../backup/backup.sh`, `../backup/backup-copy.sh` | Yalnızca `--backup-target` ile: her yedekten sonra makine dışına kopya (rsync); betikler operasyon ile ortaktır |
| `https/abarow-caddy.container` | Yalnızca `--domain` ile: ekrana HTTPS (Caddy; 80 ve 443) |

### 1. Merkezi kurun

```sh
sudo apt install podman aardvark-dns nftables
sudo deploy/install/headquarters-install.sh --address headquarters.ornek.com --account ayse
```

- `--address`: mağazaların merkeze bağlanacağı alan adı ya da IP (sunucu sertifikasına yazılır; birden fazlası virgülle).
- `--account`: merkez ekranının ilk hesabı; parola sorulur (en az 10 karakter) ya da `--password-file`'ndan okunur. Bu hesap yöneticidir; öbür kullanıcıları ekranın "Kullanıcılar" bölümünden ekler ve rollerini (yönetici, katalog, rapor) ve görecekleri mağazaları verir. Komut satırı ilk yönetici ve kurtarma içindir: `headquarters-install.sh hesap ekle|parola|kapat|ac|liste` (eklenen kullanıcı yöneticidir). Her kişinin kendi hesabı vardır; parola değişince ya da hesap kapatılınca o kişinin açık oturumları kapanır.
- **Yazdırılanlar:** operasyonlara kopyalanacak CA sertifikası (`~abarow/abarow/headquarters/ca.pem`) ve özeti, operasyonda çalıştırılacak komut.
- Güvenlik duvarında 7422 mağazalara (ya da internete) açılır. Bağlantı TLS'lidir ve yalnızca listedeki mağaza anahtarları kabul edilir.

**Stok:** Merkez ekranının "Stok" bölümü ürün başına her yerin stoğunu gösterir: merkez deposu, mağazalar ve toplam. Mağazaların stoğu kendi operasyonlarında tutulur ve buraya hareketleriyle gelir (satış, mal kabul, sayım); bir miktara tıklanınca o yerdeki hareketler görünür. Merkez deposunun mal kabulü ve sayımı buradan girilir (yönetici ve katalog kullanıcısı; rapor kullanıcısı yalnızca görür ve yalnızca kendi mağazalarını).

Kasaların kullandığı başlangıç kataloğu (`catalog.json`) merkeze bir kez yüklenir; sonra ürünler ve fiyatlar merkez ekranının "Ürünler" bölümünden yönetilir. Değişiklikler merkeze bağlı mağazaların operasyonuna, oradan kasalara kendiliğinden iner (kasa `--store` ile, operasyon `--headquarters` ile kurulduysa). Merkezden gelen ürün kasadaki `catalog.json` ile değişmez:

```sh
sudo deploy/install/headquarters-install.sh catalog-load /yol/seed/catalog.json
```

Restoranda ürünlerin menü kategorisi, sırası, istasyonu (mutfak, bar), opsiyon grupları ve görseli de "Ürünler" bölümünden verilir. Dosyada `"image": "images/kofte.png"` yazılan görseller (dosyanın klasörüne göre) `catalog-load` ile birlikte yüklenir. Görsel JPEG, PNG ya da WebP olmalı, en çok 512 KB; ekrandan yüklenen görsel tarayıcıda 640 px'e küçültülür. Örnek menü: `deploy/compose/seed/restaurant/`.

### 2. Operasyonu merkeze bağlayın

CA sertifikasını operasyona kopyalayın, özetini `sha256sum ca.pem` ile denetleyin. Operasyonda kurulum betiği `--headquarters` ile (yeniden) çalıştırılır:

```sh
sudo deploy/install/store-install.sh --store-id store-1 --address 192.168.1.10 \
  --headquarters tls://headquarters.ornek.com:7422 --headquarters-ca ca.pem
```

Betik sonunda operasyonun açık anahtarını ve merkezde çalıştırılacak komutu yazdırır. Merkezde:

```sh
sudo deploy/install/headquarters-install.sh store-add store-1 UABC... "Kadıköy"
```

Mağaza birkaç saniye içinde bağlanır; eklenene kadar (ya da internet kesikken) satışlar operasyonda birikir ve kayıpsız aktarılır; operasyon olayı, merkezin makbuzu gelmeden silmez (bkz. docs/DESIGN.md "Teslim makbuzu"). `headquarters-install.sh store-remove store-1` mağazayı çıkarır: artık bağlanamaz, gelmiş satışları raporlarda kalır.

### 3. Ekrana uzaktan erişim

Ekran varsayılan olarak yalnızca merkez makinesinden açılır (`http://127.0.0.1:9090`): parola ve raporlar şifresiz ağa çıkmasın. Dışarıdan erişim için:

- **HTTPS (önerilen):** Kurulum betiği ekranın önüne Caddy'yi kurar; Caddy alan adı için sertifikayı Let's Encrypt'ten kendisi alır ve süresi dolmadan yeniler:

  ```sh
  sudo deploy/install/headquarters-install.sh --address headquarters.ornek.com --domain report.ornek.com --email bt@ornek.com
  ```

  Alan adının DNS kaydı bu makinenin IP'sini göstermeli; 80 (sertifika doğrulaması, HTTPS'e yönlendirme) ve 443 güvenlik duvarında açık olmalı. `--address` ile aynı ad da kullanılabilir. `--email` isteğe bağlıdır: Let's Encrypt sertifikayla ilgili sorunları bu adrese yazar. Ekran `https://report.ornek.com`'dan açılır; HTTPS'le geldiğini Caddy'nin `X-Forwarded-Proto` başlığından anlar ve oturum çerezini yalnızca HTTPS'le gönderir. Tarayıcıya bir yıl yalnızca HTTPS'le gelmesi söylenir (HSTS).

  - Caddy ayrı bir container'dır (`headquarters/https/abarow-caddy.container`, `docker.io/library/caddy:2.11-alpine`); internete açık olduğu için 2.11'in yamalarını kendiliğinden alır. Sertifikalar `abarow-caddy-data` biriminde kalıcıdır.
  - Root olmayan Podman'ın 80 ve 443'ü açabilmesi için kurulum `net.ipv4.ip_unprivileged_port_start=80` yapar (`/etc/sysctl.d/90-abarow-https.conf`). Bu makinede root olmayan başka kullanıcılar da 80 ve üstündeki portları açabilir: merkez makinesi başka işler için kullanılmamalı.
  - Betik `--domain` olmadan yeniden çalıştırılırsa Caddy kaldırılır (öbür seçenekler gibi, kurulum verilen seçeneklere göre yapılır).
  - Başka bir ters vekil (ör. var olan nginx) kullanılacaksa `--domain` verilmez; vekil `127.0.0.1:9090`'a yönlendirir ve `X-Forwarded-Proto: https` başlığını ekler. (Ekranın portu 30.10.2026'da 8096'dan 9090'a alındı; betik yeniden çalıştırılınca yeni port geçerli olur, var olan vekilin adresi güncellenmelidir. `--domain` ile kurulan Caddy etkilenmez.)
- **VPN** (ör. WireGuard): ekran yalnızca VPN'deki cihazlara açılır.
- **Güvenilen yerel ağ:** `headquarters-install.sh --address ... --screen 0.0.0.0:9090` (düz HTTP; internete açılmaz).

### 4. Yedekler

Veritabanının yedeği her gün 03:30'da `~abarow/abarow/headquarters/backups` klasörüne alınır (ör. `abarow-2026-10-28-033012.dump`: tarih ve saat; pg_dump'ın sıkıştırılmış biçimi); 14 günden eskiler silinir. Kurulumda ilk yedek hemen alınır. Elle yedek: `systemctl --user start abarow-backup` (`abarow` kullanıcısıyla).

Yedekler aynı diskte durur: disk bozulursa birlikte gider. **Yedekleri makine dışına kopyalayın:** kurulum betiği her başarılı yedekten sonra yedekleri `--backup-target`'e kopyalar (rsync; `sudo apt install rsync openssh-client`):

- **Harici disk ya da NAS** (bağlanmış bir klasör): `--backup-target /mnt/backup/abarow`. Disk takılı değilken bağlama noktası boş bir klasördür; betik hedef yedeklerle aynı diskteyse kopyalamaz (kopya aynı diske yazılıp onunla birlikte gitmesin). Disk yeniden takılınca bir sonraki yedekte eksikler gider. Diskte 90 günden eski kopyalar silinir.
- **Başka bir sunucu** (SSH): kurulum kopyalama için ayrı bir anahtar üretir (`~abarow/.ssh/abarow-backup`) ve sunucuda nasıl yetkilendirileceğini yazar. Önerilen yol, Linux sunucuda anahtarı `rrsync`'le (rsync 3.3+ paketinde; python3 ister) kısıtlamaktır. Böylece anahtar yalnızca bir klasöre yazabilir; okuyamaz, silemez, var olanı ezemez. Merkez ele geçirilse de sunucudaki eski kopyalar korunur. Bu yolda klasörü sunucu belirler, hedef yolsuz verilir:

  ```sh
  sudo deploy/install/headquarters-install.sh --address headquarters.ornek.com --backup-target yedek@backup.ornek.com
  # sunucuda ~backup/.ssh/authorized_keys:
  #   command="rrsync -wo -no-del -no-overwrite /srv/abarow-backup",restrict ssh-ed25519 AAAA... abarow-backup@merkez
  sudo deploy/install/headquarters-install.sh backup-copy   # yetkilendirdikten sonra dene
  ```

  rrsync olmayan sunucuda (ör. NAS) hedef yoluyla verilir (`--backup-target yedek@nas:/volume1/abarow`), anahtar kısıtsız eklenir. Sunucudaki eski kopyaları sunucu siler. Sunucunun anahtarı ilk bağlantıda kaydedilir; sonra değişirse bağlanılmaz.

Kopyalanamayan yedek (disk takılı değil, sunucuya ulaşılamadı) yerelde kalır ve sonraki yedekte gider; hedefteki dosya ezilmez, yerelde silinen yedek hedefte silinmez. Son kopyalamanın sonucu: `journalctl --user -u abarow-backup-copy` (`abarow` kullanıcısıyla). Yedekler şifrelenmez: hedef disk ya da sunucu, satış verisi taşıdığı için güvenli tutulmalı (ör. şifreli disk). Betik `--backup-target` olmadan yeniden çalıştırılırsa kopyalama kapanır.

Ayrıca merkez stream'i mağazalardan gelen olayları 90 gün saklar. Mağazaların operasyonları ve kasalar olayı yalnızca üst katmanın makbuzu geldikten sonra siler (en az 90 ve 30 gün); süreler `ABAROW_CLEAN_MAX_AGE`, `ABAROW_CLEAN_INTERVAL` ve `ABAROW_RECEIPT_MAX_AGE` ortam değişkenleriyle ayarlanır (docs/DESIGN.md "Teslim makbuzu").

### 5. Yedekten geri yükleme

```sh
sudo deploy/install/headquarters-install.sh restore ~abarow/abarow/headquarters/backups/abarow-2026-10-28-033012.dump
```

Betik:
1. önce veritabanının o anki halini yedekler (yanlış dosya seçilirse ondan dönülür),
2. ingest ve ekranı durdurur, veritabanını yedekteki haline getirir, servisleri başlatır,
3. yedekten sonra gelmiş olayları merkez stream'inden yeniden işletir (`reprocess`). Var olan olaylar olay kimliğiyle atlanır, eksikler eklenir. Böylece son 90 günün olayları kaybolmaz.

Yedek başka makineye kurulan yeni bir merkeze de aynı komutla yüklenir.

Ayarlar `/etc/abarow/headquarters.env`'de (root); anahtarlar, CA ve veritabanı parolası `~abarow/abarow/headquarters/auth`'ta (yalnızca `abarow`); mağaza listesi `~abarow/abarow/headquarters/config/stores.json`'dadır. Veritabanı parolası yalnızca ilk kurulumda veritabanına yazılır. `auth` klasörü kaybolursa ve veritabanı birimi (volume) kalırsa parola uyuşmaz. Bu yüzden `auth` klasörü de yedeklenmelidir. Bu klasör gizli anahtarları içerir; yedeği de gizli tutulmalıdır.

### 6. Belirli bir ana geri dönme (WAL arşivi)

Günlük yedek en çok bir günlük merkez değişikliğini kaybettirir: katalog, fiyat, hesap ve stok düzeltmeleri gibi. Mağazalardan gelen satışlar bu kayba girmez, stream'den yeniden işlenir. Bu yüzden merkez ve operasyon veritabanı her değişikliğin kaydını (WAL) sürekli arşivler:

- **Arşiv:** veritabanı biriminin kökünde (`/var/lib/postgresql`). Bir yazma en geç 15 dakika içinde arşivlenir.
- **Fiziksel yedek:** her pazar 04:30'da `backups` klasörüne alınır (`base-2026-11-15-043012.tar.gz`; `abarow-basebackup.timer`, operasyonda `abarow-store-basebackup.timer`). Son 2 tanesi kalır; arşiv en eskisinin başından bu yana tutulur. Kurulumda ilki hemen alınır.
- **Geri dönülebilecek aralık:** en eski fiziksel yedekten (1–2 hafta) şimdiye kadar herhangi bir an.

Örnek: bir ürün listesi saat 14:35'te yanlışlıkla silindiyse 14:30'a dönülür:

```sh
sudo deploy/install/headquarters-install.sh restore-to "2026-11-12 14:30:00+03"
sudo deploy/install/store-install.sh restore-to "2026-11-12 14:30:00+03"      # operasyon
```

Betik:
1. önce şimdiki hali yedekler (`pg_dump`),
2. servisleri ve veritabanını durdurur,
3. o andan önceki en yeni fiziksel yedeği açar,
4. arşivi o ana kadar yeniden oynatır, veritabanını yazmaya açar,
5. servisleri başlatır ve o andan sonra gelen olayları stream'den yeniden işletir,
6. yeni bir fiziksel yedek alır.

Önceki veri klasörü silinmez; veritabanı biriminde `18/before-restore-<zaman>` olarak durur, geri dönüşü kontrol ettikten sonra silin.

Sınırlar:
- Arşiv ve veritabanı aynı diskte durur. Disk bozulursa arşiv de gider; o durumda makine dışına kopyalanan son yedeğe dönülür. `--backup-target` fiziksel yedekleri de kopyalar, arşivi kopyalamaz.
- Arşiv diski kullanır. Yoğun bir merkezde günde birkaç yüz MB ile birkaç GB arası WAL birikebilir; ölçülmedi.
- Mekanizma CI'da Docker'la uçtan uca denenir (`scripts/pitr-test.sh`): yedek, iki yazma arasındaki bir ana dönüş, yazmanın açılması. Kurulum betiğindeki `restore-to` komutu gerçek bir Podman makinesinde denenmedi.

### 7. Veritabanı bakımı ve izleme

Her pazar 05:30'da (fiziksel yedekten sonra) haftalık bakım çalışır (`abarow-dbmaint.timer`, operasyonda `abarow-store-dbmaint.timer`):

- yaprak sayfaları yarıdan az dolu, 8 MB ve üstü indeksleri yazmaları durdurmadan yeniden kurar (`REINDEX CONCURRENTLY`),
- haftanın en çok süre alan 10 sorgusunu, en büyük tabloları (ölü satır ve son autovacuum ile) ve WAL arşivinin durumunu yazar,
- arşivleme takılmışsa hata verir: o zaman WAL veri klasöründe birikir ve diski doldurur.

Rapor: `journalctl --user -u abarow-dbmaint` (`abarow` kullanıcısıyla). Ayrıca veritabanı günlüğü her katmanda 1 saniyeyi aşan sorguları, 1 saniyeden uzun kilit beklemelerini ve 10 saniyeyi aşan autovacuum çalışmalarını yazar (`journalctl --user -u abarow-db`).

**Servis ölçüleri:** her servisin sağlık portu (8081, yalnızca katmanın kendi ağından erişilir) `/metrics` ucunda Prometheus biçiminde şunları verir:
- HTTP isteklerinin rota ve duruma göre sayısı ve süre dağılımı,
- gRPC çağrılarının yöntem ve koda göre sayısı ve süre dağılımı,
- Go çalışma zamanı belleği ve goroutine sayısı.

Sürüm `/version` ucundadır. Her HTTP isteği bir istek kimliği alır (`X-Request-ID`; gelen kimlik korunur) ve gRPC çağrılarıyla taşınır. 5xx dönen istek ve iç hata veren çağrı bu kimlikle günlüğe yazılır; bir hatanın kasada hangi servislerden geçtiği `journalctl --user | grep <kimlik>` ile izlenir. Katmanlar arasında bağlantıyı olay kimliği (`ce-id`) sağlar: işlenemeyen olay bu kimlikle günlüğe yazılır.

### 8. Üç makinede merkez

Merkez kesintisiz çalışsın diye üç makineye kurulabilir ([HA-HEADQUARTERS.md](../../docs/decisions/HA-HEADQUARTERS.md)). Her makinede aynı servisler çalışır:

- NATS üç makinelik bir kümedir; merkezin stream'leri üç kopyadır.
- Veritabanını Patroni ve etcd yönetir: bir makine birincil, diğerleri senkron replikadır. Bir commit en az bir replikaya yazılmadan tamamlanmaz.
- Her makinenin HAProxy'si (`abarow-db`) servisleri birincile gönderir.
- `ingest`, `catalog-admin` ve `inventory`'nin arka plan işlerini bir kopya yapar.

Bir makine giderse veritabanı yaklaşık 30 saniyede başka makineye geçer; onaylanmış hiçbir yazma kaybolmaz.

Makineler arasında (yalnızca birbirlerine) 2379, 2380 (etcd), 5432, 8008 (PostgreSQL, Patroni) ve 6222 (NATS kümesi) açık olmalıdır. Aradaki her bağlantı merkezin CA'sıyla TLS'dir.

```sh
# 1. makine
sudo deploy/install/headquarters-install.sh --address hq1.example.com   --cluster hq1.example.com,hq2.example.com,hq3.example.com --node 1 [--vip 192.168.1.50/24]
sudo deploy/install/headquarters-install.sh cluster-export /root/abarow-cluster.tar.gz
# dosyayı güvenli bir yoldan 2. ve 3. makineye kopyalayın, sonra silin

# 2. ve 3. makine
sudo deploy/install/headquarters-install.sh --address hq2.example.com   --cluster hq1.example.com,hq2.example.com,hq3.example.com --node 2 [--vip 192.168.1.50/24]   --cluster-import /root/abarow-cluster.tar.gz
```

- **Veritabanının açılması:** veritabanı iki makine kurulunca açılır. İlk makinenin kurulumu bunu bekler. İlk yedek ve `--account` hesabı veritabanı açıldıktan sonra yapılır; hesap için kurulum o zaman `--account` ile tekrarlanır.
- **`--vip`:** ekrana ve mağazalara tek bir adres verir. Adres o an çalışan makinelerden birinde durur (keepalived) ve makine dönünce geri taşınmaz. Bunun için makineler aynı yerel ağda olmalıdır. `--vip` yoksa mağazalara üç adres verilir (`--headquarters tls://hq1...:7422,tls://hq2...:7422,tls://hq3...:7422`).
- **Mağaza ekleme ve çıkarma:** `store-add` ve `store-remove` üç makinede de çalıştırılır.
- **Yedekler:** her makine günlük `pg_dump` yedeğini birincilden alır. Haftalık fiziksel yedeği ve bakımı kendi veritabanından alır; replikada bakım yalnız arşivi denetler. Her makine WAL'i kendi arşivler (`archive_mode=always`).
- **Geri yükleme:** `restore` ve `restore-to` üç makinede çalıştırılmaz; diğer makinelerin servisleri yazmayı sürdürürdü. Adımlar için [HA-HEADQUARTERS.md](../../docs/decisions/HA-HEADQUARTERS.md) "Kümede geri yükleme" bölümüne bakın.
- **Tek makineden geçiş:** tek makineli merkezin verisi Patroni'nin klasörüne taşınmaz. Kümeye geçerken günlük yedek (`pg_dump`) yeni kümeye `pg_restore` ile yüklenir, ardından `reprocess` çalıştırılır.

## PostgreSQL ana sürüm yükseltmesi

Veritabanı imajının ana sürümü (18) birim dosyalarında sabittir; onaylanan güncellemeler yalnızca 18.x yamalarını getirir. Bir sonraki ana sürüm (19) verinin yeni bir biçimde yazılmasını ister. Bunun için henüz betik yok: PostgreSQL 19 imajı yayımlanınca betik ve CI testi yazılacak. Yöntem:

1. Şimdiki hal yedeklenir (`pg_dump`). Kasada önce olay kuyruğunun boşalması beklenir: üst katmana gitmemiş olay kalmasın.
2. Servisler durdurulur; 18 container'ından `pg_dumpall` alınır.
3. **Eski WAL arşivi kenara alınır.** Arşiv birimin kökündedir (`/var/lib/postgresql`). Yeni küme WAL dosyalarını en baştan numaralar; aynı adlı eski dosya yerinde kalırsa arşivleme takılır ve WAL diski doldurur.
4. Birim değişmez. 18 imajı veriyi `/var/lib/postgresql/18/docker`'da tutar. 19 imajı `/var/lib/postgresql/19/docker`'ı boş bulur ve yeni bir küme kurar; döküm buna yüklenir.
5. 18 klasörü silinmez. Sorun çıkarsa birim dosyası 18 imajına geri alınır ve eski küme olduğu gibi açılır.
6. Yükseltmeden sonra yeni bir fiziksel yedek alınır (`abarow-basebackup`). Eski fiziksel yedekler ve kenara alınan arşiv yalnızca 18'e geri dönüş içindir.

Kasada veri küçüktür (saniyeler). Mağaza sunucusunda dakikalar, büyük bir merkezde saatler sürebilir; bu süre önceden verinin bir kopyası üzerinde ölçülmelidir.

## Başlangıç verisi

`~/abarow/seed/catalog.json` ve `cashiers.json` işletmenin kendi dosyalarıdır. `catalog.json`'da ürünün `unit_price_kurus`'u baştan beri geçerli fiyattır. İleri tarihli fiyat `prices` ile verilir; o anda kasada kendiliğinden geçer, internet kesikken de: `"prices": [{"unit_price_kurus": 1650, "from": "2026-11-02T00:00:00+03:00"}]`. `deploy/compose/seed` altındaki dosyalar yalnızca denemedir; özellikle **deneme PIN'leri gerçek kurulumda kullanılmamalıdır**. Merkeze bağlı mağazada katalog merkezden (merkez "Ürünler"), kasiyerler operasyon ekranından yönetilir; kasadaki dosyalar ilk kurulum ve operasyonsuz kasa içindir.

## Güncelleme

Güncelleme **kendiliğinden olmaz**: `podman-auto-update.timer` açılmaz ve birimlerde `AutoUpdate=registry` yoktur. Yeni sürüm gelince merkez, operasyon ve kasa ekranı bildirir; sürüm her katmanda ayrı ayrı ekrandan onaylanınca uygulanır (tasarım: [../../docs/UPDATE.md](../../docs/UPDATE.md); ajan, ekran onayı, geri alma ve imaj aynası yazıldı; gerçek Linux/Podman makinede henüz çalıştırılmadı).

### Güncelleme ajanı ve imaj aynası (isteğe bağlı)

Kurulum betikleri güncelleme ajanını yalnızca istenirse kurar; ajan kendiliğinden güncelleme yapmaz, yalnızca üst birime kaydolur ve imaj çekme jetonunu yeniler:

- `register-install.sh ... --update-url https://192.168.1.10:8443 [--level test|pilot|prod]` (operasyonun vekili; `--store-ca` zorunlu)
- `store-install.sh ... --update-url https://headquarters.ornek.com:8443 [--level ...] [--publisher-key kok.pub]`
- `headquarters-install.sh ... --publisher-url https://update.ornek.com:8443 --publisher-ca ca.pem [--level ...] [--publisher-key kok.pub]`

Seviye verilmezse `prod`'dur. `--channel-keys channel.json` (yayıncının build/pilot/prod genel anahtarları) verilirse ajan imzalı kanal bildirimini yoklar, yeni sürümü ekranda bildirir ve müdür/yönetici onayıyla uygular (başarısız olursa önceki sürüme döner); verilmezse yalnızca kayıt olur. Bu seçenek ajana abarow kullanıcısının Podman soketini ve Quadlet dizinini bağlar (bkz. docs/UPDATE.md "Yetkiler"). Birim kaydolunca onay kodu kendi ekranında görünür (kasada Raporlar > Güncelleme); merkez yöneticisi merkez ekranındaki "Güncelleme" bölümünden bu kodla onaylar. `--publisher-key` imaj aynasını (Zot ve yetki hizmeti) kurar. Bu seçenekler gerçek bir Linux/Podman makinede henüz çalıştırılmadı.

Ekran gelene kadar elle güncelleme (her servis tek başına, `abarow` kullanıcısıyla):

```sh
podman pull ghcr.io/abarow/abarow-sales:0.1
systemctl --user restart abarow-sales.service
```

Yeni imaj sağlıklı açılmazsa birim etkin sayılmaz (`Notify=healthy`). Eski imaj yerelde duruyorsa (`podman images`) onu aynı etikete geri bağlayıp (`podman tag`) birimi yeniden başlatın; geri alma kendiliğinden yapılmaz.

Bağımlılıklar `Wants=` ile bağlıdır, `Requires=` ile değil. `Requires=` olsaydı, örneğin sales güncellenirken gateway de durur ve kasa ekranı kapanırdı. Servisler bağımlılıkları kesilince bağlantıyı kendileri yeniler. Açılışta `After=` ve `Notify=healthy`, her servisin bağımlılıkları sağlıklı olana kadar beklemesini sağlar.

## ÖKC ve yazıcı

- `abarow-fiscal` simülatördür ve mali değeri olmayan fiş basar. Türkiye'de tüketiciye satışta mali belge zorunludur; bu birim **canlı satışta tek başına kullanılmamalıdır**. Gerçek ÖKC adapter'ı gelince bu birim değişir.
- `abarow-device` kurulumda `--printer` verilmezse yazıcı simülatörüdür. Gerçek ESC/POS yazıcı için bkz. "Fiş yazıcısı" (yukarıda). Gerçek yazıcıda da fiş, `abarow-fiscal` simülatörünün "MALİ DEĞERİ YOKTUR" fişidir.

## Notlar

- **Dış ağ beklenmez:** Her birimde `[Quadlet] DefaultDependencies=false` vardır. Podman'ın root olmadan çalışan birimlere eklediği "ağ çevrimiçi" beklemesi kaldırılır. Bağlantısız bir kasada bu bekleme her birim için 90 saniye sürer ve açılış dakikalarca uzardı. Servisler yalnızca yerel `abarow` ağını kullanır.
- **Dosya izinleri:** Container'daki kullanıcı (65532), host'ta başka bir alt kimliğe (subuid) eşlenir. Başlangıç verisi bu yüzden 0644 yazılır; PIN içeren `cashiers.json` için kullanıcının ev dizini 0750'dir.
- `HealthCmd` JSON dizi biçimindedir (`["/service", "healthcheck"]`). İmajlarda kabuk yoktur (`scratch`); düz metin verilirse Podman komutu `/bin/sh -c` ile çalıştırmaya çalışır, sağlık denetimi hep başarısız olur ve birim açılmaz.
- Bu dosyalar Podman 5.4'te systemd ile denendi (26.10.2026):
  - Soğuk açılışta on birim sırayla sağlıklı ayağa kalktı.
  - Satış akışı uçtan uca çalıştı.
  - sales yeniden başlatılınca ya da durdurulunca gateway ve staff çalışmaya devam etti.
  - NATS secret'larla kimlik isteyerek çalıştı; satış olayı staff'a ulaştı.
- Kurulum betiği Debian 13 + Podman 5.4'te root olmadan denendi (systemd'li container'da, 26.10.2026):
  - Temiz makinede ilk kurulum 15 sn sürdü.
  - İkinci çalıştırma sorunsuz geçti; satış akışı çalıştı.
  - Eksik `aardvark-dns` ve deneme verisi açık hatayla reddedildi.
  - Kiosk (cage + Chromium) ekransız ortamda denenemedi; gerçek kasada denenecek.
- Operasyon ve kasa iki ayrı makinede denendi (Debian 13 + Podman 5.4, systemd'li container'lar, 26.10.2026):
  - Operasyon 9 sn'de, operasyona bağlı kasa 23 sn'de kuruldu.
  - CA sertifikasının özeti iki makinede aynıydı.
  - Kasa eklenmeden önce yapılan satışlar, `register-add`'den sonra 2 sn içinde operasyona geldi.
  - Kasa eklenmeden önce operasyon bağlantıyı reddetti (TLS kuruldu, kimlik reddedildi).
  - Girişsiz rapor isteği 401; mağaza parolasıyla giriş yapıldı.
  - İki betiğin ikinci çalıştırması bir şeyi değiştirmedi: kasanın anahtarı aynı kaldı, parola yeniden sorulmadı, kasa bağlı kaldı.
  - `register-remove`'dan sonra kasa bağlanamadı ama satışa devam etti; yeniden eklenince aradaki satışlar eksik fişsiz geldi.
  - Ödeme simülatörünün kart limiti yalnızca compose'da ayarlı olduğu için register-sim'in "limiti aşan kart" senaryosu bu kurulumda başarısız sayıldı (beklenen fark).
- Merkez, operasyon ve kasa üç ayrı makinede denendi (Debian 13 + Podman 5.4, systemd'li container'lar, 27–28.10.2026):
  - Merkez ilk kurulumu 3 dk 22 sn sürdü: Podman'ın "ağ çevrimiçi" beklemesi iki kez zaman aşımına uğradı. Birimlere `DefaultDependencies=false` eklendi; ikinci çalıştırma 3 sn sürdü ve hiçbir şeyi değiştirmedi (veritabanı parolası, CA, mağaza listesi korundu).
  - Operasyon `--headquarters` ile 14 sn'de, kasa 23 sn'de kuruldu; `store-add` 3 sn sürdü.
  - Kasa operasyona eklenmeden yapılan satışlar eklenince operasyona, oradan merkeze geldi; iki katmanda da aynı fiş sayısı ve tutar.
  - Merkezin NATS'ı kapalıyken satış devam etti; açılınca satışlar 41 sn içinde merkeze geldi.
  - `store-remove`'dan sonra merkez mağazanın bağlantısını reddetti; gelmiş satışlar raporlarda kaldı; yeniden eklenince bağlandı.
  - Hesaplar: kapatılan hesap giremedi, açılınca girdi; kullanıcı adı büyük harfle de çalıştı.
  - Yedek alındı ve ayrı bir veritabanına geri yüklendi; satış, olay, kullanıcı ve vardiya sayıları birebir aynıydı.
  - `restore`: yedekten sonra 3 satış yapıldı. Geri yüklemeden hemen sonra rapor 15 fiş gösterdi, yeniden işlemeden 2 sn sonra 18. Geri yüklemeden önceki hal ayrıca yedeklendi.
  - Üç makine birden elektrik kesilmiş gibi durdurulup (SIGKILL) açıldı; bütün servisler kendiliğinden açıldı, veri kaybı olmadı.
