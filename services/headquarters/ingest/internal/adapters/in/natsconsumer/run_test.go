// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natsconsumer

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/nodes"
	"abarow.com/services/headquarters/ingest/internal/adapters/out/natsstream"
	"abarow.com/services/headquarters/ingest/internal/app"
	"abarow.com/services/headquarters/ingest/internal/domain"
)

// memStore is the in-memory app.Store that de-duplicates events by their id.
type memStore struct {
	mu        sync.Mutex
	seen      map[string]bool
	delivered int
}

func (m *memStore) Record(_ context.Context, e domain.Event, _ any) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delivered++
	if m.seen[e.ID] {
		return false, nil
	}
	m.seen[e.ID] = true
	return true, nil
}

func (m *memStore) RecordBatch(ctx context.Context, items []app.Item) error {
	for _, it := range items {
		if _, err := m.Record(ctx, it.Event, it.Body); err != nil {
			return err
		}
	}
	return nil
}

func (m *memStore) counts() (unique, delivered int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.seen), m.delivered
}

func (m *memStore) Activity(context.Context) (map[string]domain.Activity, error) { return nil, nil }
func (m *memStore) SaveLinks(context.Context, []domain.StoreStatus, time.Time) error {
	return nil
}

type noStream struct{}

func (noStream) Ensure(context.Context, []string) error { return nil }
func (noStream) Sources(context.Context) (map[string]domain.Source, error) {
	return nil, nil
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: timeout", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// When the consumer is deleted (ingest replay) it is rebuilt and the stream is read from the start;
// events that come again are skipped by their id.
func TestRunReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a NATS server")
	}
	srv, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS did not start")
	}
	defer srv.Shutdown()
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// In the test the headquarters stream is set up without sources; the events are published with the store subject.
	if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: natsstream.StreamName, Subjects: []string{"store-1.>"}}); err != nil {
		t.Fatal(err)
	}
	publish := func(from, to int) {
		for i := from; i < to; i++ {
			msg := nats.NewMsg("store-1.node.heartbeat.v1")
			msg.Header.Set("ce-id", fmt.Sprintf("e%d", i))
			msg.Header.Set("ce-source", "abarow/store-1/register-1")
			if _, err := js.PublishMsg(ctx, msg); err != nil {
				t.Fatal(err)
			}
		}
	}
	publish(0, 5)

	store := &memStore{seen: map[string]bool{}}
	svc := app.NewService(noStream{}, store, []nodes.Node{{ID: "store-1"}})
	done := make(chan struct{})
	go func() {
		New(js, svc, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(ctx)
		close(done)
	}()
	waitUntil(t, "ilk olaylar", func() bool { u, _ := store.counts(); return u == 5 })

	if err := js.DeleteConsumer(ctx, natsstream.StreamName, Durable); err != nil {
		t.Fatal(err)
	}
	publish(5, 7)
	// The deleted consumer is rebuilt: the 5 old events come again (skipped), 2 new events are added.
	waitUntil(t, "baştan okuma", func() bool { u, d := store.counts(); return u == 7 && d >= 12 })
	cancel()
	<-done
}
