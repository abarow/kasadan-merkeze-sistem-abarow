// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"testing"
	"time"
)

func TestStatus(t *testing.T) {
	now := time.Date(2026, 10, 26, 14, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		src      *Source
		link     Link
		lastSeen time.Time
		pending  uint64
	}{
		{"stream kurulmadı", nil, LinkNever, time.Time{}, 0},
		{"hiç haber yok", &Source{Lag: 4}, LinkNever, time.Time{}, 0},
		{"bağlı", &Source{Seen: true, Since: 2 * time.Second, Lag: 3}, LinkConnected, now.Add(-2 * time.Second), 3},
		{"sınırda bağlı", &Source{Seen: true, Since: StaleAfter}, LinkConnected, now.Add(-StaleAfter), 0},
		// The lag NATS reports while there is no connection is old; the number of waiting
		// events is not shown.
		{"haber kesildi", &Source{Seen: true, Since: StaleAfter + time.Second, Lag: 7}, LinkDisconnected, now.Add(-StaleAfter - time.Second), 0},
		{"kaynak hatası", &Source{Seen: true, Since: time.Second, Error: "stream not found"}, LinkDisconnected, now.Add(-time.Second), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := Status("register-1", "Kasa 1", tt.src, now)
			if st.ID != "register-1" || st.Name != "Kasa 1" || st.Link != tt.link || !st.LastSeen.Equal(tt.lastSeen) || st.Pending != tt.pending {
				t.Errorf("durum %+v; istenen %s, son haber %v, bekleyen %d", st, tt.link, tt.lastSeen, tt.pending)
			}
			if tt.src != nil && st.Error != tt.src.Error {
				t.Errorf("error %q", st.Error)
			}
		})
	}
}
