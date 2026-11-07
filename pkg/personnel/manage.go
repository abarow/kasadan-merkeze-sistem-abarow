// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package personnel

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"abarow.com/pkg/outbox"
)

// Person management: add, change name/role/scope, change PIN or password,
// close/open the account, load from a file. At the store server the manager manages the
// store staff (cashier, manager), at headquarters the administrator manages the headquarters
// users; the rule is written once here, what changes is only which roles are
// managed, who can manage, where they are stored and where the change is
// propagated.

var (
	// ErrNotFound means the person does not exist.
	ErrNotFound = errors.New("kişi bulunamadı")
	// ErrExists means a person with this code already exists.
	ErrExists = errors.New("bu kodla bir kişi zaten var")
	// ErrForbidden means the caller has no permission for this operation.
	ErrForbidden = errors.New("bu işlem için yetkiniz yok")
	// ErrSelf means the administrator locking themselves out.
	ErrSelf = errors.New("kendi hesabınızı kapatamaz, kendi yöneticiliğinizi kaldıramazsınız; başka bir yönetici yapabilir")
	// ErrLastAdmin means closing the last administrator with an open account or changing
	// their role: no one would be left to manage the people.
	ErrLastAdmin = errors.New("hesabı açık son yönetici kapatılamaz, rolü değiştirilemez; önce başka bir yönetici ekleyin")
	// ErrEmailTaken means the email address is registered to another person: it would not be
	// clear which person is meant at the first sign-in.
	ErrEmailTaken = errors.New("bu e-posta adresi başka bir kişide kayıtlı")
	// ErrIdentityTaken means the corporate identity is linked to another person.
	ErrIdentityTaken = errors.New("bu kurumsal kimlik başka bir kişiye bağlı")
	// ErrNoIdentity means there is no open person linked to the corporate identity.
	ErrNoIdentity = errors.New("bu kurumsal kimliğe bağlı bir hesap yok")
	// ErrRoleNotManaged means a role not managed at this tier was given
	// (e.g. a headquarters administrator at the store server).
	ErrRoleNotManaged = errors.New("bu rol burada yönetilmez")
)

// Change is the record of a person change.
type Change struct {
	ID     string
	At     time.Time
	By     string
	Detail string
}

// SaveRequest is the request to write the person.
type SaveRequest struct {
	User User
	// New says the person is newly added; ErrExists should be returned if the code exists.
	New    bool
	Change Change
	// Msgs are the messages to be written to the outbox in the same transaction as the change.
	Msgs []outbox.Message
	// CloseSessions asks for the person's open sessions to be deleted in the same
	// transaction (the credential changed, the account closed); the tiers that keep the sessions
	// in memory do this with Config.CloseSessions.
	CloseSessions bool
}

// Store is where the people are kept (SQLite or PostgreSQL).
type Store interface {
	// User returns the person; ErrNotFound if none.
	User(ctx context.Context, id string) (User, error)
	// Users returns all the people (including closed accounts) by their codes.
	Users(ctx context.Context) ([]User, error)
	// Save writes the person in the same transaction as the change record and the messages.
	Save(ctx context.Context, r SaveRequest) error
	// History returns the person's changes from newest to oldest.
	History(ctx context.Context, id string, limit int) ([]Change, error)
}

// Encoder turns the person change into the messages to be propagated (store server:
// to the registers with the PIN digest, to headquarters without the PIN). If empty there is no propagation.
type Encoder interface {
	Encode(u User) ([]outbox.Message, error)
}

// Actor is the one doing the operation. Manage is the authority to do the operation: the tier
// determines it (the manager with an open session at the store server, the administrator at headquarters).
type Actor struct {
	ID, Name string
	Manage   bool
}

