// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package sequence_test

import (
	"testing"

	"github.com/nats-io/nats.go"

	"abarow.com/pkg/sequence"
)

func TestFromHeaders(t *testing.T) {
	tests := []struct {
		name     string
		seq      string
		producer string
		ok       bool
	}{
		{"geçerli", "42", "sales", true},
		{"başlıksız eski olay", "", "", false},
		{"yalnızca numara", "42", "", false},
		{"yalnızca gönderen", "", "sales", false},
		{"sayı değil", "kırk", "sales", false},
		{"sıfır", "0", "sales", false},
		{"eksi", "-3", "sales", false},
	}
	for _, tc := range tests {
		h := nats.Header{}
		if tc.seq != "" {
			h.Set(sequence.HeaderSequence, tc.seq)
		}
		if tc.producer != "" {
			h.Set(sequence.HeaderProducer, tc.producer)
		}
		producer, seq, ok := sequence.FromHeaders(h)
		if ok != tc.ok {
			t.Errorf("%s: ok = %v", tc.name, ok)
		}
		if ok && (producer != "sales" || seq != 42) {
			t.Errorf("%s: %q %d", tc.name, producer, seq)
		}
		if !ok && (producer != "" || seq != 0) {
			t.Errorf("%s: a value was returned while invalid: %q %d", tc.name, producer, seq)
		}
	}
}

func TestGap(t *testing.T) {
	one := sequence.Gap{Register: "register-1", Producer: "sales", From: 7, To: 7}
	many := sequence.Gap{Register: "register-2", Producer: "staff", From: 10, To: 14}
	if one.Count() != 1 || many.Count() != 5 {
		t.Errorf("numbers %d %d", one.Count(), many.Count())
	}
	if one.String() != "register-1/sales #7" || many.String() != "register-2/staff #10–14" {
		t.Errorf("metin %q %q", one, many)
	}
	if sequence.Total([]sequence.Gap{one, many}) != 6 || sequence.Total(nil) != 0 {
		t.Error("wrong total")
	}
}
