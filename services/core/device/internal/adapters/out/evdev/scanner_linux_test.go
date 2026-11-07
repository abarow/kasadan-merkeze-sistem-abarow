// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package evdev

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// encode writes the event as struct input_event (the reverse of parse).
func encode(ev Event) []byte {
	b := make([]byte, eventSize)
	w := eventSize/2 - 4
	put := func(p []byte, v uint64) {
		if w == 8 {
			binary.LittleEndian.PutUint64(p, v)
		} else {
			binary.LittleEndian.PutUint32(p, uint32(v))
		}
	}
	put(b, uint64(ev.Time/time.Second))
	put(b[w:], uint64(ev.Time%time.Second/time.Microsecond))
	binary.LittleEndian.PutUint16(b[2*w:], ev.Type)
	binary.LittleEndian.PutUint16(b[2*w+2:], ev.Code)
	binary.LittleEndian.PutUint32(b[2*w+4:], uint32(ev.Value))
	return b
}

// A FIFO instead of a device: the reader opens the file, reads the events, gives the code;
// if it cannot set it aside (a FIFO is not evdev) it warns and keeps reading; it
// returns when ctx ends.
func TestScannerReadsEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "okuyucu")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	codes := make(chan string, 4)
	done := make(chan struct{})
	s := &Scanner{Path: path, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Retry: 10 * time.Millisecond}
	go func() {
		s.Run(ctx, func(c string) { codes <- c })
		close(done)
	}()

	w, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	var ty typing
	ty.text("8690000000012")
	ty.press(keyEnter)
	ty.text("Abc")
	ty.press(keyEnter)
	for _, ev := range ty.events {
		if _, err := w.Write(encode(ev)); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"8690000000012", "Abc"} {
		select {
		case got := <-codes:
			if got != want {
				t.Errorf("kod %q, beklenen %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%q okunmadı", want)
		}
	}
	w.Close()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ctx bitince okuyucu durmadı")
	}
}

func TestParseRoundTrip(t *testing.T) {
	ev := Event{Time: 1234*time.Second + 567*time.Microsecond, Type: evKey, Code: keyKPEnter, Value: 1}
	if got := parse(encode(ev)); got != ev {
		t.Errorf("%+v != %+v", got, ev)
	}
}
