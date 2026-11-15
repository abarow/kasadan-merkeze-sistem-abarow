// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natsio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Connect opens the service's NATS connection: the address is ABAROW_NATS_URL, the identity
// (if any) is the seed in ABAROW_NATS_NKEY. The service's command line commands
// (replay) use the same identity as the running service.
func Connect(name string) (*nats.Conn, error) {
	opts := []nats.Option{nats.Name(name)}
	if seed := os.Getenv("ABAROW_NATS_NKEY"); seed != "" {
		opt, err := nats.NkeyOptionFromSeed(seed)
		if err != nil {
			return nil, fmt.Errorf("NATS anahtarı okunamadı: %w", err)
		}
		opts = append(opts, opt)
	}
	url := os.Getenv("ABAROW_NATS_URL")
	if url == "" {
		url = "nats://nats:4222"
	}
	return nats.Connect(url, opts...)
}

// Replay deletes the durable consumer; the running service (Consumer) rebuilds the
// consumer and reads the stream from the start. Since the events the services process are
// de-duplicated by id, existing ones are skipped and missing ones are added. After
// restoring from a backup the events between the backup and now are reprocessed this way.
// If the consumer does not exist existed returns false.
func Replay(ctx context.Context, name, stream, durable string) (existed bool, err error) {
	nc, err := Connect(name)
	if err != nil {
		return false, err
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err = js.DeleteConsumer(ctx, stream, durable)
	if errors.Is(err, jetstream.ErrConsumerNotFound) {
		return false, nil
	}
	return err == nil, err
}
