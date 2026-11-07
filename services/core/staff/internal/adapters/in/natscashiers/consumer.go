// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package natscashiers is the driving adapter that takes the cashiers managed on the store
// server screen to the register: it sets up the cashier stream (ABAROW_CASHIERS) in the
// register's NATS with the store server's store settings stream as a source, reads the
// cashiers' latest states with its own durable consumer and applies them.
package natscashiers

import (
	"context"
	"errors"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/natsio"
	"abarow.com/pkg/personnel"
	"abarow.com/services/core/staff/internal/app"
	"abarow.com/services/core/staff/internal/domain"
)

const (
	// StreamName, kasadaki kasiyer stream'idir.
	StreamName = "ABAROW_CASHIERS"
	// ConfigStream is the store server's store settings stream; the cashiers are on the
	// config.cashier.<cashier> subject.
	ConfigStream = "ABAROW_STORE_CONFIG"
	// Durable is staff's durable consumer; when staff restarts it continues where
	// it left off.
	Durable = "staff"
)

// StreamConfig is the register's cashier stream: it is fed from the config.cashier.>
// subjects of the store settings stream in the source JetStream domain (the store id on the
// store server). Only the latest state per cashier is kept.
func StreamConfig(source string) jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:              StreamName,
		Storage:           jetstream.FileStorage,
		MaxMsgsPerSubject: 1,
		Sources:           []*jetstream.StreamSource{{Name: ConfigStream, Domain: source, FilterSubject: "config.cashier.>"}},
	}
}

// New returns the cashier consumer fed from the source JetStream domain (the store server)
// (pkg/natsio): it sets up the stream and applies the cashiers. A cashier that cannot be decoded
// or is incomplete does not get better by retrying; it is written to the log and rejected. A cashier that
// cannot be applied (e.g. a database error) comes again later.
func New(js jetstream.JetStream, auth *app.Auth, source string, log *slog.Logger) *natsio.Consumer {
	return &natsio.Consumer{JS: js, Stream: StreamName, Config: jetstream.ConsumerConfig{Durable: Durable},
		What: "operasyonun kasiyerleri", Log: log,
		Ensure: func(ctx context.Context) error {
			_, err := js.CreateOrUpdateStream(ctx, StreamConfig(source))
			return err
		},
		Handle: func(ctx context.Context, msg jetstream.Msg) error {
			cashier, err := Decode(msg.Headers().Get("content-type"), msg.Data())
			if err == nil {
				err = auth.ApplyCashier(ctx, cashier)
			}
			if errors.Is(err, errUndecodable) || errors.Is(err, domain.ErrInvalidCashier) || errors.Is(err, domain.ErrInvalidRole) {
				return natsio.Reject(err)
			}
			return err
		}}
}

// errUndecodable is a cashier message that cannot be read (pkg/personnel).
var errUndecodable = personnel.ErrUndecodable

// Decode turns the store server's cashier message (config.cashier.<cashier>,
// staff.v1.CashierState) into a cashier.
func Decode(contentType string, data []byte) (domain.Cashier, error) {
	c, err := personnel.Decode(contentType, data)
	if err != nil {
		return domain.Cashier{}, err
	}
	return domain.Cashier{ID: c.ID, Name: c.Name, Role: c.Role, PINHash: c.PINHash, Disabled: c.Disabled, Origin: domain.FromStore}, nil
}
