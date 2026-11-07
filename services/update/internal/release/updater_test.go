// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package release_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"abarow.com/services/update/internal/channel"
	"abarow.com/services/update/internal/release"
	"abarow.com/services/update/internal/token"
)

const (
	repo   = "ghcr.io/abarow/abarow-"
	oldDig = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	newDig = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

func img(unit, dig string) channel.Image {
	return channel.Image{Unit: unit, Repo: repo + strings.TrimPrefix(unit, "abarow-"), Digest: dig}
}

// fakeRuntime keeps the units' images in memory and records the calls.
type fakeRuntime struct {
	mu       sync.Mutex
	cur      map[string]channel.Image
	calls    []string
	pullErr  map[string]error
	badDig   map[string]bool // if switched to this digest the unit does not come up healthy
	switchOK map[string]error
	rolled   map[string]bool
}

func newRuntime(units ...string) *fakeRuntime {
	r := &fakeRuntime{cur: map[string]channel.Image{}, pullErr: map[string]error{}, badDig: map[string]bool{}, switchOK: map[string]error{}}
	for _, u := range units {
		r.cur[u] = img(u, oldDig)
	}
	return r
}

func (r *fakeRuntime) Current(context.Context) (map[string]channel.Image, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]channel.Image{}
	for k, v := range r.cur {
		out[k] = v
	}
	return out, nil
}

func (r *fakeRuntime) Pull(_ context.Context, i channel.Image) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "pull "+i.Unit)
	return r.pullErr[i.Unit]
}

func (r *fakeRuntime) Switch(_ context.Context, i channel.Image) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "switch "+i.Unit+" "+i.Digest[:10])
	if err := r.switchOK[i.Unit]; err != nil {
		return err
	}
	r.cur[i.Unit] = i
	return nil
}

func (r *fakeRuntime) Healthy(_ context.Context, unit string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.badDig[r.cur[unit].Digest], nil
}

func (r *fakeRuntime) log() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// fakeSource returns the channel manifest.
type fakeSource struct {
	mu  sync.Mutex
	s   channel.Signed
	err error
}

func (f *fakeSource) Fetch(context.Context, string) (channel.Signed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.s, f.err
}

type fakeGate struct{ reason string }

func (g fakeGate) Unsafe(context.Context) (string, error) { return g.reason, nil }

type env struct {
	t     *testing.T
	dir   string
	prod  ed25519.PrivateKey
	trust channel.Trust
	src   *fakeSource
	rt    *fakeRuntime
	now   time.Time
}

func newEnv(t *testing.T, units ...string) *env {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return &env{t: t, dir: t.TempDir(), prod: priv, trust: channel.Trust{channel.Prod: {pub}}, src: &fakeSource{}, rt: newRuntime(units...), now: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)}
}

// publish puts a version on the channel.
func (e *env) publish(seq uint64, version string, images ...channel.Image) {
	e.t.Helper()
	m := channel.Manifest{
		Channel: "prod", Version: version, Seq: seq, Issued: e.now.Add(-time.Hour), Expires: e.now.Add(48 * time.Hour),
		Urgency: channel.UrgencySecurity, Notes: "Güvenlik yaması", Images: images,
	}
	s, err := channel.Sign(m, channel.Prod, e.prod)
	if err != nil {
		e.t.Fatal(err)
	}
	e.src.mu.Lock()
	e.src.s, e.src.err = s, nil
	e.src.mu.Unlock()
}