// Config is Manage's tier-specific settings.
type Config struct {
	// Roles are the roles managed here (store server: cashier, manager; back
	// office: administrator, catalog, report).
	Roles []Role
	// Admins are the protected administrator roles: an administrator cannot lock themselves out,
	// the last administrator with an open account cannot be closed or have their role changed. If
	// empty there is no protection (store server: when no manager with an open account is left the
	// store password opens again).
	Admins []Role
	// ByName sorts the list by name (Turkish alphabet, open accounts first);
	// if absent the code order stays.
	ByName bool
	// Encoder is the change's propagation; if empty there is no propagation.
	Encoder Encoder
	// BoundSessions says the sessions are bound to the credential's fingerprint
	// (login.Session.Binding, e.g. the PIN digest): when the credential changes the sessions with the old
	// fingerprint close anyway (Directory.Resolve), so SaveRequest.CloseSessions is not
	// requested; when a person changes their own PIN this session can be kept open with
	// login.Sessions.Rebind.
	BoundSessions bool
	// CloseSessions closes the person's sessions (if the sessions are not in the database,
	// e.g. in memory); the sessions in the database are deleted in the same transaction as the
	// SaveRequest.
	CloseSessions func(ctx context.Context, id string) error
	// Now is the clock; time.Now if empty.
	Now func() time.Time
}

// Manage is the use cases of person management.
type Manage struct {
	store Store
	cfg   Config
	// HashPIN produces the PIN digest to be stored (bcrypt; staff at the register verifies
	// with the same digest). Tests can set a fast digest.
	HashPIN func(pin string) ([]byte, error)
	// HashPassword produces the password digest to be stored.
	HashPassword func(password string) (string, error)

	// identity serializes the corporate identity linking: the same identity or
	// address cannot be linked to two people at the same time.
	identity sync.Mutex
}

// NewManage returns a service.
func NewManage(store Store, cfg Config) *Manage {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Manage{store: store, cfg: cfg,
		HashPIN: func(pin string) ([]byte, error) {
			return bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
		},
		HashPassword: func(p string) (string, error) {
			h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
			return string(h), err
		},
	}
}

// SetClock changes the service's clock (for tests).
func (m *Manage) SetClock(now func() time.Time) { m.cfg.Now = now }

func (m *Manage) managed(r Role) error {
	if slices.Contains(m.cfg.Roles, r) {
		return nil
	}
	if !r.Known() {
		names := make([]string, len(m.cfg.Roles))
		for i, o := range m.cfg.Roles {
			names[i] = RoleName(o)
		}
		return fmt.Errorf("%w (%q): %s olmalı", ErrInvalidRole, r, strings.Join(names, ", "))
	}
	return fmt.Errorf("%w (%q)", ErrRoleNotManaged, r)
}

func (m *Manage) admin(r Role) bool { return slices.Contains(m.cfg.Admins, r) }

func allowed(a Actor) error {
	if !a.Manage {
		return ErrForbidden
	}
	return nil
}

// Input is the details of the person that can be changed.
type Input struct {
	Name      string
	Role      Role
	AllStores bool
	Stores    []string
	// Email is the address by which they will be recognized at the first corporate sign-in (none if empty).
	Email string
}

// List returns the people (if ByName, open accounts first, in Turkish alphabet order).
func (m *Manage) List(ctx context.Context, actor Actor) ([]User, error) {
	if err := allowed(actor); err != nil {
		return nil, err
	}
	list, err := m.store.Users(ctx)
	if err != nil {
		return nil, err
	}
	if m.cfg.ByName {
		col := collate.New(language.Turkish)
		slices.SortStableFunc(list, func(a, b User) int {
			if a.Disabled != b.Disabled {
				if a.Disabled {
					return 1
				}
				return -1
			}
			if c := col.CompareString(a.Name, b.Name); c != 0 {
				return c
			}
			return cmp.Compare(a.ID, b.ID)
		})
	}
	return list, nil
}

