// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package natsio

import (
	"testing"
	"time"

	"abarow.com/pkg/outbox"
)

func TestMsg(t *testing.T) {
	at := time.Date(2026, 10, 30, 12, 0, 0, 5, time.FixedZone("TR", 3*3600))
	m := Msg(outbox.Message{ID: "e1", Type: "com.abarow.sale.completed.v1", Subject: "sale.completed.v1",
		StoreID: "store-1", RegisterID: "register-2", Payload: []byte{1}, ContentType: "application/protobuf", OccurredAt: at})
	want := map[string]string{
		"ce-specversion": "1.0", "ce-id": "e1", "ce-type": "com.abarow.sale.completed.v1",
		"ce-source": "abarow/store-1/register-2", "ce-time": "2026-10-30T09:00:00.000000005Z", "content-type": "application/protobuf",
	}
	for k, v := range want {
		if got := m.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if m.Subject != "sale.completed.v1" || string(m.Data) != "\x01" {
		t.Errorf("message %s %v", m.Subject, m.Data)
	}
	// An event with no register (store server, headquarters) and an old record with no
	// content type.
	m = Msg(outbox.Message{ID: "e2", StoreID: "headquarters"})
	if m.Header.Get("ce-source") != "abarow/headquarters/" || m.Header.Get("content-type") != "application/json" {
		t.Errorf("headers %v", m.Header)
	}
}

// The sequence number and sender are written to the header only if both exist
// (pkg/sequence).
func TestMsgSequence(t *testing.T) {
	m := Msg(outbox.Message{ID: "e1", Seq: 42, Producer: "sales"})
	if m.Header.Get("ce-sequence") != "42" || m.Header.Get("ce-producer") != "sales" {
		t.Errorf("headers %v", m.Header)
	}
	for _, in := range []outbox.Message{{ID: "e2", Seq: 42}, {ID: "e3", Producer: "sales"}, {ID: "e4"}} {
		m := Msg(in)
		if m.Header.Get("ce-sequence") != "" || m.Header.Get("ce-producer") != "" {
			t.Errorf("%s: headers were written with missing information: %v", in.ID, m.Header)
		}
	}
}
