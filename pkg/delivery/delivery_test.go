// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestOrigin(t *testing.T) {
	for _, tt := range []struct {
		name   string
		h      nats.Header
		stream string
		ok     bool
	}{
		{"kaynaktan", nats.Header{"Nats-Stream-Source": {"ABAROW_SALES 41 > > sale.completed.v1"}}, "ABAROW_SALES", true},
		// On a real leaf connection the domain's digest is added to the stream name (seen on the live stack).
		{"başka alandan", nats.Header{"Nats-Stream-Source": {"ABAROW_SALES:jSqTWyQ6 41 > > sale.completed.v1"}}, "ABAROW_SALES", true},
		{"yalnız ek", nats.Header{"Nats-Stream-Source": {":jSqTWyQ6 41 > >"}}, "", false},
		{"başlık yok", nats.Header{}, "", false},
		{"sıra sayı değil", nats.Header{"Nats-Stream-Source": {"ABAROW_SALES x > >"}}, "", false},
		{"sıra sıfır", nats.Header{"Nats-Stream-Source": {"ABAROW_SALES 0 > >"}}, "", false},
		{"tek alan", nats.Header{"Nats-Stream-Source": {"ABAROW_SALES"}}, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stream, ok := Origin(tt.h)
			if stream != tt.stream || ok != tt.ok {
				t.Errorf("Origin = %q %v; should be %q %v", stream, ok, tt.stream, tt.ok)
			}
		})
	}
}

func TestEventID(t *testing.T) {
	if got := EventID(nats.Header{"ce-id": {"e1"}, "Nats-Msg-Id": {"m1"}}); got != "e1" {
		t.Errorf("ce-id should take precedence: %q", got)
	}
	if got := EventID(nats.Header{"Nats-Msg-Id": {"m1"}}); got != "m1" {
		t.Errorf("Nats-Msg-Id should be the fallback: %q", got)
	}
	if got := EventID(nats.Header{}); got != "" {
		t.Errorf("kimliksiz mesaj: %q", got)
	}
}

func TestReceipt(t *testing.T) {
	r := Receipt{EventID: "e1", ReceiptID: "0190-uuid7", At: time.Unix(100, 0).UTC()}
	b, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseReceipt(b)
	if err != nil || got != r {
		t.Fatalf("ParseReceipt = %+v, %v", got, err)
	}
	for _, bad := range []string{``, `{`, `{"event_id":"e1"}`, `{"receipt_id":"r"}`} {
		if _, err := ParseReceipt([]byte(bad)); err == nil {
			t.Errorf("ParseReceipt(%q) did not fail", bad)
		}
	}
}

func TestOutboxMessage(t *testing.T) {
	r := Receipt{EventID: "e1", ReceiptID: "r1", At: time.Unix(100, 0).UTC()}
	m, err := OutboxMessage("register-1", "ABAROW_SALES", r)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "r1" || m.Type != ReceiptType || m.Subject != "receipt.register-1.abarow_sales" || m.ContentType != "application/json" {
		t.Errorf("mesaj = %+v", m)
	}
	back, err := ParseReceipt(m.Payload)
	if err != nil || back != r {
		t.Errorf("content = %+v, %v", back, err)
	}
}

// fakeLog is a table from sequence number to event.
type fakeLog struct {
	entries map[uint64]Entry
	deleted []uint64
	err     error
}

func (f *fakeLog) Bounds(context.Context) (uint64, uint64, error) {
	var first, last uint64
	for s := range f.entries {
		if first == 0 || s < first {
			first = s
		}
		last = max(last, s)
	}
	return first, last, f.err
}

func (f *fakeLog) Entry(_ context.Context, seq uint64) (Entry, bool, error) {
	e, ok := f.entries[seq]
	return e, ok, f.err
}

func (f *fakeLog) Delete(_ context.Context, seq uint64) error {
	f.deleted = append(f.deleted, seq)
	delete(f.entries, seq)
	return f.err
}

var now = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// old sets up the events numbered 1..n: event i is (n-i) days before now, its id is "e<i>".
func old(n int) *fakeLog {
	f := &fakeLog{entries: map[uint64]Entry{}}
	for i := 1; i <= n; i++ {
		f.entries[uint64(i)] = Entry{EventID: "e" + string(rune('0'+i)), Time: now.AddDate(0, 0, -(n - i))}
	}
	return f
}

func has(ids ...string) func(context.Context, []string) (map[string]bool, error) {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	return func(_ context.Context, asked []string) (map[string]bool, error) {
		out := map[string]bool{}
		for _, id := range asked {
			if set[id] {
				out[id] = true
			}
		}
		return out, nil
	}
}

