// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package hubproc is the app.Device adapter that runs the hubd bridge (the C++
// process that wraps Token's library) and talks to it with framed messages.
// The frame format is described at the top of services/fiscal/beko/hub/hubd.cpp.
package hubproc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"abarow.com/services/fiscal/beko/internal/app"
)

// maxPayload is the largest frame body accepted.
const maxPayload = 4 << 20

// ErrNotRunning means the bridge is not running (it has closed or is restarting).
var ErrNotRunning = errors.New("köprü çalışmıyor")

type reply struct {
	payload []byte
	err     error
}

// Process is the bridge process; it is restarted if it closes.
type Process struct {
	Path string
	Args []string
	Log  *slog.Logger
	// Restart is how long to wait before restarting a bridge that closed.
	Restart time.Duration

	events chan app.Event

	mu      sync.Mutex // stdin ve pending
	stdin   io.Writer
	pending map[string]chan reply
	nextID  int
	writeMu sync.Mutex
}

var _ app.Device = (*Process)(nil)

// New prepares a bridge process; Run starts it.
func New(log *slog.Logger, path string, args ...string) *Process {
	return &Process{Path: path, Args: args, Log: log, Restart: 2 * time.Second,
		events: make(chan app.Event, 256), pending: map[string]chan reply{}}
}

// Events are the events coming from the bridge.
func (p *Process) Events() <-chan app.Event { return p.events }

// Run runs the bridge until ctx ends; if it closes it restarts it after a while.
// Every close gives an "exit" event.
func (p *Process) Run(ctx context.Context) {
	for ctx.Err() == nil {
		err := p.once(ctx)
		p.emit(ctx, app.Event{Kind: "exit"})
		if ctx.Err() != nil {
			return
		}
		p.Log.Warn("the bridge ended; it will be restarted", "err", err, "delay", p.Restart)
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.Restart):
		}
	}
}

func (p *Process) emit(ctx context.Context, ev app.Event) {
	select {
	case p.events <- ev:
	case <-ctx.Done():
	}
}

// once runs the bridge one time and waits until it closes.
func (p *Process) once(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, p.Path, p.Args...)
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	p.mu.Lock()
	p.stdin = in
	p.mu.Unlock()

	readErr := p.read(ctx, bufio.NewReader(out))

	p.mu.Lock()
	p.stdin = nil
	for id, ch := range p.pending {
		ch <- reply{err: ErrNotRunning}
		delete(p.pending, id)
	}
	p.mu.Unlock()
	in.Close()
	if err := cmd.Wait(); err != nil && readErr == nil {
		readErr = err
	}
	return readErr
}

// read reads the frames until the stream ends.
func (p *Process) read(ctx context.Context, r *bufio.Reader) error {
	for {
		head, err := r.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		f := strings.Fields(head)
		if len(f) != 3 {
			return fmt.Errorf("çerçeve başlığı bozuk: %q", head)
		}
		n, err := strconv.Atoi(f[2])
		if err != nil || n < 0 || n > maxPayload {
			return fmt.Errorf("çerçeve uzunluğu geçersiz: %q", head)
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			return err
		}
		kind, ref := f[0], f[1]
		switch kind {
		case "ok", "error":
			p.mu.Lock()
			ch, found := p.pending[ref]
			delete(p.pending, ref)
			p.mu.Unlock()
			if !found {
				continue // the caller gave up
			}
			if kind == "ok" {
				ch <- reply{payload: payload}
			} else {
				ch <- reply{err: errors.New(string(payload))}
			}
		default:
			p.emit(ctx, app.Event{Kind: kind, Ref: ref, Payload: payload})
		}
	}
}

// Call sends an operation to the bridge and waits for its response.
func (p *Process) Call(ctx context.Context, op string, payload []byte) ([]byte, error) {
	ch := make(chan reply, 1)
	p.mu.Lock()
	if p.stdin == nil {
		p.mu.Unlock()
		return nil, ErrNotRunning
	}
	p.nextID++
	id := strconv.Itoa(p.nextID)
	p.pending[id] = ch
	w := p.stdin
	p.mu.Unlock()

	forget := func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}
	p.writeMu.Lock()
	_, err := io.WriteString(w, op+" "+id+" "+strconv.Itoa(len(payload))+"\n")
	if err == nil {
		_, err = w.Write(payload)
	}
	p.writeMu.Unlock()
	if err != nil {
		forget()
		return nil, fmt.Errorf("%w: %v", ErrNotRunning, err)
	}
	select {
	case r := <-ch:
		return r.payload, r.err
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	}
}
