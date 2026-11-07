// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package app holds the use cases of the Beko fiscal printer adapter: having the receipt
// recorded on the device and watching the device's connection state.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"abarow.com/services/fiscal/beko/internal/domain"
)

var (
	// ErrNotReady means a sale cannot be sent to the fiscal printer right now: the bridge is
	// closed, the device is not connected, or its fiscal information has not been received yet. Token's
	// documentation requires that no sale is started before the connection is made and the
	// fiscal information is received.
	ErrNotReady = errors.New("ÖKC hazır değil (bağlı değil ya da mali bilgisi alınamadı)")
	// ErrRejected, cihaz sepeti kabul etmedi.
	ErrRejected = errors.New("ÖKC sepeti kabul etmedi")
	// ErrNoResult means the basket was sent but the sale result did not come in time. The result
	// is unknown: the receipt may have been issued. According to Token's documentation the result comes
	// later; if the same sale is sent again, the result that came is used.
	ErrNoResult = errors.New("ÖKC satış sonucunu bildirmedi; fiş kesilmiş olabilir")
	// ErrDeclined means the sale could not be completed on the fiscal printer (e.g. the card was declined).
	ErrDeclined = errors.New("ÖKC satışı tamamlayamadı")
	// ErrVoided means the sale was cancelled on the fiscal printer.
	ErrVoided = errors.New("ÖKC fişi iptal etti")
)

// Event is an event coming from the bridge (a hubd frame).
type Event struct {
	// Kind: ready, state, serial, fatal, exit.
	Kind, Ref string
	Payload   []byte
}

// Device is the bridge operation (hubd, which wraps Token's library).
type Device interface {
	// Call performs an operation (basket, payment, fiscalinfo, device, reconnect)
	// and returns its result.
	Call(ctx context.Context, op string, payload []byte) ([]byte, error)
	// Events are the events coming from the device.
	Events() <-chan Event
}

// maxResults is the number of sale results remembered.
const maxResults = 256

// Service holds the fiscal printer use cases.
type Service struct {
	dev     Device
	cfg     domain.Config
	log     *slog.Logger
	timeout time.Duration
	retry   time.Duration

	// sale puts the sales in a queue: there is only one sale at a time on the device.
	sale sync.Mutex

	mu         sync.Mutex
	ready      bool
	refreshing bool
	results    map[string]domain.SaleStatus
	order      []string
	waiters    map[string]chan domain.SaleStatus
}

// NewService binds the port. timeout is how long to wait from sending the basket
// to the arrival of the sale result (including the card charge).
func NewService(dev Device, cfg domain.Config, timeout, retry time.Duration, log *slog.Logger) *Service {
	return &Service{dev: dev, cfg: cfg, log: log, timeout: timeout, retry: retry,
		results: map[string]domain.SaleStatus{}, waiters: map[string]chan domain.SaleStatus{}}
}

// Ready says that a sale can be sent to the fiscal printer.
func (s *Service) Ready() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return ErrNotReady
	}
	return nil
}

func (s *Service) setReady(v bool) {
	s.mu.Lock()
	s.ready = v
	s.mu.Unlock()
}

// Run processes the bridge's events until ctx ends.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-s.dev.Events():
			s.handle(ctx, ev)
		}
	}
}

func (s *Service) handle(ctx context.Context, ev Event) {
	switch ev.Kind {
	case "ready":
		s.log.Info("the bridge opened; waiting for the device")
		go s.refresh(ctx)
	case "state":
		connected := ev.Ref == "1"
		s.log.Info("fiscal printer connection state", "connected", connected, "device", string(ev.Payload))
		if connected {
			go s.refresh(ctx)
		} else {
			s.setReady(false)
		}
	case "serial":
		switch ev.Ref {
		case "3":
			st, err := domain.ParseSaleStatus(ev.Payload)
			if err != nil {
				s.log.Error("could not read the sale state", "err", err)
				return
			}
			s.deliver(st)
		case "9":
			// A basket or a payment was sent while the sale screen was not open.
			s.log.Warn("a packet that came while the fiscal printer sale screen was not open", "data", string(ev.Payload))
		default:
			s.log.Debug("fiscal printer message", "ref", ev.Ref, "data", string(ev.Payload))
		}
	case "fatal":
		s.log.Error("could not start the bridge", "reason", string(ev.Payload))
		s.setReady(false)
	case "exit":
		s.log.Warn("the bridge closed; it will be restarted")
		s.setReady(false)
	}
}

