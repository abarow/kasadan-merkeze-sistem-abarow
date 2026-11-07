// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natsstream

import (
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

func TestConfig(t *testing.T) {
	cfg := Config([]string{"store-1", "store-2"})
	if cfg.Name != StreamName || len(cfg.Subjects) != 0 || len(cfg.Sources) != 2 {
		t.Fatalf("stream: %+v", cfg)
	}
	for i, id := range []string{"store-1", "store-2"} {
		src := cfg.Sources[i]
		if src.Name != "ABAROW_STORE" || src.Domain != id || len(src.SubjectTransforms) != 1 ||
			src.SubjectTransforms[0].Source != ">" || src.SubjectTransforms[0].Destination != id+".>" {
			t.Errorf("%s kaynağı: %+v", id, src)
		}
	}
}

func TestStoreID(t *testing.T) {
	for _, tt := range []struct {
		src  *jetstream.StreamSourceInfo
		want string
	}{
		{&jetstream.StreamSourceInfo{Name: "ABAROW_STORE", External: &jetstream.ExternalStream{APIPrefix: "$JS.store-1.API"}}, "store-1"},
		{&jetstream.StreamSourceInfo{Name: "ABAROW_SALES", External: &jetstream.ExternalStream{APIPrefix: "$JS.store-1.API"}}, ""},
		{&jetstream.StreamSourceInfo{Name: "ABAROW_STORE"}, ""},
		{&jetstream.StreamSourceInfo{Name: "ABAROW_STORE", External: &jetstream.ExternalStream{APIPrefix: "$JS.a.b.API"}}, ""},
		{nil, ""},
	} {
		if got := storeID(tt.src); got != tt.want {
			t.Errorf("storeID(%+v) = %q; %q olmalı", tt.src, got, tt.want)
		}
	}
}

func TestRoute(t *testing.T) {
	if s, e, ok := Route("store-1.sale.completed.v1"); !ok || s != "store-1" || e != "sale.completed.v1" {
		t.Errorf("Route: %q %q %v", s, e, ok)
	}
	for _, bad := range []string{"sale", ".sale.completed.v1", "store-1."} {
		if _, _, ok := Route(bad); ok {
			t.Errorf("Route(%q) kabul edildi", bad)
		}
	}
}
