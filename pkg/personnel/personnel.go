// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package personnel is the single definition of a person: a cashier, a manager and a
// headquarters user are the same model (User), a cashier is a role. The code, name, role, PIN and
// password rules, person management (Manage) and the cashier message
// (staff.v1.CashierState) are here. Cashiers are managed in the back
// house; the message goes to the registers (config.cashier.<cashier>, with the PIN
// digest) and to headquarters (store.cashier.changed.v1, without the PIN). The register,
// store server and headquarters use the same rules and the same decoder.
package personnel

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	staffv1 "abarow.com/gen/com/abarow/staff/v1"
)

// Role, kasiyerin yetkisidir.
type Role string

const (
	RoleCashier Role = "cashier"
	// RoleManager takes the end of day (Z) and gives manager approval at the register.
	RoleManager Role = "manager"
)

const (
	// MaxIDLen and MaxNameLen are the length limits of the code and the name.
	MaxIDLen   = 32
	MaxNameLen = 64
)

var (
	ErrInvalidID   = fmt.Errorf("kasiyer kodu %d karakteri geçmeyen küçük harf (Türkçe karakter olmadan), rakam, - ya da _ olmalı", MaxIDLen)
	ErrInvalidName = fmt.Errorf("kasiyerin adı boş olamaz, en çok %d karakter", MaxNameLen)
	ErrInvalidRole = errors.New("rol geçersiz")
	ErrInvalidPIN  = errors.New("PIN 4–8 haneli bir sayı olmalı")
	// ErrUndecodable is a cashier message that cannot be read; it does not get better
	// by retrying.
	ErrUndecodable = errors.New("kasiyer mesajı çözülemedi")
)

// Cashier is a store's cashier. The PIN is carried only as its one-way digest
// (bcrypt); the message that goes to headquarters has no digest either.
type Cashier struct {
	StoreID, ID, Name string
	Role              Role
	PINHash           []byte
	// Disabled means the account is closed (a cashier who left the job): they cannot
	// sign in. The cashier is not deleted; their id stays in the sales and shifts.
	Disabled  bool
	UpdatedBy string
	UpdatedAt time.Time
}

// ValidateID checks that the code can be used in a NATS subject and at the register:
// a-z, 0-9, - and _.
func ValidateID(id string) error {
	if id == "" || len(id) > MaxIDLen {
		return ErrInvalidID
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return ErrInvalidID
		}
	}
	return nil
}

// CleanName strips the leading, trailing and repeated spaces of the name and
// checks it.
func CleanName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || utf8.RuneCountInString(name) > MaxNameLen {
		return "", ErrInvalidName
	}
	return name, nil
}

// ValidateRole checks that the role is a known role.
func ValidateRole(r Role) error {
	if r != RoleCashier && r != RoleManager {
		return fmt.Errorf("%w (%q): kasiyer ya da müdür olmalı", ErrInvalidRole, r)
	}
	return nil
}

// ValidatePIN checks that the PIN is a 4–8 digit number.
func ValidatePIN(pin string) error {
	if len(pin) < 4 || len(pin) > 8 {
		return ErrInvalidPIN
	}
	for i := 0; i < len(pin); i++ {
		if pin[i] < '0' || pin[i] > '9' {
			return ErrInvalidPIN
		}
	}
	return nil
}

// RoleName is the name of the role shown on the screen and in the change record.
func RoleName(r Role) string {
	switch r {
	case RoleManager:
		return "müdür"
	case RoleAdmin:
		return "yönetici"
	case RoleCatalog:
		return "katalog"
	case RoleReport:
		return "rapor"
	}
	return "kasiyer"
}

// Encode is the cashier's message; if not withPIN the PIN digest is not put in (the
// event that goes to headquarters).
func Encode(c Cashier, eventID string, withPIN bool) ([]byte, error) {
	m := &staffv1.CashierState{
		EventId: eventID, OccurredAt: timestamppb.New(c.UpdatedAt), StoreId: c.StoreID, CashierId: c.ID, Name: c.Name,
		Role: string(c.Role), Disabled: c.Disabled, UpdatedBy: c.UpdatedBy,
	}
	if withPIN {
		m.PinHash = c.PINHash
	}
	return proto.Marshal(m)
}

// Decode decodes the cashier message.
func Decode(contentType string, data []byte) (Cashier, error) {
	if !strings.HasPrefix(contentType, "application/protobuf") {
		return Cashier{}, fmt.Errorf("%w: içerik türü %q", ErrUndecodable, contentType)
	}
	var s staffv1.CashierState
	if err := proto.Unmarshal(data, &s); err != nil {
		return Cashier{}, fmt.Errorf("%w: %w", ErrUndecodable, err)
	}
	c := Cashier{StoreID: s.GetStoreId(), ID: s.GetCashierId(), Name: s.GetName(), Role: Role(s.GetRole()),
		PINHash: s.GetPinHash(), Disabled: s.GetDisabled(), UpdatedBy: s.GetUpdatedBy()}
	if s.GetOccurredAt() != nil {
		c.UpdatedAt = s.GetOccurredAt().AsTime()
	}
	return c, nil
}
