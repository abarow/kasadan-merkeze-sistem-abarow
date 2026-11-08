# abarow — Kesintisiz merkez

> Tarih: 12.11.2026, kararlar 13.11.2026. [REVIEW.md](../REVIEW.md) M3'ün ikinci seviyesi. Kararlar ve uygulama sırası aşağıdaki "Kararlar" bölümündedir.

## Bugünkü durum

Merkez tek makinedir: bir NATS düğümü, bir PostgreSQL, servislerin birer kopyası. Merkez giderse:

- **Satış kaybolmaz.** Kasalar ve mağaza sunucuları olayları biriktirir. Merkez dönünce olaylar sırayla gelir ve makbuzla doğrulanır.
- **Kaybedilen şey zamandır.** Merkez raporları ve merkezden yapılan katalog ve fiyat değişiklikleri merkez dönene kadar durur.
- **Bugünkü koruma (birinci seviye, 12.11.2026'da yapıldı):**
  - günlük `pg_dump`,
  - sürekli WAL arşivi ve haftalık fiziksel yedek (`restore-to`),
  - geri yüklemeden sonra 90 günlük akışın yeniden işlenmesi.

  Disk bozulursa makine dışındaki son yedekten dönülür. Geri dönme süresi saatler mertebesindedir ve elle yapılır.

## İkinci seviye: üç makine

Amaç: bir makine ya da bir servis giderse merkez dakikalar içinde, mümkünse kendiliğinden, çalışmayı sürdürsün.

| Parça | Bugün | Plan |
|---|---|---|
| NATS JetStream | 1 düğüm, akışlar tek kopya | 3 düğümlü küme, merkez akışları 3 kopya (R3). Bir düğüm giderse akış yazılmaya devam eder. Mağazaların leaf bağlantısı üç adresi de bilir. |
| PostgreSQL | 1 sunucu | Birincil + akış replikası (streaming replication). Replika aynı WAL'i anında alır. |
| Yük devri (veritabanı) | — | **Seçenek A:** elle; `headquarters-install.sh promote` replikayı birincil yapar, servisler yeni adrese bağlanır. **Seçenek B:** otomatik; Patroni ve etcd gerekir (üç makinede). |
| `reporting`, `catalog-admin`, `inventory` | 1'er kopya | Her makinede bir kopya, önünde Caddy. Durumları veritabanındadır, çoğaltılabilir. |
| `ingest` | 1 kopya | Her makinede bir kopya, ama aynı anda yalnız biri okur (aktif-pasif). Sıra numarası takibi ve satış defteri sıralı yazımı bekler; aynı durable tüketiciyi paylaşan iki okuyucu olayları karışık sırada işlerdi. Toplu yazma (12.11.2026, P2) tek okuyucunun kapasitesini artırır. |
| Adres | Makinenin adı | Sanal IP (keepalived) ya da DNS. Mağazalar ve ekran tek bir adres bilir. |

## Riskler ve bedel

- **Bölünmüş beyin:** ağ ikiye ayrılır ve iki taraf da kendini birincil sanırsa iki ayrı satış defteri oluşur. Otomatik yük devri bunu ancak bir uzlaşma servisiyle (etcd) önler. Elle yük devrinde bu karar operatördedir.
- **İşletme yükü:** üç makine, sertifikalar, güncellemenin üç makinede sırayla yapılması, yük devri tatbikatı.
- **Test:** CI'daki Docker simülasyonu tek makinelidir. Küme ve yük devri için ayrı bir test düzeneği gerekir.
- **Gerekli mi:** satış kaybolmadığı için kesintinin bedeli gecikmedir. Birkaç saatlik merkez kesintisi kabul ediliyorsa birinci seviye yeterli olabilir.

## Önerilen sıra (12.11.2026; yerini aşağıdaki "Uygulama sırası" aldı)

1. NATS 3 düğüm (en az risk, en büyük kazanç: olay kabulü durmaz).
2. PostgreSQL replikası ve elle yük devri komutu, tatbikat betiği.
3. Servislerin çoğaltılması ve Caddy.
4. (İsteğe bağlı) Patroni ile otomatik yük devri.

## Kararlar (13.11.2026)

Mehmet ERGULCU: ikinci seviye yapılacak, yük devri **otomatik** (seçenek B).

- **Üç makine.** Otomatik yük devrinde kimin birincil olacağına çoğunluk karar verir (etcd). İki makineyle bir makine gidince çoğunluk kalmaz; bölünmüş beyni önlemek için en az üç gerekir. İki makine aynı anda giderse kalan makine yazmayı durdurur: iki ayrı satış defteri oluşmasındansa gecikme seçilir (satışlar mağazada bekler).
- **Senkron replikasyon.** `ingest`, olayı PostgreSQL'e yazdıktan sonra mağazaya makbuz gönderir; makbuzu alan mağaza kendi kopyasını siler. Replikasyon asenkron olsaydı birincil, commit'ten hemen sonra ve replikaya göndermeden çökerse olay yeni birincilde olmazdı, mağazadaki kopya da silinmiş olurdu: yasal kayıt kaybı. Bu yüzden commit en az bir replikaya yazılmadan tamamlanmaz (Patroni `synchronous_mode_strict`). Bedeli: iki replika birden giderse yazma durur.
- **Veritabanı adresi: her makinede HAProxy** (Mehmet ERGULCU, 13.11.2026). Servisler tek adres bilir: kendi makinelerindeki HAProxy. HAProxy her 2 saniyede Patroni'ye sorar (`GET /primary` yalnız birincilde 200 döner) ve bağlantıyı birincile gönderir; birincil değişince eskisine açık bağlantıları keser, servis eski makinede asılı kalmaz. Her makinenin kendi HAProxy'si olduğu için HAProxy tek arıza noktası değildir: makine giderse HAProxy'si de o makinenin servisleriyle birlikte gider.
- **Tek çalışan kopya.** Her makinede bir `ingest` çalışır; birincil veritabanında danışma kilidini (advisory lock) alan okur, diğerleri bekler. Kilidi tutan süreç ya da bağlantısı giderse kilit kendiliğinden düşer. Aynı kural outbox yayınlayan ve dayanıklı tüketici okuyan `catalog-admin` ve `inventory` için de geçerlidir: outbox yayıncısı iki kopyada aynı anda çalışmaya uygun değildir.
- **Ekran ve mağaza adresi.** Her makinede Caddy; üç makine aynı yerel ağdaysa ekran ve mağazaların NATS bağlantısı tek adresle, sanal IP ile (keepalived) verilir. Makineler ayrı veri merkezlerindeyse sanal IP çalışmaz; o zaman üç kayıtlı tek DNS adı ya da mağazada üç adres (`natsauth -leaf` virgüllü) kullanılır.

### Uygulama sırası

1. **NATS kümesi (13.11.2026'da yapıldı):** `natsauth -routes` ve `-domain`; düğümler arası karşılıklı TLS (ortak CA); merkezin stream'leri `ABAROW_NATS_REPLICAS=3` ile üç kopya; mağazanın leaf bağlantısı üç adresi bilir (`-leaf` virgüllü). Test: üç düğüm ve bir mağaza; stream'in lideri kapatılırken olaylar kayıpsız ve tekrarsız geliyor (`tools/natsauth/cluster_test.go`).
2. **PostgreSQL (13.11.2026'da yapıldı):** Patroni 4.1 ve etcd 3.6, senkron ve katı (strict) replika, her makinede HAProxy (`deploy/container/patroni.Containerfile`, `deploy/compose/ha-db.yaml`). İmaj tek makineli merkezle aynı PostgreSQL'dir (postgres:18-alpine). Veritabanı ayarları (`reporting pgtune`) küme kurulurken ortak ayara yazılır; sonraki değişiklik `patronictl edit-config` ile yapılır. Docker testi (`scripts/ha-db-test.sh`, CI'da):
   - Servis gibi HAProxy üzerinden yazılırken birincil öldürülüyor: senkron replika devralıyor, yazmalar en çok ~32 sn duruyor (Patroni'nin lider süresi 30 sn), onaylanan 2.499 ve 2.509 yazmanın (iki deneme) hiçbiri kaybolmuyor. Eski birincil dönünce replika olarak katılıyor.
   - İki replika durdurulunca birincil yazmayı tamamlamıyor; replikalar dönünce yazmalar sürüyor.
   - Bilinen davranış: replika beklenirken iptal edilen bir yazma (bağlantı kopması, zaman aşımı) birincilde yerel olarak kalabilir ve replikalar dönünce onlara da gider. İstemci bunu hata olarak görür; `ingest` olayı yeniden işler ve var olan olayı atlar, makbuz yalnızca başarılı commit'ten sonra gider.
3. **Tek çalışan kopya (13.11.2026'da yapıldı):** `pkg/leader`. Servisin kendi bağlantı havuzundan bir bağlantıda danışma kilidi tutulur (yeni bağlantı açılmaz; mağaza sunucusunun 20 bağlantılık bütçesi değişmez). Kilidi tutan kopya her 2 sn'de kilidin hâlâ kendisinde olduğuna bakar; kaybederse işini durdurur. Kilidi yeni alan kopya işe başlamadan 10 sn bekler: kilit kesin bir çit değildir, eski sahibin durmasına süre bırakılır. Kilit bırakılırken bağlantı havuza dönmez, kapatılır. Kapsam: merkezde `ingest` (tüketici, makbuz outbox'ı, mağaza bağlantı durumu, saklama), `catalog-admin` (outbox, saklama) ve `inventory` (tüketiciler, outbox, saklama); ekran ve HTTP istekleri her kopyada karşılanır. Mağaza sunucusunda (tek makine) kilit kullanılmaz. Test: iki kopya, çalışanın kilit bağlantısı kesiliyor, diğeri devralıyor, hiçbir anda ikisi birden çalışmıyor (`pkg/leader`, 10 tekrar).
4. **Makineler arası TLS (13.11.2026'da yapıldı):** her makinenin sertifikası, NATS için zaten ürettiği merkez CA'sının imzaladığı sertifikadır (`natsauth -role headquarters`; sunucu ve istemci kullanımı). etcd istemci ve eş bağlantıları karşılıklı TLS; Patroni'nin REST API'si HTTPS, değiştiren uçları (yeniden başlatma, devir) parolalı, okuma parolasız; HAProxy `/primary` sorusunu HTTPS ile sorar ve sertifikayı CA ile doğrular; replikasyon ve servis bağlantıları `sslmode=verify-ca`, `pg_hba` yalnız `hostssl` kabul eder. `archive_mode=always`: replikalar da aldıkları WAL'i arşivler, birincil değişince her makinede kesintisiz arşiv kalır. Docker testi bunları da sınar: servis bağlantısı ve iki replikasyon TLS, şifresiz bağlantı reddediliyor; yük devri TLS ile de ~31 sn, 2.542 onaylı yazmadan kayıp yok.
5. Kurulum betiği: Caddy ve keepalived (sanal IP); bağlantı bütçesi: üç makinenin servis havuzları birincile bağlanır, merkezin `max_connections` değeri (30) makine sayısıyla büyür (`pgtune`); ilk makinede anahtarların ve CA'nın dışa aktarılması, diğer makinelerde içe alınması; mağaza ekleme üç makinede; Patroni, etcd ve HAProxy için Quadlet birimleri; yedek ve bakım betiklerinin birincile göre çalışması.
6. Tatbikat: üç makineli Docker düzeneğinde her parçanın sırayla kapatılması.

Bu adımlar bitene kadar kurulum betiği tek makinelidir.
