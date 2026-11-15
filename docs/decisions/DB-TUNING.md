# Veritabanı ayarı: dört aşamalı öneri ve eleştirisi (14.11.2026)

> Öneri, 14.11.2026'da getirilen genel bir "kıdemli DBA" planıdır. Plan dört aşamadan oluşuyor:
>
> 1. `pgbench` ile referans ölçüm.
> 2. PGTune ya da `timescaledb-tune` ile donanıma göre ayar ve ayarların `ALTER SYSTEM` ile uygulanması.
> 3. `pgbench` ile yeniden ölçüm; `pg_tuner` ya da DBtune ile yinelemeli ince ayar.
> 4. `pg_stat_statements` ile sorgu ve indeks analizi.
>
> Bu belge planın her adımını abarow'un bugünkü durumuyla karşılaştırıyor ve neyin plana alındığını yazıyor. Planın kuralları da geçerli: yalnız ücretsiz ve açık kaynak araçlar, değişiklikten önce yedek, kesinti ya da ağır yükten önce onay.

## Neden olduğu gibi uygulanmıyor

Öneri tek bir sunucuyu, bir kişinin elle ayarlaması için yazılmış. abarow'da durum farklı:

- **Kurulum sayısı:** üç katmanda yüzlerce kurulum var: her kasa, her mağaza sunucusu ve merkez. Elle ve sunucu başına yapılan ayar ölçeklenmez ve kurulumlar arasında farka yol açar.
- **Ayarların kaynağı:** ayarlar zaten koddan hesaplanıyor (`pkg/pgtune`: `ForRegister`, `ForStore`, `ForHeadquarters`, `ForHeadquartersCluster`). Bu, PGTune mantığının kendisidir; formüller ölçümle düzeltildi ([PERFORMANCE.md](../PERFORMANCE.md)) ve testli.
- **Yük ölçütü:** ayarlar donanıma göre değil, yüke göre (kasa ve mağaza sayısı) seçiliyor. Makinede veritabanının yanında 10 civarında container ve kasada Chromium çalışıyor. Bu yüzden veritabanı makinenin bütün belleğini almıyor; ölçülen ihtiyaç kadar bellek ve bir container tavanı alıyor.

## Aşama aşama eleştiri

### Aşama 1 — Referans ölçüm (`pgbench`)

- **Donanım bilgisi:** doğru ve eksik. Kurulum bugün RAM, çekirdek ve disk türünü hiç sormuyor (bkz. plana alınanlar 2 ve 3).
- **`SHOW ALL`:** yararlı, ama ayarlar zaten birim dosyasında (`Exec=postgres -c ...`) ve `pgtune` çıktısında yazılı.
- **`pgbench`'in varsayılan yükü (TPC-B benzeri):** abarow'un yükünü temsil etmiyor. Kasada tek bağlantılı küçük satış işlemleri, merkezde toplu olay yazma ve büyük rapor sorguları var. `pgbench`'in TPS'i bu yüklerden hiçbirini söylemiyor. Yüke özgü ölçüm araçları zaten var:
  - `storebench` ve `headquartersbench`,
  - gerçekçi yük (`-rate 0.05`) ve stres yükü,
  - tek ve çok ekranlı rapor ölçümleri.
- **`pgbench`'in yararlı olduğu yer:** donanımın ham gücünü ölçmek. Diskin commit gecikmesi `pg_test_fsync` ile, küçük yazma işlemleri `pgbench -N` ile, okuma `pgbench -S` ile ölçülür. Bu da her kurulumda değil, hedef donanım sınıflarında bir kez yapılmalı: kasa mini PC'si, mağaza sunucusu, merkez ve ARM (P4).
- **Canlı veritabanında:** ayrı bir test veritabanı bile aynı diski ve belleği paylaşır. Kasada iş saatinde `pgbench` çalıştırılmaz.

### Aşama 2 — Donanıma göre ayar

- **OOM'dan kaçınma:** doğru ve abarow'da bu zaten tasarımın temeli. Her container'ın bir bellek tavanı var. Paralel sorgu belleği (`/dev/shm`) en kötü duruma göre ayrılıyor (P5).
  - **Eksik:** makinenin belleğinin bütün tavanların toplamına yettiği hiç denetlenmiyor (plana alınanlar 2).
