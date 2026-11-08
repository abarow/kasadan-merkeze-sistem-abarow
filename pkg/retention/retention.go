// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package retention removes, at intervals, the rows a service no longer needs: the
// published outbox messages, old delivery receipts, the register's copies of sales that
// the store server already holds, the raw event content at headquarters. Each service
// cleans only its own schema. The sales ledgers of the store server and headquarters
// (the records) are never removed here. The rules and the reasons are in
// docs/decisions/RETENTION.md.
package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"abarow.com/pkg/delivery"
)

// The environment variables that set the durations. A duration is of the form "90d",
// "36h", "2d12h" (delivery.ParseDuration).
const (
	// EnvInterval is the interval of the cleaning rounds (default 24 hours).
	EnvInterval = "ABAROW_RETENTION_INTERVAL"
	// EnvOutboxKeep is how long a published outbox message is kept (default 7 days).
	EnvOutboxKeep = "ABAROW_OUTBOX_KEEP"
	// EnvReceiptsKeep is how long a given or received delivery receipt is kept
	// (default 180 days).
	EnvReceiptsKeep = "ABAROW_RECEIPTS_KEEP"
	// EnvRegisterKeep is how long the register keeps a finished sale and the details of a
	// closed day (default 90 days).
	EnvRegisterKeep = "ABAROW_REGISTER_KEEP"
	// EnvEventPayloadKeep is how long headquarters keeps the raw content of an event
	// (default 90 days).
	EnvEventPayloadKeep = "ABAROW_EVENT_PAYLOAD_KEEP"
)

// Config is the durations of the cleaning.
type Config struct {
	Interval     time.Duration
	Outbox       time.Duration
	Receipts     time.Duration
	Register     time.Duration
	EventPayload time.Duration
}

// Defaults are the durations used when the environment gives none.
var Defaults = Config{
	Interval:     24 * time.Hour,
	Outbox:       7 * 24 * time.Hour,
	Receipts:     180 * 24 * time.Hour,
	Register:     90 * 24 * time.Hour,
	EventPayload: 90 * 24 * time.Hour,
}

// ConfigFromEnv reads the durations; a value not given is the default. An invalid value
// is an error: the service does not start instead of quietly cleaning with a wrong
// setting.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	c := Defaults
	for _, v := range []struct {
		name string
		to   *time.Duration
		min  time.Duration
	}{
		{EnvInterval, &c.Interval, time.Minute},
		{EnvOutboxKeep, &c.Outbox, time.Hour},
		{EnvReceiptsKeep, &c.Receipts, 24 * time.Hour},
		{EnvRegisterKeep, &c.Register, 24 * time.Hour},
		{EnvEventPayloadKeep, &c.EventPayload, 24 * time.Hour},
	} {
		s := getenv(v.name)
		if s == "" {
			continue
		}
		d, err := delivery.ParseDuration(s)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %q is invalid (example: 90d, 36h): %w", v.name, s, err)
		}
		if d < v.min {
			return Config{}, fmt.Errorf("%s: %q is too short; it must be at least %s", v.name, s, v.min)
		}
		*v.to = d
	}
	return c, nil
}

// CheckReceipts fails if the received receipts would be removed before the stream cleaner
// is done with them: the cleaner deletes an event from the stream only while its receipt
// is recorded, at the earliest cleanMaxAge after the event (delivery.CleanConfig). A week
// is left for a cleaner that was stopped for a while.
func (c Config) CheckReceipts(cleanMaxAge time.Duration) error {
	if least := cleanMaxAge + 7*24*time.Hour; c.Receipts < least {
		return fmt.Errorf("%s (%s) must be at least %s plus 7 days (%s): the stream cleaner needs the receipts until then",
			EnvReceiptsKeep, c.Receipts, delivery.EnvCleanMaxAge, least)
	}
	return nil
}

// Job is one cleaning step of a service.
type Job struct {
	// Name says what is cleaned (in the log).
	Name string
	// Keep is how long the rows are kept; the job removes what is older.
	Keep time.Duration
	// Clean removes the rows older than before and returns how many it removed.
	Clean func(ctx context.Context, before time.Time) (int64, error)
}

// ErrSkipped is returned by a job that removed nothing on purpose this round (for example
// the register keeps its sales while an older event has no receipt). The reason is wrapped.
var ErrSkipped = errors.New("skipped")

// Runner runs the jobs at intervals.
type Runner struct {
	Interval time.Duration
	// Delay is the wait before the first round (so that it does not slow the start).
	Delay time.Duration
	Jobs  []Job
	Log   *slog.Logger
	Now   func() time.Time
}

// Run cleans after Delay and then at every Interval until ctx ends.
func (r Runner) Run(ctx context.Context) {
	if r.Now == nil {
		r.Now = time.Now
	}
	wait := r.Delay
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		r.Round(ctx)
		wait = r.Interval
	}
}

// Round runs every job once. A job that fails is logged and the others still run; the
// next round tries again.
func (r Runner) Round(ctx context.Context) {
	now := r.Now()
	for _, j := range r.Jobs {
		start := time.Now()
		n, err := j.Clean(ctx, now.Add(-j.Keep))
		switch {
		case errors.Is(err, ErrSkipped):
			r.Log.Warn("retention: kept", "job", j.Name, "reason", err)
		case err != nil:
			r.Log.Error("retention failed", "job", j.Name, "removed", n, "err", err)
		case n > 0:
			r.Log.Info("retention", "job", j.Name, "removed", n, "keep", j.Keep, "took", time.Since(start).Round(time.Millisecond))
		}
	}
}

// Batches calls step with limit until a call removes fewer rows than limit and returns the
// total. Each step is its own short statement, so a large first cleaning does not hold
// the service's connection or a lock for long.
func Batches(ctx context.Context, limit int, step func(ctx context.Context, limit int) (int64, error)) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := step(ctx, limit)
		total += n
		if err != nil || n < int64(limit) {
			return total, err
		}
	}
}
