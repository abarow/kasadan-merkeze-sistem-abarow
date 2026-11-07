# Güvenlik politikası

abarow bir satış noktası yazılımıdır; para, kişi bilgisi ve mali kayıtlarla çalışır. Güvenlik açıklarını ciddiye alıyoruz.

## Açık bildirimi

**Açığı herkese açık issue, tartışma ya da PR olarak yazmayın.** Bunun yerine GitHub'ın özel bildirim yolunu kullanın: deponun **Security** sekmesi > **Report a vulnerability**. Bildirim yalnızca bakımcılara görünür.

Bildirimde şunlar işimizi hızlandırır: etkilenen bileşen ve sürüm, yeniden üretme adımları, etkisi (ne yapılabiliyor), varsa önerdiğiniz düzeltme. Gerçek müşteri verisi içeren kayıt göndermeyin.

## Ne bekleyebilirsiniz

- Bildirimi ilk **7 gün** içinde yanıtlamayı hedefleriz. Bu bir taahhüt ya da hizmet seviyesi sözü değildir; proje gönüllü emeğiyle yürür.
- Açık doğrulanırsa düzeltmeyi hazırlar, düzeltme yayınlandıktan sonra (ve sizinle koordineli olarak) duyururuz. Bildirimi yapan kişinin adı, istemezse yazılmaz.
- Düzeltme yayınlanana kadar ayrıntıyı herkese açık paylaşmamanızı rica ederiz.

## Kapsam

Kapsam içinde: bu depodaki kod, container imajları, kurulum betikleri ve güncelleme (imza ve kanal) mekanizması.

Kapsam dışında: sizin kendi sunucunuzdaki yapılandırma hataları, ÖKC simülatörünün mali işlevlerinin gerçek olmaması (bilerek böyledir, bkz. README "Mali mevzuat uyarısı"), hizmet reddi için aşırı kaynak gerektiren saldırılar, kullanılan üçüncü taraf yazılımların kendi açıkları (onlara bildirin).

## Desteklenen sürümler

Proje alfa aşamasındadır; yalnızca en son sürüm düzeltme alır.

## Bilinen noktalar

Tasarım gereği kabul edilen noktalar ve iç inceleme sonuçları [docs/PENTEST-PREP.md](docs/PENTEST-PREP.md) ve [docs/SECURITY-SCAN.md](docs/SECURITY-SCAN.md) dosyalarındadır. Orada yazılı olması bir noktanın bildirilmesini engellemez; etkisini ölçen bildirimler değerlidir.
