// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"

	"abarow.com/services/core/device/internal/domain"
)

// Scanner is a barcode reader (simulator or evdev). Run reads the reader until ctx
// ends and gives every code read to emit; when the reader is disconnected it
// reconnects itself.
type Scanner interface {
	// Name is the reader type ("simulator", "evdev", "none").
	Name() string
	Run(ctx context.Context, emit func(code string))
}

// ScanSimulator is a reader whose scans can be imitated (trial and tests).
type ScanSimulator interface {
	Simulate(code string)
}

// watchBuffer is the number of reads a listener can have waiting; if it fills that
// listener misses the read, the reader and the other listeners do not wait.
const watchBuffer = 16

// RunScanner runs the reader until ctx ends; valid codes go to the listeners,
// an invalid read is discarded.
func (s *Service) RunScanner(ctx context.Context) {
	s.scanner.Run(ctx, s.publish)
}

func (s *Service) publish(code string) {
	if domain.ValidateScan(code) != nil {
		s.rejected.Add(1)
		return
	}
	s.scanned.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.watchers {
		select {
		case ch <- code:
		default:
		}
	}
}

// Watch gives the codes read until ctx ends; the channel closes when ctx ends.
// A code read while no one is listening is lost.
func (s *Service) Watch(ctx context.Context) <-chan string {
	ch := make(chan string, watchBuffer)
	s.mu.Lock()
	s.watchers[ch] = struct{}{}
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		delete(s.watchers, ch)
		s.mu.Unlock()
		close(ch)
	}()
	return ch
}

// Simulate imitates a scan on the simulator reader.
func (s *Service) Simulate(code string) error {
	sim, ok := s.scanner.(ScanSimulator)
	if !ok {
		return domain.ErrNoSimulator
	}
	if err := domain.ValidateScan(code); err != nil {
		return err
	}
	sim.Simulate(code)
	return nil
}
