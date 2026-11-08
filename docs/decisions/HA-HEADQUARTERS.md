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
5. **Kurulum betiği (13.11.2026'da yapıldı):** `headquarters-install.sh --cluster h1,h2,h3 --node N [--vip adres/önek]`, `cluster-export` ve `--cluster-import` ([deploy/quadlet/README.md](../../deploy/quadlet/README.md) "Üç makinede merkez"). Ayrıntılar:
   - **Ortak dosyalar:** ilk makine anahtarları, CA'yı ve parolaları üretir; diğer iki makine bunları dışa aktarılan dosyadan alır. Başka bir CA'sı olan makinenin üzerine yazılmaz.
   - **Birimler:** Patroni, etcd ve HAProxy için Quadlet birimleri (`deploy/quadlet/headquarters/cluster`). HAProxy `abarow-db` adıyla kurulur; servislerin adresi ve birimleri değişmez, yalnız bağlantı TLS olur (`sslmode=verify-ca`).
   - **Bağlantı bütçesi:** üç makinenin havuzları ve Patroni'nin bağlantıları (`pgtune --machines 3`; 50 mağazada 30 yerine 72). Sonraki ayar değişiklikleri `patronictl edit-config` ile ortak ayara yazılır ve gerekirse makineler sırayla yeniden başlatılır.
   - **Tek adres:** `--vip` ile keepalived. Adres, ekranı (reporting) ve NATS portu (7422) cevap veren makinededir. Makine dönünce adres geri taşınmaz (`nopreempt`); diğer makinelere doğrudan (unicast) duyurulur.
   - **Yedek ve bakım:** her makinenin haftalık fiziksel yedeği ve bakımı kendi Patroni container'ında çalışır; günlük `pg_dump` HAProxy üzerinden birincilden TLS ile alınır.
   - **Kurulum sırası:** veritabanı iki makine kurulunca açılır; o zamana kadar ilk yedek ve hesap ertelenir.
   - **Test:** `scripts/hq-cluster-units-test.sh` (CI'da) kurulumun yazdığı birimleri ve ek dosyaları Podman'ın Quadlet üreticisinden, HAProxy yapılandırmasını `haproxy -c`'den geçirir; dışa ve içe aktarmanın aynı dosyaları verdiğini ve başka CA'nın üzerine yazılmadığını dener. Kurulum betiği gerçek üç makinede **denenmedi** (aşağıdaki "Denenmemiş olanlar").
6. Tatbikat: üç sanal ya da gerçek makinede kurulum ve her parçanın (makine, ağ bağlantısı, etcd, Patroni, NATS düğümü) sırayla kapatılması.

### Kümede geri yükleme

`restore` ve `restore-to` kümede çalışmaz: tek makinede servisler durdurulsa bile diğer makinelerin `ingest` kopyaları yazmayı sürdürür. Günlük yedekten (`pg_dump`) geri yükleme elle yapılır:

1. Üç makinede, `abarow` kullanıcısıyla: `systemctl --user stop abarow-ingest abarow-catalog-admin abarow-inventory-headquarters abarow-reporting`.
2. Herhangi bir makinede: `podman exec abarow-patroni patronictl -c /tmp/patroni.yml list` ile birincili görün. Yedeği HAProxy üzerinden TLS ile yükleyin: `psql` ile `DROP DATABASE abarow WITH (FORCE)` ve `CREATE DATABASE abarow`, ardından `pg_restore -h abarow-db`. Bağlantıda `sslmode=verify-ca` ve merkezin CA'sı kullanılır. Yazılanlar replikalara da gider.
3. Üç makinede servisleri başlatın; bir makinede `headquarters-install.sh reprocess`.

Belirli bir ana dönüş (WAL arşivi, `restore-to`) kümede henüz yok. Gerekirse Patroni'nin yedekten kurma yöntemiyle (custom bootstrap) yapılmalıdır; bu iş açık.

### Denenmemiş olanlar

Docker testleri her parçayı ayrı ayrı sınıyor: NATS kümesi, Patroni, etcd, HAProxy, TLS, yük devri, yedek ve bakım. Kurulumun yazdığı dosyalar da doğrulanıyor. Gerçek üç makinede ise henüz denenmedi:

- **Rootless Podman'da kendi adresine bağlanma:** etcd'nin duyurduğu adresler makinenin dış adıdır. Patroni kendi makinesindeki etcd'ye bu dış adla da bağlanabilir. Bu, container'dan aynı makinenin yayımlanmış portuna gitmek demektir (pasta). HAProxy ve Patroni kendi makineleri için container adını kullanıyor; etcd'nin üye listesi ise dış adı veriyor.
- **keepalived:** ağ arayüzünün bulunması, unicast duyurusu ve adresin taşınma süresi.
- **Caddy ve `--domain`:** her makinede Caddy çalışır. Sertifikayı yalnız adresi o an tutan makine alabilir. Adres taşınınca yeni makine sertifikayı ilk istekte alır ve bu birkaç saniye sürer.

Bu noktalar 6. adımdaki tatbikatta görülecek.
