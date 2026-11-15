# Beko X30TR: hazırlık notu

29.10.2026 · Mehmet ERGULCU

Karar (29.10.2026): ilk gerçek ÖKC Beko X30TR; kart ve yemek kartı ödemesi ÖKC'nin içindeki POS'tan alınır. Geliştirme ÖKC simülatörüyle sürer; bu not cihaza geçerken yapılacakları toplar. Bilgiler herkese açık kaynaklardan derlendi. Protokolün büyük kısmı Token'ın herkese açık geliştirici portalında çıktı (aşağıda "Herkese açık belgeler"); bu notun ilk sürümündeki "belge açık değil" bilgisi 04.11.2026'da düzeltildi.

## Cihaz ve bağlantı

- X30TR, Token Finansal Teknolojiler'in (Beko markası) Android tabanlı yeni nesil ÖKC'sidir; içinde banka POS'u vardır (4G, Wi-Fi, Bluetooth).
- İki entegrasyon yolu var:
  1. **Uygulama cihazın içinde** (Token'ın "In-App Integration"ı): satış uygulaması X30TR'nin Android'inde çalışır. abarow'un yolu bu değil: abarow kasası ayrı bir Linux makinedir.
  2. **Harici satış uygulaması:** abarow kasası bu yoldan bağlanır. Cihazda Token'ın atadığı "TokenX Connect" uygulaması çalışır; bağlantı kablolu (cihazın solundaki USB-C) ya da kablosuzdur (Token Cloud). Token'ın belgesine göre harici satış uygulamaları X30TR ile "Token Serbest Protokolü" ile entegre olur. Bazı yazılım firmaları 300TR ve X30TR için GMP3 protokolünden söz ediyor.
- Kablolu protokolün belgesi kısmen açık (aşağıda "Herkese açık belgeler"); hat düzeyi ayrıntılar ve kütüphane lisansı açık değil. Token entegrasyonu "anlaşmalı firmalar"la yürütüyor (tokeninc.com/tokenx-connect).
- Kurulum bilgileri, eczane örneğinden (2025):
  - Entegrasyon cihaz başına yıllık ücretlidir (eczaneler için 2.999 TL, KDV dahil).
  - Token cihaza "Entegrasyon Onaylı Platform" yazılımını ve TokenX Connect'i atar.
  - Bir cihaz yalnızca bir bilgisayara bağlanır.
  - Cihazda geliştirici modu (ADB) kapalı olmalıdır.
  - Örnekteki eczane yazılımı Windows 10 istiyor. Linux kasayla kablolu bağlantının desteklenip desteklenmediği sorulmalı.

## Herkese açık belgeler (araştırma 04.11.2026)

Token'ın geliştirici portalında (developer.tokeninc.com, "X Platform" altında "Token X Connect Wire") kablolu entegrasyonun belgeleri herkese açık. Sayfaların Markdown halleri `.md` uzantısıyla alınabiliyor; dizin `.../token-developer-portal-1/llms.txt`.

**Bulunanlar**
- **Bağlantı:** USB üzerinden. X30TR'de ek sürücü gerekmiyor (libusb); 300TR'de FTDI sürücüsü gerekiyor. Protokolün adı "Token Integration Hub".
- **Linux desteği var:** SSS'ye göre Ubuntu 18.04+ ve GLIBC 2.27+, **yalnızca 64 bit**. Token'ın GitHub'ında (TokenPublication) Ubuntu için C++ (`IntegrationHub-CppSoLinux-64bit`) ve Java (`IntegrationHub-SoJava`) örnekleri var; .NET, Delphi ve Qt örnekleri de var.
- **Uygulama arayüzü (JSON):** sepet (`items`, `paymentItems`, `isVoid`, `documentType`...), ödeme, `getFiscalInfo`, satış durumu (`status`: 0 başarılı, -1 başarısız, 99 fiş iptal; `receiptNo`, `zNo`). Tutarlar kuruş, miktar 1000 ile çarpılmış (abarow'la aynı). Ödeme tipleri: 1 nakit, 3 kredi kartı, **7 yemek kartı**, 8 ödemesiz, 9 ikram... Kart ve yemek kartında `operatorId` ile belirli bir ödeme uygulamasına yönlendirilebiliyor.
- **Bağlantı kopması:** bağlantı yokken tamamlanan satışın fiş ve ödeme bilgisi bağlantı gelince geri döner; satış durumunu sorgulayan bir metot yok. Bağlantı olmadan ve mali bilgi alınmadan satışa başlanmamalı.
- **Kablolu hat güvenliği:** paketler TLV (2 bayt tür, 4 bayt uzunluk, sağlama toplamı), **sıkıştırılmış ve oturum anahtarıyla şifreli**; cihaz kimliği ECDSA ve cihaz sertifikasıyla doğrulanıyor.

**Bulunamayanlar**
- Hat düzeyi ayrıntılar (sıkıştırma ve şifre algoritması, anahtar türetme, bayt düzeni, mesaj etiketleri) belgelenmemiş. Hattı belgeye bakarak sıfırdan yazmak mümkün görünmüyor.
- Token'ın kütüphanesi (`libIntegrationHub.so`) **derlenmiş ikili**; GitHub depolarında lisans dosyası yok. Dağıtım ve sözleşme koşulları belli değil.
- İade akışı, X/Z raporu alma ve kağıt/kapak durumları belgede yok.
- Entegrasyon paketi satın alma ("Buy Integration Package") yalnızca Token'ın listelediği satış uygulamaları için görünüyor; listede olmayan uygulama için yolu Token'a sormak gerekiyor.

**abarow için sonuçlar**
- Mali adapter yazılabilir ama Token'ın kütüphanesi üzerinden: kasa servisleri `CGO_ENABLED=0` ile `scratch` imajında çalışıyor, ikili kütüphane ise glibc ve libusb ister. Bu yüzden adapter büyük olasılıkla ayrı bir küçük container olur (Token'ın C++ ya da Java örneğine dayanan, USB cihazı gören); `fiscal.v1` gRPC sözleşmesi ona karşı Go tarafında kalır.
- JSON yüzeyi `fiscal.v1`'e büyük ölçüde oturuyor. İade ve X/Z için belge eksik olduğundan simülatörde olduğu gibi yönetim özeti kasada kalır.
- Önce Token'a lisans ve dağıtım koşulunu sormak gerekiyor: ikiliyi açık kaynaklı depoda ve container imajında dağıtabilir miyiz?

## Adapter: `services/fiscal/beko` (yazıldı, cihazla denenmedi)

Herkese açık belgelere göre yazıldı (04.11.2026). Cihaz elde olmadığı için gerçek donanımla ve Token'ın gerçek kütüphanesiyle **denenmedi**; sınanan her şey sahte SDK'yla yapıldı (karar: donanım denenmeyecek, geliştirme simülasyonla sürer).

**Yapı.** Ayrı bir container (`deploy/container/beko.Containerfile`, adı `fiscal-beko`): ÖKC simülatörünün yerine, aynı `fiscal.v1` sözleşmesiyle konuşur; kasanın öbür servisleri (scratch image, CGO'suz) değişmez. İçinde iki parça vardır:
- **Go servisi** (`services/fiscal/beko`): `fiscal.v1`'i sunar. Fişi Token'ın sepet JSON'una çevirir, sonucu bekler, mali numarayı döner. Hexagonal: `domain` (sepet kurma, durum okuma), `app` (hazırlık kapısı, tek satış sırası, sonuç eşleme), `hubproc` (köprü süreci), `grpcserver`.
- **`hubd` köprüsü** (C++, `services/fiscal/beko/hub/hubd.cpp`, ~170 satır): Token'ın `libIntegrationHub.so`'sunu sarar. Kütüphanenin arayüzü `std::string` değer parametreli C++ imzaları olduğu için Go'dan doğrudan çağrılamaz; kütüphaneye dokunan her şey burada, geri kalanı Go'dadır. C++ seçildi (Java'dan hafif: JVM yok, tek küçük ikili). Go ile arasında stdin/stdout üzerinde düz çerçeveler (`<işlem> <kimlik> <uzunluk>
<gövde>`), C++ tarafında JSON kütüphanesi bile gerekmez. Cihaz ISO-8859-9 beklediği için sepeti göndermeden çevirir.

**Token'ın SDK'sı depoda yoktur** (lisansı belli değil): `services/fiscal/beko/sdk/token/` içine `IntegrationHubWrapper.h` ve `libIntegrationHub.so` konur, image o zaman derlenir. Yoksa derleme açık bir hatayla durur.

**Ayarlar:** `ABAROW_BEKO_SECTIONS` (KDV oranı:ÖKC bölüm no, ör. `1000:1,2000:2`; zorunlu), `ABAROW_BEKO_CASH_NET=1`, `ABAROW_BEKO_HELPER`, `ABAROW_BEKO_COMPANY`, `ABAROW_BEKO_TIMEOUT` (varsayılan 90 sn).

**Davranış (belgeye göre).** Köprü açılıp mali bilgi alınmadan satış gönderilmez (`Unavailable`; Token'ın şartı). Sepet kimliği satış kimliğidir; aynı satış tekrar gönderilirse ÖKC'nin bildirdiği sonuç kullanılır, fiş ikinci kez kesilmez. Sonuç süresinde gelmezse "belirsiz" hatası döner (fiş kesilmiş olabilir); sonuç sonradan gelirse bir sonraki denemede kullanılır. Kart reddi (`status -1`) `FailedPrecondition`, fiş iptali (`99`) `Aborted`. Köprü çökerse yeniden başlar, sağlık denetimi cihaz hazır olana kadar "sağlıksız" der.

**Sınananlar.** Go birim testleri (sepet kurma, durum, hazırlık kapısı, tekrar gönderim, red, geç sonuç, köprü yeniden başlatma, hata kodları) ve gerçek `hubd` ikilisinin sahte SDK'ya karşı uçtan uca testi (Linux; CI'da `make -C services/fiscal/beko/hub fake`): Go → C++ köprü → sahte kütüphane → geri çağrı → Go, ISO-8859-9 kodlaması dahil.

**Doğrulanmamış varsayımlar (cihazla ya da Token'dan teyit edilmeli)**
1. **Kart iki kez çekilmesin.** Beko kartı kendi POS'undan çeker; ödeme servisi de kartı çekerse müşteri iki kez öder. Bu yüzden `fiscal.v1`'de `Payment.collect` var: kart ve yemek kartı "ödeme henüz alınmadı, ÖKC kendi POS'undan alsın" diye işaretlenmeden gelirse adapter fişi reddeder (`InvalidArgument`). Sales'te `ABAROW_FISCAL_COLLECTS_CARDS=1` verilen kurulumda kartlı satış payment servisinden geçmez; kart doğrudan fişle birlikte ÖKC'ye gider (bkz. "abarow'da değişecekler"). Beko kasasında bu ayar açık olmalıdır.
2. **Nakitte para üstü:** alınan tutar mı (varsayılan), para üstü düşülmüş tutar mı (`CASH_NET`) gönderilmeli bilinmiyor.
3. **Bölüm numaraları:** `getFiscalInfo` çıktısındaki `sections` alanlarının adları belgede kısaltılmış; bölümler elle eşlenir (`ABAROW_BEKO_SECTIONS`).
4. **Belge türü:** örnekte X30TR için 9008, belgede satış için 0 geçiyor; varsayılan 0.
5. **Kuruş farkı:** ÖKC `fiyat × miktar`'ı kendi yuvarlamasıyla hesaplar; abarow'un kalem toplamıyla bir kuruş ayrışabilir.
6. **Geri çağrı verisinin kodlaması** (cihazdan gelen metinler UTF-8 mi, ISO-8859-9 mu) belgede yok; şimdilik olduğu gibi okunur.
7. **Linux:** kütüphane 64 bit Ubuntu 18.04+ ister; USB aygıt izni için ana makinede udev kuralı ya da `plugdev` üyeliği gerekir.

## Token'a sorulacaklar

1. abarow'un (açık kaynak, Linux kasa) anlaşmalı yazılım firması olma koşulları; test cihazı ya da cihaz simülatörü. `libIntegrationHub.so`'nun lisansı: açık kaynaklı depoda ve container imajında dağıtılabilir mi?
2. Hangi protokol: Token Integration Hub mı (herkese açık belgeler bunu anlatıyor), Token Serbest Protokolü ya da GMP3 ayrı mı? GMP3 için "YN ÖKC Harici Donanım ve Yazılım Haberleşme Protokolü (GMP-3)" başlıklı bir belgenin 07.12.2022'de bir mevzuat haber sitesine yüklendiği görülüyor; yayımlayanı ve metni doğrulanamadı, herkese açık bir kopyası bulunamadı.
3. Kablolu bağlantıda USB-C bilgisayara ne olarak görünür (seri port, ağ arayüzü, başka)? Linux'ta sürücü gerekir mi?
4. Kablosuz (Token Cloud) bağlantının bir API'si var mı? İnternet kesikken ne olur?
5. Bir satışın akışı:
   - fiş açma, satır, KDV oranı, ödeme, fiş kapatma
   - kartın reddedilmesi; nakit + kart birlikte ödeme
   - yemek kartı (Multinet, Pluxee, Ticket, Setcard)
   - iptal; bağlantı satışın ortasında koparsa fişin durumu nasıl öğrenilir
6. Mal iadesi: ÖKC'den e-Gider Pusulası destekleniyor mu (bkz. [goods-return-note.md](goods-return-note.md))?
7. Z ve X raporu ile kağıt bitti ve kapak açık gibi durumlar harici uygulamadan istenebilir ya da okunabilir mi?
8. Ücretler: cihaz başına yıllık entegrasyon bedeli, yazılım firması için ayrı bedel var mı?

## abarow'da değişecekler

- **Yeni mali adapter:** `services/fiscal/beko` yazıldı (yukarıda). `fiscal.v1` sözleşmesini uygular; kasa kurulumda simülatör ya da Beko'yu seçer. Simülatör geliştirme ve deneme için kalır.
- **Ödeme akışı (yapıldı, sales `ABAROW_FISCAL_COLLECTS_CARDS=1`):** Varsayılan akışta satış önce ödeme servisinden parayı alır, sonra ÖKC'ye fişi kaydettirir. Beko'da kart ve yemek kartı ÖKC'nin içinde çekilir; bu iki adım ÖKC'de tek işlem olur. Bu ayarla kartlı satış şöyle işler (nakit satış değişmez):
  - Nakitte para üstünü yine abarow hesaplar.
  - Kart ve yemek kartı tutarı fişle birlikte ÖKC'ye gider (`Payment.collect`); ÖKC kartı çeker, fişi kapatır. Ödeme ve fiş numarası tek işlemde kaydedilir.
  - Cihaz işlemi kesin olarak yapmadıysa (kart reddi, cihaz hazır değil, sepet kabul edilmedi: `FailedPrecondition`; fiş iptal: `Aborted`; geçersiz fiş: `InvalidArgument`) satış açık kalır, para alınmamıştır; kasiyer başka ödemeyle tamamlar.
  - Sonuç belirsizse (bağlantı koptu, süre doldu: `Unavailable`) kart çekilmiş olabilir. Satış "ödemesi alınmış, fişi bekliyor" (charged) olarak kaydedilir, operasyonda görünür; "fişi tamamla" aynı sepeti (satış kimliğiyle) yeniden gönderir. Token'ın belgesine göre aynı sepet için sonuç sonradan gelir; cihazın aynı sepeti ikinci kez çekmediği gerçek cihazda doğrulanmalıdır.
  - Sınır: böyle bir satışın ödemesi payment servisinde değil ÖKC'dedir; "ödemeyi geri ver" (RefundCharged) payment servisini çağırır ve bu satışta işe yaramaz. Kart iadesi ÖKC üzerinden yapılır (mal iadesi işi ayrı).
  - Reddin ve kopan bağlantının cihazdan nasıl bildirildiği Token'ın belgesine ve gerçek cihaza göre doğrulanacak.
- **Ayrı banka POS'u yazılmaz.** Ödeme servisindeki kart terminali simülatörü geliştirme için kalır.
- **Fiş ÖKC'den basılır.** Kasanın fiş yazıcısı yalnızca yönetim raporlarını (X, Z özeti, vardiya) basar.
- **Z raporu:** Resmi Z raporu ÖKC'nindir. abarow'un Z'si yönetim özeti olarak kalır; başlığında bu zaten yazıyor.
- **Yemek kartı:** ödeme türü olarak satışta, fişte ve Z'de ayrı satırdır. Simülatörde kart gibi alınır; Beko'da ÖKC'nin ödeme uygulamasından alınacak.

## Kaynaklar

- [Token, Beko ÖKC – Eczane Otomasyon Entegrasyonu Bilgilendirme Dokümanı V3.0 (20.2.2025)](https://entegrasyon.teb.org.tr/img/Beko_X30TR_EOS_Bilgilendirme.pdf)
- [Token geliştirici portalı, X30TR In-App Integration](https://developer.tokeninc.com/token-developer-portal-1/x-platform/in-app-integration)
- [Token, entegrasyon](https://www.tokeninc.com/en-us/token-integration/)
- [Akınsoft, Beko X30TR Token kablolu entegrasyonu](https://bilgibankasi.akinsoft.net/tr/home/makale/3950-beko-x30tr-token-kablolu-entegrasyonu)
- [Akınsoft, Beko ÖKC entegrasyonu (GMP-3)](https://www.akinsoft.com.tr/okc-entegrasyonu/beko)
- [SambaPOS, Beko 300TR ÖKC ile kablolu entegrasyon](https://kb.sambapos.com/6-32-beko-300tr-okc-ile-kablolu-entegrasyon-nasil-yapilir/)
- [BenimPOS, Beko X30 TR ve 300 TR GMP3 entegrasyonu](https://fiyatbu.com.tr/bdesk/knowledge-base/article/beko-x30-tr-ve-300-tr-yazarkasa-pos-gmp3-entegrasyonu-nasil-kullanilir)
