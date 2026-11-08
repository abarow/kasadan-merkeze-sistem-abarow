// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package release runs the new release flow on a unit: it polls and verifies the signed
// channel manifest, announces the new version (does not apply it by itself), when an
// authorized person approves it changes the images one by one, waits for health
// and if it fails rolls back to the previous digests.
//
// The package knows nothing of Podman, systemd or the network: these are behind the
// Runtime, Source and Gate interfaces (implemented under adapters/out). Every step is written
// to the persistent state file; if the agent dies during an application (as when updating
// itself) Recover finishes or rolls back the operation at startup.
// This document gives no security guarantee.
package release

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"abarow.com/services/update/internal/channel"
	"abarow.com/services/update/internal/registration"
	"abarow.com/services/update/internal/token"
)

// Runtime is the layer that manages the images and units (Podman + systemd).
type Runtime interface {
	// Current is the running images of the abarow units installed on this machine (unit name → image).
	Current(ctx context.Context) (map[string]channel.Image, error)
	// Pull downloads the image by digest and verifies that the digest matches.
	Pull(ctx context.Context, img channel.Image) error
	// Switch pins the unit to the image and restarts it.
	Switch(ctx context.Context, img channel.Image) error
	// Healthy reports that the unit is running healthy.
	Healthy(ctx context.Context, unit string) (bool, error)
}

// Source fetches the channel manifest from the tier above.
type Source interface {
	Fetch(ctx context.Context, channel string) (channel.Signed, error)
}

// Gate says whether the update is at a safe moment (it is not applied while a shift is
// open or a basket exists at the register). Empty reason: safe.
type Gate interface {
	Unsafe(ctx context.Context) (reason string, err error)
}

// Error values.
var (
	ErrNoOffer = errors.New("onaylanacak yeni sürüm yok")
	ErrBusy    = errors.New("bir güncelleme zaten sürüyor")
	ErrStale   = errors.New("onaylanan sürüm artık güncel değil; ekranı yenileyin")
	ErrBlocked = errors.New("şu an uygulanamaz")
)

// BlockedError carries why the application was blocked.
type BlockedError struct{ Reason string }

func (e BlockedError) Error() string { return "şu an uygulanamaz: " + e.Reason }
func (e BlockedError) Unwrap() error { return ErrBlocked }

// Phase values.
const (
	PhaseNone        = "none"
	PhaseAvailable   = "available"
	PhaseDownloading = "downloading"
	PhaseApplying    = "applying"
	PhaseApplied     = "applied"
	PhaseRolledBack  = "rolled_back"
)

// Result values (written to the audit log).
const (
	ResultApplied    = "applied"
	ResultRolledBack = "rolled_back"
	ResultSkipped    = "skipped"
)

// Config is the updater's settings.
type Config struct {
	// Dir is the directory of the persistent state and the audit log.
	Dir   string
	Level token.Level
	Trust channel.Trust
	// Refresh, if given, is called at the start of every poll and refreshes the trusted keys
	// (the trust file is re-read; in key rotation a key whose transition period is over drops out by itself).
	// If it fails the keys in hand are kept and the error shows up as a polling error.
	Refresh func(now time.Time) (channel.Trust, error)
	// Source and Runtime are required. If Gate is empty every moment is safe.
	Source  Source
	Runtime Runtime
	Gate    Gate
	// Predownload is downloading the images in the background when the manifest is received (on by default).
	Predownload bool
	// If Ready returns false no polling is done (e.g. the agent is not approved yet, has no token). Always ready if empty.
	Ready func() bool
	// OnChange is called when the state changes (a new version announced, applied, rolled back, skipped);
	// it is used so that the agent reports the state to the tier above right away. Ignored if empty.
	OnChange func()
	// OwnUnit is the agent's own unit; if present it is changed last.
	OwnUnit string
	// HealthTimeout is how long to wait for the new image to become healthy (default 3 min).
	HealthTimeout time.Duration
	// HealthEvery is the health polling interval (default 2 s).
	HealthEvery time.Duration
	Now         func() time.Time
	Log         *slog.Logger
}

