// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natsconsumer

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	inventoryv1 "abarow.com/gen/com/abarow/inventory/v1"

	"abarow.com/pkg/stock"
)

// The place is taken from the subject; if the place in the event does not match the subject (writing
// in the name of another store) the event is rejected.
func TestDecodeMoved(t *testing.T) {
	data, _ := proto.Marshal(&inventoryv1.StockMoved{
		LocationId: "store-1", MovementId: "sale:s1", Kind: inventoryv1.MovementKind_MOVEMENT_KIND_SALE, Ref: "s1",
		Lines: []*inventoryv1.MovementLine{{ProductId: "ekmek", ProductName: "Ekmek", QtyMilli: -2000}},
	})
	loc, m, err := DecodeMoved("store-1.store.stock.moved.v1", "application/protobuf", data)
	if err != nil || loc != "store-1" || m.Kind != stock.Sale || m.Lines[0].Qty != -2000 {
		t.Fatalf("%s %+v %v", loc, m, err)
	}
	for subject, ct := range map[string]string{
		"store-2.store.stock.moved.v1": "application/protobuf", // the name of another store
		"store.stock.moved.v1":         "application/protobuf",
		"store-1.store.stock.moved.v2": "application/protobuf",
		"store-1.store.stock.moved.v1": "application/json",
	} {
		if _, _, err := DecodeMoved(subject, ct, data); !errors.Is(err, errInvalid) {
			t.Errorf("%s %s: %v", subject, ct, err)
		}
	}
}
