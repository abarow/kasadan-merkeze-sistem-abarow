// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package price

import (
	"errors"
	"slices"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func TestEffective(t *testing.T) {
	headquarters := []Point{{Amount: 1000}, {Amount: 1200, From: now.Add(time.Hour)}}
	store := []Point{{Amount: 900, From: now.Add(-time.Hour)}, {Clear: true, From: now.Add(2 * time.Hour)}}
	for _, tt := range []struct {
		at     time.Time
		amount int64
		store  bool
	}{
		{now.Add(-2 * time.Hour), 1000, false}, // no exception yet
		{now, 900, true},                       // the exception is ahead of the headquarters price
		{now.Add(90 * time.Minute), 900, true}, // the headquarters price changed, the exception continues
		{now.Add(3 * time.Hour), 1200, false},  // the exception lifted
	} {
		a, s, ok := Effective(headquarters, store, tt.at)
		if !ok || int64(a) != tt.amount || s != tt.store {
			t.Errorf("%v: %d %v %v; want %d %v", tt.at, a, s, ok, tt.amount, tt.store)
		}
	}
	// A product not yet in effect.
	if _, _, ok := Effective([]Point{{Amount: 500, From: now.Add(time.Hour)}}, nil, now); ok {
		t.Error("a price not in effect was returned")
	}
}

func TestPlanCancelPrune(t *testing.T) {
	var p []Point
	var err error
	// A past-dated price becomes valid now; the price in effect counts as from the
	// beginning.
	if p, err = Plan(p, 900, false, now.Add(-time.Hour), now); err != nil || !slices.Equal(p, []Point{{Amount: 900}}) {
		t.Fatalf("now: %+v %v", p, err)
	}
	later := now.Add(24 * time.Hour)
	if p, err = Plan(p, 950, false, later, now); err != nil || len(p) != 2 || p[1] != (Point{Amount: 950, From: later}) {
		t.Fatalf("future date: %+v %v", p, err)
	}
	// A new record at the same moment takes the place of the old one.
	if p, _ = Plan(p, 0, true, later, now); len(p) != 2 || !p[1].Clear {
		t.Fatalf("removal: %+v", p)
	}
	if _, err := Cancel(p, now, now); !errors.Is(err, ErrStarted) {
		t.Errorf("the one in effect was withdrawn: %v", err)
	}
	if _, err := Cancel(p, later.Add(time.Hour), now); !errors.Is(err, ErrNotScheduled) {
		t.Errorf("non-existent record: %v", err)
	}
	if p, err = Cancel(p, later, now); err != nil || !slices.Equal(p, []Point{{Amount: 900}}) {
		t.Errorf("undo: %+v %v", p, err)
	}
	if _, err := Plan(nil, -1, false, now, now); !errors.Is(err, ErrInvalid) {
		t.Errorf("negative price: %v", err)
	}
	// If the record in effect is a removal it drops out of the schedule.
	if got := Prune([]Point{{Amount: 900}, {Clear: true, From: now.Add(-time.Minute)}}, now); len(got) != 0 {
		t.Errorf("lifted exception: %+v", got)
	}
	if up := Upcoming([]Point{{Amount: 1, From: later}, {Amount: 2}, {Amount: 3, From: now.Add(time.Hour)}}, now); len(up) != 2 || up[0].Amount != 3 {
		t.Errorf("future-dated: %+v", up)
	}
}

func TestValidate(t *testing.T) {
	if err := Validate([]Point{{Amount: 1}, {Amount: 2, From: now}}); err != nil {
		t.Error(err)
	}
	for _, bad := range [][]Point{{{Amount: -1}}, {{Amount: 1, From: now}, {Amount: 2, From: now}}, {{Amount: 1}, {Amount: 2}}} {
		if err := Validate(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v was accepted", bad)
		}
	}
}
