// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package app is the application layer of the store server's report service: it builds the store
// stream from the registers, processes the sale events into the report database,
// gives the registers' state. The store report is in the shared report core
// (pkg/report; Summaries).
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"abarow.com/pkg/ledger"
	"abarow.com/pkg/nodes"
	"abarow.com/services/store/internal/domain"
)

// StoreStream is the port of the store stream: the registers' sales streams flow
// into this stream as sources over the leaf connection.
type StoreStream interface {
	// Ensure sets up or updates the stream so that it takes these registers as its sources.
	// The events that came from a register removed from the list stay in the stream.
	Ensure(ctx context.Context, registerIDs []string) error
	// Sources are the source states by register id; empty if the
	// stream has not been set up yet.
	Sources(ctx context.Context) (map[string]domain.Source, error)
}

// Reports is the port of the report database.
type Reports interface {
	// SaveSale and SaveVoid change nothing if the same sale comes a second time
	// (events come at least once).
	SaveSale(ctx context.Context, s domain.Sale) error
	SaveVoid(ctx context.Context, v domain.Void) error
	// SaveUnfinished records a sale whose receipt could not be issued; if the same sale comes
	// again it updates the last attempt and its reason.
	SaveUnfinished(ctx context.Context, u ledger.Unfinished) error
	// SaveShift records the shift: an opening event adds the shift (does nothing
	// if it exists), a closing event writes the closing details.
	SaveShift(ctx context.Context, s domain.Shift) error
	// Report is the report of the sales ledger (pkg/ledger).
	Report(ctx context.Context, f ledger.Filter) (ledger.Report, error)
	// Activity is the summary of the sales that came, by register id.
	Activity(ctx context.Context) (map[string]domain.Activity, error)
}

// Service holds the use cases of the report service.
type Service struct {
	store   StoreStream
	reports Reports
	regs    []nodes.Node
	loc     *time.Location
	// RetryInterval is the retry interval if the store stream cannot be set up
	// (NATS is down).
	RetryInterval time.Duration
	now           func() time.Time
}

// NewService returns a service with the registers in registers.json; days are
// computed in the store's time zone (loc).
func NewService(store StoreStream, reports Reports, regs []nodes.Node, loc *time.Location) *Service {
	return &Service{store: store, reports: reports, regs: regs, loc: loc, RetryInterval: 2 * time.Second, now: time.Now}
}

// SetupStore sets up the store stream; it tries until NATS is ready. It sets it up
// even if there is no connected register (the registers' sources are added once a register is added
// and the service restarted).
func (s *Service) SetupStore(ctx context.Context, log *slog.Logger) error {
	// It is set up even with no register: the store server's own events (store
	// prices) go to headquarters from this stream too.
	if len(s.regs) == 0 {
		log.Warn("no register is connected to the store server (registers.json is empty); the registers' events will come once a register is added and the service restarted")
	}
	ids := make([]string, len(s.regs))
	for i, r := range s.regs {
		ids[i] = r.ID
	}
	for {
		err := s.store.Ensure(ctx, ids)
		if err == nil {
			log.Info("the store stream is ready", "registers", ids)
			return nil
		}
		log.Warn("could not set up the store stream; will retry", "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.RetryInterval):
		}
	}
}

// RecordSale processes a completed sale coming from a register into the report.
func (s *Service) RecordSale(ctx context.Context, sale domain.Sale) error {
	if err := sale.Validate(); err != nil {
		return fmt.Errorf("satış %q: %w", sale.ID, err)
	}
	return s.reports.SaveSale(ctx, sale)
}

// RecordVoid processes a cancellation coming from a register into the report.
func (s *Service) RecordVoid(ctx context.Context, v domain.Void) error {
	if err := v.Validate(); err != nil {
		return fmt.Errorf("iptal %q: %w", v.SaleID, err)
	}
	return s.reports.SaveVoid(ctx, v)
}

// RecordUnfinished records a sale whose payment was taken but whose receipt could not be issued
// (shown until it is completed or refunded at the register).
func (s *Service) RecordUnfinished(ctx context.Context, u ledger.Unfinished) error {
	if err := u.Validate(); err != nil {
		return fmt.Errorf("fişi kesilemeyen satış %q: %w", u.SaleID, err)
	}
	return s.reports.SaveUnfinished(ctx, u)
}

// RecordShift records a shift opening or closing coming from a register.
func (s *Service) RecordShift(ctx context.Context, sh domain.Shift) error {
	if err := sh.Validate(); err != nil {
		return fmt.Errorf("vardiya %s/%d: %w", sh.RegisterID, sh.ID, err)
	}
	return s.reports.SaveShift(ctx, sh)
}

// RegisterName is the register's name shown on the screen; its id if it is not in the list.
func (s *Service) RegisterName(id string) string {
	for _, r := range s.regs {
		if r.ID == id {
			return r.DisplayName()
		}
	}
	return id
}

// Registers is the registers' state (in the order of registers.json).
func (s *Service) Registers(ctx context.Context) ([]domain.RegisterStatus, error) {
	sources, err := s.store.Sources(ctx)
	if err != nil {
		return nil, err
	}
	activity, err := s.reports.Activity(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]domain.RegisterStatus, len(s.regs))
	for i, r := range s.regs {
		var src *domain.Source
		if v, ok := sources[r.ID]; ok {
			src = &v
		}
		out[i] = domain.Status(r.ID, r.DisplayName(), src, now)
		out[i].Activity = activity[r.ID]
	}
	return out, nil
}
