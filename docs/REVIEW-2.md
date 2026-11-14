# abarow — İkinci inceleme (14.11.2026)

> Kod `9a1afd9` üzerinde okundu ve araçlarla tarandı; hiçbir şey değiştirilmedi. İlk incelemeden ([REVIEW.md](REVIEW.md), `35d20a5`) bu yana gelen 21 commit (307 dosya, ~7.250 satır) ağırlıkla okundu: saklama süreleri, görünüm sözleşmesi, toplu `ingest`, gateway'in `staff` yedeği, metrikler, PITR ve bakım, üç makineli merkez (M3). Bakış açıları ilk incelemeyle aynıdır. Numaralar oradan devam eder (G8, D9, B5, M5, P6...).
>
> Kullanılan araçlar:
> - `staticcheck` ve `govulncheck` (bütün modüller),
> - `gosec` (yüksek dereceli bulgular),
> - elle okuma,
> - bir Docker denemesi (D9).
>
> "Denenmedi" yazan bulgular yalnız kod okumasına dayanır.

## Özet

| # | Bakış | Önem | Bulgu |
|---|---|---|---|
| D9 | DBA | **Yüksek** | Merkezde çalışan kopyanın makinesi kapanırsa kilidi birincilde ~2 saat kalıyor; `ingest`, `catalog-admin` ve `inventory` duruyor (denendi) |
| D10 | DBA | Orta | Kümede makine değiştirme yolu yok; diski değişen makinenin etcd'si kümeye dönemez (denenmedi) |
| D11 | DBA | Orta | Servisler veritabanına süper kullanıcıyla bağlanıyor; kümede 5432 bütün ağa açık |
| M5 | Mikroservis | Orta | NATS kümesi ve leaf portu container içi adresleri duyuruyor (denenmedi) |
| M6 | Mikroservis | Orta | CI 8 committir çalışmıyor; M3'ün bütün testleri GitHub'da hiç koşmadı |
| M7 | Mikroservis | Düşük | Mağaza listesi üç makinede ayrı tutuluyor, aynı olduğunu denetleyen yok |
| D12 | DBA | Düşük | Patroni container'ının bellek tavanı Patroni sürecini saymıyor (ölçülmedi) |
| P6 | Performans | Düşük | Üç makine her gece birincilden aynı `pg_dump`'ı alıyor |
| P7 | Performans | Düşük | Raporlar birincile gidiyor, iki senkron replika okumada boşta |
| G8 | Go | Düşük | Adlandırma kuralı kalıntıları: `nats.conf` yorumları ve `ingest` hata metinleri |

## Çözüm durumu

Bulgular aşağıda yazıldıkları gibi duruyor; yapılanlar bu tabloda izleniyor.

