// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package agent is the registration and token lifecycle of the update agent
// that runs on a unit (register, store server, headquarters).
//
// On first start the agent generates the device identity, asks the registration server of
// the tier above for registration and waits for approval. When approved it gets an image pull
// token and renews it before it expires. It exposes its state (waiting for approval, approved,
// rejected, revoked) so that the unit's screen can show it; it shows the approval code here too.
// The private key is only in the identity directory and is not sent anywhere.
package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"abarow.com/services/update/internal/identity"
	"abarow.com/services/update/internal/registration"
	"abarow.com/services/update/internal/token"
)

// Transport is the port that goes to the registration server of the tier above.
type Transport interface {
	Submit(ctx context.Context, s registration.Submission) (registration.Status, error)
	Status(ctx context.Context, device string) (registration.Status, error)
	Challenge(ctx context.Context, device string) (string, error)
	Token(ctx context.Context, device, nonce string, sig []byte) (registration.TokenResult, error)
	// Report sends the signed status report to the tier above.
	Report(ctx context.Context, device string, ts time.Time, payload, sig []byte) error
}

// State is the agent's state.
type State string

// Durumlar.
const (
	StateStarting         State = "starting"
	StatePending          State = "pending"
	StateApproved         State = "approved"
	StateRejected         State = "rejected"
	StateRevoked          State = "revoked"
	StateIdentityMismatch State = "identity_mismatch"
)

// Status is the state the screen shows (it contains no token).
type Status struct {
	State       State     `json:"state"`
	Device      string    `json:"device"`
	Code        string    `json:"code"` // the code the approver will enter
	Tier        string    `json:"tier"`
	Level       string    `json:"level"`
	TokenExpiry time.Time `json:"token_expiry,omitzero"`
	Error       string    `json:"error,omitempty"`
	Updated     time.Time `json:"updated"`
}

// File names (in the identity directory).
const (
	tokenFile   = "token.json"
	certFile    = "certificate.jwt"
	signingFile = "signing.key"
)

// Config is the agent's settings.
type Config struct {
	// Dir is the directory where the identity and the token are stored (only the agent writes).
	Dir string
	// Root is the machine's root file system for the hardware fingerprint (os.DirFS("/") in production).
	Root fs.FS
	Tier token.Tier
	// Level is the update level; prod if empty.
	Level    token.Level
	Store    string
	Register string
	Name     string

	Transport Transport
	Log       *slog.Logger
	Now       func() time.Time
	// Rand is for key generation (tests); crypto/rand if nil.
	Rand io.Reader
	// OnToken is called when a valid token is seen for the first time or is renewed
	// (to write the credentials file that feeds the mirror with this token). An error is written to the log.
	OnToken func(token string, expires time.Time) error
	// Reporter gives the unit's running version state; if given, the agent reports this state to the tier above
	// while approved (immediately when it changes, once per ReportEvery when it does not).
	Reporter func(ctx context.Context) (registration.Report, error)
	// ReportEvery is the interval at which an unchanged state is reported again; 5 minutes if zero.
	ReportEvery time.Duration
}

// Zamanlama.
const (
	pendingEvery   = 15 * time.Second
	terminalEvery  = 10 * time.Minute
	maxApprovedGap = time.Hour
	minBackoff     = 30 * time.Second
	maxBackoff     = 5 * time.Minute
)

type saved struct {
	Token   string    `json:"token"`
	Issued  time.Time `json:"issued_at"`
	Expires time.Time `json:"expires_at"`
}

// Agent is a unit's update agent.
type Agent struct {
	cfg Config
	id  *identity.Identity
	// signing, only at the headquarters tier: headquarters' token signing key.
	signing ed25519.PrivateKey

	mu        sync.Mutex
	status    Status
	tok       saved
	submitted bool
	backoff   time.Duration
	announced string // OnToken'a bildirilen son jeton

	wake        chan struct{} // Nudge: beklemeyi keser
	lastReport  time.Time
	lastPayload []byte
}

