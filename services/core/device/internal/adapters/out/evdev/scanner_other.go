// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package evdev

import (
	"context"
	"log/slog"
	"time"

	"abarow.com/services/core/device/internal/app"
)

// Scanner: evdev exists only on Linux; on another system the reader is not
// read (so that it can be compiled on a development machine).
type Scanner struct {
	Path  string
	Log   *slog.Logger
	Retry time.Duration
}

var _ app.Scanner = (*Scanner)(nil)

func (s *Scanner) Name() string { return "evdev" }

func (s *Scanner) Run(ctx context.Context, _ func(string)) {
	s.Log.Error("the barcode reader (evdev) is read only on Linux", "path", s.Path)
	<-ctx.Done()
}
