// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package retention

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestConfigFromEnv(t *testing.T) {
	c, err := ConfigFromEnv(env(nil))
	if err != nil || c != Defaults {
		t.Fatalf("defaults = %+v, %v", c, err)
	}
	c, err = ConfigFromEnv(env(map[string]string{EnvRegisterKeep: "30d", EnvOutboxKeep: "36h", EnvInterval: "1h"}))
	if err != nil || c.Register != 30*24*time.Hour || c.Outbox != 36*time.Hour || c.Interval != time.Hour || c.Receipts != Defaults.Receipts {
		t.Fatalf("given = %+v, %v", c, err)
	}
	for _, bad := range []map[string]string{
		{EnvRegisterKeep: "ninety"},
		{EnvRegisterKeep: "-3d"},
		{EnvRegisterKeep: "2h"}, // shorter than a day
		{EnvInterval: "10s"},
	} {
		if _, err := ConfigFromEnv(env(bad)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestCheckReceipts(t *testing.T) {
	day := 24 * time.Hour
	if err := Defaults.CheckReceipts(90 * day); err != nil {
		t.Errorf("180 days against a 90-day cleaner: %v", err)
	}
	c := Defaults
	c.Receipts = 30 * day
	if err := c.CheckReceipts(30 * day); err == nil {
		t.Error("the receipts would go before the stream cleaner is done with them")
	}
}

func TestBatches(t *testing.T) {
	ctx := context.Background()
	left := int64(25)
	calls := 0
	n, err := Batches(ctx, 10, func(_ context.Context, limit int) (int64, error) {
		calls++
		k := min(left, int64(limit))
		left -= k
		return k, nil
	})
	if err != nil || n != 25 || calls != 3 {
		t.Fatalf("Batches = %d, %v in %d calls; 25 in 3 expected", n, err, calls)
	}
	// An error stops it and the removed rows are still counted.
	boom := errors.New("boom")
	n, err = Batches(ctx, 10, func(context.Context, int) (int64, error) { return 4, boom })
	if !errors.Is(err, boom) || n != 4 {
		t.Fatalf("Batches with an error = %d, %v", n, err)
	}
}

func TestRoundLogsAndGoesOn(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC)
	var got []time.Time
	r := Runner{Log: slog.New(slog.NewTextHandler(&buf, nil)), Now: func() time.Time { return now }, Jobs: []Job{
		{Name: "skips", Keep: time.Hour, Clean: func(context.Context, time.Time) (int64, error) {
			return 0, fmt.Errorf("%w: an event has no receipt", ErrSkipped)
		}},
		{Name: "fails", Keep: time.Hour, Clean: func(context.Context, time.Time) (int64, error) { return 0, errors.New("db down") }},
		{Name: "works", Keep: 48 * time.Hour, Clean: func(_ context.Context, before time.Time) (int64, error) {
			got = append(got, before)
			return 7, nil
		}},
	}}
	r.Round(context.Background())
	if len(got) != 1 || !got[0].Equal(now.Add(-48*time.Hour)) {
		t.Fatalf("the third job ran with %v; it must run after the others failed, with now - keep", got)
	}
	log := buf.String()
	for _, want := range []string{"level=WARN msg=\"retention: kept\" job=skips", "level=ERROR msg=\"retention failed\" job=fails", "job=works removed=7"} {
		if !strings.Contains(log, want) {
			t.Errorf("log has no %q:\n%s", want, log)
		}
	}
}