// Record is a result in the history.
type Record struct {
	Time    time.Time `json:"time"`
	Seq     uint64    `json:"seq"`
	Version string    `json:"version"`
	Result  string    `json:"result"`
	By      string    `json:"by,omitempty"`
	Detail  string    `json:"detail,omitempty"`
}

type offer struct {
	Manifest   channel.Manifest `json:"manifest"`
	FirstSeen  time.Time        `json:"first_seen"`
	Downloaded bool             `json:"downloaded"`
	RemindAt   time.Time        `json:"remind_at,omitzero"`
}

// operation is the application in progress; if the agent dies Recover reads it.
type operation struct {
	Manifest channel.Manifest         `json:"manifest"`
	By       string                   `json:"by"`
	Started  time.Time                `json:"started"`
	Previous map[string]channel.Image `json:"previous"`
	Changed  []channel.Image          `json:"changed"`
}

type state struct {
	// LastSeq is the highest sequence number accepted on this channel (rollback protection).
	LastSeq uint64 `json:"last_seq"`
	// SkippedSeq and FailedSeq are the sequence numbers of versions that are not announced again.
	SkippedSeq uint64     `json:"skipped_seq"`
	FailedSeq  uint64     `json:"failed_seq"`
	Offer      *offer     `json:"offer,omitempty"`
	Op         *operation `json:"op,omitempty"`
	History    []Record   `json:"history,omitempty"`
}

// Updater is a unit's updater.
type Updater struct {
	cfg Config

	mu       sync.Mutex
	st       state
	phase    string
	pollErr  string
	polled   time.Time
	gateText string
	running  bool
}