// New builds the agent: loads or generates the identity, reads the saved token if there is one.
// If the hardware fingerprint does not match the saved identity (a disk was copied) the agent
// stays in the identity_mismatch state and sends no request.
func New(cfg Config) (*Agent, error) {
	if cfg.Dir == "" || cfg.Root == nil || cfg.Transport == nil || cfg.Tier == "" {
		return nil, errors.New("ajan ayarları eksik")
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
	if cfg.ReportEvery <= 0 {
		cfg.ReportEvery = 5 * time.Minute
	}
	a := &Agent{cfg: cfg, wake: make(chan struct{}, 1)}
	fp, _, err := identity.ReadFingerprint(cfg.Root)
	if err != nil {
		return nil, err
	}
	a.status = Status{State: StateStarting, Tier: string(cfg.Tier), Level: string(cfg.Level), Updated: cfg.Now()}

	id, err := identity.Load(cfg.Dir, fp)
	switch {
	case err == nil:
	case errors.Is(err, identity.ErrNotFound):
		var r io.Reader = rand.Reader
		if cfg.Rand != nil {
			r = cfg.Rand
		}
		if id, err = identity.Generate(fp, r); err != nil {
			return nil, err
		}
		if err := identity.Save(cfg.Dir, id); err != nil {
			return nil, err
		}
	case errors.Is(err, identity.ErrFingerprintChanged):
		a.setState(StateIdentityMismatch, identity.ErrFingerprintChanged.Error())
		return a, nil
	default:
		return nil, err
	}
	a.id = id
	a.status.Device, a.status.Code = id.ID, identity.Code(id.ID)

	if cfg.Tier == token.Headquarters {
		if a.signing, err = loadOrCreateSigningKey(filepath.Join(cfg.Dir, signingFile), cfg.Rand); err != nil {
			return nil, err
		}
	}
	if raw, err := os.ReadFile(filepath.Join(cfg.Dir, tokenFile)); err == nil {
		var s saved
		if json.Unmarshal(raw, &s) == nil && s.Token != "" {
			a.tok = s
			a.status.TokenExpiry = s.Expires
		}
	}
	return a, nil
}

func loadOrCreateSigningKey(path string, r io.Reader) (ed25519.PrivateKey, error) {
	if raw, err := os.ReadFile(path); err == nil {
		seed, err := hex.DecodeString(string(raw))
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, errors.New("imza anahtarı dosyası bozuk")
		}
		return ed25519.NewKeyFromSeed(seed), nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if r == nil {
		r = rand.Reader
	}
	_, priv, err := ed25519.GenerateKey(r)
	if err != nil {
		return nil, err
	}
	if err := writeFile(path, []byte(hex.EncodeToString(priv.Seed()))); err != nil {
		return nil, err
	}
	return priv, nil
}

// writeFile writes the file with a temporary file + rename (no half-written file is left).
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

func (a *Agent) setState(s State, errMsg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status.State, a.status.Error, a.status.Updated = s, errMsg, a.cfg.Now()
}

// Status returns the state the screen will show.
func (a *Agent) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// Token returns the token if it has not expired.
func (a *Agent) Token() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tok.Token == "" || !a.cfg.Now().Before(a.tok.Expires) {
		return "", false
	}
	return a.tok.Token, true
}

// SigningKey is the token signing key at the headquarters tier (nil at the other tiers).
func (a *Agent) SigningKey() ed25519.PrivateKey { return a.signing }

// renewAt is the moment the token should be renewed (the last third of its lifetime).
func (s saved) renewAt() time.Time {
	return s.Expires.Add(-s.Expires.Sub(s.Issued) / 3)
}

// Run runs the loop until ctx ends.
func (a *Agent) Run(ctx context.Context) {
	for {
		wait := a.Step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-a.wake:
		}
	}
}

// Step takes a step and returns how long to wait until the next one.
func (a *Agent) Step(ctx context.Context) time.Duration {
	if a.id == nil { // identity mismatch: no request is sent
		return terminalEvery
	}
	wait, err := a.step(ctx)
	if err == nil {
		a.mu.Lock()
		a.backoff = 0
		a.mu.Unlock()
		return wait
	}
	a.cfg.Log.Warn("update agent step failed", "err", err)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status.Error, a.status.Updated = err.Error(), a.cfg.Now()
	switch {
	case a.backoff == 0:
		a.backoff = minBackoff
	case a.backoff < maxBackoff:
		a.backoff *= 2
	}
	return min(a.backoff, maxBackoff)
}

func (a *Agent) step(ctx context.Context) (time.Duration, error) {
	if !a.submitted {
		if err := a.submit(ctx); err != nil {
			return 0, err
		}
	}
	st, err := a.cfg.Transport.Status(ctx, a.id.ID)
	if errors.Is(err, registration.ErrNotFound) { // the server lost the registration: send it again
		a.submitted = false
		return minBackoff, nil
	}
	if err != nil {
		return 0, err
	}
	switch st {
	case registration.Pending:
		a.setState(StatePending, "")
		return pendingEvery, nil
	case registration.Rejected:
		a.setState(StateRejected, "")
		return terminalEvery, nil
	case registration.Revoked:
		a.setState(StateRevoked, "")
		return terminalEvery, nil
	case registration.Approved:
	default:
		return 0, errors.New("sunucu bilinmeyen durum döndü: " + string(st))
	}
	wait, err := a.ensureToken(ctx)
	if err != nil {
		return 0, err
	}
	a.report(ctx)
	if a.cfg.Reporter != nil {
		wait = min(wait, a.cfg.ReportEvery)
	}
	return wait, nil
}

