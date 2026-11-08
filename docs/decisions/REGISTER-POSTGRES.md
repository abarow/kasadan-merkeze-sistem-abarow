# abarow — Kasanın depolaması: SQLite yerine PostgreSQL

> Tarih: 08.11.2026 (karar: "kasaları da PostgreSQL yapalım"). Mağaza sunucusu ve merkez zaten PostgreSQL'deydi ([STORE-POSTGRES.md](STORE-POSTGRES.md)); artık üç katmanda tek veritabanı teknolojisi vardır.

## Kararlar

1. **Kasa başına tek PostgreSQL container'ı** (`abarow-register-db`, `postgres:18-alpine`). Ağa açılmaz, yalnızca kasa ağındaki servisler bağlanır. Parola kurulumda rastgele üretilir ve yeniden kurulumda korunur.
2. **Her servis kendi şemasında çalışır** (`sales`, `staff`, `catalog`, `payment`, `retail`, `restaurant`; `pkg/pgdb`). Bir servisin verisi yine kendisinindir, tablo adları çakışmaz, her şemanın kendi migration geçmişi vardır.
3. **Servis başına tek bağlantı.** SQLite'taki "servis başına tek yazar" davranışı korunur: bir servisin işlemleri sırayla çalışır.
4. **Atlamaması gereken numaralar sayaç satırından gelir.** Fiş numarası (`receipt_counter`), Z numarası, vardiya numarası ve olay kuyruğu numarası (`outbox.seq`) işlemle birlikte geri alınan bir satırdan verilir. PostgreSQL `SEQUENCE`/`BIGSERIAL` geri alınan işlemde numara harcar: fişte boşluk yasal sorun olur, olay numarasındaki boşluk merkezde sahte "kayıp olay" alarmı üretir. Geri alma ve eşzamanlı satış testleri `numbers_test.go` dosyalarındadır.
5. **Veri aktarım aracı yazılmadı.** Karar anında sahada kurulum yoktu. Sahada SQLite ile çalışan bir kasa olursa eski `abarow-*-data` birimlerinden aktarım ayrı bir iştir.

## Bedel (ölçüldü, 08.11.2026)

16 senaryoluk kasa simülasyonu üç kez ve tarayıcı testi çalışırken container başına en yüksek bellek:

| | SQLite (önceki) | PostgreSQL |
|---|---|---|
| Kasa servislerinin toplam zirvesi | ≈137 MiB | ≈141 MiB (veritabanı container'ı 55 MiB) |
| Asgari (zirve + %40, 64 MB'ın katı) | 192 MB | **256 MB** |
| Önerilen (container tavanları toplamı) | 544 MB | **736 MB** |

Servislerin kendi SQLite önbellekleri ortadan kalktığı için toplam zirve az değişti. Ölçüm kısa sürelidir ve az veriyle yapılmıştır; aylarca çalışmış bir kasanın bellek kullanımı ölçülmedi.

## Açık noktalar

- **Quadlet kurulumu gerçek Podman'da denenmedi.** CI ve yerel simülasyonlar Docker Compose kullanır. `abarow-register-db.container` ve `register-install.sh` değişikliği yalnızca sözdizimi ve ayar testiyle doğrulandı.
- **Ana sürüm yükseltmesi** (`pg_upgrade`) artık kasalar için de güncelleme sürecinin bir konusudur; otomatik yolu yoktur.
- **Elektrik kesintisinde toparlanma** PostgreSQL'in WAL kurtarmasına bağlıdır; ayrıca test edilmedi.
- `staff` servisinde zaman sütunları sabit genişlikli UTC metin olarak kaldı (`COLLATE "C"`: bayt sırası zaman sırasıdır). Diğer servisler `TIMESTAMPTZ` kullanır.
