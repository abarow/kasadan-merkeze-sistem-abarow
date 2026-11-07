// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type memSettings struct {
	mu  sync.Mutex
	m   map[string]string
	err error
}

func (s *memSettings) Setting(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok, s.err
}

func (s *memSettings) SetSetting(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[key] = value
	return nil
}

// stream records what is applied to the catalog stream; the application fails
// failures times.
type stream struct {
	mu       sync.Mutex
	failures int
	calls    []bool
}

func (s *stream) Apply(_ context.Context, connected bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures > 0 {
		s.failures--
		return errors.New("nats: no responders available for request")
	}
	s.calls = append(s.calls, connected)
	return nil
}

func (s *stream) last() (connected, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return false, false
	}
	return s.calls[len(s.calls)-1], true
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// If headquarters is not installed the store is always disconnected and cannot connect.
func TestHeadquartersNotInstalled(t *testing.T) {
	ctx := context.Background()
	b := NewHeadquarters(&memSettings{}, &stream{}, false)
	if c, err := b.Connected(ctx); err != nil || c {
		t.Errorf("connected while not installed: %v %v", c, err)
	}
	if _, err := b.Set(ctx, true); !errors.Is(err, ErrNoHeadquarters) {
		t.Errorf("connecting while not installed: %v", err)
	}
	// Keeping it off is allowed.
	if applied, err := b.Set(ctx, false); err != nil || !applied {
		t.Errorf("kapatma: %v %v", applied, err)
	}
}

// If headquarters is installed it is connected while the setting was never changed; the manager can turn it off
// and on, and every change is applied to the catalog stream.
func TestHeadquartersToggle(t *testing.T) {
	ctx := context.Background()
	st := &stream{}
	b := NewHeadquarters(&memSettings{}, st, true)

	if c, _ := b.Connected(ctx); !c {
		t.Fatal("an installed headquarters should count as connected by default")
	}
	if applied, err := b.Set(ctx, false); err != nil || !applied {
		t.Fatalf("kapatma: %v %v", applied, err)
	}
	if c, _ := b.Connected(ctx); c {
		t.Error("turned off but connected")
	}
	if got, ok := st.last(); !ok || got {
		t.Errorf("the stream was not put in disconnected mode: %v %v", got, ok)
	}
	if applied, err := b.Set(ctx, true); err != nil || !applied {
		t.Fatalf("turning on: %v %v", applied, err)
	}
	if c, _ := b.Connected(ctx); !c {
		t.Error("turned on but disconnected")
	}
	if got, ok := st.last(); !ok || !got {
		t.Errorf("the stream was not put in connected mode: %v %v", got, ok)
	}
}

func TestHeadquartersSettingError(t *testing.T) {
	ctx := context.Background()
	b := NewHeadquarters(&memSettings{err: errors.New("veritabanı kapalı")}, &stream{}, true)
	if _, err := b.Connected(ctx); err == nil {
		t.Error("the read error was swallowed")
	}
	if _, err := b.Set(ctx, false); err == nil {
		t.Error("the write error was swallowed")
	}
}

// While NATS is not ready the setting is still saved; it is applied once the stream is ready.
func TestHeadquartersAppliesWhenNATSArrives(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := &stream{failures: 1}
	b := NewHeadquarters(&memSettings{}, st, true)
	b.Retry = 5 * time.Millisecond
	b.Bind(ctx, quiet)

	applied, err := b.Set(ctx, false)
	if err != nil || applied {
		t.Fatalf("NATS yokken Set: applied=%v err=%v", applied, err)
	}
	if c, _ := b.Connected(ctx); c {
		t.Error("ayar kaydedilmedi")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got, ok := st.last(); ok && !got {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the stream was not applied in the background")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Run tries until the first application succeeds and stops; it stops when ctx ends too.
func TestHeadquartersRun(t *testing.T) {
	st := &stream{failures: 3}
	b := NewHeadquarters(&memSettings{}, st, true)
	b.Retry = time.Millisecond
	done := make(chan struct{})
	go func() { b.Run(context.Background(), quiet); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after a successful application")
	}
	if got, ok := st.last(); !ok || !got {
		t.Errorf("the connected mode should be applied at start: %v %v", got, ok)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stuck := NewHeadquarters(&memSettings{}, &stream{failures: 1 << 30}, true)
	stuck.Retry = time.Millisecond
	done = make(chan struct{})
	go func() { stuck.Run(ctx, quiet); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop when ctx ended")
	}
}

// If the stream is not managed (nil) Apply does nothing.
func TestHeadquartersNoStream(t *testing.T) {
	b := NewHeadquarters(&memSettings{}, nil, true)
	if err := b.Apply(context.Background()); err != nil {
		t.Error(err)
	}
	if !b.Installed() {
		t.Error("an installed headquarters should be Installed")
	}
}
