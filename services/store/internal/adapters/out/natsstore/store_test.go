// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natsstore

import (
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

func TestConfig(t *testing.T) {
	cfg := Config([]string{"register-1", "register-2"})
	if cfg.Name != StreamName || cfg.Storage != jetstream.FileStorage || cfg.MaxAge != 0 || len(cfg.Subjects) != 1 || cfg.Subjects[0] != "store.>" {
		t.Errorf("ayarlar %+v", cfg)
	}
	if len(cfg.Sources) != 4 || cfg.Sources[0].Name != "ABAROW_SALES" || cfg.Sources[0].Domain != "register-1" ||
		cfg.Sources[1].Name != "ABAROW_STAFF" || cfg.Sources[1].Domain != "register-1" || cfg.Sources[3].Domain != "register-2" {
		t.Errorf("kaynaklar %+v", cfg.Sources)
	}
}

func TestRegisterID(t *testing.T) {
	ext := func(prefix string) *jetstream.ExternalStream { return &jetstream.ExternalStream{APIPrefix: prefix} }
	tests := []struct {
		src  *jetstream.StreamSourceInfo
		want string
	}{
		{&jetstream.StreamSourceInfo{Name: "ABAROW_SALES", External: ext("$JS.register-1.API")}, "register-1"},
		{&jetstream.StreamSourceInfo{Name: "ABAROW_SALES", External: ext("$JS.API")}, ""},
		{&jetstream.StreamSourceInfo{Name: "ABAROW_SALES", External: ext("$JS.a.b.API")}, ""},
		{&jetstream.StreamSourceInfo{Name: "ABAROW_SALES"}, ""},
		{&jetstream.StreamSourceInfo{Name: "BASKA", External: ext("$JS.register-1.API")}, ""},
		{nil, ""},
	}
	for _, tt := range tests {
		if got := registerID(tt.src); got != tt.want {
			t.Errorf("%+v: %q, want %q", tt.src, got, tt.want)
		}
	}
}

// The catalog stream carries the local subjects in every case; it is fed from
// headquarters only while connected.
func TestCatalogConfig(t *testing.T) {
	local := CatalogConfig("")
	if local.Name != CatalogStream || local.Storage != jetstream.FileStorage || local.MaxMsgsPerSubject != 1 ||
		len(local.Sources) != 0 || len(local.Subjects) != 2 || local.Subjects[0] != "catalog.product.>" || local.Subjects[1] != "catalog.image.>" {
		t.Errorf("kopuk kip: %+v", local)
	}
	linked := CatalogConfig("headquarters")
	if len(linked.Sources) != 1 || linked.Sources[0].Name != CatalogStream || linked.Sources[0].Domain != "headquarters" || len(linked.Subjects) != 2 {
		t.Errorf("connected mode: %+v", linked)
	}
}
