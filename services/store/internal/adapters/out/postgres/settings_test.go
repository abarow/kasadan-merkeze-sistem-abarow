// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"testing"

	"abarow.com/services/store/internal/adapters/out/postgres/postgrestest"
)

func TestSettings(t *testing.T) {
	ctx := context.Background()
	st := postgrestest.Open(t)

	if _, ok, err := st.Setting(ctx, "headquarters"); err != nil || ok {
		t.Fatalf("a setting never written: ok=%v err=%v", ok, err)
	}
	if err := st.SetSetting(ctx, "headquarters", "off"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := st.Setting(ctx, "headquarters"); err != nil || !ok || v != "off" {
		t.Errorf("written setting: %q %v %v", v, ok, err)
	}
	// When the same key is written again it changes, it does not multiply.
	if err := st.SetSetting(ctx, "headquarters", "on"); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := st.Setting(ctx, "headquarters"); v != "on" {
		t.Errorf("changed setting %q", v)
	}
	if _, ok, _ := st.Setting(ctx, "baska"); ok {
		t.Error("another key came")
	}
}

// The register's event numbers are recorded; a gap in between shows in the register
// state, it closes when the missing event comes.
func TestActivityMissingEvents(t *testing.T) {
	ctx := context.Background()
	st := postgrestest.Open(t)
	for _, seq := range []int64{1, 2, 5} {
		if err := st.RecordSequence(ctx, "store-1", "register-1", "sales", seq); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.RecordSequence(ctx, "store-1", "register-1", "staff", 1); err != nil {
		t.Fatal(err)
	}
	a, err := st.Activity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gaps := a["register-1"].MissingEvents
	if len(gaps) != 1 || gaps[0].Producer != "sales" || gaps[0].From != 3 || gaps[0].To != 4 || gaps[0].Store != "store-1" {
		t.Fatalf("missing events: %+v", gaps)
	}
	for _, seq := range []int64{4, 3} {
		if err := st.RecordSequence(ctx, "store-1", "register-1", "sales", seq); err != nil {
			t.Fatal(err)
		}
	}
	a, _ = st.Activity(ctx)
	if len(a["register-1"].MissingEvents) != 0 {
		t.Errorf("the gap did not close: %+v", a["register-1"].MissingEvents)
	}
}
