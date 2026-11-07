// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import "time"

// SetClock changes the service's clock in tests.
func SetClock(s *Service, now func() time.Time) { s.now = now }
