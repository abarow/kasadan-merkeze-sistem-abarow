# abarow — Saklama süreleri ve temizlik

> Tarih: 12.11.2026. [REVIEW.md](../REVIEW.md) D1 ve P3 bulgularının çözümüdür. Kod: `pkg/retention` ve her servisin `retention.go` dosyası.

## İlke

**Kayıt silinmez; kopya ve teknik iz silinir.**

- **Kayıt:** mağaza sunucusundaki ve merkezdeki satış defteri. Satışlar, satırlar, ödemeler, KDV dökümü, iptaller, yarım kalan satışlar ve vardiyalar bu temizliğin dışındadır. Yasal saklama süresi bu tablolar için ayrıca değerlendirilmelidir (örneğin VUK 5 yıl, TTK 10 yıl). Kod bu tablolardan hiçbir şey silmez.
- **Kopya:** kasanın satışları ve gün ayrıntıları. Kasa, mağaza sunucusu satışı veritabanına işlediğini makbuzla doğrulayana kadar tek kopyadır. Makbuz gelmeden hiçbir şey silinmez.
- **Teknik iz:** yayınlanmış olay kuyruğu satırları, eski makbuzlar ve merkezdeki ham olay içeriği.

Her servis yalnızca kendi şemasını temizler. Temizlik her gün 03:00'te (`ABAROW_TZ`, varsayılan Türkiye saati) çalışır (14.11.2026): kasada servisin tek bağlantısını tuttuğu için satışın olmadığı saatte. `ABAROW_RETENTION_INTERVAL` verilirse eskisi gibi servis açıldıktan 1 dakika sonra başlar ve o aralıkla çalışır. Silme 1000 (kasa) ya da 5000 (mağaza, merkez) satırlık kısa adımlarla yapılır. Kasada servisin tek bağlantısı olduğu için bir satış en fazla bir adım kadar bekler.

## Ne, nerede, ne kadar

| Katman | Ne silinir | Koşul | Varsayılan |
|---|---|---|---|
| Kasa (`sales`, `staff`) | Yayınlanmış olay kuyruğu satırı | Yayından bu yana süre geçti **ve** mağaza sunucusunun makbuzu kayıtlı | 7 gün |
| Kasa (`sales`) | Bitmiş satış (ödenmiş ya da iptal) ile satırları ve ödemeleri | Son değişikliğinden bu yana süre geçti **ve** o tarihten eski, makbuzu gelmemiş tek bir olay yok | 90 gün |
| Kasa (`staff`) | Kapanmış günün satış, iptal, kasa hareketi ve çekmece açılışı ayrıntıları; süresi geçmiş müdür onayları | Z'den bu yana süre geçti **ve** aynı makbuz koşulu | 90 gün |
| Kasa, mağaza | Alınan makbuz | Alındığından bu yana süre geçti | 180 gün |
| Mağaza, merkez | Verilen makbuz | Verildiğinden bu yana süre geçti | 180 gün |
| Mağaza, merkez (katalog, stok) | Yayınlanmış olay kuyruğu satırı | Yayından bu yana süre geçti | 7 gün |
| Merkez (`ingest`) | Ham olay içeriği (`events.payload`) | Alındığından bu yana süre geçti. Satır kalır: kimliği tekrar gelen olayı ayıklamaya devam eder. | 90 gün |

Süreler ortam değişkeniyle değişir. Değer `90d`, `36h`, `2d12h` biçimindedir. Geçersiz bir değerde servis açılmaz; yanlış ayarla sessizce silmesi daha kötü olurdu.

| Değişken | Varsayılan | Asgari |
|---|---|---|
| `ABAROW_RETENTION_AT` | 03:00 | `SS:DD`; `ABAROW_RETENTION_INTERVAL` ile birlikte verilmez |
| `ABAROW_RETENTION_INTERVAL` | (verilmez; turlar günlük) | 1 dakika |
| `ABAROW_OUTBOX_KEEP` | 7d | 1 saat |
| `ABAROW_REGISTER_KEEP` | 90d | 1 gün |
| `ABAROW_RECEIPTS_KEEP` | 180d | Akış temizleyicisinin süresi (`ABAROW_CLEAN_MAX_AGE`, kasada 30, mağazada 90 gün) + 7 gün |
| `ABAROW_EVENT_PAYLOAD_KEEP` | 90d | 1 gün |

