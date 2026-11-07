# abarow'a katkı

Katkın için teşekkürler. Tasarım [docs/DESIGN.md](docs/DESIGN.md) dosyasında, yapılacaklar GitHub Issues'ta.

## Lisans

abarow [Apache-2.0](LICENSE) ile lisanslıdır. Gönderdiğin katkılar da aynı lisansla projeye dahil olur (Apache-2.0 md. 5).

## Commit imzası (DCO)

Her commit bir `Signed-off-by` satırı taşır. Bu satırla katkıyı gönderme hakkın olduğunu beyan edersin ([Developer Certificate of Origin](https://developercertificate.org)).

```sh
git commit -s -m "sales: KDV yuvarlama hatası düzeltildi"
```

## Adını ekleme

- **`AUTHORS`:** Katkı yapan herkes adını kendi PR'ında [AUTHORS](AUTHORS) dosyasına ekleyebilir.
- **Dosya başlığı:** Bir dosyada kayda değer değişiklik yaptıysan başlığa kendi telif satırını da ekleyebilirsin.

## Dosya başlıkları

Her kaynak dosya şu başlıkla başlar. Yıl, dosyanın oluşturulduğu yıldır:

```go
// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0
```

Kendi satırını eklerken:

```go
// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// Copyright 2027 Ayşe Yılmaz
// SPDX-License-Identifier: Apache-2.0
```

- Mevcut telif satırları silinmez veya değiştirilmez; yeni satır altına eklenir.
- CI, başlığı olmayan dosyaları ve kurucu satırı silinmiş dosyaları reddeder (`scripts/check-headers.sh`).
- Yeni dosyalara başlık eklemek için:

  ```sh
  go install github.com/google/addlicense@latest
  addlicense -c "Mehmet ERGULCU and the abarow contributors" -l apache -s=only .
  ```

## Göndermeden önce

```sh
bash scripts/check.sh           # gofmt, build, vet, test (her modül)
bash scripts/check-headers.sh   # telif başlıkları
bash scripts/gen.sh             # proto değiştiyse: lint + kod üretimi
```

Aynı kontroller ve Docker simülasyonu CI'da (`.github/workflows/ci.yml`) her push'ta çalışır. CI ayrıca proto sözleşmelerinde kırıcı değişikliği (`buf breaking`; alan silme, numara ya da tür değiştirme) reddeder: yayınlanmış bir alanı değiştirmek yerine yeni alan ekleyin.

## Kod kuralları

- Para ve KDV hesaplarına dokunan her değişiklik test içerir. Para her yerde `int64` kuruştur (bkz. DESIGN.md bölüm 9).
- Simülatörün "MALİ DEĞERİ YOKTUR" uyarıları ve arayüzdeki simülatör göstergesi kaldırılmaz.
- Bilinen hatalar gizlenmez; issue olarak açılır.
- **Dizin düzeni:** Go servisleri `services/` altındadır (`core`, `modules`, `fiscal`, `store`, `headquarters`, `inventory`, `update`); ortak kütüphaneler `pkg/`, `gen/` ve `proto/`, ekranlar `ui/`, kurulum `deploy/`, araçlar `tools/`, betikler `scripts/`. Go modül yolu dizin yoluna eşittir (`services/core/sales` dizini `abarow.com/services/core/sales` modülüdür); yeni modül eklerken `go.work`a ve `replace` satırlarına dizin yolunu yazın.
- **Adlandırma:** Her ad İngilizcedir: dizin, Go modülü ve paketi, protobuf paketi, imaj, container, Quadlet birimi, Compose servisi, tip, fonksiyon, değişken, dosya adı, günlük alan adı, komut satırı alt komut ve bayrağı, veritabanı tablo ve sütunu, ortam değişkeni, NATS stream/konu/olay adı, HTTP yolu, betik ve belge dosya adı (container adları `abarow-` ile başlar). Türkçe yalnızca ekran çevirilerinde (ürünün Türkçe arayüzü, fiş metinleri) ve belge içeriğindedir; kod yorumları, günlük ve hata iletileri İngilizcedir. Go paket adı dizin adıyla aynıdır (üretilen protobuf paketleri hariç: `salesv1` gibi).
- Servisler arası sözleşmeler geriye uyumlu kalır: protobuf'a yalnızca alan eklenir, olaylar sürümlüdür, migration'lar expand/contract kuralına uyar (bkz. DESIGN.md bölüm 12).