// SetReporter connects the status report; it is called before Run. 5 minutes if every is zero.
func (a *Agent) SetReporter(fn func(ctx context.Context) (registration.Report, error), every time.Duration) {
	a.cfg.Reporter = fn
	if every > 0 {
		a.cfg.ReportEvery = every
	}
}

// Nudge cuts the agent's wait short: the state changed (e.g. a version was applied), it should report right away.
func (a *Agent) Nudge() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// report reports the unit's running version state to the tier above. A failure does not break
// the step (the report is auxiliary information); it goes right away when the state changes, once per ReportEvery when it does not.
func (a *Agent) report(ctx context.Context) {
	if a.cfg.Reporter == nil {
		return
	}
	rep, err := a.cfg.Reporter(ctx)
	if err != nil {
		a.cfg.Log.Warn("could not prepare the status report", "err", err)
		return
	}
	rep.At = time.Time{} // the server writes the time
	payload, err := json.Marshal(rep)
	if err != nil {
		return
	}
	now := a.cfg.Now()
	if bytes.Equal(payload, a.lastPayload) && now.Sub(a.lastReport) < a.cfg.ReportEvery {
		return
	}
	sig := a.id.Sign(registration.ReportMessage(a.id.ID, now, payload))
	if err := a.cfg.Transport.Report(ctx, a.id.ID, now, payload, sig); err != nil {
		a.cfg.Log.Warn("durum bildirilemedi", "err", err)
		return
	}
	a.lastPayload, a.lastReport = payload, now
}

func (a *Agent) submit(ctx context.Context) error {
	s := registration.Submission{
		Device: a.id.ID, Public: a.id.Public, Fingerprint: string(a.id.Fingerprint),
		Tier: a.cfg.Tier, Level: a.cfg.Level, Store: a.cfg.Store, Register: a.cfg.Register, Name: a.cfg.Name,
	}
	if a.signing != nil {
		s.SigningKey = a.signing.Public().(ed25519.PublicKey)
	}
	s.Signature = a.id.Sign(registration.SubmitMessage(s.Device, s.Tier, s.Level, s.Name, s.SigningKey))
	if _, err := a.cfg.Transport.Submit(ctx, s); err != nil {
		return err
	}
	a.submitted = true
	return nil
}

func (a *Agent) ensureToken(ctx context.Context) (time.Duration, error) {
	now := a.cfg.Now()
	a.mu.Lock()
	t := a.tok
	a.mu.Unlock()
	if t.Token != "" && now.Before(t.renewAt()) {
		a.setState(StateApproved, "")
		a.announce(t)
		return min(t.renewAt().Sub(now), maxApprovedGap), nil
	}
	nonce, err := a.cfg.Transport.Challenge(ctx, a.id.ID)
	if err != nil {
		return 0, err
	}
	res, err := a.cfg.Transport.Token(ctx, a.id.ID, nonce, a.id.Sign(token.PossessionMessage(a.id.ID, nonce)))
	if err != nil {
		return 0, err
	}
	s := saved{Token: res.Token, Issued: now, Expires: res.Expiry}
	raw, err := json.Marshal(s)
	if err != nil {
		return 0, err
	}
	if err := writeFile(filepath.Join(a.cfg.Dir, tokenFile), raw); err != nil {
		return 0, err
	}
	if res.Cert != "" {
		if err := writeFile(filepath.Join(a.cfg.Dir, certFile), []byte(res.Cert)); err != nil {
			return 0, err
		}
	}
	a.mu.Lock()
	a.tok = s
	a.status.TokenExpiry = s.Expires
	a.mu.Unlock()
	a.setState(StateApproved, "")
	a.announce(s)
	return min(s.renewAt().Sub(now), maxApprovedGap), nil
}

// announce, yeni jetonu OnToken'a bir kez bildirir.
func (a *Agent) announce(t saved) {
	if a.cfg.OnToken == nil || a.announced == t.Token {
		return
	}
	if err := a.cfg.OnToken(t.Token, t.Expires); err != nil {
		a.cfg.Log.Warn("token notification failed", "err", err)
		return // it is retried at the next step
	}
	a.announced = t.Token
}
