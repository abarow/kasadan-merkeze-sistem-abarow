// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package evdev

import (
	"context"
	"encoding/binary"
	"log/slog"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"abarow.com/services/core/device/internal/app"
)

// eventSize is the size of struct input_event: time as two unsigned longs
// (seconds, microseconds) + type + code + value; 24 bytes on 64-bit, 16 bytes
// on 32-bit.
const eventSize = 2*int(unsafe.Sizeof(uintptr(0))) + 8

// eviocgrab, _IOW('E', 0x90, int): sets the device aside for this file.
const eviocgrab = 0x40044590

// Scanner reads the evdev device.
type Scanner struct {
	Path string
	Log  *slog.Logger
	// Retry is the retry interval if the device cannot be opened or breaks.
	Retry time.Duration
}

var _ app.Scanner = (*Scanner)(nil)

func (s *Scanner) Name() string { return "evdev" }

// Run reads the device until ctx ends; if the device is missing or breaks (the reader
// was unplugged) Retry reopens it at intervals. The same error is written once.
func (s *Scanner) Run(ctx context.Context, emit func(string)) {
	retry := s.Retry
	if retry <= 0 {
		retry = 2 * time.Second
	}
	var last string
	for ctx.Err() == nil {
		err := s.read(ctx, emit)
		if ctx.Err() != nil {
			return
		}
		if msg := err.Error(); msg != last {
			s.Log.Warn("cannot read the barcode reader; will retry", "path", s.Path, "err", err)
			last = msg
		}
		select {
		case <-ctx.Done():
		case <-time.After(retry):
		}
	}
}

func (s *Scanner) read(ctx context.Context, emit func(string)) error {
	f, err := os.Open(s.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	// The device is set aside for this file: its keys do not fall to the kiosk browser. Control,
	// not Fd(): Fd puts the file in blocking mode, Close could not interrupt the read.
	if rc, err := f.SyscallConn(); err == nil {
		var grabErr error
		if err := rc.Control(func(fd uintptr) { grabErr = unix.IoctlSetInt(int(fd), eviocgrab, 1) }); err == nil && grabErr != nil {
			s.Log.Warn("could not set the barcode reader aside; its keys may fall to the screen too", "path", s.Path, "err", grabErr)
		}
	}
	stop := context.AfterFunc(ctx, func() { f.Close() })
	defer stop()
	s.Log.Info("barcode reader connected", "path", s.Path)

	var dec Decoder
	buf := make([]byte, eventSize*64)
	for {
		n, err := f.Read(buf)
		if err != nil {
			return err
		}
		for off := 0; off+eventSize <= n; off += eventSize {
			if code, ok := dec.Feed(parse(buf[off : off+eventSize])); ok {
				emit(code)
			}
		}
	}
}

// parse reads struct input_event (in the machine's byte order; little-endian on
// amd64 and arm64).
func parse(b []byte) Event {
	w := eventSize/2 - 4 // one of the time fields
	word := func(p []byte) uint64 {
		if w == 8 {
			return binary.LittleEndian.Uint64(p)
		}
		return uint64(binary.LittleEndian.Uint32(p))
	}
	sec, usec := word(b), word(b[w:])
	return Event{
		Time:  time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond,
		Type:  binary.LittleEndian.Uint16(b[2*w:]),
		Code:  binary.LittleEndian.Uint16(b[2*w+2:]),
		Value: int32(binary.LittleEndian.Uint32(b[2*w+4:])),
	}
}