## Neden böyle

- **Kasada makbuz şartı.** Olay kimlikleri rastgeledir; bir satışı makbuzuna doğrudan bağlayan bir sütun yoktur. Bunun yerine şöyle bir zincir kurulur:
  1. Kasa, olay kuyruğu satırını yalnızca makbuzu geldiyse siler.
  2. Kuyrukta kalan eski bir satır, mağaza sunucusunun henüz doğrulamadığı bir olay demektir.
  3. Kesim tarihinden eski böyle bir satır varsa o turda satış ve gün ayrıntısı silinmez. Günlüğe bir uyarı yazılır: "retention: kept … has no receipt".
- **Mağaza sunucusu yoksa** (`ABAROW_RECEIPT_SOURCE` boş) kasada temizlik hiç başlamaz. Kasa tek kopyadır.
- **Kasadaki eski Z ve vardiya raporları.** Kasa geçmiş bir Z raporunu satış ayrıntılarından yeniden hesaplar. Ayrıntılar silinince rapor sıfır görünürdü. Bunun yerine dönem ve vardiya satırları kalır ve `details_removed_at` ile işaretlenir. Rapor istenince "ayrıntılar kasadan silindi; mağaza sunucusunun raporlarına bakın" hatası döner (`FailedPrecondition`). Gün sonu açık vardiyayla alınamadığı için her vardiya tek bir Z dönemi içinde kalır, bu yüzden temizlik gün gün yapılır.
- **Makbuz süresi akış temizleyicisinden uzundur.** Kasa ve mağaza, akışlarındaki eski bir olayı ancak makbuzu kayıtlıysa siler. Makbuz bundan önce silinirse olay akışta kalır. Servis bu durumu açılışta reddeder.
- **Verilen makbuz.** Gönderenin akışında hâlâ duran bir olay tekrar gelirse aynı makbuz verilir. 180 günden eski bir olay tekrar gelirse yeni bir makbuz kimliği verilir. Gönderen bunu da aynı şekilde kabul eder. Satış defteri satışı, iptali ve vardiyayı kendi kimliğiyle bir kez yazar, merkezde ayrıca `events` satırı kalır; satış iki kez yazılmaz.
- **Ham olay içeriği.** Merkez her olayın işlenmiş hâlini satış defterinde tutar. Ham içerik yalnızca eski olayları yeniden işlemek için gerekirdi. `ingest replay` NATS akışını okur, bu tabloyu okumaz. 90 günden eski içerik `NULL` yapılır. Yalnız içeriği duran satırları tutan kısmi bir indeks (`events_payload_kept`) temizliğin tabloyu taramamasını sağlar.

## Bilinen sınırlar

- **Restoranda ödemesi süren adisyon.** Satış bulunamazsa restoran modülü bunu "ödeme hiç başlamadı" sayar ve tutarı adisyona geri koyar. Ödemesi süren adisyonlar masa planı her açıldığında sonuçlandırılır. Ödenmiş bir satışın 90 gün boyunca sonuçlandırılmadan kalması bu yüzden beklenmez, ama kodla engellenmiş değildir.
- **İlk temizlik uzun sürebilir.** Yıllarca birikmiş bir veritabanında ilk tur, kısa adımlarla da olsa dakikalar sürebilir. Merkezdeki `00003_event_payload.sql` migration'ı `events` tablosunda indeks kurar. Büyük bir tabloda bu, kurulum sırasında yazmaları birkaç dakika bekletir. Karar anında sahada kurulum yoktu.
- **Disk hemen küçülmez.** PostgreSQL boşalan yeri yeni satırlar için yeniden kullanır, işletim sistemine geri vermez.
- **Merkezde satış tablolarının aylık bölümlenmesi (partition) yapılmadı.** Kayıt silinmediği için bölümlemenin asıl kazancı olan eski bölümü düşürme burada kullanılmaz. Rapor süresine etkisi ölçülmeden eklenmeyecek.
