// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package channelsync_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"abarow.com/services/update/internal/channel"
	"abarow.com/services/update/internal/channelsync"
)

var now = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

const dig = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type source struct {
	mu    sync.Mutex
	files map[string][]byte
}

func (s *source) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.files[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(b)
}

func (s *source) put(name string, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := json.Marshal(v)
	s.files["/"+name] = raw
}

func manifest(name string, seq uint64, expires time.Time) channel.Manifest {
	return channel.Manifest{
		Channel: name, Version: "0.4.0", Seq: seq, Issued: now.Add(-time.Hour), Expires: expires, Urgency: "normal",
		Images: []channel.Image{{Unit: "abarow-sales", Repo: "ghcr.io/abarow/abarow-sales", Digest: dig}},
	}
}

func setup(t *testing.T) (*channelsync.Syncer, *source, ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	src := &source{files: map[string][]byte{}}
	srv := httptest.NewServer(src)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	return &channelsync.Syncer{
		BaseURL: srv.URL, Dir: dir, Trust: channel.Trust{channel.Prod: {pub}}, AllowInsecure: true,
		Now: func() time.Time { return now },
	}, src, priv, dir
}

func mustSign(m channel.Manifest, key ed25519.PrivateKey) channel.Signed {
	s, err := channel.Sign(m, channel.Prod, key)
	if err != nil {
		panic(err)
	}
	return s
}

func TestSyncVerifiesAndWrites(t *testing.T) {
	s, src, priv, dir := setup(t)
	signed := mustSign(manifest("prod", 5, now.Add(24*time.Hour)), priv)
	src.put("prod.json", signed)

	res, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res["prod"] != channelsync.Updated || res["pilot"] != channelsync.Missing || res["test"] != channelsync.Missing {
		t.Fatalf("result: %v", res)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "prod.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got channel.Signed
	if json.Unmarshal(raw, &got) != nil || got.Payload != signed.Payload {
		t.Fatalf("dosya: %s", raw)
	}
	// The same manifest counts as unchanged in the second round.
	if res, err := s.Sync(context.Background()); err != nil || res["prod"] != channelsync.Unchanged {
		t.Fatalf("ikinci tur: %v %v", res, err)
	}
	// A newer sequence number replaces the file.
	src.put("prod.json", mustSign(manifest("prod", 6, now.Add(24*time.Hour)), priv))
	if res, err := s.Sync(context.Background()); err != nil || res["prod"] != channelsync.Updated {
		t.Fatalf("yeni tur: %v %v", res, err)
	}
}

// In key rotation TrustFn makes the new key valid without a restart; if it fails the last known is used.
func TestSyncTrustFnRotation(t *testing.T) {
	s, src, priv, _ := setup(t)
	newPub, newPriv, _ := ed25519.GenerateKey(rand.Reader)
	src.put("prod.json", mustSign(manifest("prod", 5, now.Add(24*time.Hour)), newPriv))
	if _, err := s.Sync(context.Background()); err == nil {
		t.Fatal("a manifest signed with an unknown key was accepted")
	}
	pubOld := s.Trust[channel.Prod]
	var failing bool
	s.TrustFn = func(time.Time) (channel.Trust, error) {
		if failing {
			return nil, errors.New("okunamadı")
		}
		return channel.Trust{channel.Prod: append(append([]ed25519.PublicKey{}, pubOld...), newPub)}, nil
	}
	if res, err := s.Sync(context.Background()); err != nil || res["prod"] != channelsync.Updated {
		t.Fatalf("yeni anahtarla: %v %v", res, err)
	}
	// The refresh failed: the last known (old) key still works.
	failing = true
	src.put("prod.json", mustSign(manifest("prod", 6, now.Add(24*time.Hour)), priv))
	if res, err := s.Sync(context.Background()); err != nil || res["prod"] != channelsync.Updated {
		t.Fatalf("on a refresh error: %v %v", res, err)
	}
}

func TestSyncRejectsUntrustedStaleAndRollback(t *testing.T) {
	s, src, priv, dir := setup(t)
	src.put("prod.json", mustSign(manifest("prod", 5, now.Add(24*time.Hour)), priv))
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "prod.json"))

	_, other, _ := ed25519.GenerateKey(rand.Reader)
	for label, v := range map[string]any{
		"wrong key":    mustSign(manifest("prod", 9, now.Add(time.Hour)), other),
		"old sequence": mustSign(manifest("prod", 4, now.Add(time.Hour)), priv),
		"expired":      mustSign(manifest("prod", 9, now.Add(-time.Minute)), priv),
		"bozuk":        map[string]string{"payload": "###"},
	} {
		src.put("prod.json", v)
		res, err := s.Sync(context.Background())
		if err == nil || res["prod"] != "" {
			t.Errorf("%s: kabul edildi (%v, %v)", label, res, err)
		}
		after, _ := os.ReadFile(filepath.Join(dir, "prod.json"))
		if string(after) != string(before) {
			t.Fatalf("%s: the manifest in the directory changed", label)
		}
	}
}

func TestSyncRequiresHTTPSUnlessAllowed(t *testing.T) {
	s := channelsync.Syncer{BaseURL: "http://x", Dir: t.TempDir()}
	if _, err := s.Sync(context.Background()); err == nil {
		t.Fatal("plain HTTP was accepted")
	}
}

func TestSyncSourceErrorsDoNotBlockOtherChannels(t *testing.T) {
	s, src, priv, _ := setup(t)
	src.put("prod.json", mustSign(manifest("prod", 5, now.Add(time.Hour)), priv))
	src.put("pilot.json", map[string]string{"payload": "bozuk"})
	res, err := s.Sync(context.Background())
	if err == nil || res["prod"] != channelsync.Updated {
		t.Fatalf("prod should be fetched, pilot should fail: %v %v", res, err)
	}
}