// refresh tries until it gets the device's fiscal information; once it does the fiscal printer is ready.
func (s *Service) refresh(ctx context.Context) {
	s.mu.Lock()
	if s.refreshing {
		s.mu.Unlock()
		return
	}
	s.refreshing = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.refreshing = false
		s.mu.Unlock()
	}()
	for ctx.Err() == nil {
		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		out, err := s.dev.Call(callCtx, "fiscalinfo", nil)
		cancel()
		var info map[string]any
		if err == nil && json.Unmarshal(out, &info) == nil && len(info) > 0 {
			s.log.Info("fiscal printer ready", "sections", info["sectionCount"])
			s.setReady(true)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.retry):
		}
	}
}

// deliver stores the sale result that came from the device and passes it on to whoever is waiting.
func (s *Service) deliver(st domain.SaleStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.results[st.BasketID]; !ok {
		s.order = append(s.order, st.BasketID)
		if len(s.order) > maxResults {
			delete(s.results, s.order[0])
			s.order = s.order[1:]
		}
	}
	s.results[st.BasketID] = st
	if w, ok := s.waiters[st.BasketID]; ok {
		select {
		case w <- st:
		default:
		}
	}
}

// take returns the result of the sale and forgets it (so that a failed result does not
// block a retry; so that a successful result is reused when sent again,
// only failures are forgotten).
func (s *Service) take(id string) (domain.SaleStatus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.results[id]
	if ok && st.Status != domain.StatusOK {
		delete(s.results, id)
		for i, k := range s.order {
			if k == id {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
	}
	return st, ok
}

// Register has the receipt recorded on the fiscal printer and returns its fiscal number. If the same
// sale is sent again (e.g. the first response was lost when the connection dropped), the result the
// fiscal printer reported for that sale is used; the receipt is not issued a second time.
func (s *Service) Register(ctx context.Context, r domain.Receipt) (string, error) {
	s.sale.Lock()
	defer s.sale.Unlock()

	if st, ok := s.take(r.SaleID); ok {
		return finish(st)
	}
	if err := s.Ready(); err != nil {
		return "", err
	}
	basket, err := domain.NewBasket(r, s.cfg)
	if err != nil {
		return "", err
	}
	data, err := basket.JSON()
	if err != nil {
		return "", err
	}

	wait := make(chan domain.SaleStatus, 1)
	s.mu.Lock()
	s.waiters[r.SaleID] = wait
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.waiters, r.SaleID)
		s.mu.Unlock()
	}()

	out, err := s.dev.Call(ctx, "basket", data)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNotReady, err)
	}
	if string(out) != "1" {
		return "", ErrRejected
	}
	select {
	case st := <-wait:
		s.take(r.SaleID)
		return finish(st)
	case <-time.After(s.timeout):
		return "", ErrNoResult
	case <-ctx.Done():
		return "", fmt.Errorf("%w: %v", ErrNoResult, ctx.Err())
	}
}

func finish(st domain.SaleStatus) (string, error) {
	switch st.Status {
	case domain.StatusOK:
		return st.FiscalNo(), nil
	case domain.StatusVoided:
		return "", fmt.Errorf("%w: %s", ErrVoided, st.Message)
	default:
		return "", fmt.Errorf("%w: %s (durum %d)", ErrDeclined, st.Message, st.Status)
	}
}