// New builds the updater and reads the persistent state.
func New(cfg Config) (*Updater, error) {
	if cfg.Dir == "" || cfg.Source == nil || cfg.Runtime == nil {
		return nil, errors.New("güncelleyici ayarları eksik")
	}
	if cfg.Trust == nil {
		return nil, errors.New("güncelleyici: güvenilen kanal anahtarları gerekli")
	}
	if cfg.Level == "" {
		cfg.Level = token.Prod
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.HealthTimeout == 0 {
		cfg.HealthTimeout = 3 * time.Minute
	}
	if cfg.HealthEvery == 0 {
		cfg.HealthEvery = 2 * time.Second
	}
	u := &Updater{cfg: cfg, phase: PhaseNone}
	if raw, err := os.ReadFile(filepath.Join(cfg.Dir, stateFile)); err == nil {
		if err := json.Unmarshal(raw, &u.st); err != nil {
			return nil, fmt.Errorf("%s bozuk: %w", stateFile, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	u.refreshPhase()
	return u, nil
}

const (
	stateFile = "release.json"
	auditFile = "denetim.jsonl"
	cacheDir  = "channel"
)

func (u *Updater) save() error {
	raw, err := json.MarshalIndent(u.st, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(u.cfg.Dir, stateFile), raw)
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// audit appends a line to the audit log (who, when, which version, the result).
func (u *Updater) audit(event, by string, m channel.Manifest, detail string) {
	line, _ := json.Marshal(map[string]any{
		"time": u.cfg.Now().UTC().Format(time.RFC3339), "event": event, "by": by,
		"tier": "", "level": u.cfg.Level, "version": m.Version, "seq": m.Seq, "detail": detail,
	})
	if err := os.MkdirAll(u.cfg.Dir, 0o700); err != nil {
		u.cfg.Log.Warn("could not write the audit log", "err", err)
		return
	}
	f, err := os.OpenFile(filepath.Join(u.cfg.Dir, auditFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		u.cfg.Log.Warn("could not write the audit log", "err", err)
		return
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	_, _ = w.Write(append(line, '\n'))
	_ = w.Flush()
	u.cfg.Log.Info("update", "event", event, "by", by, "version", m.Version, "detail", detail)
}

func (u *Updater) record(r Record) {
	u.st.History = append(u.st.History, r)
	if n := len(u.st.History); n > 50 {
		u.st.History = slices.Clone(u.st.History[n-50:])
	}
}

// refreshPhase derives the phase from the persistent state (called under the lock).
func (u *Updater) refreshPhase() {
	switch {
	case u.running || u.st.Op != nil:
		u.phase = PhaseApplying
	case u.st.Offer != nil:
		u.phase = PhaseAvailable
	default:
		u.phase = PhaseNone
	}
}

// ImageStatus is the image shown on the screen.
type ImageStatus struct {
	Unit   string `json:"unit"`
	Digest string `json:"digest"`
}

// Report is the running version state the unit will report to the tier above: the running
// image digests of the units, the last applied version label, the phase of the flow and the
// announced version.
func (u *Updater) Report(ctx context.Context) (registration.Report, error) {
	cur, err := u.cfg.Runtime.Current(ctx)
	if err != nil {
		return registration.Report{}, err
	}
	rep := registration.Report{Units: make(map[string]string, len(cur))}
	for unit, img := range cur {
		if img.Digest != "" {
			rep.Units[unit] = img.Digest
		}
	}
	st := u.Status()
	rep.Phase = st.Phase
	if st.Phase == PhaseAvailable || st.Phase == PhaseDownloading || st.Phase == PhaseApplying {
		rep.Offered = st.Version
	}
	u.mu.Lock()
	for i := len(u.st.History) - 1; i >= 0; i-- {
		if u.st.History[i].Result == ResultApplied {
			rep.Version = u.st.History[i].Version
			break
		}
	}
	u.mu.Unlock()
	return rep, nil
}

func (u *Updater) changed() {
	if u.cfg.OnChange != nil {
		u.cfg.OnChange()
	}
}

// Status is the update status the screen shows.
type Status struct {
	Phase      string        `json:"phase"`
	Channel    string        `json:"channel"`
	Version    string        `json:"version,omitempty"`
	Seq        uint64        `json:"seq,omitempty"`
	Urgency    string        `json:"urgency,omitempty"`
	Notes      string        `json:"notes,omitempty"`
	Images     []ImageStatus `json:"images,omitempty"`
	FirstSeen  time.Time     `json:"first_seen,omitzero"`
	RemindAt   time.Time     `json:"remind_at,omitzero"`
	Downloaded bool          `json:"downloaded"`
	// Blocked is why the application cannot be done right now (such as an open shift at the register).
	Blocked string `json:"blocked,omitempty"`
	// Error is the error of the last poll.
	Error   string    `json:"error,omitempty"`
	Polled  time.Time `json:"polled,omitzero"`
	Last    *Record   `json:"last,omitempty"`
	History []Record  `json:"history,omitempty"`
}

// Status returns the update status.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := Status{Phase: u.phase, Channel: channel.ChannelOf(u.cfg.Level), Error: u.pollErr, Polled: u.polled, Blocked: u.gateText}
	var m *channel.Manifest
	switch {
	case u.st.Op != nil:
		m = &u.st.Op.Manifest
	case u.st.Offer != nil:
		m = &u.st.Offer.Manifest
		s.FirstSeen, s.RemindAt, s.Downloaded = u.st.Offer.FirstSeen, u.st.Offer.RemindAt, u.st.Offer.Downloaded
	}
	if m != nil {
		s.Version, s.Seq, s.Urgency, s.Notes = m.Version, m.Seq, m.Urgency, m.Notes
		for _, im := range m.Images {
			s.Images = append(s.Images, ImageStatus{Unit: im.Unit, Digest: im.Digest})
		}
	}
	if n := len(u.st.History); n > 0 {
		last := u.st.History[n-1]
		s.Last = &last
		s.History = slices.Clone(u.st.History)
		if u.phase == PhaseNone && last.Result == ResultRolledBack {
			s.Phase = PhaseRolledBack
		}
	}
	return s
}

func (u *Updater) currentTrust() channel.Trust {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.cfg.Trust
}

// refreshTrust refreshes the trust keys; on failure the ones in hand are kept.
func (u *Updater) refreshTrust() error {
	if u.cfg.Refresh == nil {
		return nil
	}
	t, err := u.cfg.Refresh(u.cfg.Now())
	if err != nil {
		return fmt.Errorf("güven anahtarları yenilenemedi (eskileri kullanılıyor): %w", err)
	}
	u.mu.Lock()
	u.cfg.Trust = t
	u.mu.Unlock()
	return nil
}

// Poll polls the channel manifest once. On error the state in hand is kept.
func (u *Updater) Poll(ctx context.Context) error {
	if u.cfg.Ready != nil && !u.cfg.Ready() {
		return nil
	}
	u.mu.Lock()
	if u.running || u.st.Op != nil {
		u.mu.Unlock()
		return nil // no polling while an application is in progress
	}
	last := u.st.LastSeq
	u.mu.Unlock()

	trustErr := u.refreshTrust()
	err := u.poll(ctx, last)
	if err == nil {
		err = trustErr
	}
	u.mu.Lock()
	u.polled = u.cfg.Now()
	if err != nil {
		u.pollErr = err.Error()
	} else {
		u.pollErr = ""
	}
	u.mu.Unlock()
	return err
}

func (u *Updater) poll(ctx context.Context, last uint64) error {
	signed, err := u.cfg.Source.Fetch(ctx, channel.ChannelOf(u.cfg.Level))
	if err != nil {
		return fmt.Errorf("kanal bildirimi alınamadı: %w", err)
	}
	m, err := channel.Verify(signed, u.cfg.Level, u.currentTrust(), u.cfg.Now(), last)
	if err != nil {
		return err
	}
	// The verified manifest is stored to be offered to the layers below.
	if raw, err := json.Marshal(signed); err == nil {
		if err := writeFile(filepath.Join(u.cfg.Dir, cacheDir, m.Channel+".json"), raw); err != nil {
			u.cfg.Log.Warn("could not store the channel manifest", "err", err)
		}
	}
	cur, err := u.cfg.Runtime.Current(ctx)
	if err != nil {
		return fmt.Errorf("çalışan imajlar okunamadı: %w", err)
	}
	var changed []channel.Image
	for _, im := range m.Images {
		if c, ok := cur[im.Unit]; ok && c.Digest != im.Digest {
			changed = append(changed, im)
		}
	}

	var reason string
	if u.cfg.Gate != nil {
		reason, _ = u.cfg.Gate.Unsafe(ctx)
	}

	u.mu.Lock()
	u.gateText = reason
	if m.Seq > u.st.LastSeq {
		u.st.LastSeq = m.Seq
	}
	switch {
	case len(changed) == 0:
		u.st.Offer = nil
	case m.Seq == u.st.SkippedSeq || m.Seq == u.st.FailedSeq:
		u.st.Offer = nil // a version that was skipped or rolled back is not announced again
	case u.st.Offer != nil && u.st.Offer.Manifest.Seq == m.Seq:
		// same manifest: the wait time and the postponement are kept
	default:
		u.st.Offer = &offer{Manifest: m, FirstSeen: u.cfg.Now()}
		u.audit("bildirildi", "", m, fmt.Sprintf("%d imaj değişti", len(changed)))
		u.changed()
	}
	u.refreshPhase()
	err = u.save()
	pull := err == nil && u.st.Offer != nil && u.cfg.Predownload && !u.st.Offer.Downloaded
	u.mu.Unlock()
	if err != nil {
		return err
	}
	if pull {
		return u.predownload(ctx, m, changed)
	}
	return nil
}

// predownload downloads the images in the background (does not apply). The download is done without
// holding the lock; the screen can read the state meanwhile.
func (u *Updater) predownload(ctx context.Context, m channel.Manifest, changed []channel.Image) error {
	u.mu.Lock()
	u.phase = PhaseDownloading
	u.mu.Unlock()
	var failure error
	for _, im := range changed {
		if err := u.cfg.Runtime.Pull(ctx, im); err != nil {
			failure = fmt.Errorf("%s indirilemedi: %w", im.Unit, err)
			break
		}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if failure == nil && u.st.Offer != nil && u.st.Offer.Manifest.Seq == m.Seq {
		u.st.Offer.Downloaded = true
		u.audit("indirildi", "", m, "")
	}
	if !u.running {
		u.refreshPhase()
	}
	u.changed() // the download is finished (or failed): the phase changed, the tier above should learn right away
	if failure != nil {
		return failure
	}
	return u.save()
}

// Skip skips the version in the manifest; the same version is not announced again (a newer one is announced).
func (u *Updater) Skip(by string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.st.Offer == nil || u.running {
		return ErrNoOffer
	}
	m := u.st.Offer.Manifest
	u.st.SkippedSeq, u.st.Offer = m.Seq, nil
	u.record(Record{Time: u.cfg.Now(), Seq: m.Seq, Version: m.Version, Result: ResultSkipped, By: by})
	u.audit("atlandı", by, m, "")
	u.changed()
	u.refreshPhase()
	return u.save()
}

// Snooze postpones the reminder; the version stays in the manifest, only the screen is silent for this time.
func (u *Updater) Snooze(by string, d time.Duration) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.st.Offer == nil || u.running {
		return ErrNoOffer
	}
	u.st.Offer.RemindAt = u.cfg.Now().Add(d)
	u.audit("ertelendi", by, u.st.Offer.Manifest, d.String())
	return u.save()
}

// Apply applies the version in the manifest. seq is the version the approver saw on the screen
// (ErrStale if a new manifest came in the meantime). The application continues in the background; the
// result is seen with Status().Last. If it is not a safe moment it returns BlockedError.
func (u *Updater) Apply(ctx context.Context, by string, seq uint64) error {
	u.mu.Lock()
	if u.running || u.st.Op != nil {
		u.mu.Unlock()
		return ErrBusy
	}
	if u.st.Offer == nil {
		u.mu.Unlock()
		return ErrNoOffer
	}
	if u.st.Offer.Manifest.Seq != seq {
		u.mu.Unlock()
		return ErrStale
	}
	m := u.st.Offer.Manifest
	u.mu.Unlock()

	if u.cfg.Gate != nil {
		reason, err := u.cfg.Gate.Unsafe(ctx)
		if err != nil {
			return fmt.Errorf("güvenli an denetlenemedi: %w", err)
		}
		if reason != "" {
			return BlockedError{Reason: reason}
		}
	}
	cur, err := u.cfg.Runtime.Current(ctx)
	if err != nil {
		return fmt.Errorf("çalışan imajlar okunamadı: %w", err)
	}
	op := &operation{Manifest: m, By: by, Started: u.cfg.Now(), Previous: map[string]channel.Image{}}
	for _, im := range m.Images {
		if c, ok := cur[im.Unit]; ok && c.Digest != im.Digest {
			op.Changed = append(op.Changed, im)
			op.Previous[im.Unit] = c
		}
	}
	if len(op.Changed) == 0 {
		return ErrNoOffer
	}
	// The agent's own unit is taken last: when it changes the process dies, Recover finishes.
	slices.SortStableFunc(op.Changed, func(a, b channel.Image) int {
		switch {
		case a.Unit == u.cfg.OwnUnit && b.Unit != u.cfg.OwnUnit:
			return 1
		case b.Unit == u.cfg.OwnUnit && a.Unit != u.cfg.OwnUnit:
			return -1
		}
		return 0
	})

	u.mu.Lock()
	if u.running {
		u.mu.Unlock()
		return ErrBusy
	}
	u.running, u.st.Op = true, op
	u.phase = PhaseApplying
	u.audit("onaylandı", by, m, "")
	u.changed()
	if err := u.save(); err != nil {
		u.running, u.st.Op = false, nil
		u.refreshPhase()
		u.mu.Unlock()
		return err
	}
	u.mu.Unlock()

	// The application is independent of the request context: it continues even if the browser closes.
	go u.run(context.WithoutCancel(ctx), op)
	return nil
}

func (u *Updater) run(ctx context.Context, op *operation) {
	var switched []channel.Image
	fail := func(failed channel.Image, cause error) {
		u.rollback(ctx, op, append(switched, failed), cause)
	}
	for _, im := range op.Changed {
		if err := u.cfg.Runtime.Pull(ctx, im); err != nil { // it returns quickly if already downloaded
			fail(im, fmt.Errorf("%s indirilemedi: %w", im.Unit, err))
			return
		}
		if err := u.cfg.Runtime.Switch(ctx, im); err != nil {
			fail(im, fmt.Errorf("%s değiştirilemedi: %w", im.Unit, err))
			return
		}
		if err := u.waitHealthy(ctx, im.Unit); err != nil {
			fail(im, err)
			return
		}
		switched = append(switched, im)
	}
	u.finish(op)
}

// waitHealthy waits until the unit is healthy or the time is up.
func (u *Updater) waitHealthy(ctx context.Context, unit string) error {
	deadline := time.Now().Add(u.cfg.HealthTimeout)
	for {
		ok, err := u.cfg.Runtime.Healthy(ctx, unit)
		if err == nil && ok {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("%s sağlık denetimi başarısız: %w", unit, err)
			}
			return fmt.Errorf("%s %s içinde sağlıklı açılmadı", unit, u.cfg.HealthTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(u.cfg.HealthEvery):
		}
	}
}

// rollback returns the changed units to the previous digests in reverse order.
func (u *Updater) rollback(ctx context.Context, op *operation, touched []channel.Image, cause error) {
	var problems []string
	for i := len(touched) - 1; i >= 0; i-- {
		prev, ok := op.Previous[touched[i].Unit]
		if !ok {
			continue
		}
		if err := u.cfg.Runtime.Switch(ctx, prev); err != nil {
			problems = append(problems, fmt.Sprintf("%s geri alınamadı: %v", prev.Unit, err))
			continue
		}
		if err := u.waitHealthy(ctx, prev.Unit); err != nil {
			problems = append(problems, err.Error())
		}
	}
	detail := cause.Error()
	if len(problems) > 0 {
		detail += "; " + strings.Join(problems, "; ")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	m := op.Manifest
	u.st.FailedSeq, u.st.Offer, u.st.Op, u.running = m.Seq, nil, nil, false
	u.record(Record{Time: u.cfg.Now(), Seq: m.Seq, Version: m.Version, Result: ResultRolledBack, By: op.By, Detail: detail})
	u.audit("geri-alındı", op.By, m, detail)
	u.changed()
	u.refreshPhase()
	if err := u.save(); err != nil {
		u.cfg.Log.Error("could not write the state", "err", err)
	}
}

func (u *Updater) finish(op *operation) {
	u.mu.Lock()
	defer u.mu.Unlock()
	m := op.Manifest
	u.st.Offer, u.st.Op, u.running = nil, nil, false
	u.record(Record{Time: u.cfg.Now(), Seq: m.Seq, Version: m.Version, Result: ResultApplied, By: op.By})
	u.audit("uygulandı", op.By, m, "")
	u.changed()
	u.refreshPhase()
	if err := u.save(); err != nil {
		u.cfg.Log.Error("could not write the state", "err", err)
	}
}

// Recover finishes the operation when the agent dies in the middle of an application and is
// reopened (e.g. while updating its own unit): if all units are at the target digest and
// healthy it counts as applied, otherwise it rolls back to the previous digests.
func (u *Updater) Recover(ctx context.Context) {
	u.mu.Lock()
	op := u.st.Op
	if op == nil || u.running {
		u.mu.Unlock()
		return
	}
	u.running = true
	u.mu.Unlock()

	cur, err := u.cfg.Runtime.Current(ctx)
	if err == nil {
		done := true
		for _, im := range op.Changed {
			if c, ok := cur[im.Unit]; !ok || c.Digest != im.Digest {
				done = false
				break
			}
			if err := u.waitHealthy(ctx, im.Unit); err != nil {
				done = false
				break
			}
		}
		if done {
			u.finish(op)
			return
		}
	}
	u.rollback(ctx, op, op.Changed, errors.New("ajan uygulama sırasında yeniden başladı; sürüm doğrulanamadı"))
}

// Run polls the channel at every interval until ctx ends. At startup it finishes an
// unfinished operation with Recover.
func (u *Updater) Run(ctx context.Context, interval time.Duration) {
	u.Recover(ctx)
	for {
		if err := u.Poll(ctx); err != nil {
			u.cfg.Log.Warn("channel poll failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
