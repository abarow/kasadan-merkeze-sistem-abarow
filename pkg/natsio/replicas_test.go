// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natsio

import (
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

func TestReplicated(t *testing.T) {
	cfg := jetstream.StreamConfig{Name: "S"}
	got, err := Replicated(cfg)
	if err != nil || got.Replicas != 0 {
		t.Errorf("without the setting: %d, %v", got.Replicas, err)
	}
	t.Setenv(ReplicasEnv, "3")
	if got, err := Replicated(cfg); err != nil || got.Replicas != 3 || got.Name != "S" {
		t.Errorf("3: %+v, %v", got, err)
	}
	for _, v := range []string{"0", "6", "-1", "üç", "3 "} {
		t.Setenv(ReplicasEnv, v)
		if _, err := Replicated(cfg); err == nil {
			t.Errorf("%q was accepted", v)
		}
	}
}
