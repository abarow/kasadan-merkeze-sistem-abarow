// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestParseSections(t *testing.T) {
	got, err := parseSections(" 1000:1, 2000:3 ")
	if err != nil || len(got) != 2 || got[1000] != 1 || got[2000] != 3 {
		t.Errorf("sections: %v %v", got, err)
	}
	for _, bad := range []string{"", "  ", "1000", "x:1", "1000:y", "1000:0", "-5:1", "1000:1,"} {
		if _, err := parseSections(bad); err == nil {
			t.Errorf("%q kabul edildi", bad)
		}
	}
}
