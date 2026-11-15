// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package channelsync takes the channel manifests served by the publisher's registration
// server from a source (the files the promotion workflow uploads to the release page)
// into the channel directory. The source is not trusted: each manifest is verified with the
// trusted channel keys as the agents would; a manifest that has expired or has an old
// sequence number is not taken; the file in the directory is replaced only with a verified
// manifest and atomically.
package channelsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"abarow.com/services/update/internal/channel"
	"abarow.com/services/update/internal/token"
)

// maxBody is the manifest file limit.
const maxBody = 64 << 10

// Channels are the channels that are synchronized.
var Channels = []string{"test", "pilot", "prod"}

// Syncer takes the channel manifests from the source into the directory.
type Syncer struct {
	// BaseURL is the address where the "<channel>.json" files are (https://.../download).
	BaseURL string
	// Dir is the publisher's channel directory (the registration server serves from here).
	Dir   string
	Trust channel.Trust
	// If TrustFn is given it refreshes the trusted keys in every sync round (in key rotation a key whose
	// transition period is over drops out by itself). If it fails Trust (the last known) is used.
	TrustFn func(now time.Time) (channel.Trust, error)
	// AllowInsecure allows a plain HTTP source (testing only).
	AllowInsecure bool
	Client        *http.Client
	Now           func() time.Time
	Log           *slog.Logger
}

// Result is the per-channel result of a sync round.
type Result map[string]string

// Result values.
const (
	Updated   = "updated"
	Unchanged = "unchanged"
	Missing   = "missing"
)

// Sync fetches the manifest for each channel, verifies it and writes it to the directory. One channel's
// failure does not block the others; the errors are joined and returned.
func (s Syncer) Sync(ctx context.Context) (Result, error) {
	if !strings.HasPrefix(s.BaseURL, "https://") && !(s.AllowInsecure && strings.HasPrefix(s.BaseURL, "http://")) {
		return nil, errors.New("the channel source must start with https://")
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	res := Result{}
	var errs []error
	for _, ch := range Channels {
		r, err := s.syncOne(ctx, client, now(), ch)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ch, err))
			continue
		}
		res[ch] = r
	}
	return res, errors.Join(errs...)
}

func (s Syncer) syncOne(ctx context.Context, client *http.Client, now time.Time, ch string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.BaseURL, "/")+"/"+ch+".json", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Missing, nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the source returned %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxBody {
		return "", errors.New("the manifest is too large")
	}
	var signed channel.Signed
	if err := json.Unmarshal(raw, &signed); err != nil {
		return "", fmt.Errorf("could not read the manifest: %w", err)
	}
	last := s.currentSeq(ch, now)
	m, err := channel.Verify(signed, token.Level(ch), s.trust(now), now, last)
	if err != nil {
		return "", err
	}
	if last > 0 && m.Seq == last {
		return Unchanged, nil
	}
	canonical, err := json.Marshal(signed)
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.Dir, ch+".json")
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, canonical, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	if s.Log != nil {
		s.Log.Info("channel manifest fetched", "channel", ch, "version", m.Version, "seq", m.Seq)
	}
	return Updated, nil
}

// trust is the keys trusted at the moment now; if the refresh fails the last known ones are kept.
func (s Syncer) trust(now time.Time) channel.Trust {
	if s.TrustFn == nil {
		return s.Trust
	}
	t, err := s.TrustFn(now)
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("could not refresh the trust keys, using the last known ones", "err", err)
		}
		return s.Trust
	}
	return t
}

// currentSeq is the sequence number of the valid manifest in the directory (0 if none or invalid).
func (s Syncer) currentSeq(ch string, now time.Time) uint64 {
	raw, err := os.ReadFile(filepath.Join(s.Dir, ch+".json"))
	if err != nil {
		return 0
	}
	var signed channel.Signed
	if json.Unmarshal(raw, &signed) != nil {
		return 0
	}
	m, err := channel.Verify(signed, token.Level(ch), s.trust(now), now, 0)
	if err != nil {
		return 0
	}
	return m.Seq
}

// Run runs Sync at every interval until ctx ends.
func (s Syncer) Run(ctx context.Context, interval time.Duration) {
	for {
		if _, err := s.Sync(ctx); err != nil && s.Log != nil {
			s.Log.Warn("channel sync failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
