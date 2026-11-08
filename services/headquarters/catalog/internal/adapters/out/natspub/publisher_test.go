// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natspub

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"abarow.com/pkg/outbox"
)

// The stream keeps only the latest state per product; a republished message is de-duplicated
// by its id.
func TestPublish(t *testing.T) {
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
	js, _ := jetstream.New(nc)
	ctx := context.Background()
	pub := New(js)
	at := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	for _, m := range []outbox.Message{
		{ID: "e1", Subject: "catalog.product.ekmek", Payload: []byte("v1")},
		{ID: "e2", Subject: "catalog.product.sut", Payload: []byte("v1")},
		{ID: "e3", Subject: "catalog.product.ekmek", Payload: []byte("v2")},
		{ID: "e3", Subject: "catalog.product.ekmek", Payload: []byte("v2")}, // outbox repeat
	} {
		m.Type, m.StoreID, m.ContentType, m.OccurredAt = "com.abarow.catalog.product.v1", "headquarters", "application/protobuf", at
		if err := pub.Publish(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	stream, err := js.Stream(ctx, StreamName)
	if err != nil {
		t.Fatal(err)
	}
	if n := stream.CachedInfo().State.Msgs; n != 2 {
		t.Errorf("%d messages in the stream; 1 per product, 2 in all expected", n)
	}
	last, err := stream.GetLastMsgForSubject(ctx, "catalog.product.ekmek")
	if err != nil {
		t.Fatal(err)
	}
	if string(last.Data) != "v2" || last.Header.Get("ce-id") != "e3" || last.Header.Get("ce-source") != "abarow/headquarters/" {
		t.Errorf("last state %s %v", last.Data, last.Header)
	}
}
