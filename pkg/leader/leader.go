// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package leader lets one of a service's copies do the work that must not run twice at the
// same time: reading a durable consumer in order, publishing the outbox, the retention jobs.
// At headquarters on three machines every machine runs the services
// (docs/decisions/HA-HEADQUARTERS.md); the copy holding a PostgreSQL advisory lock works,
// the others wait. The lock is held on one connection of the service's pool: if the copy,
// its machine or the connection to the primary is gone, PostgreSQL drops the lock and a waiting copy
// takes it.
//
// The lock is not a hard fence: the copy that lost it notices at its next check (Check)
// and stops its work. So the copy that takes the lock waits Grace before starting; Grace
// must be longer than Check plus the time the work needs to stop. When the primary
// changes the old copy's connections are cut too (HAProxy), so its writes cannot complete.
package leader

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"hash/fnv"
	"log/slog"
	"sync"
	"time"
)

// Pool gives a connection of the service's pool (*sql.DB).
type Pool interface {
	Conn(ctx context.Context) (*sql.Conn, error)
}

// Key is the advisory lock key of a service; every service has its own.
func Key(service string) int64 {
	h := fnv.New64a()
	h.Write([]byte("abarow.leader." + service))
	return int64(h.Sum64())
}

// Leader runs the work while this copy holds the lock.
type Leader struct {
	DB   Pool
	Key  int64
	Name string // the service's name, for the log
	Log  *slog.Logger
	// Retry is how often a waiting copy tries to take the lock (default 5 s).
	Retry time.Duration
	// Check is how often the holder checks that it still holds the lock (default 2 s).
	Check time.Duration
	// Grace is the wait after taking the lock before the work starts (default 10 s).
	Grace time.Duration
}

func (l Leader) defaults() Leader {
	if l.Retry <= 0 {
		l.Retry = 5 * time.Second
	}
	if l.Check <= 0 {
		l.Check = 2 * time.Second
	}
	if l.Grace <= 0 {
		l.Grace = 10 * time.Second
	}
	if l.Log == nil {
		l.Log = slog.Default()
	}
	return l
}

// Run runs work while this copy holds the lock. When the lock is lost work's context is
// cancelled; Run waits for work to return and tries to take the lock again. work must return
// when its context ends. Run returns when ctx ends.
func (l Leader) Run(ctx context.Context, work func(ctx context.Context)) {
	l = l.defaults()
	waiting := false
	for ctx.Err() == nil {
		conn, err := l.acquire(ctx)
		if conn == nil {
			if !waiting {
				if err != nil {
					l.Log.Warn("cannot ask for the "+l.Name+" lock; will retry", "err", err)
				} else {
					l.Log.Info("another copy of " + l.Name + " is working; this one waits")
				}
				waiting = true
			}
			sleep(ctx, l.Retry)
			continue
		}
		waiting = false
		l.Log.Info("this copy of "+l.Name+" took the lock; the work starts after the wait", "wait", l.Grace)
		if l.held(ctx, conn, l.Grace) {
			l.work(ctx, conn, work)
		}
		l.release(conn)
	}
}

// acquire returns the connection holding the lock; nil if another copy holds it.
func (l Leader) acquire(ctx context.Context) (*sql.Conn, error) {
	conn, err := l.DB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var got bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", l.Key).Scan(&got); err != nil || !got {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// held checks the lock every Check for d; false if it is lost or ctx ends.
func (l Leader) held(ctx context.Context, conn *sql.Conn, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if !l.check(ctx, conn) {
			return false
		}
		sleep(ctx, min(l.Check, time.Until(deadline)))
	}
	return l.check(ctx, conn)
}

func (l Leader) work(ctx context.Context, conn *sql.Conn, work func(ctx context.Context)) {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		work(wctx)
	}()
	tick := time.NewTicker(l.Check)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return
		case <-done:
			if ctx.Err() == nil {
				l.Log.Error("the work of " + l.Name + " stopped by itself; the lock is released")
			}
			return
		case <-tick.C:
			if !l.check(ctx, conn) {
				l.Log.Warn("this copy of " + l.Name + " lost the lock; the work stops")
				cancel()
				<-done
				return
			}
		}
	}
}

// check says the connection still holds the lock.
func (l Leader) check(ctx context.Context, conn *sql.Conn) bool {
	if ctx.Err() != nil {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, l.Check)
	defer cancel()
	var held bool
	err := conn.QueryRowContext(cctx,
		"SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype = 'advisory' AND pid = pg_backend_pid() AND granted)").Scan(&held)
	return err == nil && held
}

// release gives the lock back and closes the connection. The connection does not go back to
// the pool: if the unlock did not work it would keep holding the lock for another job of the
// service and no copy would work. Closing it ends the session, and the lock with it.
func (l Leader) release(conn *sql.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", l.Key)
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	conn.Close()
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// All runs the jobs together; it returns when all have returned.
func All(jobs ...func(ctx context.Context)) func(ctx context.Context) {
	return func(ctx context.Context) {
		var wg sync.WaitGroup
		for _, job := range jobs {
			wg.Go(func() { job(ctx) })
		}
		wg.Wait()
	}
}
