// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Whether headquarters counts as connected: a single setting (the manager changes it from the screen).
// If on, the store counts as connected: the catalog (products and headquarters prices) comes from
// headquarters and is read-only at the store. If off it counts as disconnected: the catalog is
// managed at the store and headquarters' catalog is not taken. The setting depends not on the live
// network state but on the store's preference; the ownership does not change even if the connection drops briefly.
//
// If headquarters is not installed (no ABAROW_CATALOG_SOURCE) the store is always
// disconnected and the setting cannot be turned on.

// SettingHeadquarters is the key of the setting; its value is "on" or "off".
const SettingHeadquarters = "headquarters"

// ErrNoHeadquarters means headquarters cannot be connected at a store where it is not installed.
var ErrNoHeadquarters = errors.New("bu mağaza merkeze kurulmamış: merkez bağlanamaz (kurulumda --headquarters verilir)")

// Settings is where the store settings are kept.
type Settings interface {
	// Setting returns the value of the setting; ok is false if it was never written.
	Setting(ctx context.Context, key string) (value string, ok bool, err error)
	SetSetting(ctx context.Context, key, value string) error
}

// CatalogStream sets the source of the catalog stream at the store: if connected
// it is fed from headquarters, otherwise it keeps only what is written at the store.
type CatalogStream interface {
	Apply(ctx context.Context, connected bool) error
}

// Headquarters manages the headquarters connection setting.
type Headquarters struct {
	settings  Settings
	stream    CatalogStream
	installed bool
	// ctx and log are what was given to Run; Set starts the background retry
	// with them.
	ctx context.Context
	log *slog.Logger
	// Retry is the interval while waiting for NATS to be ready when setting up the stream.
	Retry time.Duration
}

// NewHeadquarters sets up the settings service. installed says that headquarters is installed;
// stream can be nil (if the stream is not managed).
func NewHeadquarters(settings Settings, stream CatalogStream, installed bool) *Headquarters {
	return &Headquarters{settings: settings, stream: stream, installed: installed, Retry: 2 * time.Second}
}

// Installed says whether headquarters is installed.
func (b *Headquarters) Installed() bool { return b.installed }

// Connected says whether headquarters counts as connected. If headquarters is installed
// and the setting was never changed, it is connected.
func (b *Headquarters) Connected(ctx context.Context) (bool, error) {
	if !b.installed {
		return false, nil
	}
	v, ok, err := b.settings.Setting(ctx, SettingHeadquarters)
	if err != nil {
		return false, err
	}
	return !ok || v != "off", nil
}

// Set changes the setting and applies it to the catalog stream. If the stream cannot be
// applied right away (NATS is down) the setting is still saved; it is retried in the background
// until the stream is applied and applied returns false.
func (b *Headquarters) Set(ctx context.Context, connected bool) (applied bool, err error) {
	if connected && !b.installed {
		return false, ErrNoHeadquarters
	}
	v := "off"
	if connected {
		v = "on"
	}
	if err := b.settings.SetSetting(ctx, SettingHeadquarters, v); err != nil {
		return false, err
	}
	if b.Apply(ctx) == nil {
		return true, nil
	}
	if b.ctx != nil {
		go b.Run(b.ctx, b.log)
	}
	return false, nil
}

// Apply applies the setting to the catalog stream.
func (b *Headquarters) Apply(ctx context.Context) error {
	if b.stream == nil {
		return nil
	}
	connected, err := b.Connected(ctx)
	if err != nil {
		return err
	}
	return b.stream.Apply(ctx, connected)
}

// Bind gives the service's lifetime (ctx) and its log; Set applies the stream
// in the background with them. It is called before the service takes requests.
func (b *Headquarters) Bind(ctx context.Context, log *slog.Logger) { b.ctx, b.log = ctx, log }

// Run tries until the catalog stream is consistent with the setting (NATS may not be
// ready) and then stops. When the setting changes Apply must be called again.
func (b *Headquarters) Run(ctx context.Context, log *slog.Logger) {
	warned := false
	for {
		err := b.Apply(ctx)
		if err == nil {
			return
		}
		if !warned {
			log.Warn("could not set up the catalog stream; will retry", "err", err)
			warned = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(b.Retry):
		}
	}
}