func (e *env) updater(mod ...func(*release.Config)) *release.Updater {
	e.t.Helper()
	cfg := release.Config{
		Dir: e.dir, Level: token.Prod, Trust: e.trust, Source: e.src, Runtime: e.rt, Predownload: true,
		OwnUnit: "abarow-update", HealthTimeout: 200 * time.Millisecond, HealthEvery: 5 * time.Millisecond,
		Now: func() time.Time { return e.now },
	}
	for _, m := range mod {
		m(&cfg)
	}
	u, err := release.New(cfg)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func waitPhase(t *testing.T, u *release.Updater, want string) release.Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := u.Status()
		if s.Phase == want {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("phase %q did not occur, currently %q (%+v)", want, s.Phase, s)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPollOffersNewVersionWithoutApplying(t *testing.T) {
	e := newEnv(t, "abarow-sales", "abarow-gateway")
	e.publish(1, "0.4.0", img("abarow-sales", newDig), img("abarow-gateway", oldDig), img("abarow-yok", newDig))
	u := e.updater()
	if err := u.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := u.Status()
	if s.Phase != release.PhaseAvailable || s.Version != "0.4.0" || s.Urgency != channel.UrgencySecurity || s.Notes == "" {
		t.Fatalf("durum: %+v", s)
	}
	// The manifest is not applied by itself.
	for _, c := range e.rt.log() {
		if strings.HasPrefix(c, "switch") {
			t.Fatalf("applied without approval: %v", e.rt.log())
		}
	}
	// Only the unit installed on this machine and changed is downloaded (on: pre-download).
	if got := e.rt.log(); len(got) != 1 || got[0] != "pull abarow-sales" {
		t.Fatalf("indirme: %v", got)
	}
	if !s.Downloaded {
		t.Error("not downloaded while pre-download is on")
	}
}

// The trust keys are refreshed at every poll: a key added in rotation becomes valid without the agent restarting;
// if the refresh fails the keys in hand are kept and the error is visible.
func TestPollRefreshesTrust(t *testing.T) {
	e := newEnv(t, "abarow-sales")
	e.publish(1, "0.4.0", img("abarow-sales", newDig))
	good := e.trust
	e.trust = channel.Trust{channel.Prod: {}} // the agent does not know the new key yet
	var next channel.Trust
	var failure error
	u := e.updater(func(c *release.Config) {
		c.Refresh = func(time.Time) (channel.Trust, error) { return next, failure }
	})
	ctx := context.Background()

	next = channel.Trust{channel.Prod: {}}
	if err := u.Poll(ctx); !errors.Is(err, channel.ErrSignature) {
		t.Fatalf("without a key: %v", err)
	}
	// The trust file was updated: the agent accepts the new key without restarting.
	next = good
	if err := u.Poll(ctx); err != nil {
		t.Fatalf("yenilenen anahtarla: %v", err)
	}
	if u.Status().Phase != release.PhaseAvailable {
		t.Fatalf("durum: %+v", u.Status())
	}
	// When the file cannot be read the keys in hand are kept; the error shows up as a polling error.
	failure = errors.New("dosya yok")
	e.publish(2, "0.5.0", img("abarow-sales", oldDig))
	err := u.Poll(ctx)
	if err == nil || !strings.Contains(err.Error(), "dosya yok") {
		t.Fatalf("the refresh error is not visible: %v", err)
	}
	// Since the key in hand (last known) is still valid the manifest is still verified.
	if u.Status().Version == "" && u.Status().Phase == release.PhaseNone && u.Status().Error == "" {
		t.Fatalf("durum: %+v", u.Status())
	}
	// When the expired key drops out the signature is rejected (the refresh returns an empty key).
	failure, next = nil, channel.Trust{channel.Prod: {}}
	e.publish(3, "0.6.0", img("abarow-sales", newDig))
	if err := u.Poll(ctx); !errors.Is(err, channel.ErrSignature) {
		t.Fatalf("an expired key was accepted: %v", err)
	}
}

func TestPredownloadCanBeDisabled(t *testing.T) {
	e := newEnv(t, "abarow-sales")
	e.publish(1, "0.4.0", img("abarow-sales", newDig))
	u := e.updater(func(c *release.Config) { c.Predownload = false })
	if err := u.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.rt.log()) != 0 || u.Status().Downloaded {
		t.Fatalf("downloaded while pre-download is off: %v", e.rt.log())
	}
}

func TestPollNoChangeNoOffer(t *testing.T) {
	e := newEnv(t, "abarow-sales")
	e.publish(1, "0.4.0", img("abarow-sales", oldDig))
	u := e.updater()
	if err := u.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := u.Status(); s.Phase != release.PhaseNone {
		t.Fatalf("announced when nothing changed: %+v", s)
	}
}

func TestPollRejectsBadManifests(t *testing.T) {
	e := newEnv(t, "abarow-sales")
	u := e.updater()
	ctx := context.Background()

	// Unsigned (an untrusted key).
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	m := channel.Manifest{Channel: "prod", Version: "9", Seq: 9, Issued: e.now, Expires: e.now.Add(time.Hour), Urgency: "normal", Images: []channel.Image{img("abarow-sales", newDig)}}
	s, _ := channel.Sign(m, channel.Prod, other)
	e.src.s = s
	if err := u.Poll(ctx); !errors.Is(err, channel.ErrSignature) {
		t.Fatalf("wrong key: %v", err)
	}
	if u.Status().Phase != release.PhaseNone || u.Status().Error == "" {
		t.Fatalf("durum: %+v", u.Status())
	}

	// The source cannot be reached.
	e.src.err = errors.New("ağ yok")
	if err := u.Poll(ctx); err == nil {
		t.Fatal("the source error was swallowed")
	}

	// Rollback: first 5 is accepted, then 4 is rejected.
	e.src.err = nil
	e.publish(5, "0.5.0", img("abarow-sales", newDig))
	if err := u.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	e.publish(4, "0.4.0", img("abarow-sales", newDig))
	if err := u.Poll(ctx); !errors.Is(err, channel.ErrRollback) {
		t.Fatalf("an old version was accepted: %v", err)
	}
	if u.Status().Version != "0.5.0" {
		t.Fatalf("eski bildirim durumu bozdu: %+v", u.Status())
	}
}

func TestApplyAllChangedThenOwnUnitLast(t *testing.T) {
	e := newEnv(t, "abarow-update", "abarow-sales", "abarow-gateway")
	e.publish(1, "0.4.0", img("abarow-update", newDig), img("abarow-sales", newDig), img("abarow-gateway", newDig))
	u := e.updater()
	ctx := context.Background()
	if err := u.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := u.Apply(ctx, "ayse", 1); err != nil {
		t.Fatal(err)
	}
	s := waitPhase(t, u, release.PhaseNone)
	if s.Last == nil || s.Last.Result != release.ResultApplied || s.Last.By != "ayse" {
		t.Fatalf("result: %+v", s.Last)
	}
	var switches []string
	for _, c := range e.rt.log() {
		if strings.HasPrefix(c, "switch") {
			switches = append(switches, strings.Fields(c)[1])
		}
	}
	if len(switches) != 3 || switches[2] != "abarow-update" {
		t.Fatalf("order: %v (the agent's own unit should be last)", switches)
	}
	for _, u := range []string{"abarow-sales", "abarow-gateway", "abarow-update"} {
		if e.rt.cur[u].Digest != newDig {
			t.Errorf("%s is still on the old version", u)
		}
	}
	// Audit log: who approved, when, the result.
	raw, err := os.ReadFile(filepath.Join(e.dir, "denetim.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"event":"bildirildi"`, `"event":"onaylandı"`, `"event":"uygulandı"`, `"by":"ayse"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("no %s in the audit log:\n%s", want, raw)
		}
	}
}

func TestFailedHealthRollsBackEverythingAndIsNotReoffered(t *testing.T) {
	e := newEnv(t, "abarow-sales", "abarow-gateway")
	bad := "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	e.rt.badDig[bad] = true
	e.publish(1, "0.4.0", img("abarow-sales", newDig), img("abarow-gateway", bad))
	u := e.updater()
	ctx := context.Background()
	if err := u.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := u.Apply(ctx, "mudur", 1); err != nil {
		t.Fatal(err)
	}
	s := waitPhase(t, u, release.PhaseRolledBack)
	if s.Last.Result != release.ResultRolledBack || !strings.Contains(s.Last.Detail, "abarow-gateway") {
		t.Fatalf("result: %+v", s.Last)
	}
	// Everything went back to the previous digest, including the sales that had passed.
	for _, unit := range []string{"abarow-sales", "abarow-gateway"} {
		if got := e.rt.cur[unit].Digest; got != oldDig {
			t.Errorf("%s was not rolled back: %s", unit, got)
		}
	}
	// A version that was rolled back is not announced again; a newer one is announced.
	if err := u.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if got := u.Status().Phase; got != release.PhaseRolledBack {
		t.Fatalf("the rolled-back version was announced again: %s", got)
	}
	e.publish(2, "0.4.1", img("abarow-sales", newDig), img("abarow-gateway", newDig))
	if err := u.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if s := u.Status(); s.Phase != release.PhaseAvailable || s.Version != "0.4.1" {
		t.Fatalf("the new version was not announced: %+v", s)
	}
}

func TestSwitchErrorRollsBack(t *testing.T) {
	e := newEnv(t, "abarow-sales", "abarow-gateway")
	e.publish(1, "0.4.0", img("abarow-sales", newDig), img("abarow-gateway", newDig))
	e.rt.switchOK["abarow-gateway"] = errors.New("systemd reddetti")
	u := e.updater()
	ctx := context.Background()
	_ = u.Poll(ctx)
	if err := u.Apply(ctx, "mudur", 1); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, u, release.PhaseRolledBack)
	if e.rt.cur["abarow-sales"].Digest != oldDig {
		t.Error("sales was not rolled back")
	}
}

