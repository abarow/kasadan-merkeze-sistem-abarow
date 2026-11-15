// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package app holds the use case and the ports of the fiscal printer simulator.
package app

import (
	"context"
	"fmt"

	"abarow.com/services/fiscal/simulator/internal/domain"
)

// Counter gives the simulator numbers in order.
type Counter interface {
	Next(ctx context.Context) (int64, error)
}

// Printer prints the receipt text. In the Docker simulation it goes to the device service,
// when running without device it goes to standard output. title shows in the logs.
type Printer interface {
	Print(ctx context.Context, title, text string) error
}

// Drawer is the cash drawer. In reality the drawer is connected to the fiscal printer or to the
// receipt printer and opens when a receipt with cash is printed; the simulator does the same.
// If the drawer cannot be opened the receipt is still valid; the adapter writes the error to the log.
type Drawer interface {
	Open(ctx context.Context, reason string)
}

// Service is the simulator's Register use case.
type Service struct {
	counter Counter
	printer Printer
	drawer  Drawer
	layout  domain.Layout
}

// NewService binds the ports and the receipt layout.
func NewService(counter Counter, printer Printer, drawer Drawer, layout domain.Layout) *Service {
	return &Service{counter: counter, printer: printer, drawer: drawer, layout: layout}
}

// Register validates the receipt, gives it a simulator number and prints the receipt that has no
// fiscal value. If printing fails the number has been used up; a gap in the simulator
// numbers does not matter, the sale's receipt number is separate in sales.
func (s *Service) Register(ctx context.Context, r domain.Receipt) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	n, err := s.counter.Next(ctx)
	if err != nil {
		return "", fmt.Errorf("simülatör numarası alınamadı: %w", err)
	}
	no := domain.FormatFiscalNo(n)
	if err := s.printer.Print(ctx, "Fiş "+no, domain.Render(r, no, s.layout)); err != nil {
		return "", fmt.Errorf("fiş basılamadı: %w", err)
	}
	if r.HasCash() {
		s.drawer.Open(ctx, "nakit satış "+no)
	}
	return no, nil
}