// Names is the mapping from codes to names (for reports). It asks for no permission: reports
// show the people's names anyway.
func (m *Manage) Names(ctx context.Context) (map[string]string, error) {
	list, err := m.store.Users(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(list))
	for _, u := range list {
		names[u.ID] = u.Name
	}
	return names, nil
}

// Get returns the person with their changes.
func (m *Manage) Get(ctx context.Context, actor Actor, id string) (User, []Change, error) {
	if err := allowed(actor); err != nil {
		return User{}, nil, err
	}
	u, err := m.find(ctx, id)
	if err != nil {
		return User{}, nil, err
	}
	history, err := m.store.History(ctx, u.ID, 100)
	return u, history, err
}

// find resolves the code without the role: first as it is, otherwise lower-cased (headquarters
// usernames are lower case).
func (m *Manage) find(ctx context.Context, id string) (User, error) {
	u, err := m.store.User(ctx, id)
	if errors.Is(err, ErrNotFound) {
		if lower, uerr := Username(id); uerr == nil && lower != id {
			u, err = m.store.User(ctx, lower)
		}
	}
	return u, err
}

// Add adds a new person; secret is the role's credential (PIN or password).
func (m *Manage) Add(ctx context.Context, actor Actor, id, secret string, in Input) (User, error) {
	if err := allowed(actor); err != nil {
		return User{}, err
	}
	if err := m.managed(in.Role); err != nil {
		return User{}, err
	}
	id, err := CleanID(in.Role, id)
	if err != nil {
		return User{}, err
	}
	// For a headquarters user, if the name is empty the username is used; for store
	// staff the name is mandatory.
	if strings.TrimSpace(in.Name) == "" && !in.Role.StoreStaff() {
		in.Name = id
	}
	u, err := m.apply(User{ID: id, CreatedAt: m.cfg.Now()}, in)
	if err != nil {
		return User{}, err
	}
	if err := m.setSecret(&u, secret); err != nil {
		return User{}, err
	}
	m.identity.Lock()
	defer m.identity.Unlock()
	if err := m.freeEmail(ctx, u); err != nil {
		return User{}, err
	}
	detail := fmt.Sprintf("eklendi: %s (%s)", u.Name, RoleName(u.Role))
	if !u.Role.StoreStaff() {
		detail = fmt.Sprintf("eklendi: %s (%s; %s)", u.Name, RoleName(u.Role), u.ScopeName())
	}
	return u, m.save(ctx, actor, &u, detail, true, false)
}

// Update changes the person's name, role and scope.
func (m *Manage) Update(ctx context.Context, actor Actor, id string, in Input) (User, error) {
	if err := allowed(actor); err != nil {
		return User{}, err
	}
	u, err := m.find(ctx, id)
	if err != nil {
		return User{}, err
	}
	next, err := m.apply(u, in)
	if err != nil {
		return User{}, err
	}
	m.identity.Lock()
	defer m.identity.Unlock()
	if err := m.freeEmail(ctx, next); err != nil {
		return User{}, err
	}
	if next.Role.Credential() != u.Role.Credential() {
		return User{}, fmt.Errorf("%w: rol, kimlik bilgisi türünü değiştiremez (%s → %s)", ErrRoleNotManaged, RoleName(u.Role), RoleName(next.Role))
	}
	if m.admin(u.Role) && next.Role != u.Role {
		if err := m.keepAdmin(ctx, actor, u); err != nil {
			return User{}, err
		}
	}
	var details []string
	if u.Name != next.Name {
		details = append(details, fmt.Sprintf("ad: %s → %s", u.Name, next.Name))
	}
	if u.Role != next.Role {
		details = append(details, fmt.Sprintf("rol: %s → %s", RoleName(u.Role), RoleName(next.Role)))
	}
	if !u.Role.StoreStaff() && u.ScopeName() != next.ScopeName() {
		details = append(details, fmt.Sprintf("mağazalar: %s → %s", u.ScopeName(), next.ScopeName()))
	}
	if u.Email != next.Email {
		details = append(details, fmt.Sprintf("e-posta: %s → %s", orNone(u.Email), orNone(next.Email)))
	}
	if len(details) == 0 {
		return u, nil
	}
	return next, m.save(ctx, actor, &next, strings.Join(details, "; "), false, false)
}

