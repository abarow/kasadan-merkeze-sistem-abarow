// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package dryrun is the dry-run implementation of release.Runtime that does not touch real
// containers: it keeps the units and their images in a file and imitates downloading and
// switching. It is for end-to-end tests and development; a real install uses
// ABAROW_UPDATE_RUNTIME=podman. It downloads or restarts nothing, so it is not
// evidence of security or availability.
package dryrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"abarow.com/services/update/internal/channel"
)

// Runtime is the dry-run runtime.
type Runtime struct {
	path string
	// bad are the digests that do not come up healthy (for rollback tests).
	bad map[string]bool
	// PullDelay is the wait time that imitates downloading.
	PullDelay time.Duration

	mu    sync.Mutex
	units map[string]channel.Image
}

// New reads the state in the file; if there is no file it starts with initial. initial is
// "unit=repo@sha256:..." pairs (comma-separated); bad is comma-separated digests.
func New(path, initial, bad string) (*Runtime, error) {
	r := &Runtime{path: path, bad: map[string]bool{}, units: map[string]channel.Image{}, PullDelay: time.Second}
	for _, d := range strings.Split(bad, ",") {
		if d = strings.TrimSpace(d); d != "" {
			r.bad[d] = true
		}
	}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &r.units); err != nil {
			return nil, fmt.Errorf("%s bozuk: %w", path, err)
		}
		return r, nil
	}
	for _, kv := range strings.Split(initial, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		unit, ref, ok := strings.Cut(kv, "=")
		repo, digest, ok2 := strings.Cut(ref, "@")
		if !ok || !ok2 {
			return nil, fmt.Errorf("deneme birimi geçersiz: %q (birim=depo@sha256:... bekleniyordu)", kv)
		}
		r.units[unit] = channel.Image{Unit: unit, Repo: repo, Digest: digest}
	}
	return r, r.save()
}

func (r *Runtime) save() error {
	raw, err := json.Marshal(r.units)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(r.path, raw, 0o600)
}

// Current is the images of the imitated units.
func (r *Runtime) Current(context.Context) (map[string]channel.Image, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]channel.Image, len(r.units))
	for k, v := range r.units {
		out[k] = v
	}
	return out, nil
}

// Pull, indirmeyi taklit eder.
func (r *Runtime) Pull(ctx context.Context, img channel.Image) error {
	r.mu.Lock()
	_, ok := r.units[img.Unit]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("%s bu makinede kurulu değil", img.Unit)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(r.PullDelay):
		return nil
	}
}

// Switch changes the unit's image.
func (r *Runtime) Switch(_ context.Context, img channel.Image) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.units[img.Unit]; !ok {
		return errors.New(img.Unit + " bu makinede kurulu değil")
	}
	r.units[img.Unit] = img
	return r.save()
}

// Healthy: the image is healthy if it is not in the "bad" list.
func (r *Runtime) Healthy(_ context.Context, unit string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	img, ok := r.units[unit]
	if !ok {
		return false, errors.New(unit + " bu makinede kurulu değil")
	}
	return !r.bad[img.Digest], nil
}