| # | Durum | Ne yapıldı |
|---|---|---|
| D9 | Çözüldü (14.11.2026) | Kilit bağlantısı `idle_session_timeout` koyuyor (varsayılan 5 × denetim aralığı = 10 sn, `pkg/leader`); susan kopyanın oturumunu PostgreSQL kapatıyor, kilit diğer kopyaya geçiyor. Kümede sunucu genelinde `idle_session_timeout=15min` (`pkg/pgtune.ForHeadquartersCluster`): kaybolan makinenin havuz bağlantıları 15 dk'da kapanıyor; havuzlar boştaki bağlantıyı 5 dk'da kendisi kapatır. Test: bağlantıları kapatmadan susan bir vekil arkasındaki kopya; diğeri devralıyor, ikisi aynı anda çalışmıyor. Ayar olmadan aynı test başarısız oluyor. `ha-db-test.sh` ayarın Patroni kümesinde geçerli olduğunu denetliyor. Rootless Podman'da (pasta) gerçek makinede denenmedi; ayar ağ durumuna değil boşta geçen süreye baktığı için orada da geçerli olmalı. |
| D10 | Çözüldü (14.11.2026) | `headquarters-install.sh ... --cluster-import <dosya> --rejoin`: makinenin eski etcd kaydı diğer iki makineden silinip yeniden ekleniyor, etcd'si mevcut kümenin üyesi olarak boş başlıyor (`ETCD_INITIAL_CLUSTER_STATE=existing`); Patroni veritabanını birincilden kopyalıyor ([deploy/quadlet/README.md](../deploy/quadlet/README.md) "Makine değiştirme"). `ha-db-test.sh` 3. aşama: 3. makinenin etcd'si ve veritabanı silinip boş başlatılıyor; `--rejoin` olmadan etcd 20 sn içinde kümeye dönmüyor, kurulumun aynı fonksiyonuyla dönüyor ve `hq-3` replika olarak akıyor. Gerçek makinede denenmedi. |
| D11 | Kısmen (14.11.2026) | Rol ayrımı şimdilik yapılmıyor (karar 14.11.2026): servisler süper kullanıcıyla bağlanmayı sürdürüyor. Kümede kurulum özeti portların yalnız üç makineye açılması gerektiğini söylüyor; güvenlik duvarına dokunmuyor. [deploy/quadlet/README.md](../deploy/quadlet/README.md)'de nftables örnek kuralı var; gerçek makinede denenmedi. |
| M5 | Çözüldü (14.11.2026) | `natsauth -routes` ile `cluster {}` ve `leafnodes {}` bloklarında `no_advertise: true`; tek makineli merkez ve mağaza değişmedi. NATS küme testi (üç merkez sunucusu ve bir mağaza, stream lideri kapatılıyor) bu ayarla geçti. Ayrı ağlarda (container adreslerine ulaşılamayan makineler) denenmedi. |

İlk incelemeden açık kalanlar:
- **B2 (kısmen):** satışın ödeme ve ÖKC'ye senkron bağı bilinçli olarak kaldı.
- **M1 (kısmen):** OpenTelemetry izi ve bir metrik toplayıcının kurulumu yok.
- **D8:** ana sürüm yükseltme betiği PostgreSQL 19 imajını bekliyor.
- **P4:** ölçüm planı yazıldı, ölçümler yapılmadı.
- **M3:** gerçek makinede tatbikat bekliyor.

## Araçların sonucu

- **`staticcheck`:** bulgu yok.
- **`govulncheck`:**
  - 9 modülde bilinen açık yok.
  - 11 modülde, kullanılan bir bağımlılıkta açık var, ama kod açığın olduğu fonksiyonu çağırmıyor.
- **`gosec`:** 46 yüksek dereceli bulgu elle kontrol edildi; hepsi yanlış alarm.
  - **Tamsayı dönüşümleri (G115):** miktar, fiyat ve satır sayıları `pkg/calc` sınırlarıyla dar tutuluyor. Menü sırası ve seçenek sayısı küçük.
  - **Dosya yolu (G703):** yollar ortam değişkeninden ya da izin listesinden geliyor; örneğin kanal adı yalnız `test`, `pilot` ya da `prod` olabiliyor (`services/update/internal/adapters/in/httpapi/update.go:44`).
  - **İstek sahteciliği (G704, SSRF):** gateway'in vekil adresi yapılandırmadan geliyor.
  - **Zayıf rastgele sayı (G404):** yalnız benchmark araçlarında.
- **Testler:** `scripts/check.sh` yerelde temiz. M3 testleri ayrıca yerelde geçti: `ha-db-test.sh` ve `hq-cluster-units-test.sh`. Bu testlerin GitHub'da hiç koşmadığı için bkz. M6.

**Genel durum iyi.**
- **İlk incelemenin bulguları:** yapılanlar testlerle birlikte geldi. Saklama işleri kısa adımlarla siliyor. Görünüm sözleşmesi şema değişikliğinde kırılıyor. Toplu `ingest` sorun çıkınca tek tek işlemeye dönüyor.
- **Üç makineli merkez:** senkron replika, katı mod ve TLS'le dikkatli kurulmuş. Docker testinde onaylı yazma kaybı olmadı.
- **Yeni bulgular:** neredeyse hepsi üç makineli merkezde, ve testlerin denemediği arıza türlerinde. Testler birincili öldürüyor ve kilit bağlantısını sonlandırıyor. Bir makinenin sessizce kaybolmasını, diskinin değişmesini ya da ayrı ağları denemiyor.