// SetEmail changes the person's predefined email address (empty: removes it);
// their other details are kept.
func (m *Manage) SetEmail(ctx context.Context, actor Actor, id, email string) (User, error) {
	if err := allowed(actor); err != nil {
		return User{}, err
	}
	u, err := m.find(ctx, id)
	if err != nil {
		return User{}, err
	}
	return m.Update(ctx, actor, id, Input{Name: u.Name, Role: u.Role, AllStores: u.AllStores, Stores: u.Stores, Email: email})
}

// SetSecret changes the person's PIN or password (by their role); their open sessions
// close, and if locked at the register the lock is released.
func (m *Manage) SetSecret(ctx context.Context, actor Actor, id, secret string) (User, error) {
	if err := allowed(actor); err != nil {
		return User{}, err
	}
	return m.changeSecret(ctx, actor, id, secret, "")
}

// ChangeOwnSecret is a person changing their own password or PIN.
// The caller has verified the current credential; no permission is asked.
func (m *Manage) ChangeOwnSecret(ctx context.Context, self Actor, secret string) (User, error) {
	return m.changeSecret(ctx, self, self.ID, secret, " (kendisi)")
}

func (m *Manage) changeSecret(ctx context.Context, actor Actor, id, secret, suffix string) (User, error) {
	u, err := m.find(ctx, id)
	if err != nil {
		return User{}, err
	}
	if err := m.setSecret(&u, secret); err != nil {
		return User{}, err
	}
	what := "PIN"
	if u.Role.Credential() == CredentialPassword {
		what = "parola"
	}
	// When the PIN and password change the sessions close: no session opened with the old
	// credential should remain.
	return u, m.save(ctx, actor, &u, what+" değişti"+suffix, false, !m.cfg.BoundSessions)
}

// SetDisabled closes the account (the sessions close too) or reopens it.
func (m *Manage) SetDisabled(ctx context.Context, actor Actor, id string, disabled bool) (User, error) {
	if err := allowed(actor); err != nil {
		return User{}, err
	}
	u, err := m.find(ctx, id)
	if err != nil {
		return User{}, err
	}
	if u.Disabled == disabled {
		return u, nil
	}
	if disabled && m.admin(u.Role) {
		if err := m.keepAdmin(ctx, actor, u); err != nil {
			return User{}, err
		}
	}
	detail := "hesap açıldı"
	if disabled {
		detail = "hesap kapatıldı"
	}
	u.Disabled = disabled
	return u, m.save(ctx, actor, &u, detail, false, disabled)
}

// Seed is a person in the registers' person file (cashiers.json); once the file is loaded
// to the back house the staff is managed from there.
type Seed struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role Role   `json:"role"`
	PIN  string `json:"pin"`
}

