// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package delivery

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The environment variables that set the durations. A duration is of the form "90d" (days), "36h", "1h30m",
// "2d12h".
const (
	// EnvCleanMaxAge is the least time an event is kept even if its receipt has come
	// (default by service: 30 days at the register, 90 days at the store server).
	EnvCleanMaxAge = "ABAROW_CLEAN_MAX_AGE"
	// EnvCleanInterval is the interval of the cleaning rounds (default 1 minute).
	EnvCleanInterval = "ABAROW_CLEAN_INTERVAL"
	// EnvCleanBudget is the largest number of messages examined in a round (default 20000).
	EnvCleanBudget = "ABAROW_CLEAN_BUDGET"
	// EnvReceiptMaxAge is the longest a receipt stays in the receipt stream
	// (default 30 days).
	EnvReceiptMaxAge = "ABAROW_RECEIPT_MAX_AGE"
)

// DefaultInterval is the default interval of the cleaning rounds; it need not be
// instant.
const DefaultInterval = time.Minute

// CleanConfig is the cleaner's settings.
type CleanConfig struct {
	// MaxAge is the least time an event is kept even if its receipt has come.
	MaxAge time.Duration
	// Interval is the interval of the rounds.
	Interval time.Duration
	// Budget is the largest number of messages examined in a round (0: default).
	Budget int
}

// CleanConfigFromEnv reads the cleaner settings from the environment variables;
// defaultMaxAge and the defaults are used for a value not given. An invalid
// value is an error: the service does not start instead of quietly running with a wrong setting.
func CleanConfigFromEnv(getenv func(string) string, defaultMaxAge time.Duration) (CleanConfig, error) {
	c := CleanConfig{MaxAge: defaultMaxAge, Interval: DefaultInterval}
	var err error
	if v := getenv(EnvCleanMaxAge); v != "" {
		if c.MaxAge, err = positiveDuration(EnvCleanMaxAge, v, time.Second); err != nil {
			return CleanConfig{}, err
		}
	}
	if v := getenv(EnvCleanInterval); v != "" {
		if c.Interval, err = positiveDuration(EnvCleanInterval, v, time.Second); err != nil {
			return CleanConfig{}, err
		}
	}
	if v := getenv(EnvCleanBudget); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return CleanConfig{}, fmt.Errorf("%s: %q is invalid; it must be a number 1 or greater", EnvCleanBudget, v)
		}
		c.Budget = n
	}
	return c, nil
}

// ReceiptMaxAgeFromEnv reads the receipt stream's retention time (ReceiptMaxAge if
// not given).
func ReceiptMaxAgeFromEnv(getenv func(string) string) (time.Duration, error) {
	v := getenv(EnvReceiptMaxAge)
	if v == "" {
		return ReceiptMaxAge, nil
	}
	return positiveDuration(EnvReceiptMaxAge, v, time.Minute)
}

func positiveDuration(name, v string, min time.Duration) (time.Duration, error) {
	d, err := ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is invalid (example: 90d, 36h, 1h30m): %w", name, v, err)
	}
	if d < min {
		return 0, fmt.Errorf("%s: %q is too short; it must be at least %s", name, v, min)
	}
	return d, nil
}

// ParseDuration reads Go's duration format with the addition of days: "90d", "2d12h", "36h",
// "1h30m". A negative or empty duration is an error.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	var days time.Duration
	if i := strings.IndexByte(s, 'd'); i >= 0 {
		n, err := strconv.ParseUint(s[:i], 10, 32)
		if err != nil {
			return 0, fmt.Errorf("could not read the number of days")
		}
		days = time.Duration(n) * 24 * time.Hour
		s = s[i+1:]
		if s == "" {
			return days, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d < 0 || strings.HasPrefix(s, "-") {
		return 0, fmt.Errorf("a duration cannot be negative")
	}
	return days + d, nil
}
