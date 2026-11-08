// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"abarow.com/services/core/device/internal/adapters/out/simulator"
	"abarow.com/services/core/device/internal/app"
	"abarow.com/services/core/device/internal/domain"
)

func scaleService(sc app.Scale) *app.Service {
	svc := app.NewService(simulator.NewPrinter(&bytes.Buffer{}, 32), simulator.Drawer{Log: slog.New(slog.DiscardHandler)}, simulator.NoScanner{})
	if sc != nil {
		svc.WithScale(sc)
	}
	return svc
}

// If the product was just put on, it waits until the scale settles; an empty scale
// is reported immediately; at a register with no scale the weight is not read.
func TestWeigh(t *testing.T) {
	ctx := context.Background()
	sc := simulator.NewScale()
	svc := scaleService(sc)

	if _, err := svc.Weigh(ctx); !errors.Is(err, domain.ErrScaleEmpty) {
		t.Errorf("empty scale: %v", err)
	}
	sc.SetReading(domain.Reading{Grams: 1240, Stable: false})
	go func() {
		time.Sleep(300 * time.Millisecond)
		sc.SetReading(domain.Reading{Grams: 1250, Stable: true})
	}()
	if g, err := svc.Weigh(ctx); err != nil || g != 1250 {
		t.Errorf("durulan terazi: %d %v", g, err)
	}
	for r, want := range map[domain.Reading]error{
		{Grams: -5, Stable: true}:                  domain.ErrScaleNegative,
		{Grams: domain.MaxGrams + 1, Stable: true}: domain.ErrScaleOver,
	} {
		sc.SetReading(r)
		if _, err := svc.Weigh(ctx); !errors.Is(err, want) {
			t.Errorf("%+v: %v", r, err)
		}
	}
	if st := svc.Status(); st.Scale != "simulator" || st.Weighed != 1 {
		t.Errorf("state: %+v", st)
	}

	if _, err := scaleService(nil).Weigh(ctx); !errors.Is(err, domain.ErrNoScale) {
		t.Errorf("terazisiz: %v", err)
	}
	if err := scaleService(nil).SimulateWeight(domain.Reading{}); !errors.Is(err, domain.ErrNoScaleSim) {
		t.Errorf("terazisiz taklit: %v", err)
	}
}

// A scale that never settles says "not stabilized" in 3 seconds.
func TestWeighNeverSettles(t *testing.T) {
	sc := simulator.NewScale()
	sc.SetReading(domain.Reading{Grams: 800, Stable: false})
	start := time.Now()
	if _, err := scaleService(sc).Weigh(context.Background()); !errors.Is(err, domain.ErrScaleMotion) {
		t.Errorf("sallanan terazi: %v", err)
	}
	if d := time.Since(start); d < 2*time.Second || d > 5*time.Second {
		t.Errorf("bekleme %v", d)
	}
}
