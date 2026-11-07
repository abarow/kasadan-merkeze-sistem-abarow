// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"time"

	"abarow.com/services/core/device/internal/domain"
)

// Scale is the scale at the register (simulator or serial port). Read makes one reading:
// the weight and whether it is stable. If the scale reports a problem instead of a weight
// (negative, over capacity, error) a domain error is returned.
type Scale interface {
	// Name is the scale type ("simulator", "toledo8217", "nci").
	Name() string
	Read(ctx context.Context) (domain.Reading, error)
}

// ScaleSimulator is a scale whose reading can be set (trial and tests).
type ScaleSimulator interface {
	SetReading(domain.Reading)
}

const (
	// weighFor is the longest time to wait for a stable weight: if the product was
	// just put on the scale it settles within a few readings.
	weighFor = 3 * time.Second
	// weighEvery is the wait between two readings.
	weighEvery = 200 * time.Millisecond
)

// WithScale connects the scale to the service (not called at a register with no scale).
func (s *Service) WithScale(sc Scale) *Service {
	s.scale = sc
	return s
}

// Weigh reads the stable weight of the product on the scale in grams. If the scale
// is swinging it re-reads for at most 3 seconds; if empty, negative or over
// capacity it returns an error immediately.
func (s *Service) Weigh(ctx context.Context) (int64, error) {
	if s.scale == nil {
		return 0, domain.ErrNoScale
	}
	ctx, cancel := context.WithTimeout(ctx, weighFor)
	defer cancel()
	for {
		r, err := s.scale.Read(ctx)
		if err == nil {
			err = r.Check()
		}
		if err == nil {
			s.weighed.Add(1)
			return r.Grams, nil
		}
		if !errors.Is(err, domain.ErrScaleMotion) {
			return 0, err
		}
		select {
		case <-ctx.Done():
			return 0, domain.ErrScaleMotion
		case <-time.After(weighEvery):
		}
	}
}

// SimulateWeight sets the simulator scale's reading.
func (s *Service) SimulateWeight(r domain.Reading) error {
	sim, ok := s.scale.(ScaleSimulator)
	if !ok {
		return domain.ErrNoScaleSim
	}
	sim.SetReading(r)
	return nil
}

func (s *Service) scaleName() string {
	if s.scale == nil {
		return "none"
	}
	return s.scale.Name()
}
