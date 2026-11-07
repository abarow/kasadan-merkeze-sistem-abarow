// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package podman_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"abarow.com/services/update/internal/adapters/out/podman"
	"abarow.com/services/update/internal/channel"
)

// Test against the real Podman API. It runs only if the environment variables are
// given (like the Postgres tests):
//
//	ABAROW_TEST_PODMAN_URL     Podman API address (podman system service tcp:...), e.g. http://127.0.0.1:8888
//	ABAROW_TEST_PODMAN_MIRROR  the mirror Podman can see (address:port), e.g. zot:5000
//	ABAROW_TEST_PODMAN_REPO    the repo path on the mirror without the channel prefix (e.g. abarow-x); looked up as "<mirror>/prod/<repo>"
//	ABAROW_TEST_PODMAN_DIGEST  the manifest digest of that image (sha256:...)
func TestRealPodmanPull(t *testing.T) {
	url, mirror, repo, digest := os.Getenv("ABAROW_TEST_PODMAN_URL"), os.Getenv("ABAROW_TEST_PODMAN_MIRROR"), os.Getenv("ABAROW_TEST_PODMAN_REPO"), os.Getenv("ABAROW_TEST_PODMAN_DIGEST")
	if url == "" || mirror == "" || repo == "" || digest == "" {
		t.Skip("gerçek Podman için ABAROW_TEST_PODMAN_* verilmedi")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "abarow-x.container"), []byte("[Container]\nContainerName=abarow-x\nImage=ghcr.io/abarow/abarow-x:0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := podman.New(podman.Options{
		BaseURL: url, UnitsDir: dir, Systemd: &fakeSystemd{active: true},
		Token: func() (string, bool) { return "token", true }, Mirror: mirror, Channel: "prod",
	})
	if err != nil {
		t.Fatal(err)
	}
	img := channel.Image{Unit: "abarow-x", Repo: "ghcr.io/abarow/" + repo, Digest: digest}
	ctx := context.Background()
	if err := rt.Pull(ctx, img); err != nil {
		t.Fatalf("gerçek Podman çekmesi: %v", err)
	}
	// With no running container Current uses the pinned digest: pin with Switch first.
	if err := rt.Switch(ctx, img); err != nil {
		t.Fatal(err)
	}
	cur, err := rt.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := cur["abarow-x"]; got.Digest != digest || !strings.HasPrefix(got.Repo, mirror+"/prod/") {
		t.Errorf("Current: %+v", got)
	}
	// Wrong digest: Podman cannot pull, it returns an error.
	bad := img
	bad.Digest = "sha256:" + strings.Repeat("0", 64)
	if err := rt.Pull(ctx, bad); err == nil {
		t.Error("olmayan digest çekildi")
	}
}
