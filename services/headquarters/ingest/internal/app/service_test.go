// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"abarow.com/pkg/nodes"
	"abarow.com/services/headquarters/ingest/internal/domain"
)

type fakeStream struct {
	failures int
	ensured  [][]string
	sources  map[string]domain.Source
}

func (f *fakeStream) Ensure(_ context.Context, ids []string) error {
	if f.failures > 0 {
		f.failures--
		return errors.New("nats: no responders available for request")
	}
	f.ensured = append(f.ensured, ids)
	return nil
}

func (f *fakeStream) Sources(context.Context) (map[string]domain.Source, error) {
	return f.sources, nil
}

type fakeStore struct {
	events   []domain.Event
	bodies   []any
	seen     map[string]bool
	activity map[string]domain.Activity
	links    []domain.StoreStatus
	linksAt  time.Time
}

func (f *fakeStore) Record(_ context.Context, e domain.Event, body any) (bool, error) {
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[e.ID] {
		return false, nil
	}
	f.seen[e.ID] = true
	f.events = append(f.events, e)
	f.bodies = append(f.bodies, body)
	return true, nil
}

func (f *fakeStore) Activity(context.Context) (map[string]domain.Activity, error) {
	return f.activity, nil
}

func (f *fakeStore) SaveLinks(_ context.Context, links []domain.StoreStatus, at time.Time) error {
	f.links, f.linksAt = links, at
	return nil
}

var (
	discard = slog.New(slog.NewTextHandler(io.Discard, nil))
	stores  = []nodes.Node{{ID: "store-2", Name: "Kadıköy"}, {ID: "store-1"}}
)

func TestSetupStreamRetries(t *testing.T) {
	stream := &fakeStream{failures: 2}
	svc := NewService(stream, &fakeStore{}, stores)
	svc.RetryInterval = time.Millisecond
	if err := svc.SetupStream(context.Background(), discard); err != nil {
		t.Fatal(err)
	}
	if len(stream.ensured) != 1 || !slices.Equal(stream.ensured[0], []string{"store-2", "store-1"}) {
		t.Errorf("kurulum: %v", stream.ensured)
	}
	empty := &fakeStream{}
	if err := NewService(empty, &fakeStore{}, nil).SetupStream(context.Background(), discard); err != nil || len(empty.ensured) != 0 {
		t.Errorf("the stream was set up while there is no store: %v", empty.ensured)
	}
}

func TestRecord(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{}
	svc := NewService(&fakeStream{}, store, stores)
	now := time.Now()
	ev := domain.Event{ID: "e1", Subject: "sale.completed.v1", StoreID: "store-1", RegisterID: "register-1", OccurredAt: now}
	sale := domain.Sale{ID: "s1", StoreID: "store-1", RegisterID: "register-1", ReceiptNo: 1, PaidAt: now}

	if fresh, err := svc.Record(ctx, ev, sale); err != nil || !fresh {
		t.Fatalf("first record: %v, %v", fresh, err)
	}
	if fresh, err := svc.Record(ctx, ev, sale); err != nil || fresh {
		t.Errorf("a repeated event counted as new: %v, %v", fresh, err)
	}
	// An incomplete event or body is not recorded.
	if _, err := svc.Record(ctx, domain.Event{ID: "e2", Subject: "s", OccurredAt: now}, nil); !errors.Is(err, domain.ErrInvalidEvent) {
		t.Errorf("event with no store: %v", err)
	}
	if _, err := svc.Record(ctx, domain.Event{ID: "e3", Subject: "sale.completed.v1", StoreID: "store-1", OccurredAt: now}, domain.Sale{ID: "s3", StoreID: "store-1"}); !errors.Is(err, domain.ErrInvalidEvent) {
		t.Errorf("incomplete sale: %v", err)
	}
	if _, err := svc.Record(ctx, domain.Event{ID: "e4", Subject: "x", StoreID: "store-1", OccurredAt: now}, 42); err == nil {
		t.Error("an unknown body was accepted")
	}
	// An unknown event kind is stored in its raw form.
	if fresh, err := svc.Record(ctx, domain.Event{ID: "e5", Subject: "node.heartbeat.v1", StoreID: "store-1", OccurredAt: now}, nil); err != nil || !fresh {
		t.Errorf("event with no body: %v, %v", fresh, err)
	}
	if len(store.events) != 2 {
		t.Errorf("saklanan olaylar: %+v", store.events)
	}
}

func TestStores(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	stream := &fakeStream{sources: map[string]domain.Source{"store-1": {Seen: true, Since: time.Second}}}
	store := &fakeStore{activity: map[string]domain.Activity{"store-1": {Events: 12, Sales: 6, Registers: []domain.RegisterActivity{{ID: "register-1"}, {ID: "register-2"}}}}}
	svc := NewService(stream, store, stores)
	svc.now = func() time.Time { return now }
	got, err := svc.Stores(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Kadıköy" || got[0].Link != domain.LinkNever ||
		got[1].ID != "store-1" || got[1].Link != domain.LinkConnected || got[1].Activity.Sales != 6 || len(got[1].Activity.Registers) != 2 {
		t.Errorf("stores %+v", got)
	}
}

func TestPublishLinks(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	stream := &fakeStream{sources: map[string]domain.Source{"store-1": {Seen: true, Since: time.Second}}}
	store := &fakeStore{}
	svc := NewService(stream, store, stores)
	svc.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // writes once and returns
	svc.PublishLinks(ctx, discard)
	if len(store.links) != 2 || store.links[1].Link != domain.LinkConnected || store.links[0].Link != domain.LinkNever || !store.linksAt.Equal(now) {
		t.Errorf("connections %+v %v", store.links, store.linksAt)
	}
}
