// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natsio

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Consumer is the loop of a durable consumer: it sets up the consumer and gives the
// messages in order to Handle; if the consumer is deleted or the connection to NATS is
// lost it rebuilds it; if the stream does not exist or NATS cannot be reached it waits and
// retries.
//
// The result of Handle is the fate of the message: nil is acknowledged; an error wrapped with Reject
// (an event that cannot be read or is incomplete) is written to the log and permanently rejected;
// another error (e.g. the database is down) brings the message again after a while;
// an error wrapped with RetryAfter is an expected wait (try again after the given
// time).
type Consumer struct {
	JS     jetstream.JetStream
	Stream string
	// Config is the consumer's settings (Durable, filters). Its zero values are
	// explicit acknowledgement (AckExplicitPolicy) and everything from the start (DeliverAllPolicy).
	Config jetstream.ConsumerConfig
	// What is the name of what is being read in the log (e.g. "sale events").
	What string
	// Ensure sets up the stream before reading; it can be nil (the stream
	// belongs to someone else).
	Ensure func(ctx context.Context) error
	Handle func(ctx context.Context, msg jetstream.Msg) error
	Log    *slog.Logger

	// failing is whether we are in an outage in which the messages cannot be processed: the outage
	// is written to the log once. Since the messages are processed in order no lock
	// is needed.
	failing bool
}

// RetryDelay is the retry interval of a message that cannot be processed and of a consumer
// that cannot be set up.
const RetryDelay = 2 * time.Second

type rejected struct{ err error }

func (r rejected) Error() string { return r.err.Error() }
func (r rejected) Unwrap() error { return r.err }

// Reject marks that the message will be permanently rejected: it does not get better even if it
// comes again (an event that cannot be read or is incomplete).
func Reject(err error) error {
	if err == nil {
		return nil
	}
	return rejected{err}
}

// IsReject says whether the error was wrapped with Reject (consumer tests tell a rejected
// event from an event to be retried).
func IsReject(err error) bool {
	var r rejected
	return errors.As(err, &r)
}

type retryAfter struct {
	err   error
	delay time.Duration
}

func (r retryAfter) Error() string { return r.err.Error() }
func (r retryAfter) Unwrap() error { return r.err }

// RetryAfter asks for the message to come again after d: it is an expected
// situation (e.g. the store price waits because the product has not come down yet), it is not
// written to the log as an outage.
func RetryAfter(err error, d time.Duration) error { return retryAfter{err, d} }

// Run processes the messages until ctx ends.
func (c *Consumer) Run(ctx context.Context) {
	warned := false
	for {
		err := c.consume(ctx)
		if ctx.Err() != nil {
			return
		}
		switch {
		case err == nil:
			warned = false
		case !warned:
			c.Log.Warn(c.What+" cannot be read; will retry", "stream", c.Stream, "err", err)
			warned = true
		}
		// If a deleted consumer is rebuilt before it has been fully deleted on the server it may
		// bind to the one being deleted; a short wait is made.
		select {
		case <-ctx.Done():
			return
		case <-time.After(RetryDelay):
		}
	}
}

// consume sets up the consumer and waits until reading is interrupted; if it cannot be
// set up it returns the error.
func (c *Consumer) consume(ctx context.Context) error {
	if c.Ensure != nil {
		if err := c.Ensure(ctx); err != nil {
			return err
		}
	}
	cfg := c.Config
	cons, err := c.JS.CreateOrUpdateConsumer(ctx, c.Stream, cfg)
	if err != nil {
		return err
	}
	lost := make(chan error, 1)
	cc, err := cons.Consume(func(msg jetstream.Msg) { c.process(ctx, msg) },
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
			if errors.Is(err, jetstream.ErrConsumerDeleted) || errors.Is(err, jetstream.ErrConsumerNotFound) ||
				errors.Is(err, jetstream.ErrNoHeartbeat) {
				select {
				case lost <- err:
				default:
				}
			}
		}))
	if err != nil {
		return err
	}
	defer cc.Stop()
	c.Log.Info(c.What+" okunuyor", "stream", c.Stream, "consumer", cfg.Durable)
	select {
	case <-ctx.Done():
	case err := <-lost:
		if errors.Is(err, jetstream.ErrNoHeartbeat) {
			c.Log.Warn("NATS'tan haber kesildi; "+c.What+" yeniden okunacak", "err", err)
		} else {
			c.Log.Info("consumer deleted; it will be rebuilt and the stream read from the start", "stream", c.Stream, "consumer", cfg.Durable)
		}
	case <-cc.Closed():
	}
	return nil
}

func (c *Consumer) process(ctx context.Context, msg jetstream.Msg) {
	err := c.Handle(ctx, msg)
	var rej rejected
	var later retryAfter
	switch {
	case err == nil:
		if c.failing {
			c.Log.Info(c.What + " is being reprocessed")
			c.failing = false
		}
		_ = msg.Ack()
	case errors.As(err, &rej):
		c.Log.Error(c.What+": event could not be read and was rejected", "subject", msg.Subject(), "ce-id", msg.Headers().Get("ce-id"),
			"ce-source", msg.Headers().Get("ce-source"), "err", rej.err)
		_ = msg.Term()
	case errors.As(err, &later):
		_ = msg.NakWithDelay(later.delay)
	default:
		if !c.failing {
			c.Log.Warn(c.What+" cannot be processed; it will come again", "subject", msg.Subject(), "err", err)
			c.failing = true
		}
		_ = msg.NakWithDelay(RetryDelay)
	}
}
