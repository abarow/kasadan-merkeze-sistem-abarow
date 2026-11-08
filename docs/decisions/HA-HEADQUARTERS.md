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

## Önerilen sıra

1. NATS 3 düğüm (en az risk, en büyük kazanç: olay kabulü durmaz).
2. PostgreSQL replikası ve elle yük devri komutu, tatbikat betiği.
3. Servislerin çoğaltılması ve Caddy.
4. (İsteğe bağlı) Patroni ile otomatik yük devri.

## Kararlar (13.11.2026)

Mehmet ERGULCU: ikinci seviye yapılacak, yük devri **otomatik** (seçenek B).

- **Üç makine.** Otomatik yük devrinde kimin birincil olacağına çoğunluk karar verir (etcd). İki makineyle bir makine gidince çoğunluk kalmaz; bölünmüş beyni önlemek için en az üç gerekir. İki makine aynı anda giderse kalan makine yazmayı durdurur: iki ayrı satış defteri oluşmasındansa gecikme seçilir (satışlar mağazada bekler).
- **Senkron replikasyon.** `ingest`, olayı PostgreSQL'e yazdıktan sonra mağazaya makbuz gönderir; makbuzu alan mağaza kendi kopyasını siler. Replikasyon asenkron olsaydı birincil, commit'ten hemen sonra ve replikaya göndermeden çökerse olay yeni birincilde olmazdı, mağazadaki kopya da silinmiş olurdu: yasal kayıt kaybı. Bu yüzden commit en az bir replikaya yazılmadan tamamlanmaz (Patroni `synchronous_mode_strict`). Bedeli: iki replika birden giderse yazma durur.
- **Veritabanı adresi.** Servisler üç makinenin adresini bilir ve yazılabilir olanı bağlantı ayarıyla bulur (`target_session_attrs=read-write`); araya ayrı bir yönlendirici (HAProxy) girmez.
- **`ingest` tek okuyucu.** Her makinede bir `ingest` çalışır; birincil veritabanında danışma kilidini (advisory lock) alan okur, diğerleri bekler. Kilidi tutan süreç ya da bağlantısı giderse kilit kendiliğinden düşer.
- **Ekran adresi.** Her makinede Caddy; tek adres sanal IP ile (keepalived) verilir.

### Uygulama sırası

1. **NATS kümesi (13.11.2026'da yapıldı):** `natsauth -routes` ve `-domain`; düğümler arası karşılıklı TLS (ortak CA); merkezin stream'leri `ABAROW_NATS_REPLICAS=3` ile üç kopya; mağazanın leaf bağlantısı üç adresi bilir (`-leaf` virgüllü). Test: üç düğüm ve bir mağaza; stream'in lideri kapatılırken olaylar kayıpsız ve tekrarsız geliyor (`tools/natsauth/cluster_test.go`).
2. PostgreSQL: Patroni ve etcd, senkron replika; servislerin çok adresli bağlantısı; Docker'da yük devri testi.
3. `ingest` danışma kilidi; servislerin her makinede çalışması; Caddy ve keepalived.
4. Kurulum betiği: ilk makinede anahtarların ve CA'nın dışa aktarılması, diğer makinelerde içe alınması; mağaza ekleme üç makinede.
5. Tatbikat: üç makineli Docker düzeneğinde her parçanın sırayla kapatılması.

Bu adımlar bitene kadar kurulum betiği tek makinelidir.
