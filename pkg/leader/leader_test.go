// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package leader

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"abarow.com/pkg/pgdb/pgdbtest"
)

// copies counts the copies working at the same time.
type copies struct {
	mu      sync.Mutex
	working map[string]bool
	most    int
}

func (c *copies) work(name string) func(ctx context.Context) {
	return func(ctx context.Context) {
		c.mu.Lock()
		c.working[name] = true
		c.most = max(c.most, len(c.working))
		c.mu.Unlock()
		<-ctx.Done()
		c.mu.Lock()
		delete(c.working, name)
		c.mu.Unlock()
	}
}

func (c *copies) who() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var w []string
	for n := range c.working {
		w = append(w, n)
	}
	return w
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: did not happen", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func open(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { db.Close() })
	return db
}

// TestOneCopyWorks: two copies (as on two machines, each with its own pool); only one works.
// When the working copy's lock connection is cut the other takes over; when a copy stops it
// gives the lock back. At no moment do both work.
func TestOneCopyWorks(t *testing.T) {
	dsn := pgdbtest.URL(t)
	admin := open(t, dsn)
	c := &copies{working: map[string]bool{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	run := func(name string) context.CancelFunc {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		l := Leader{DB: open(t, dsn), Key: Key("test"), Name: name, Log: log,
			Retry: 100 * time.Millisecond, Check: 100 * time.Millisecond, Grace: 400 * time.Millisecond}
		go func() {
			defer close(done)
			l.Run(ctx, c.work(name))
		}()
		return func() { cancel(); <-done }
	}
	only := func(name string) func() bool {
		return func() bool { w := c.who(); return len(w) == 1 && w[0] == name }
	}

	stopA := run("a")
	waitFor(t, "a works", only("a"))
	stopB := run("b")
	time.Sleep(time.Second)
	if !only("a")() {
		t.Fatalf("working: %v; only a should", c.who())
	}

	// a's lock connection is cut (its machine or its connection to the primary is gone).
	var killed atomic.Int64
	waitFor(t, "a's lock connection is cut", func() bool {
		var n int64
		err := admin.QueryRow("SELECT count(pg_terminate_backend(pid)) FROM pg_locks WHERE locktype = 'advisory' AND granted").Scan(&n)
		killed.Add(n)
		return err == nil && n == 1
	})
	waitFor(t, "b takes over", only("b"))

	// b stops: it gives the lock back, a takes it again.
	stopB()
	waitFor(t, "a takes over again", only("a"))
	stopA()
	if w := c.who(); len(w) != 0 {
		t.Errorf("still working after stopping: %v", w)
	}
	var locks int
	if err := admin.QueryRow("SELECT count(*) FROM pg_locks WHERE locktype = 'advisory'").Scan(&locks); err != nil || locks != 0 {
		t.Errorf("locks left: %d, %v", locks, err)
	}
	if c.most != 1 {
		t.Errorf("%d copies worked at the same time", c.most)
	}
}

// TestWorkStopsByItself: when the work returns by itself the lock is given back.
func TestWorkStopsByItself(t *testing.T) {
	dsn := pgdbtest.URL(t)
	db := open(t, dsn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runs atomic.Int32
	l := Leader{DB: db, Key: Key("test"), Name: "t", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Retry: 50 * time.Millisecond, Check: 50 * time.Millisecond, Grace: 50 * time.Millisecond}
	go l.Run(ctx, func(context.Context) { runs.Add(1) })
	waitFor(t, "the work runs again after giving the lock back", func() bool { return runs.Load() >= 3 })
}

func TestKey(t *testing.T) {
	keys := map[int64]string{}
	for _, s := range []string{"ingest", "catalog-admin", "inventory"} {
		if other, ok := keys[Key(s)]; ok {
			t.Errorf("%s and %s have the same key", s, other)
		}
		keys[Key(s)] = s
	}
}
