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
	// An interval given: the rounds go by it, not at a time of day.
	if c.At != "" || c.Daily() != nil {
		t.Errorf("with an interval the rounds are daily: %+v", c)
	}
	if c, err := ConfigFromEnv(env(map[string]string{EnvAt: "22:30", EnvZone: "UTC"})); err != nil || c.At != "22:30" || c.Zone != "UTC" {
		t.Errorf("at = %+v, %v", c, err)
	}
	for _, bad := range []map[string]string{
		{EnvRegisterKeep: "ninety"},
		{EnvRegisterKeep: "-3d"},
		{EnvRegisterKeep: "2h"}, // shorter than a day
		{EnvInterval: "10s"},
		{EnvAt: "25:00"},
		{EnvAt: "3"},
		{EnvAt: "03:00", EnvInterval: "1h"},
		{EnvZone: "Mars/Olympus"},
	} {
		if _, err := ConfigFromEnv(env(bad)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

// TestDaily: the rounds run every day at the given time in the given time zone (by default
// 03:00 in Türkiye, when the registers do not sell).
func TestDaily(t *testing.T) {
	next := Defaults.Daily()
	ist, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ now, want time.Time }{
		{time.Date(2026, 11, 14, 2, 59, 0, 0, ist), time.Date(2026, 11, 14, 3, 0, 0, 0, ist)},
		{time.Date(2026, 11, 14, 3, 0, 0, 0, ist), time.Date(2026, 11, 15, 3, 0, 0, 0, ist)},
		{time.Date(2026, 11, 14, 15, 20, 0, 0, ist), time.Date(2026, 11, 15, 3, 0, 0, 0, ist)},
		// From another zone (UTC 23:30 is 02:30 in Istanbul).
		{time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC), time.Date(2027, 1, 1, 3, 0, 0, 0, ist)},
	} {
		if got := next(c.now); !got.Equal(c.want) {
			t.Errorf("after %s: %s, want %s", c.now, got, c.want)
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

// TestRunnerNext: with Next the rounds run at the times it gives, not by Interval.
func TestRunnerNext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rounds := make(chan struct{}, 10)
	r := Runner{Interval: time.Hour, Delay: time.Hour, Log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		Next: func(now time.Time) time.Time { return now.Add(10 * time.Millisecond) },
		Jobs: []Job{{Name: "t", Clean: func(context.Context, time.Time) (int64, error) { rounds <- struct{}{}; return 0, nil }}}}
	go r.Run(ctx)
	for i := 0; i < 3; i++ {
		select {
		case <-rounds:
		case <-time.After(5 * time.Second):
			t.Fatalf("round %d did not run at the time Next gave", i+1)
		}
	}
}
