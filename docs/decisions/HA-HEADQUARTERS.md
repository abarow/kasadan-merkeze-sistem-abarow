# abarow — Kesintisiz merkez (plan, onay bekliyor)

> Tarih: 12.11.2026. [REVIEW.md](../REVIEW.md) M3'ün ikinci seviyesi. Bu belge bir plandır; kod yazılmadı. Uygulamaya geçmeden önce aşağıdaki "Karar gereken noktalar" bölümünün cevaplanması gerekir.

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

## Karar gereken noktalar

1. İkinci seviye gerekli mi, yoksa birinci seviye yeterli mi?
2. Gerekliyse yük devri elle mi (A), otomatik mi (B)?
3. Kaç makine var ya da alınacak (üç, ayrı veri merkezlerinde mi)?
