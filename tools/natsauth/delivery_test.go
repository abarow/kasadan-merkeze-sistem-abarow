// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/delivery"
)

func jetstreamServer(t *testing.T) jetstream.JetStream {
	t.Helper()
	s := startServer(t, &server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true, NoLog: true})
	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return js
}

// TestDeliveryCleaner tries the receipt scheme with real JetStream: the receipt
// consumer writes the receipts to the sender's record; the cleaner deletes only a message whose
// receipt is recorded, which is old enough and which the local consumers have processed.
func TestDeliveryCleaner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	js := jetstreamServer(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	store, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: "ABAROW_STORE", Subjects: []string{"store.>"}, Storage: jetstream.FileStorage})
	if err != nil {
		t.Fatal(err)
	}
	publish := func(ids ...string) {
		for _, id := range ids {
			m := nats.NewMsg("store.x")
			m.Header.Set("ce-id", id)
			if _, err := js.PublishMsg(ctx, m); err != nil {
				t.Fatal(err)
			}
		}
	}
	publish("e1", "e2", "e3")
	time.Sleep(1500 * time.Millisecond)
	publish("e4", "e5") // yeni
	const maxAge = time.Second

	// The sender's receipt record (in reality deliverydb.Store).
	var mu sync.Mutex
	saved := map[string]delivery.Receipt{}
	received := func(_ context.Context, ids []string) (map[string]bool, error) {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]bool{}
		for _, id := range ids {
			_, ok := saved[id]
			out[id] = ok
		}
		return out, nil
	}
	if _, err := js.CreateStream(ctx, delivery.ReceiptConfig("", "", 0)); err != nil {
		t.Fatal(err)
	}
	filter := delivery.Subject("store-1", "ABAROW_STORE")
	go delivery.NewReceiptConsumer(js, "receipts", filter, func(_ context.Context, r delivery.Receipt) error {
		mu.Lock()
		defer mu.Unlock()
		saved[r.EventID] = r
		return nil
	}, log).Run(ctx)

	sendReceipt := func(subject, event, receipt string) {
		b, err := delivery.Receipt{EventID: event, ReceiptID: receipt, At: time.Now()}.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := js.Publish(ctx, subject, b); err != nil {
			t.Fatal(err)
		}
	}
	waitSaved := func(n int) {
		t.Helper()
		waitFor(t, 10*time.Second, "makbuzların kaydı", func() bool { mu.Lock(); defer mu.Unlock(); return len(saved) == n })
	}
	// The receipts of e1 and e2 come; another owner's receipt (e3) and a corrupt receipt are not recorded.
	sendReceipt(filter, "e1", "r1")
	sendReceipt(filter, "e2", "r2")
	sendReceipt(delivery.Subject("store-2", "ABAROW_STORE"), "e3", "r3")
	if _, err := js.Publish(ctx, filter, []byte("bozuk")); err != nil {
		t.Fatal(err)
	}
	waitSaved(2)

	first := func() uint64 {
		info, err := store.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return info.State.FirstSeq
	}
	c := &delivery.Cleaner{Log: delivery.JSLog{S: store}, Received: received}
	// There is no local named consumer yet: no limit.
	if _, has, err := delivery.LocalFloor(ctx, store); err != nil || has {
		t.Fatalf("with no consumer named LocalFloor = %v, %v", has, err)
	}
	// The consumer named report has processed only e1: e2 stays even though it has a receipt and is old.
	cons, err := js.CreateOrUpdateConsumer(ctx, "ABAROW_STORE", jetstream.ConsumerConfig{Durable: "report", AckPolicy: jetstream.AckExplicitPolicy})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := cons.Fetch(1)
	if err != nil {
		t.Fatal(err)
	}
	for m := range batch.Messages() {
		if err := m.DoubleAck(ctx); err != nil {
			t.Fatal(err)
		}
	}
	limit, has, err := delivery.LocalFloor(ctx, store)
	if err != nil || !has || limit != 1 {
		t.Fatalf("LocalFloor = %d, %v, %v; 1 expected", limit, has, err)
	}
	if n, err := c.Clean(ctx, limit, time.Now().Add(-maxAge)); err != nil || n != 1 || first() != 2 {
		t.Fatalf("local limit 1: deleted=%d err=%v first=%d", n, err, first())
	}
	// When report processes e2 too, e2 is deleted; e3 has no receipt, e4 and e5 are new so they stay.
	batch, err = cons.Fetch(1)
	if err != nil {
		t.Fatal(err)
	}
	for m := range batch.Messages() {
		if err := m.DoubleAck(ctx); err != nil {
			t.Fatal(err)
		}
	}
	limit, _, _ = delivery.LocalFloor(ctx, store)
	if n, err := c.Clean(ctx, limit, time.Now().Add(-maxAge)); err != nil || n != 1 {
		t.Fatalf("local limit 2: deleted=%d err=%v", n, err)
	}
	info, err := store.Info(ctx)
	if err != nil || info.State.Msgs != 3 {
		t.Fatalf("%d messages remain; should be e3, e4, e5 (%v)", info.State.Msgs, err)
	}
	// When e3's receipt comes later (from the right owner) it is deleted.
	sendReceipt(filter, "e3", "r3")
	waitSaved(3)
	// Let report process e3 too.
	batch, err = cons.Fetch(1)
	if err != nil {
		t.Fatal(err)
	}
	for m := range batch.Messages() {
		if err := m.DoubleAck(ctx); err != nil {
			t.Fatal(err)
		}
	}
	limit, _, _ = delivery.LocalFloor(ctx, store)
	if n, err := c.Clean(ctx, limit, time.Now().Add(-maxAge)); err != nil || n != 1 {
		t.Fatalf("after the receipt of e3: deleted=%d err=%v", n, err)
	}
	if info, _ := store.Info(ctx); info.State.Msgs != 2 {
		t.Errorf("%d messages remain; only the new e4, e5 should remain", info.State.Msgs)
	}
}
