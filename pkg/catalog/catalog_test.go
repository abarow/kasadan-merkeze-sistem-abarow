// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "abarow.com/gen/com/abarow/catalog/v1"

	"abarow.com/pkg/menu"
	"abarow.com/pkg/price"
)

func TestDecode(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	data, err := proto.Marshal(&catalogv1.ProductState{
		Product: &catalogv1.Product{Id: "kofte", Name: "Köfte", Unit: catalogv1.Unit_UNIT_KG, TaxRateBp: 1000,
			Barcodes: []string{"2700"}, QuickKey: true, Category: "Izgara", MenuOrder: 3, Station: menu.Kitchen,
			OptionGroups: []*catalogv1.OptionGroup{{Id: "pismis", Name: "Pişme", Min: 1, Max: 1,
				Options: []*catalogv1.Option{{Id: "az", Name: "Az pişmiş"}, {Id: "cok", Name: "Çok pişmiş", PriceDeltaKurus: 500}}}},
			ImageSha256: "abc", ImageType: "image/png"},
		Prices:       []*catalogv1.PriceChange{{UnitPriceKurus: 25000}, {UnitPriceKurus: 27500, From: timestamppb.New(from)}},
		Discontinued: true, UpdatedBy: "headquarters",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode("application/protobuf", data)
	if err != nil {
		t.Fatal(err)
	}
	want := Product{ID: "kofte", Name: "Köfte", Unit: Kg, TaxRate: 1000, Barcodes: []string{"2700"}, QuickKey: true,
		Category: "Izgara", MenuOrder: 3, Station: menu.Kitchen,
		Options: []menu.Group{{ID: "pismis", Name: "Pişme", Min: 1, Max: 1,
			Options: []menu.Option{{ID: "az", Name: "Az pişmiş"}, {ID: "cok", Name: "Çok pişmiş", PriceDelta: 500}}}},
		ImageSHA256: "abc", ImageType: "image/png",
		Prices: []price.Point{{Amount: 25000}, {Amount: 27500, From: from}}, Discontinued: true, UpdatedBy: "headquarters"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("product:\n%+v\nwant\n%+v", got, want)
	}
	if _, err := Decode("application/json", data); !errors.Is(err, ErrUndecodable) {
		t.Errorf("JSON: %v", err)
	}
	if _, err := Decode("application/protobuf", []byte{0xff}); !errors.Is(err, ErrUndecodable) {
		t.Errorf("bozuk: %v", err)
	}
}