---

## 1. Go uzmanı

**Genel durum iyi.**
- **Yeni kodun yapısı:** yeni paketler (`pkg/leader`, `pkg/retention`, `pkg/metrics`, `pkg/natsio` toplu tüketici) küçük, belgeli ve testli.
- **`pkg/leader`:** bağlantıyı havuza geri vermiyor (`driver.ErrBadConn`). Bırakılmayan bir kilidin başka bir işe geçmesi böylece önleniyor.
- **Metrikler:** etiketleri sınırlı. HTTP'de yol yerine `ServeMux` deseni, gRPC'de yöntem adı kullanılıyor.

**G8 — Düşük: Adlandırma kuralı kalıntıları.**
- **`nats.conf` yorumları:** `tools/natsauth/main.go` içindeki şablon Türkçe yorumlar yazıyor (örneğin satır 475: "Merkez kümesi: makineler birbirine yalnızca TLS ile bağlanır"). Dosya yapılandırma, belge değil.
- **`ingest` hata metinleri:** `services/headquarters/ingest/internal/app/service.go` doğrulamasında dört hata metni Türkçe başlıyor ("olay %q", "iptal %q", "vardiya", "kasiyer"). Yanlarındakiler İngilizce ("sale %q", "store price"). Bu metinler kullanıcıya değil, günlüğe gidiyor.

---

## 2. Veritabanı yöneticisi (DBA)

**Genel durum iyi.**
- **Saklama işleri:** 1000 satırlık adımlarla siliyor. Kasa, satışı ancak mağazaya giden her olayın makbuzu gelince siliyor.
- **Arşiv:** `archive_mode=always` sayesinde birincil değişse de her makinede kesintisiz bir WAL arşivi kalıyor.
- **Migration'lar:** migration denetimi değişen, silinen ve yeniden adlandırılan dosyayı yakalıyor.

**D9 — Yüksek: Makinesi kapanan kopyanın kilidi birincilde ~2 saat kalıyor (denendi).**
- **Senaryo:** merkez üç makinede. `ingest`, `catalog-admin` ve `inventory`'nin çalışan kopyası, kilidi birincil olmayan bir makinede tutuyor. Bu makine elektrikten gidiyor ya da ağdan kopuyor. Bağlantının öbür ucuna ne FIN ne RST gidiyor.
  - Bağlantı yalnız kısa bir ağ kesintisinde de kopabilir. Makinenin HAProxy'si üç başarısız denetimden sonra (~6–9 sn) birincile giden oturumları keser, ama kesme paketi birincile ulaşmayabilir (denenmedi).
- **Sonuç:**
  - Birincildeki oturum boşta istemciyi beklemeye devam ediyor ve kilit (`pg_try_advisory_lock`, `pkg/leader/leader.go:106`) onda kalıyor. PostgreSQL'de TCP canlılık ayarı yok (`tcp_keepalives_*`, `deploy/container/patroni/entrypoint.sh`). Linux varsayılanıyla ölü bağlantı ancak ~2 sa 11 dk sonra fark ediliyor.
  - Bu süre boyunca diğer iki makinedeki kopyalar kilidi alamıyor:
    - Mağazaların satışları merkeze yazılmıyor ve raporlar eskiyor. Veri kaybolmuyor, olaylar akışta bekliyor.
    - Katalog değişiklikleri mağazalara gitmiyor.
    - Merkez stoku güncellenmiyor.
  - Aynı makinenin havuzdaki açık bağlantıları da birincilde yer tutuyor (`max_connections`). Makine dönüp yeniden bağlanınca bütçe aşılabilir.
- **Deneme:** Docker'da bir istemci kilidi aldı ve `pkg/leader` gibi her 2 sn'de bir sorgu yaptı; sonra ağı koparıldı (`docker network disconnect`).
  - **Ayar yokken:** 93 sn sonra kilit hâlâ tutuluyordu.
  - **İstemcinin oturumunda `SET idle_session_timeout = '10s'` varken:** kilit 11 sn'de serbest kaldı.