func TestApplyGuards(t *testing.T) {
	e := newEnv(t, "abarow-sales")
	ctx := context.Background()
	u := e.updater()
	if err := u.Apply(ctx, "x", 1); !errors.Is(err, release.ErrNoOffer) {
		t.Fatalf("bildirim yokken: %v", err)
	}
	e.publish(1, "0.4.0", img("abarow-sales", newDig))
	_ = u.Poll(ctx)
	if err := u.Apply(ctx, "x", 7); !errors.Is(err, release.ErrStale) {
		t.Fatalf("old sequence number: %v", err)
	}

	// Not a safe moment: an open shift at the register.
	blocked := e.updater(func(c *release.Config) { c.Gate = fakeGate{reason: "açık vardiya var"} })
	err := blocked.Apply(ctx, "x", 1)
	var be release.BlockedError
	if !errors.As(err, &be) || be.Reason != "açık vardiya var" || !errors.Is(err, release.ErrBlocked) {
		t.Fatalf("engel: %v", err)
	}
	if got := e.rt.cur["abarow-sales"].Digest; got != oldDig {
		t.Fatal("a blocked update was applied")
	}
}

func TestBlockedReasonShownOnStatus(t *testing.T) {
	e := newEnv(t, "abarow-sales")
	e.publish(1, "0.4.0", img("abarow-sales", newDig))
	u := e.updater(func(c *release.Config) { c.Gate = fakeGate{reason: "açık sepet var"} })
	_ = u.Poll(context.Background())
	if got := u.Status().Blocked; got != "açık sepet var" {
		t.Fatalf("the reason for the block is not on the screen: %q", got)
	}
}

