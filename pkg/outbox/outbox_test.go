// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package outbox_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"abarow.com/pkg/outbox"
)

type memOutbox struct {
	msgs      []outbox.Pending
	published map[int64]bool
	markErr   error
}

func newMemOutbox(n int) *memOutbox {
	o := &memOutbox{published: map[int64]bool{}}
	for i := 1; i <= n; i++ {
		o.msgs = append(o.msgs, outbox.Pending{Seq: int64(i), Message: outbox.Message{ID: fmt.Sprintf("e%d", i)}})
	}
	return o
}

func (o *memOutbox) Pending(_ context.Context, limit int) ([]outbox.Pending, error) {
	var out []outbox.Pending
	for _, m := range o.msgs {
		if !o.published[m.Seq] && len(out) < limit {
			out = append(out, m)
		}
	}
	return out, nil
}

func (o *memOutbox) CountPending(context.Context) (int64, error) {
	return int64(len(o.msgs) - len(o.published)), nil
}

func (o *memOutbox) MarkPublished(_ context.Context, seq int64, _ time.Time) error {
	if o.markErr != nil {
		return o.markErr
	}
	o.published[seq] = true
	return nil
}

// memPublisher returns an error on the failAt-th call (starting from 1).
type memPublisher struct {
	ids    []string
	calls  int
	failAt int
}

func (p *memPublisher) Publish(_ context.Context, m outbox.Message) error {
	p.calls++
	if p.calls == p.failAt {
		return errors.New("NATS erişilemiyor")
	}
	p.ids = append(p.ids, m.ID)
	return nil
}

func newRelay(o outbox.Store, p outbox.Publisher) *outbox.Relay {
	return outbox.NewRelay(o, p, time.Second, slog.New(slog.DiscardHandler))
}

func TestRelayPublishesInOrder(t *testing.T) {
	o, p := newMemOutbox(3), &memPublisher{}
	n, err := newRelay(o, p).Flush(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("Flush = %d, %v; want 3, nil", n, err)
	}
	if !reflect.DeepEqual(p.ids, []string{"e1", "e2", "e3"}) {
		t.Errorf("publish order %v", p.ids)
	}
	if n, _ := newRelay(o, p).Flush(context.Background()); n != 0 {
		t.Errorf("the second Flush published %d messages, want 0", n)
	}
}

func TestRelayStopsOnPublishErrorAndResumes(t *testing.T) {
	o, p := newMemOutbox(3), &memPublisher{failAt: 2}
	relay := newRelay(o, p)
	if n, err := relay.Flush(context.Background()); err == nil || n != 1 {
		t.Fatalf("Flush = %d, %v; want 1 and an error", n, err)
	}
	if n, _ := relay.Backlog(context.Background()); n != 2 {
		t.Errorf("%d events pending, want 2", n)
	}
	// The message path came back: the rest go without breaking the order.
	if n, err := relay.Flush(context.Background()); err != nil || n != 2 {
		t.Fatalf("Flush = %d, %v; want 2, nil", n, err)
	}
	if n, _ := relay.Backlog(context.Background()); n != 0 {
		t.Errorf("%d events pending after all were published", n)
	}
	if !reflect.DeepEqual(p.ids, []string{"e1", "e2", "e3"}) {
		t.Errorf("publish order %v", p.ids)
	}
}

func TestRelayRepublishesWhenMarkFails(t *testing.T) {
	o, p := newMemOutbox(1), &memPublisher{}
	o.markErr = errors.New("disk dolu")
	relay := newRelay(o, p)
	if _, err := relay.Flush(context.Background()); err == nil {
		t.Fatal("the marking error was not returned")
	}
	o.markErr = nil
	if _, err := relay.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	// At-least-once delivery: the same ID was published twice, the receiver de-duplicates the repeat.
	if !reflect.DeepEqual(p.ids, []string{"e1", "e1"}) {
		t.Errorf("publishes %v, want [e1 e1]", p.ids)
	}
}

// seqPublisher records the sequence information of the published messages.
type seqPublisher struct{ got []outbox.Message }

func (p *seqPublisher) Publish(_ context.Context, m outbox.Message) error {
	p.got = append(p.got, m)
	return nil
}

// The Sequenced relay adds the outbox sequence number and the outbox's name to the message;
// a relay that is not Sequenced (an outbox whose numbers are not gapless) does not.
func TestRelaySequenced(t *testing.T) {
	o, p := newMemOutbox(3), &seqPublisher{}
	o.msgs[0].Seq, o.msgs[1].Seq, o.msgs[2].Seq = 10, 11, 13
	if _, err := newRelay(o, p).Sequenced("sales").Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, want := range []int64{10, 11, 13} {
		if p.got[i].Seq != want || p.got[i].Producer != "sales" {
			t.Errorf("message %d: seq %d producer %q", i, p.got[i].Seq, p.got[i].Producer)
		}
	}

	o, p = newMemOutbox(2), &seqPublisher{}
	if _, err := newRelay(o, p).Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, m := range p.got {
		if m.Seq != 0 || m.Producer != "" {
			t.Errorf("a relay that is not Sequenced added a sequence: %+v", m)
		}
	}
}
