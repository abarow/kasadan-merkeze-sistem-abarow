// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package natsio holds the services' shared work with NATS JetStream: publishing the outbox
// messages with CloudEvents headers.
package natsio

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/outbox"
	"abarow.com/pkg/sequence"
)

// Publisher implements outbox.Publisher with JetStream. If a stream is given it sets it
// up at the first publish (the service can start without NATS being up); if not given
// the stream belongs to someone else.
type Publisher struct {
	js     jetstream.JetStream
	stream *jetstream.StreamConfig
	// Timeout is the time a single publish waits for acknowledgement. While there is no connection
	// nats.go buffers the publish; without a time limit the relay would quietly get stuck.
	Timeout time.Duration

	mu      sync.Mutex
	ensured bool
}

var _ outbox.Publisher = (*Publisher)(nil)

// NewPublisher returns a publisher; stream can be nil.
func NewPublisher(js jetstream.JetStream, stream *jetstream.StreamConfig) *Publisher {
	return &Publisher{js: js, stream: stream, Timeout: 5 * time.Second}
}

// Publish writes the message to JetStream and waits for the stream's acknowledgement. If the same
// message is published again the stream de-duplicates it by the message id.
func (p *Publisher) Publish(ctx context.Context, m outbox.Message) error {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	if err := p.Ensure(ctx); err != nil {
		return err
	}
	_, err := p.js.PublishMsg(ctx, Msg(m), jetstream.WithMsgID(m.ID))
	return err
}

// Ensure sets up or updates the stream (once); if no stream is given
// it does nothing.
func (p *Publisher) Ensure(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ensured || p.stream == nil {
		return nil
	}
	if _, err := p.js.CreateOrUpdateStream(ctx, *p.stream); err != nil {
		return err
	}
	p.ensured = true
	return nil
}

// Msg turns the outbox message into a NATS message. The event properties are carried in CloudEvents
// (binary mode) headers; the source is abarow/<store>/<register>
// (for an event with no register the register is empty). If the content type is empty it is JSON (old
// records).
func Msg(m outbox.Message) *nats.Msg {
	msg := nats.NewMsg(m.Subject)
	msg.Data = m.Payload
	msg.Header.Set("ce-specversion", "1.0")
	msg.Header.Set("ce-id", m.ID)
	msg.Header.Set("ce-type", m.Type)
	msg.Header.Set("ce-source", "abarow/"+m.StoreID+"/"+m.RegisterID)
	msg.Header.Set("ce-time", m.OccurredAt.UTC().Format(time.RFC3339Nano))
	// The sender outbox's sequence number (pkg/sequence): if the receiver sees a missing number
	// in between there is a lost event.
	if m.Seq > 0 && m.Producer != "" {
		msg.Header.Set(sequence.HeaderSequence, strconv.FormatInt(m.Seq, 10))
		msg.Header.Set(sequence.HeaderProducer, m.Producer)
	}
	contentType := m.ContentType
	if contentType == "" {
		contentType = "application/json"
	}
	msg.Header.Set("content-type", contentType)
	return msg
}