func TestSkipAndSnooze(t *testing.T) {
	e := newEnv(t, "abarow-sales")
	ctx := context.Background()
	u := e.updater()
	e.publish(1, "0.4.0", img("abarow-sales", newDig))
	_ = u.Poll(ctx)

	if err := u.Snooze("ayse", time.Hour); err != nil {
		t.Fatal(err)
	}
	if s := u.Status(); s.Phase != release.PhaseAvailable || !s.RemindAt.Equal(e.now.Add(time.Hour)) {
		t.Fatalf("erteleme: %+v", s)
	}
	if err := u.Skip("ayse"); err != nil {
		t.Fatal(err)
	}
	if u.Status().Phase != release.PhaseNone {
		t.Fatal("the skipped version is still in the manifest")
	}
	_ = u.Poll(ctx)
	if u.Status().Phase != release.PhaseNone {
		t.Fatal("the skipped version was announced again")
	}
	if err := u.Skip("ayse"); !errors.Is(err, release.ErrNoOffer) {
		t.Fatalf("bildirim yokken atlama: %v", err)
	}
	e.publish(2, "0.4.1", img("abarow-sales", newDig))
	_ = u.Poll(ctx)
	if s := u.Status(); s.Phase != release.PhaseAvailable || s.Version != "0.4.1" {
		t.Fatalf("a newer version was not announced: %+v", s)
	}
}

func TestOfferKeepsFirstSeenAcrossPolls(t *testing.T) {
	e := newEnv(t, "abarow-sales")
	ctx := context.Background()
	u := e.updater()
	e.publish(1, "0.4.0", img("abarow-sales", newDig))
	_ = u.Poll(ctx)
	first := u.Status().FirstSeen
	e.now = e.now.Add(6 * time.Hour)
	_ = u.Poll(ctx)
	if got := u.Status().FirstSeen; !got.Equal(first) {
		t.Fatalf("the wait time was reset: %v → %v", first, got)
	}
}

func TestStatePersistsAcrossRestart(t *testing.T) {
	e := newEnv(t, "abarow-sales")
	ctx := context.Background()
	u := e.updater()
	e.publish(3, "0.4.0", img("abarow-sales", newDig))
	_ = u.Poll(ctx)

	u2 := e.updater()
	if s := u2.Status(); s.Phase != release.PhaseAvailable || s.Seq != 3 {
		t.Fatalf("the manifest was lost on restart: %+v", s)
	}
	// The rollback protection is persistent too.
	e.publish(2, "0.3.9", img("abarow-sales", newDig))
	if err := u2.Poll(ctx); !errors.Is(err, channel.ErrRollback) {
		t.Fatalf("the rollback protection was lost on restart: %v", err)
	}
}

func TestPollCachesVerifiedManifestForLowerTiers(t *testing.T) {
	e := newEnv(t, "abarow-sales")
	e.publish(1, "0.4.0", img("abarow-sales", newDig))
	u := e.updater()
	_ = u.Poll(context.Background())
	raw, err := os.ReadFile(filepath.Join(e.dir, "channel", "prod.json"))
	if err != nil || !strings.Contains(string(raw), "payload") {
		t.Fatalf("the verified manifest was not stored: %v", err)
	}
	// A manifest with an invalid signature is not stored.
	e2 := newEnv(t, "abarow-sales")
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	m := channel.Manifest{Channel: "prod", Version: "9", Seq: 9, Issued: e2.now, Expires: e2.now.Add(time.Hour), Urgency: "normal", Images: []channel.Image{img("abarow-sales", newDig)}}
	e2.src.s, _ = channel.Sign(m, channel.Prod, other)
	_ = e2.updater().Poll(context.Background())
	if _, err := os.Stat(filepath.Join(e2.dir, "channel", "prod.json")); err == nil {
		t.Fatal("a manifest with an invalid signature was stored to be offered to the layers below")
	}
}