// Import loads the person file: a missing person is added, an existing one's name, role and
// PIN change to what is in the file (if the account is closed it stays closed). The whole
// file is checked first; if faulty none is loaded. It returns the number loaded.
func (m *Manage) Import(ctx context.Context, actor Actor, seeds []Seed) (int, error) {
	if err := allowed(actor); err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	for i, sc := range seeds {
		fail := func(err error) (int, error) { return 0, fmt.Errorf("%d. kişi (%q): %w", i+1, sc.ID, err) }
		if err := m.managed(sc.Role); err != nil {
			return fail(err)
		}
		if sc.Role.Credential() != CredentialPIN {
			return fail(fmt.Errorf("%w: dosya yalnızca PIN'li rolleri yükler", ErrRoleNotManaged))
		}
		if err := ValidateID(sc.ID); err != nil {
			return fail(err)
		}
		if seen[sc.ID] {
			return fail(ErrExists)
		}
		seen[sc.ID] = true
		if _, err := CleanName(sc.Name); err != nil {
			return fail(err)
		}
		if err := ValidatePIN(sc.PIN); err != nil {
			return fail(err)
		}
	}
	for _, sc := range seeds {
		name, _ := CleanName(sc.Name)
		u, err := m.store.User(ctx, sc.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return 0, err
		}
		isNew := err != nil
		detail := fmt.Sprintf("dosyadan yüklendi: %s (%s)", name, RoleName(sc.Role))
		if isNew {
			u = User{ID: sc.ID, CreatedAt: m.cfg.Now()}
			detail = "eklendi; " + detail
		}
		u.Name, u.Role = name, sc.Role
		if u.PINHash, err = m.HashPIN(sc.PIN); err != nil {
			return 0, err
		}
		if err := m.save(ctx, actor, &u, detail, isNew, !isNew && !m.cfg.BoundSessions); err != nil {
			return 0, err
		}
	}
	return len(seeds), nil
}

// apply applies the details that can be changed to the person and checks them.
func (m *Manage) apply(u User, in Input) (User, error) {
	if err := m.managed(in.Role); err != nil {
		return User{}, err
	}
	name, err := CleanName(in.Name)
	if err != nil {
		return User{}, err
	}
	email, err := CleanEmail(in.Email)
	if err != nil {
		return User{}, err
	}
	u.Name, u.Role, u.AllStores, u.Stores, u.Email = name, in.Role, in.AllStores, slices.Clone(in.Stores), email
	return u, u.ValidateScope()
}

func orNone(s string) string {
	if s == "" {
		return "yok"
	}
	return s
}

// freeEmail checks that u's email address is not registered to another
// person.
func (m *Manage) freeEmail(ctx context.Context, u User) error {
	if u.Email == "" {
		return nil
	}
	list, err := m.store.Users(ctx)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(list, func(o User) bool { return o.ID != u.ID && o.Email == u.Email }) {
		return ErrEmailTaken
	}
	return nil
}

// setSecret checks the credential by the role and digests it.
func (m *Manage) setSecret(u *User, secret string) (err error) {
	switch u.Role.Credential() {
	case CredentialPIN:
		if err = ValidatePIN(secret); err != nil {
			return err
		}
		u.PINHash, err = m.HashPIN(secret)
	case CredentialPassword:
		if err = ValidatePassword(secret); err != nil {
			return err
		}
		u.PasswordHash, err = m.HashPassword(secret)
	}
	return err
}