- **`timescaledb-tune`:** TimescaleDB için yazılmış. `postgresql.conf`'u kendisi düzenliyor ve TimescaleDB ayarları ekliyor. abarow TimescaleDB kullanmıyor; araç ayarları birim dosyasındaki hesapla çakıştırır. **Reddedildi.**
- **`ALTER SYSTEM SET` ile uygulama abarow'da yanlış sonuç verir:**
  - **Tek makinede:** komut satırındaki `-c` ayarı `postgresql.auto.conf`'tan önce gelir. Aynı ayar `ALTER SYSTEM` ile verilirse sessizce yok sayılır.
  - **Üç makineli merkezde:** ayarları Patroni yönetir (`patronictl edit-config`, [HA-HEADQUARTERS.md](HA-HEADQUARTERS.md)). `ALTER SYSTEM` ile verilen değer makineler arasında fark yaratır.
  - **Kurulum tekrarı:** kurulum her tekrarda ayarları hesaptan yeniden yazar. Elle yapılan ayar ya kaybolur ya da hesabın dışında kalır.

  **Doğru yol:** formülü (`pkg/pgtune`) değiştirmek, ölçmek ve yeni sürümle dağıtmak.
- **"Yeniden yükleme yeter mi, yeniden başlatma mı gerekir" ayrımı:** doğru ve gerekli.
  - `shared_buffers`, `max_connections`, `max_worker_processes`, `wal_buffers` ve `shared_preload_libraries` yeniden başlatma ister.
  - `work_mem`, `effective_cache_size`, `random_page_cost`, `effective_io_concurrency`, WAL boyutları ve `checkpoint_completion_target` yeniden yüklemeyle geçer.

  Kurulum bugün ayar değişince veritabanını her durumda yeniden başlatıyor. Değişen ayarlar yalnız yeniden yükleme istiyorsa kesinti gereksiz (plana alınanlar 4; 14.11.2026'da yapıldı, yeniden yükleme yerine sonraki yeniden başlamayı bekleyerek).
- **Parametre listesi:** listedekilerin hepsi `pgtune`'de var, şu ikisi dışında:
  - **`effective_io_concurrency`:** hiç verilmiyor.
  - **`random_page_cost = 1.1`:** her kurulumda SSD varsayıyor. Ucuz bir kasa kutusunda eMMC ya da dönen disk varsa bu değer yanlış (plana alınanlar 3).

### Aşama 3 — Yeniden ölçüm ve yinelemeli ince ayar

- **Aynı ölçümün tekrarı ve yüzde fark:** doğru. abarow'da bu, değişen formülün aynı bench ile ölçülmesi demek. [PERFORMANCE.md](../PERFORMANCE.md) böyle yürüyor.
- **`pg_tuner` (Optuna) ve DBtune:**
  - **Nasıl çalışıyorlar:** ayarı bulmak için veritabanını yapay yük altında defalarca yeniden başlatıyorlar.
  - **Canlıda:** bir kasada ya da mağaza sunucusunda bu yapılamaz.
  - **Ölçek:** yüzlerce kurulumda kurulum başına öğrenen ayar bakımı mümkün olmayan bir farklılık doğurur.
  - **DBtune:** ücretsiz paketi olan ticari bir hizmet. Aracısı veritabanı ölçümlerini işletmenin dışına gönderiyor. Bu, "her işletme kendi merkezini çalıştırır, verileri kendisinde kalır" ilkesine de, önerinin "yalnız ücretsiz ve açık kaynak" kuralına da aykırı.

  **İkisi de reddedildi.** Bunların yerine formül hedef donanım sınıflarında, gerçek yük araçlarıyla bir kez ayarlanır.

### Aşama 4 — Sorgu ve indeks darboğazları (`pg_stat_statements`)

- **Bugünkü durum:**
  - `pg_stat_statements` mağaza sunucusunda ve merkezde açık.
  - Haftalık bakım (`dbmaint.sh`) toplam süreye göre en pahalı 10 sorguyu ve en büyük tabloları yazıyor; şişmiş indeksleri yeniden kuruyor.
  - Kasada eklenti kapalı.
- **Eksik:**
  - Ortalama süresi en uzun ve diskten en çok okuyan (`shared_blks_read`) sorgular yazılmıyor.
  - Hiç kullanılmayan indeksler (`idx_scan = 0`) ve indeks yerine çok sık tam tarama yapılan tablolar (`pg_stat_user_tables.seq_scan`, `seq_tup_read`) raporda yok (plana alınanlar 5).
- **Eksik indeks önerisi:** bu raporlardan insan çıkarır. Otomatik indeks oluşturulmaz. Her indeks bir migration'dır, CI'da ölçülür ve bütün kurulumlara aynı gider.

## Plana alınanlar

1. **Donanım sınıfı ölçümü:** kasa mini PC'si, mağaza sunucusu, merkez ve ARM'de birer kez yapılır. Ölçümler `pg_test_fsync` (commit gecikmesi), `pgbench -i -s 50` + `-N` ve `-S` (okuma ve küçük yazma; 60 sn, aynı parametreler) ve yük araçları (`storebench`, `headquartersbench`, `-rate 0.05`). Sonuçlar [PERFORMANCE.md](../PERFORMANCE.md)'ye "referans donanım" olarak yazılır. Bu ölçüm P4'ün ölçüm planına eklenir; gerçek donanım gerekir.
2. **Kurulumda bellek denetimi:** kurulum, makinenin belleğini (`MemTotal`) bütün container tavanlarının toplamıyla, işletim sistemi payıyla ve kasada Chromium'la karşılaştırır. Yetmiyorsa açıkça uyarır ya da durur. Bugün yetersiz bellek ancak OOM ile fark ediliyor.
   - **Yapıldı (14.11.2026):** `memory_check` (`deploy/install/common.sh`), üç kurulum betiğinin özetinden önce. İşletim sistemi payı 256 MB, kiosk payı 512 MB ([DESIGN.md](../DESIGN.md) §13). Kurulum durmuyor, yalnız uyarıyor: tavanlar üst sınır, servisler kullanımda çok altında kalıyor ([HARDWARE.md](../HARDWARE.md), asgari ve optimum). Test: `scripts/install-machine-test.sh`.
3. **Disk türüne göre G/Ç ayarı:** kurulum veritabanı diskinin dönen disk olup olmadığına bakar (`/sys/block/*/queue/rotational`) ve sonucu `pgtune`'e verir. SSD ve NVMe'de `random_page_cost = 1.1` ve `effective_io_concurrency` yüksek tutulur. Dönen diskte `random_page_cost = 4` ve `effective_io_concurrency` düşük tutulur, ayrıca kurulum özetinde uyarı verilir.
   - **Yapıldı (14.11.2026):**
     - **Algılama:** mağaza sunucusu ve merkez kurulumu, abarow kullanıcısının container deposunun (veritabanı birimi de orada) bulunduğu diske bakar (`disk_rotational`, `findmnt` ve `lsblk ROTA`). Dönen diskse `pgtune --rotational` verir ve özette uyarır.
     - **Değerler:** dönen diskte `random_page_cost = 4`, `effective_io_concurrency = 2` ve `maintenance_io_concurrency = 2`. SSD'de değişiklik yok: `random_page_cost = 1.1`, `effective_io_concurrency` PostgreSQL 18'in varsayılanı olan 16 (denendi).
     - **Sanal makine:** sanal disk çoğu zaman altındaki diskten bağımsız olarak "dönen disk" der (KVM'nin virtio diski varsayılan olarak böyle). Bu yüzden sanal makinede SSD ayarı korunur; disk okunamazsa da öyle.
     - **Kasa:** kasanın veritabanı ayarları Quadlet dosyasında sabit ve kasada `pgtune` çalışmıyor; kasa diske göre ayarlanmıyor. Kasanın veritabanı küçük, raporu da tek kasanın verisi.
     - **Üç makineli merkez:** ayarlar Patroni'de bütün küme için ortak. Makinelerin diskleri farklıysa son kurulan makinenin değeri geçerli olur.
     - **Test:** `pkg/pgtune` ve iki `pgtune` komutu için birim testleri; `scripts/install-machine-test.sh`.
4. **Yeniden başlatmayı yalnız gerekince yapmak:** `store-add` ya da `store-remove` ayarları değiştirince kurulum yalnız yeniden başlatma isteyen bir ayar değiştiyse veritabanını yeniden başlatır. Diğer durumlarda ayarı yeniden yükler (`pg_reload_conf`). Kümede bunu zaten Patroni yapıyor (`restart --pending`).
   - **Yapıldı, değişiklikle (14.11.2026):**
     - **Yeniden yükleme işe yaramıyor:** ayarlar komut satırında (`-c`) ve yeniden yükleme komut satırı ayarlarını değiştirmez. Bu ayarlar `ALTER SYSTEM` ile de verilemez (yukarıda, Aşama 2). Yeniden başlatma istemeyen bir ayar bu yüzden veritabanının bir sonraki yeniden başlamasını (güncelleme ya da makinenin yeniden açılması) bekliyor. Kurulum bunu özette söylüyor.
     - **Hangi ayarın yeniden başlatma istediği:** kurulum bunu çalışan veritabanına soruyor (`pg_settings.context = 'postmaster'`, `tune_restart`). Şu durumlarda veritabanı eskisi gibi yeniden başlatılıyor: container belleği değiştiyse, veritabanının tanımadığı bir ayar değiştiyse (ör. `basic_archive.archive_directory` oturumun `pg_settings`'inde yok) ya da veritabanı cevap vermiyorsa.
     - **Ne kazandırıyor:** taramada (`pkg/pgtune`, 1000 kasaya ve mağazaya kadar) yalnız yeniden başlatma istemeyen ayarların değiştiği tek durum, `shared_buffers` tavanının üstünde `max_wal_size`'ın büyümesi. Bu, mağaza sunucusunda 28 kasadan, merkezde 62 mağazadan sonra her yeni kasa ya da mağaza demek; artık bunlar için veritabanı durmuyor. Altında her değişiklik `shared_buffers`'ı ya da belleği değiştirdiği için yeniden başlatma yine gerekiyor. Disk türünün değişmesi de yeniden başlatmasız.
     - **Kapsam:** tek makineli mağaza sunucusu ve merkez. Kümede Patroni kendisi karar veriyor.
     - **Test:** `scripts/install-machine-test.sh`; sorgu PostgreSQL 18'de gerçek `pgtune` çıktısıyla denendi (30 → 31 kasa yeniden başlatmasız, 5 → 6 kasa yeniden başlatmalı).
5. **Haftalık bakım raporuna ekler:** ortalama süresi en uzun 10 sorgu, diskten en çok okuyan 10 sorgu, hiç kullanılmamış indeksler ve tam taramanın çok olduğu tablolar. Rapor yalnız okur, bir şey değiştirmez.
   - **Yapıldı (14.11.2026):** `dbmaint.sh`.
     - **Listeler:** benzersiz olmayan ve hiç kullanılmamış indeksler; 10 000 satırın üstünde, tam taraması indeks taramasından çok olan tablolar.
     - **Gürültü:** bakımın kendi sorguları (`pgstattuple` taraması, `REINDEX`) sayılmıyor (oturumunda `pg_stat_statements.track=none`); karşı denetimde bunlar sayılmazsa listeye girdiği görüldü. Günlük yedeğin tablo başına `COPY`'si sorgu listelerine alınmıyor.
     - **Kümede:** indeks ve tablo sayaçları makine başına tutuluyor. Replikalarda okunan sorgular birincilin listesinde görünmüyor.
     - **Test:** `scripts/pitr-test.sh` dört listeyi gerçek PostgreSQL 18'de deniyor.

## Reddedilenler

- **Sunucu başına `ALTER SYSTEM` ile elle ayar:** komut satırındaki ayar onu geçersiz kılıyor ve Patroni ile çakışıyor.
- **`timescaledb-tune`:** TimescaleDB'ye özgü ve `postgresql.conf`'u kendisi düzenliyor.
- **`pg_tuner` ve DBtune:**
  - ikisi de canlıda yeniden başlatmalı deneme yapıyor;
  - kurulum başına öğrenen ayar kurulumlar arasında fark yaratıyor;
  - DBtune ayrıca ticari ve ölçümleri dışarı gönderiyor.
- **Canlı kurulumda `pgbench` stres testi:** ölçüm yalnız ayrı bir makinede ya da kurulumdan önce yapılır.