func TestCleanDeletesOnlyReceiptedOld(t *testing.T) {
	cutoff := now.AddDate(0, 0, -3) // 1..6 old (the 7th is exactly at the limit, it does not count as old), 7..9 new
	f := old(9)
	c := &Cleaner{Log: f, Received: has("e1", "e3", "e4", "e8", "e9")}
	n, err := c.Clean(context.Background(), 0, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	// Receipt recorded and old: 1, 3, 4. e8 and e9 have receipts but are new; e2 e5 e6 are old but have no receipt.
	if n != 3 {
		t.Errorf("silinen %d; 3 beklenir (%v)", n, f.deleted)
	}
	for _, s := range []uint64{2, 5, 6, 7, 8, 9} {
		if _, ok := f.entries[s]; !ok {
			t.Errorf("%d deleted; they should have stayed", s)
		}
	}
}

func TestCleanNoReceiptsDeletesNothing(t *testing.T) {
	f := old(9)
	c := &Cleaner{Log: f, Received: has()}
	if n, err := c.Clean(context.Background(), 0, now); err != nil || n != 0 || len(f.entries) != 9 {
		t.Fatalf("makbuz yokken n=%d err=%v kalan=%d", n, err, len(f.entries))
	}
}

func TestCleanRespectsLocalLimit(t *testing.T) {
	f := old(9)
	c := &Cleaner{Log: f, Received: has("e1", "e2", "e3", "e4", "e5")}
	// The local consumer has processed up to 3: 4 and 5 stay even though they have receipts and are old.
	if n, err := c.Clean(context.Background(), 3, now); err != nil || n != 3 {
		t.Fatalf("n=%d err=%v; 3 beklenir", n, err)
	}
	for _, s := range []uint64{4, 5} {
		if _, ok := f.entries[s]; !ok {
			t.Errorf("%d local consumers were deleted without processing", s)
		}
	}
}

func TestCleanSkipsMessagesWithoutEventID(t *testing.T) {
	f := old(3)
	f.entries[2] = Entry{Time: f.entries[2].Time} // kimliksiz
	c := &Cleaner{Log: f, Received: has("e1", "e3")}
	if n, err := c.Clean(context.Background(), 0, now.Add(time.Hour)); err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, ok := f.entries[2]; !ok {
		t.Error("kimliksiz mesaj silindi")
	}
}

func TestCleanGapsAndBudgetResume(t *testing.T) {
	f := old(9)
	for _, s := range []uint64{1, 2, 5} {
		delete(f.entries, s) // sequences deleted before
	}
	c := &Cleaner{Log: f, Received: has("e3", "e4", "e6"), Budget: 2}
	// Budget 2: two messages are examined per round; by the end of all the rounds 3, 4 and 6 are deleted.
	total := 0
	for range 6 {
		n, err := c.Clean(context.Background(), 0, now.AddDate(0, 0, -1))
		if err != nil {
			t.Fatal(err)
		}
		total += n
	}
	if total != 3 {
		t.Errorf("toplam %d silindi; 3 beklenir", total)
	}
}

func TestCleanLateReceiptIsPickedUp(t *testing.T) {
	f := old(4)
	got := map[string]bool{}
	c := &Cleaner{Log: f, Received: func(_ context.Context, ids []string) (map[string]bool, error) {
		out := map[string]bool{}
		for _, id := range ids {
			out[id] = got[id]
		}
		return out, nil
	}}
	cutoff := now.AddDate(0, 0, -1)
	if n, _ := c.Clean(context.Background(), 0, cutoff); n != 0 {
		t.Fatalf("makbuzsuz silindi: %d", n)
	}
	got["e2"] = true // makbuz sonradan geldi
	if n, err := c.Clean(context.Background(), 0, cutoff); err != nil || n != 1 {
		t.Fatalf("sonradan gelen makbuz: n=%d err=%v", n, err)
	}
}

func TestCleanErrors(t *testing.T) {
	boom := errors.New("NATS yok")
	f := old(3)
	f.err = boom
	c := &Cleaner{Log: f, Received: has("e1")}
	if _, err := c.Clean(context.Background(), 0, now); !errors.Is(err, boom) {
		t.Errorf("no stream error returned: %v", err)
	}
	g := old(3)
	c = &Cleaner{Log: g, Received: func(context.Context, []string) (map[string]bool, error) { return nil, boom }}
	if _, err := c.Clean(context.Background(), 0, now); !errors.Is(err, boom) {
		t.Errorf("no receipt query error returned: %v", err)
	}
	if len(g.deleted) != 0 {
		t.Error("deleted while the receipt query was failing")
	}
}
