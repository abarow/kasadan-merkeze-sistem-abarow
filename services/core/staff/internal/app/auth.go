// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"abarow.com/pkg/login"
	"abarow.com/services/core/staff/internal/domain"
)

// Cashiers keeps the cashiers and the sessions.
type Cashiers interface {
	// Cashier returns the cashier; domain.ErrUnknownCashier if none.
	Cashier(ctx context.Context, id string) (*domain.Cashier, error)
	// Cashiers returns the open (not closed) cashiers.
	Cashiers(ctx context.Context) ([]domain.Cashier, error)
	// SaveCashier adds the cashier or updates it with all its fields.
	SaveCashier(ctx context.Context, c domain.Cashier) error
	// SaveAttempts writes only the wrong-attempt counter and the lock; it does not overwrite
	// a change (name, PIN, closed) coming from the store server during sign-in.
	SaveAttempts(ctx context.Context, c domain.Cashier) error
	// DeleteCashierSessions closes all of the cashier's sessions.
	DeleteCashierSessions(ctx context.Context, cashierID string) error
	SaveSession(ctx context.Context, s domain.Session) error
	// Session returns the session with the given digest; domain.ErrInvalidSession if none.
	Session(ctx context.Context, tokenHash string) (*domain.Session, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	// DeleteExpiredSessions cleans up the expired sessions.
	DeleteExpiredSessions(ctx context.Context, now time.Time) error
	// SaveApproval records the manager approval.
	SaveApproval(ctx context.Context, a domain.Approval) error
	// UseApproval marks the approval as used at the moment now and returns the
	// approving manager. domain.ErrInvalidApproval if there is no approval, it is for another
	// operation, it was used or it has expired. The marking is atomic: the same
	// approval cannot be used twice.
	UseApproval(ctx context.Context, tokenHash string, action domain.Action, now time.Time) (managerID string, err error)
}

// PINHasher stores the PIN with a one-way, slow digest.
type PINHasher interface {
	Hash(pin string) ([]byte, error)
	Matches(hash []byte, pin string) bool
}

// SeedCashier is a cashier definition for the first install and simulation.
type SeedCashier struct {
	ID   string      `json:"id"`
	Name string      `json:"name"`
	Role domain.Role `json:"role"`
	PIN  string      `json:"pin"`
}

// Auth is the cashier sign-in use cases.
type Auth struct {
	cashiers Cashiers
	hasher   PINHasher
	ttl      time.Duration
	// Now returns the current time; it can be changed in tests.
	Now func() time.Time
}

// NewAuth connects the ports. ttl is how long a session stays open.
func NewAuth(cashiers Cashiers, hasher PINHasher, ttl time.Duration) *Auth {
	return &Auth{cashiers: cashiers, hasher: hasher, ttl: ttl, Now: time.Now}
}

// Seed loads the cashiers. An existing cashier's name, role and PIN are
// updated, the lock is released. A cashier managed on the store server screen
// does not change; the ids of those skipped are returned.
func (a *Auth) Seed(ctx context.Context, seeds []SeedCashier) (skipped []string, err error) {
	for _, s := range seeds {
		if err := domain.ValidatePIN(s.PIN); err != nil {
			return nil, err
		}
		if err := domain.ValidateRole(s.Role); err != nil {
			return nil, err
		}
		old, err := a.cashiers.Cashier(ctx, s.ID)
		if err == nil && old.Origin == domain.FromStore {
			skipped = append(skipped, s.ID)
			continue
		}
		if err != nil && !errors.Is(err, domain.ErrUnknownCashier) {
			return nil, err
		}
		hash, err := a.hasher.Hash(s.PIN)
		if err != nil {
			return nil, err
		}
		if err := a.cashiers.SaveCashier(ctx, domain.Cashier{ID: s.ID, Name: s.Name, Role: s.Role, PINHash: hash, Origin: domain.FromSeed}); err != nil {
			return nil, err
		}
	}
	return skipped, nil
}

// ApplyCashier applies the latest state of a cashier managed on the store server. The PIN
// comes already digested at the store server (bcrypt). If the PIN changed the wrong-attempt
// counter and the lock are reset; if the account was closed the open sessions close.
func (a *Auth) ApplyCashier(ctx context.Context, c domain.Cashier) error {
	if c.ID == "" || c.Name == "" || len(c.PINHash) == 0 {
		return fmt.Errorf("%w: %q", domain.ErrInvalidCashier, c.ID)
	}
	if err := domain.ValidateRole(c.Role); err != nil {
		return err
	}
	c.Origin = domain.FromStore
	c.FailedAttempts, c.LockedUntil = 0, time.Time{}
	old, err := a.cashiers.Cashier(ctx, c.ID)
	switch {
	case err == nil && bytes.Equal(old.PINHash, c.PINHash):
		c.FailedAttempts, c.LockedUntil = old.FailedAttempts, old.LockedUntil
	case err != nil && !errors.Is(err, domain.ErrUnknownCashier):
		return err
	}
	if err := a.cashiers.SaveCashier(ctx, c); err != nil {
		return err
	}
	if c.Disabled {
		return a.cashiers.DeleteCashierSessions(ctx, c.ID)
	}
	return nil
}

// List returns the cashiers who can sign in (not closed).
func (a *Auth) List(ctx context.Context) ([]domain.Cashier, error) { return a.cashiers.Cashiers(ctx) }

// Login verifies the PIN and opens a session; it returns the session key. After
// MaxFailedAttempts successive wrong attempts the cashier is locked.
func (a *Auth) Login(ctx context.Context, cashierID, pin string) (string, domain.Cashier, time.Time, error) {
	c, err := a.verifyPIN(ctx, cashierID, pin)
	if err != nil {
		return "", domain.Cashier{}, time.Time{}, err
	}
	now := a.Now()
	if err := a.cashiers.DeleteExpiredSessions(ctx, now); err != nil {
		return "", domain.Cashier{}, time.Time{}, err
	}
	token := newToken()
	session := domain.Session{TokenHash: hashToken(token), CashierID: c.ID, ExpiresAt: now.Add(a.ttl)}
	if err := a.cashiers.SaveSession(ctx, session); err != nil {
		return "", domain.Cashier{}, time.Time{}, err
	}
	return token, *c, session.ExpiresAt, nil
}

// verifyPIN verifies the cashier's PIN. Wrong attempts are counted; after
// MaxFailedAttempts successive wrong attempts the cashier is locked.
func (a *Auth) verifyPIN(ctx context.Context, cashierID, pin string) (*domain.Cashier, error) {
	c, err := a.cashiers.Cashier(ctx, cashierID)
	if err != nil {
		return nil, err
	}
	if c.Disabled {
		return nil, domain.ErrDisabled
	}
	now := a.Now()
	if c.Locked(now) {
		return nil, domain.LockedError{Remaining: c.LockedUntil.Sub(now)}
	}
	if !a.hasher.Matches(c.PINHash, pin) {
		remaining := c.RecordFailure(now)
		if err := a.cashiers.SaveAttempts(ctx, *c); err != nil {
			return nil, err
		}
		return nil, domain.WrongPINError{Remaining: remaining}
	}
	c.RecordSuccess()
	return c, a.cashiers.SaveAttempts(ctx, *c)
}

// Approve produces an approval key for a single operation with the manager's PIN.
// requestedBy is the cashier in the session who asks for the approval. A wrong PIN counts
// toward the manager's attempt counter.
func (a *Auth) Approve(ctx context.Context, requestedBy, managerID, pin string, action domain.Action) (string, time.Time, error) {
	if err := domain.ValidateAction(action); err != nil {
		return "", time.Time{}, err
	}
	m, err := a.verifyPIN(ctx, managerID, pin)
	if err != nil {
		return "", time.Time{}, err
	}
	if m.Role != domain.RoleManager {
		return "", time.Time{}, domain.ErrNotManager
	}
	token := newToken()
	approval := domain.Approval{
		TokenHash: hashToken(token), ManagerID: m.ID, Action: action,
		RequestedBy: requestedBy, ExpiresAt: a.Now().Add(domain.ApprovalTTL),
	}
	if err := a.cashiers.SaveApproval(ctx, approval); err != nil {
		return "", time.Time{}, err
	}
	return token, approval.ExpiresAt, nil
}

// Actor determines who is doing the operation: if an approval key is given the manager who
// gave it (the approval is used), otherwise the cashier in the session.
// The operation itself checks the authority (whether a manager is needed).
func (a *Auth) Actor(ctx context.Context, cashierID, approvalToken string, action domain.Action) (string, error) {
	if approvalToken == "" {
		return cashierID, nil
	}
	return a.cashiers.UseApproval(ctx, hashToken(approvalToken), action, a.Now())
}

// Authorize gives authority to a manager operation to be done in another service
// (e.g. refunding the payment of a sale whose receipt could not be printed): the one doing
// the operation (Actor) must be a manager. If an approval key is given it is used.
// It returns the manager's identity.
func (a *Auth) Authorize(ctx context.Context, cashierID, approvalToken string, action domain.Action) (string, error) {
	if err := domain.ValidateAction(action); err != nil {
		return "", err
	}
	actor, err := a.Actor(ctx, cashierID, approvalToken, action)
	if err != nil {
		return "", err
	}
	c, err := a.cashiers.Cashier(ctx, actor)
	if err != nil {
		return "", err
	}
	if c.Disabled {
		return "", domain.ErrDisabled
	}
	if c.Role != domain.RoleManager {
		return "", domain.ErrManagerRequired
	}
	return c.ID, nil
}

// Validate verifies the session key and returns the cashier.
func (a *Auth) Validate(ctx context.Context, token string) (domain.Cashier, error) {
	if token == "" {
		return domain.Cashier{}, domain.ErrInvalidSession
	}
	s, err := a.cashiers.Session(ctx, hashToken(token))
	if err != nil {
		return domain.Cashier{}, err
	}
	if s.Expired(a.Now()) {
		_ = a.cashiers.DeleteSession(ctx, s.TokenHash)
		return domain.Cashier{}, domain.ErrInvalidSession
	}
	c, err := a.cashiers.Cashier(ctx, s.CashierID)
	// The session of a cashier whose account is closed closes; a session opened in the
	// meantime does not pass either.
	if err != nil || c.Disabled {
		return domain.Cashier{}, domain.ErrInvalidSession
	}
	return *c, nil
}

// Logout closes the session.
func (a *Auth) Logout(ctx context.Context, token string) error {
	return a.cashiers.DeleteSession(ctx, hashToken(token))
}

// newToken produces an unguessable 256-bit session key.
func newToken() string { return login.NewToken() }

func hashToken(token string) string {
	sum := login.TokenHash(token)
	return hex.EncodeToString(sum[:])
}
