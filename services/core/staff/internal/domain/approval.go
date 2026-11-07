// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"time"
)

// Action is an operation that requires manager authority.
type Action string

const (
	// ActionCloseDay is the end of day (Z).
	ActionCloseDay Action = "close_day"
	// ActionCashOut is taking money out of the register.
	ActionCashOut Action = "cash_out"
	// ActionCloseShift is closing someone else's shift.
	ActionCloseShift Action = "close_shift"
	// ActionRefundCharged is refunding the payment of a sale whose receipt could not be printed.
	ActionRefundCharged Action = "refund_charged"
	// ActionOpenDrawer is opening the drawer without a sale.
	ActionOpenDrawer Action = "open_drawer"
	// ActionVoidOrderLine is cancelling an order sent to the kitchen in a
	// restaurant.
	ActionVoidOrderLine Action = "void_order_line"
	// ActionEditFloor is editing the floor and tables in a restaurant.
	ActionEditFloor Action = "edit_floor"
	// ActionApplyUpdate is approving the installation of the new version at the register.
	ActionApplyUpdate Action = "apply_update"
)

// ApprovalTTL is how long a manager approval is valid: the approval is used right away
// while the manager is at the register.
const ApprovalTTL = time.Minute

var (
	ErrUnknownAction      = errors.New("bilinmeyen işlem")
	ErrNotManager         = errors.New("onay yalnızca müdürden alınabilir")
	ErrInvalidApproval    = errors.New("müdür onayı geçersiz, kullanılmış ya da süresi dolmuş; yeniden onay alın")
	ErrCloseDayNotManager = errors.New("gün sonu (Z) için müdür yetkisi gerekli")
	ErrManagerRequired    = errors.New("bu işlem için müdür yetkisi gerekli")
)

// ValidateAction checks that the operation is a known operation.
func ValidateAction(a Action) error {
	switch a {
	case ActionCloseDay, ActionCashOut, ActionCloseShift, ActionRefundCharged, ActionOpenDrawer, ActionVoidOrderLine, ActionEditFloor, ActionApplyUpdate:
		return nil
	}
	return ErrUnknownAction
}

// Approval is the approval a manager gives with their PIN for a single operation while in
// a cashier session. It is short-lived and used once; who asked and
// who approved stay in the record.
type Approval struct {
	// TokenHash is the SHA-256 digest of the approval key; the key is not stored.
	TokenHash string
	ManagerID string
	Action    Action
	// RequestedBy is the cashier in the session who asks for the approval.
	RequestedBy string
	ExpiresAt   time.Time
}