func TestRecoverFinishesWhenAllUnitsAreOnTarget(t *testing.T) {
	e := newEnv(t, "abarow-sales", "abarow-update")
	e.publish(1, "0.4.0", img("abarow-sales", newDig), img("abarow-update", newDig))
	ctx := context.Background()
	u := e.updater()
	_ = u.Poll(ctx)
	// The agent dies while changing its own unit: sales changed, the agent changed, the process died.
	e.rt.cur["abarow-sales"] = img("abarow-sales", newDig)
	e.rt.cur["abarow-update"] = img("abarow-update", newDig)
	writeOp(t, e, u)

	u2 := e.updater()
	u2.Recover(ctx)
	s := u2.Status()
	if s.Phase != release.PhaseNone || s.Last == nil || s.Last.Result != release.ResultApplied {
		t.Fatalf("the unfinished operation was not finished: %+v", s)
	}
}

func TestRecoverRollsBackWhenTargetIsUnhealthy(t *testing.T) {
	e := newEnv(t, "abarow-sales", "abarow-update")
	bad := "sha256:4444444444444444444444444444444444444444444444444444444444444444"
	e.rt.badDig[bad] = true
	e.publish(1, "0.4.0", img("abarow-sales", newDig), img("abarow-update", bad))
	ctx := context.Background()
	u := e.updater()
	_ = u.Poll(ctx)
	e.rt.cur["abarow-sales"] = img("abarow-sales", newDig)
	e.rt.cur["abarow-update"] = img("abarow-update", bad)
	writeOp(t, e, u)

	u2 := e.updater()
	u2.Recover(ctx)
	s := u2.Status()
	if s.Last == nil || s.Last.Result != release.ResultRolledBack {
		t.Fatalf("the unhealthy new agent was not rolled back: %+v", s)
	}
	for _, unit := range []string{"abarow-sales", "abarow-update"} {
		if e.rt.cur[unit].Digest != oldDig {
			t.Errorf("%s was not rolled back", unit)
		}
	}
}

// writeOp writes a half-finished operation to the persistent state (the agent dying during an application).
func writeOp(t *testing.T, e *env, u *release.Updater) {
	t.Helper()
	st := u.Status()
	var changed []channel.Image
	prev := map[string]channel.Image{}
	for _, im := range st.Images {
		changed = append(changed, channel.Image{Unit: im.Unit, Repo: repo + strings.TrimPrefix(im.Unit, "abarow-"), Digest: im.Digest})
		prev[im.Unit] = img(im.Unit, oldDig)
	}
	if err := release.WriteInFlight(e.dir, st.Seq, st.Version, changed, prev); err != nil {
		t.Fatal(err)
	}
}

// Status report: the running digests, the last applied version and the announced version.
func TestReportDescribesRunningState(t *testing.T) {
	e := newEnv(t, "abarow-sales")
	ctx := context.Background()
	changes := 0
	u := e.updater(func(c *release.Config) { c.OnChange = func() { changes++ } })

	rep, err := u.Report(ctx)
	if err != nil || rep.Version != "" || rep.Offered != "" || rep.Units["abarow-sales"] != oldDig || rep.Phase != release.PhaseNone {
		t.Fatalf("start: %+v %v", rep, err)
	}
	e.publish(1, "0.4.0", img("abarow-sales", newDig))
	_ = u.Poll(ctx)
	if rep, _ = u.Report(ctx); rep.Offered != "0.4.0" || rep.Phase != release.PhaseAvailable || rep.Version != "" {
		t.Fatalf("bildirimden sonra: %+v", rep)
	}
	if changes == 0 {
		t.Error("OnChange was not called when a new version was announced")
	}
	before := changes
	if err := u.Apply(ctx, "ayse", 1); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, u, release.PhaseNone)
	rep, _ = u.Report(ctx)
	if rep.Version != "0.4.0" || rep.Offered != "" || rep.Units["abarow-sales"] != newDig {
		t.Fatalf("after applying: %+v", rep)
	}
	if changes <= before {
		t.Error("OnChange was not called when the application finished")
	}
}
