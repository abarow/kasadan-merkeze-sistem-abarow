// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"time"

	"abarow.com/services/core/device/internal/domain"
)

// PrinterStatusReader is a printer that can report its state (ESC/POS: DLE EOT;
// simulator: the state that was set). If the printer cannot be reached it returns PrinterOffline,
// if it connects but no state can be obtained PrinterUnknown.
type PrinterStatusReader interface {
	State(ctx context.Context) domain.PrinterState
}

// PrinterSimulator is a printer whose state can be set (trial and tests).
type PrinterSimulator interface {
	SetState(domain.PrinterState)
}

// printerCheckTimeout is the longest time of a state query.
const printerCheckTimeout = 3 * time.Second

// RunPrinterMonitor asks the printer's state every every interval and after every print
// failure; when it changes it notifies the listeners. It stops when ctx ends. For a printer
// that does not report state the state stays unknown.
func (s *Service) RunPrinterMonitor(ctx context.Context, every time.Duration) {
	if _, ok := s.printer.(PrinterStatusReader); !ok {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s.checkPrinter(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.recheck:
		}
	}
}

// askPrinter requests that the state be asked again without waiting (a print failure,
// the paper may have been changed).
func (s *Service) askPrinter() {
	select {
	case s.recheck <- struct{}{}:
	default:
	}
}

func (s *Service) checkPrinter(ctx context.Context) {
	r, ok := s.printer.(PrinterStatusReader)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, printerCheckTimeout)
	defer cancel()
	s.setPrinterState(r.State(ctx))
}

func (s *Service) setPrinterState(st domain.PrinterState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st == s.printerState {
		return
	}
	s.printerState = st
	for ch := range s.printerWatchers {
		// Only the latest state matters: an old unread state is discarded.
		select {
		case <-ch:
		default:
		}
		ch <- st
	}
}

// PrinterState is the printer's last known state.
func (s *Service) PrinterState() domain.PrinterState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.printerState
}

// WatchPrinter gives first the current state, then every change; the channel closes when
// ctx ends. If the listener is slow it misses the intermediate states and gets the
// last one.
func (s *Service) WatchPrinter(ctx context.Context) <-chan domain.PrinterState {
	ch := make(chan domain.PrinterState, 1)
	s.mu.Lock()
	s.printerWatchers[ch] = struct{}{}
	ch <- s.printerState
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		delete(s.printerWatchers, ch)
		s.mu.Unlock()
		close(ch)
	}()
	return ch
}

// SimulatePrinter sets the simulator printer's state; the listeners learn of it
// immediately.
func (s *Service) SimulatePrinter(ctx context.Context, st domain.PrinterState) error {
	sim, ok := s.printer.(PrinterSimulator)
	if !ok {
		return domain.ErrNoPrinterSimulator
	}
	if !st.Valid() {
		return domain.ErrInvalidPrinterState
	}
	sim.SetState(st)
	s.checkPrinter(ctx)
	return nil
}
