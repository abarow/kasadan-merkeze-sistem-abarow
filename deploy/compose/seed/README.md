# Örnek veri

## Kasiyerler

`cashiers.json`, staff servisine açılışta yüklenen **deneme** kasiyerleridir: Kasiyer Ayşe (PIN 1111), Kasiyer Mert (2222), Müdür Ali (9999). PIN'ler yüklenirken bcrypt ile özetlenir; dosyadaki açık PIN'ler yalnızca simülasyon içindir, **gerçek kurulumda kullanılmamalıdır**. Gün sonu (Z) yalnızca müdür rolündedir.

## Katalog

`catalog.json`, Docker simülasyonundaki catalog servisine açılışta yüklenen örnek veridir. **Ürünler, fiyatlar ve KDV oranları gerçeği yansıtmaz**; yalnızca deneme içindir.

- Barkodlar geçerli EAN-13'tür (kontrol hanesi doğru), ama gerçek ürünlere ait değildir.
- Tartılı ürünlerin (`"unit": "kg"`) barkodu terazi ön eki + ürün kodudur (`2700001`). Kasada terazinin bastığı barkod okutulur: `2700001012504` → Domates, 1,250 kg.