- **Testlerin görmediği:**
  - `TestOneCopyWorks` kilit bağlantısını `pg_terminate_backend` ile sonlandırıyor; bağlantı temiz kapanıyor.
  - `ha-db-test.sh` birincili öldürüyor; kilitler onunla birlikte gidiyor.
- **Rootless Podman'da ek risk (denenmedi):** dışarıdan gelen bağlantının PostgreSQL tarafındaki ucu muhtemelen aynı makinedeki `pasta` sürecidir. O zaman TCP canlılık ayarı da ölü makineyi göremeyebilir. Bağlantının ağ durumuna bakmayan, sunucu tarafında zamana bağlı bir ayar daha güvenli.
- **Öneri:**
  - **Kilit bağlantısı:** `pkg/leader` oturumuna kısa bir `idle_session_timeout` (örneğin 10 sn; sağlıklı kopya her 2 sn'de sorgu yaptığı için boşta kalmaz).
  - **Havuz bağlantıları:** merkezde sunucu genelinde, havuzun boşta kapatma süresinden (5 dk, `pkg/pgpool`) uzun bir `idle_session_timeout`.
  - **TCP:** canlılık ayarları ayrıca açılabilir.
  - **Test:** `ha-db-test.sh`'e kilidi tutan kopyanın ağını koparan bir aşama.

**D10 — Orta: Kümede makine değiştirme yolu yok (denenmedi).**
- **Neden:** etcd birimi her zaman `ETCD_INITIAL_CLUSTER_STATE=new` ile başlıyor (`deploy/quadlet/headquarters/cluster/abarow-etcd.container`).
- **Senaryo:** diski değişen ya da yeniden kurulan bir makine `--cluster-import` ile kuruluyor. etcd'si boş veri klasörüyle "yeni küme" olarak açılıyor. Diğer iki üye bu üyeyi eski kayıt geçmişiyle tanıyor. etcd böyle bir üyeyi kabul etmez ya da çöker. Bu bilinen bir durumdur (log aralık dışında, "tocommit out of range").
- **Sonuç:**
  - Makine kümeye dönemiyor ve küme iki makineyle kalıyor; bir arıza daha veritabanını durdurur.
  - Patroni boş makineyi kendisi kopyalıyor, ama ona etcd gerekiyor.
- **Eksik yordam:** gerekli olan `etcdctl member remove` ve `member add`, sonra `existing` durumuyla başlatma. Bu yordam ne [deploy/quadlet/README.md](../deploy/quadlet/README.md) "Üç makinede merkez" bölümünde ne [HA-HEADQUARTERS.md](decisions/HA-HEADQUARTERS.md)'de var.
- **Öneri:** makine değiştirme yordamı (betik ya da belge) ve bunu deneyen bir Docker testi (bir makinenin birimlerini silip yeniden kurmak).

**D11 — Orta: Servisler veritabanına süper kullanıcıyla bağlanıyor.**
- **Durum:**
  - Üç katmanda da `POSTGRES_USER=abarow` süper kullanıcıdır.
  - Kümede `abarow` aynı zamanda Patroni'nin süper kullanıcısıdır (`entrypoint.sh`, `authentication.superuser`).
  - Kasada gateway'in arkasındaki servisler, merkezde ekranı sunan `reporting` dahil bütün servisler bu hesapla bağlanıyor.
- **Sonuç:**
  - **Sözleşme:** görünüm sözleşmesi (B1) yalnız bir kural. Bir servisin başka bir servisin tablosunu okumasını ya da yazmasını veritabanı engellemiyor.
  - **Açık durumunda:** bir SQL enjeksiyonu ya da ele geçirilmiş bir servis her şeye ulaşır. Buna `ALTER SYSTEM` ve `COPY ... TO PROGRAM` (veritabanı container'ında komut çalıştırma) dahildir.
- **Kümede ek durum:**
  - 5432 her makinede bütün ağ arayüzlerine yayımlanıyor (`abarow-patroni.container`). Belge "yalnız diğer iki makineye açın" diyor, ama kurulum güvenlik duvarı kurmuyor.
  - Aynı ağdan bu süper kullanıcı parolayla bağlanabilir (TLS ve `scram-sha-256` var).
  - Patroni'nin okuma uçları (8008) parolasız; küme durumunu gösteriyor.
  - etcd (2379, 2380) ve NATS kümesi (6222) istemci sertifikası istiyor; onlar güvende.
- **Öneri:**
  - **Rol ayrımı:** servis başına bir rol. Her servis kendi şemasının sahibi olur, `reporting` yalnız görünümleri okur. Süper kullanıcı yalnız Patroni ve bakım için kalır.
  - **Güvenlik duvarı:** kurulum küme portlarını yalnız diğer iki makineye açan bir güvenlik duvarı kuralı yazar, ya da bunu açıkça kullanıcıya bırakıp kurulum özetinde söyler.

**D12 — Düşük: Patroni container'ının bellek tavanı Patroni'yi saymıyor (ölçülmedi).**
- **Durum:** `reporting pgtune --machines 3` tavanı PostgreSQL için hesaplıyor (`MEMORY_MB`). Aynı container'da Patroni'nin Python süreci de çalışıyor; tahminen birkaç on MB.
- **Risk:** yük altında tavana yaklaşan bir makinede bellek yetmezliğinden öldürülen (OOM) Patroni gereksiz bir yük devrine yol açar.
- **Öneri:** Patroni'nin belleğini ölçmek, tavana ayrı bir pay eklemek.

---

## 3. Zayıf bağlı uygulama uzmanı

**Genel durum iyi.**
- **Gateway:** `staff` kesintisinde son geçerli cevabı yalnız ulaşılamama durumunda kullanıyor. Kesin bir "oturum geçersiz" cevabı saklanan cevabı hemen siliyor; çıkış gateway'de hemen geçerli.
- **Toplu `ingest`:** tek bir hatalı olayda toplu yazmayı bırakıp her olayı eskisi gibi tek tek işliyor, olayın kaderi değişmiyor.
- **Görünüm sözleşmesi:** `ingest_v1` ve `catalog_v1` sahip servisin migration'ında kırılıyor; bağlılık görünür oldu.

Bu bakışta yeni bulgu yok. Kümede mağazaların merkeze bağlanmasını etkileyen sorun M5'te.

---

## 4. Mikroservis mimarı

**Genel durum iyi.**
- **Ölçüm ve izlenebilirlik:** her servis `/metrics` ve `/version` sunuyor; istek kimliği HTTP'den gRPC'ye taşınıyor.
- **Uyumluluk testi:** önceki sürüm `sales` ve `staff` yeni servislerle CI'da deneniyor.
- **Kümede:** bir işin tek kopyası kilitle sağlanıyor. NATS akışları üç kopya.

**M5 — Orta: NATS adres duyurusu kapalı değil (denenmedi).**
- **Durum:** `natsauth`'un yazdığı `cluster {}` ve `leafnodes {}` bloklarında `no_advertise` yok (`tools/natsauth/main.go:474-498`).
- **NATS'ın davranışı:**
  - Kümedeki sunucular birbirlerine ve bağlananlara kendi adreslerini duyurur.
  - Container'ın içinde dinleyen sunucunun bildiği adres container ağının adresidir (rootless Podman'da 10.89.x.x gibi). Bu adrese diğer makinelerden ve mağazalardan ulaşılamaz.
- **Sonuç:**
  - **Mağazalar:** leaf bağlantısı duyurulan adresleri yeniden bağlanma listesine ekler. Merkezde bir makine gidince önce bu ulaşılamayan adresleri deneyip zaman kaybedebilir.
  - **Servisler:** yerel NATS yeniden başlarken diğer makinelerin container adreslerini dener.
  - **Günlükler:** bağlanamama kayıtlarıyla dolar.
- **Testlerin görmediği:** Docker testinde bütün container'lar tek ağda olduğu için bu adreslere ulaşılabiliyor.
- **Öneri:** iki blokta da `no_advertise: true`. Küme yolları üç makineyi zaten açıkça listeliyor; mağazalara üç adres ya da `--vip` veriliyor.

**M6 — Orta: CI 8 committir çalışmıyor.**
- **Durum:** son başarılı CI koşusu `f6f4131` (12.11.2026). Sonraki 8 commit'in hepsi GitHub'ın ödeme sınırı yüzünden başarısız; işler hiç başlamıyor.
- **Hiç koşmayanlar:** M3'ün bütün testleri GitHub'da hiç çalışmadı. Bunlar `ha-db-test.sh`, `hq-cluster-units-test.sh` ve yayın akışına eklenen Patroni imajının arm64 derlemesi (QEMU). Yalnız Windows'ta Docker Desktop'la denendiler.
- **Belirsiz:** Ubuntu çalıştırıcısında süreler (Go işi 25 dk sınırlı), Docker sürümü ve port davranışı bilinmiyor.
- **Bağlı akış:** terfi kapısı yeşil CI istediği için yayın da duruyor.
- **Öneri:** ödeme sorunu giderilince son commit'i çalıştırmak ve sonuçlarına göre düzeltmek.

**M7 — Düşük: Mağaza listesi üç makinede ayrı tutuluyor.**
- **Durum:** `store-add` ve `store-remove` üç makinede ayrı ayrı çalıştırılmalı. Her makinenin NATS'ı kendi `stores.json`'una göre mağaza kabul ediyor. Listelerin aynı olduğunu denetleyen bir şey yok.
- **Sonuç:** bir makinede unutulursa o makine mağazayı reddeder.
  - **`--vip` ile:** adres o makinedeyken mağaza hiç bağlanamaz.
  - **Üç adresle:** mağaza diğer makinelere bağlanana kadar bekler.

  İkisinde de ekranda bir uyarı yok.
- **Öneri:** kurulumun ya da ekranın üç makinenin listesini karşılaştırması, ya da listenin ortak bir yerde (veritabanı) tutulması.

---

## 5. Performanslı, hafif ve ölçeklenebilir yazılım uzmanı

**Genel durum iyi.**
- **Toplu `ingest`:** kapasiteyi iki katından fazla artırdı (260–280 satış/sn); gerçekçi yük bunun ~%2'si.
- **Kümede bağlantı bütçesi:** makine sayısıyla büyüyor.
- **Bakım ve yedek işleri:** replikada ağır iş yapmıyor.

**P6 — Düşük: Üç makine her gece birincilden aynı `pg_dump`'ı alıyor.**
- **Durum:** günlük yedek her makinede 03:30'da, en çok 15 dk rastgele gecikmeyle başlıyor. Üçü de HAProxy üzerinden birincilden okuyor.
- **Sonuç:** büyük bir merkezde (50 mağazada yılda ~25 GB satış verisi) üç tam döküm aynı saatlerde birincilin diskini ve işlemcisini kullanıyor ve diskte üç kat yer tutuyor.
- **Öneri:** dökümü tek makinenin alması, ya da her makinenin kendi replikasından alması. Replikadan almanın bedeli, replikasyonla çakışan uzun sorgular için ayar (`max_standby_streaming_delay` ya da `hot_standby_feedback`).

**P7 — Düşük: Replikalar okumada boşta.**
- **Durum:** üç makinenin `reporting`'i de birincile bağlanıyor. İki senkron replika yalnız yazmayı kopyalıyor.
- **Ne zaman önemli:** P1'de rapor süresini aynı anda açık ekran sayısı belirliyordu. Ekran sayısı artarsa raporlar replikalardan okunabilir. HAProxy'de `/replica` denetimli ikinci bir dinleyici yeter. Senkron replikada gecikme commit anına çok yakındır.
- **Bugün:** 1–3 ekranla gerekmiyor; yalnız seçenek olarak not edildi.