// keepAdmin: does it allow the administrator (u) to lose administratorship or have their
// account closed: they cannot do it themselves; they cannot be the last administrator
// with an open account.
func (m *Manage) keepAdmin(ctx context.Context, actor Actor, u User) error {
	if actor.ID == u.ID {
		return ErrSelf
	}
	if u.Disabled {
		return nil
	}
	list, err := m.store.Users(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(list, func(o User) bool { return o.ID != u.ID && m.admin(o.Role) && !o.Disabled }) {
		return ErrLastAdmin
	}
	return nil
}

func (m *Manage) save(ctx context.Context, actor Actor, u *User, detail string, isNew, closeSessions bool) error {
	u.UpdatedBy, u.UpdatedAt = actor.Name, m.cfg.Now()
	var msgs []outbox.Message
	if m.cfg.Encoder != nil {
		var err error
		if msgs, err = m.cfg.Encoder.Encode(*u); err != nil {
			return err
		}
	}
	err := m.store.Save(ctx, SaveRequest{
		User: *u, New: isNew, Msgs: msgs, CloseSessions: closeSessions,
		Change: Change{ID: u.ID, At: u.UpdatedAt, By: actor.Name, Detail: detail},
	})
	if err != nil {
		return err
	}
	if closeSessions && m.cfg.CloseSessions != nil {
		return m.cfg.CloseSessions(ctx, u.ID)
	}
	return nil
}

// Identity is the identity the IdP proves (corporate sign-in).
type Identity struct {
	// Issuer and Subject together identify the person unambiguously.
	Issuer, Subject string
	// Email is the address the IdP reported; EmailVerified means the IdP verified it.
	// An unverified address is not enough for any match.
	Email         string
	EmailVerified bool
}

// Identify returns the person linked to the corporate identity (a closed account is returned
// too: the caller rejects it). If the identity is not linked to any person and the verified
// address is the same as the predefined address of a person with an open account whose
// identity is not yet linked, the identity is linked to that person (first sign-in); otherwise
// ErrNoIdentity.
func (m *Manage) Identify(ctx context.Context, id Identity) (User, error) {
	if id.Issuer == "" || id.Subject == "" {
		return User{}, ErrNoIdentity
	}
	m.identity.Lock()
	defer m.identity.Unlock()
	list, err := m.store.Users(ctx)
	if err != nil {
		return User{}, err
	}
	if i := slices.IndexFunc(list, func(u User) bool { return u.Issuer == id.Issuer && u.Subject == id.Subject }); i >= 0 {
		return list[i], nil
	}
	email := strings.ToLower(strings.TrimSpace(id.Email))
	if !id.EmailVerified || email == "" {
		return User{}, ErrNoIdentity
	}
	i := slices.IndexFunc(list, func(u User) bool { return u.Email == email && u.Subject == "" && !u.Disabled })
	if i < 0 {
		return User{}, ErrNoIdentity
	}
	u := list[i]
	u.Issuer, u.Subject = id.Issuer, id.Subject
	err = m.save(ctx, Actor{ID: u.ID, Name: u.Name}, &u, "kurumsal kimlik bağlandı (ilk girişte e-postayla eşleşti)", false, false)
	return u, err
}

// Own is the signed-in person's own record (no permission is asked).
func (m *Manage) Own(ctx context.Context, self Actor) (User, error) {
	return m.find(ctx, self.ID)
}

// LinkOwn is a signed-in person linking a corporate identity to their own account.
// The person themselves proved the identity at the IdP; it is not linked on behalf of someone else.
func (m *Manage) LinkOwn(ctx context.Context, self Actor, issuer, subject string) (User, error) {
	if issuer == "" || subject == "" {
		return User{}, ErrNoIdentity
	}
	m.identity.Lock()
	defer m.identity.Unlock()
	list, err := m.store.Users(ctx)
	if err != nil {
		return User{}, err
	}
	var u User
	for _, o := range list {
		if o.ID == self.ID {
			u = o
		} else if o.Issuer == issuer && o.Subject == subject {
			return User{}, ErrIdentityTaken
		}
	}
	if u.ID == "" {
		return User{}, ErrNotFound
	}
	if u.Issuer == issuer && u.Subject == subject {
		return u, nil
	}
	u.Issuer, u.Subject = issuer, subject
	return u, m.save(ctx, self, &u, "kurumsal kimlik bağlandı (kendisi)", false, false)
}

// Unlink removes the person's corporate identity link: the person themselves or an administrator.
// Open sessions do not close; the person keeps signing in with the local method (if any).
func (m *Manage) Unlink(ctx context.Context, actor Actor, id string) (User, error) {
	if actor.ID != id {
		if err := allowed(actor); err != nil {
			return User{}, err
		}
	}
	m.identity.Lock()
	defer m.identity.Unlock()
	u, err := m.find(ctx, id)
	if err != nil {
		return User{}, err
	}
	if u.Subject == "" {
		return u, nil
	}
	u.Issuer, u.Subject = "", ""
	detail := "kurumsal kimlik bağı kaldırıldı"
	if actor.ID == u.ID {
		detail += " (kendisi)"
	}
	return u, m.save(ctx, actor, &u, detail, false, false)
}
