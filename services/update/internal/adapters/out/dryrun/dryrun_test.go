// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package dryrun_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"abarow.com/services/update/internal/adapters/out/dryrun"
	"abarow.com/services/update/internal/channel"
)

const (
	d1 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	d2 = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

func TestRuntime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.json")
	r, err := dryrun.New(path, "abarow-sales=ghcr.io/abarow/abarow-sales@"+d1, d2)
	if err != nil {
		t.Fatal(err)
	}
	r.PullDelay = time.Millisecond
	ctx := context.Background()
	cur, _ := r.Current(ctx)
	if cur["abarow-sales"].Digest != d1 {
		t.Fatalf("start: %+v", cur)
	}
	if ok, _ := r.Healthy(ctx, "abarow-sales"); !ok {
		t.Fatal("unhealthy at the start")
	}
	img := channel.Image{Unit: "abarow-sales", Repo: "ghcr.io/abarow/abarow-sales", Digest: d2}
	if err := r.Pull(ctx, img); err != nil {
		t.Fatal(err)
	}
	if err := r.Switch(ctx, img); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.Healthy(ctx, "abarow-sales"); ok {
		t.Fatal("a digest on the unhealthy list was taken as healthy")
	}
	// The state persists in the file.
	r2, err := dryrun.New(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if cur, _ := r2.Current(ctx); cur["abarow-sales"].Digest != d2 {
		t.Fatalf("the state is not persisted: %+v", cur)
	}
	// Kurulu olmayan birim.
	if err := r.Pull(ctx, channel.Image{Unit: "abarow-yok", Digest: d1}); err == nil {
		t.Error("a unit that is not installed was pulled")
	}
	if _, err := dryrun.New(filepath.Join(t.TempDir(), "x.json"), "bozuk", ""); err == nil {
		t.Error("a broken start was accepted")
	}
}
