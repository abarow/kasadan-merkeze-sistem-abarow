// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natsio

import (
	"fmt"
	"os"
	"strconv"

	"github.com/nats-io/nats.go/jetstream"
)

// ReplicasEnv is the number of copies of the streams a service sets up. It is set only at
// headquarters on three machines (3): every event is then on three machines, the stream keeps
// working when one machine is gone. If empty the stream has one copy.
const ReplicasEnv = "ABAROW_NATS_REPLICAS"

// Replicated returns the stream setting with the number of copies in ABAROW_NATS_REPLICAS. An
// invalid value is an error: the stream is not set up with fewer copies than intended.
func Replicated(cfg jetstream.StreamConfig) (jetstream.StreamConfig, error) {
	v := os.Getenv(ReplicasEnv)
	if v == "" {
		return cfg, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 5 {
		return cfg, fmt.Errorf("%s=%q is invalid: a number from 1 to 5", ReplicasEnv, v)
	}
	cfg.Replicas = n
	return cfg, nil
}
