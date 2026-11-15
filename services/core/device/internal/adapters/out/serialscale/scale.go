// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Package serialscale is a register scale connected to a serial port (RS-232 or a
// USB-serial converter): the weight is asked, the reply is decoded according to the scale's
// protocol (Toledo 8217 or NCI ECR).
package serialscale

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"abarow.com/services/core/device/internal/app"
	"abarow.com/services/core/device/internal/domain"
)

// Settings are the serial port settings.
type Settings struct {
	Baud     int
	DataBits int  // 7 or 8
	Parity   byte // 'N', 'E' or 'O'
	StopBits int  // 1 or 2
}

// ParseSettings reads a setting of the form "9600,7E1" (speed, data bits,
// parity, stop bits).
func ParseSettings(s string) (Settings, error) {
	bad := fmt.Errorf("seri port ayarı %q geçersiz (ör. 9600,7E1)", s)
	baud, frame, ok := strings.Cut(strings.ToUpper(strings.TrimSpace(s)), ",")
	if !ok || len(frame) != 3 {
		return Settings{}, bad
	}
	b, err := strconv.Atoi(baud)
	if err != nil || !validBaud(b) {
		return Settings{}, bad
	}
	st := Settings{Baud: b, DataBits: int(frame[0] - '0'), Parity: frame[1], StopBits: int(frame[2] - '0')}
	if (st.DataBits != 7 && st.DataBits != 8) || !strings.ContainsRune("NEO", rune(st.Parity)) || (st.StopBits != 1 && st.StopBits != 2) {
		return Settings{}, bad
	}
	return st, nil
}

func validBaud(b int) bool {
	switch b {
	case 1200, 2400, 4800, 9600, 19200, 38400:
		return true
	}
	return false
}

// port is an open serial port (a fake in tests).
type port interface {
	io.ReadWriteCloser
	SetDeadline(time.Time) error
	// Flush discards old unread data.
	Flush() error
}

// replyTimeout is the longest time to wait for the scale's reply.
const replyTimeout = time.Second

// maxReply is the longest a reply can be; a reply that exceeds it is corrupt.
const maxReply = 256

// Scale implements app.Scale with the serial port. The port is opened at the first read;
// on a communication error it is closed and reopened at the next read (a USB-serial
// converter can be unplugged and plugged back in).
type Scale struct {
	path     string
	settings Settings
	proto    Protocol
	open     func(string, Settings) (port, error)

	mu sync.Mutex
	p  port
}

var _ app.Scale = (*Scale)(nil)

// New returns the scale connected to the serial port at path.
func New(path string, s Settings, proto Protocol) *Scale {
	return &Scale{path: path, settings: s, proto: proto, open: openSerial}
}

func (s *Scale) Name() string { return s.proto.Name }

// Read asks the scale for the weight.
func (s *Scale) Read(ctx context.Context) (domain.Reading, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p == nil {
		p, err := s.open(s.path, s.settings)
		if err != nil {
			hint := ""
			if errors.Is(err, syscall.ENXIO) || errors.Is(err, syscall.ENODEV) || errors.Is(err, os.ErrNotExist) {
				hint = " (terazi kapalı ya da USB-seri çevirici takılı değil)"
			}
			return domain.Reading{}, fmt.Errorf("terazi (%s) açılamadı%s: %w", s.path, hint, err)
		}
		s.p = p
	}
	r, err := s.ask(ctx)
	var broken portError
	if errors.As(err, &broken) {
		s.p.Close()
		s.p = nil
	}
	return r, err
}

// portError is an error that requires the port to be closed and reopened.
type portError struct{ error }

func (e portError) Unwrap() error { return e.error }

// ask writes the question and reads the reply up to the protocol's last byte.
func (s *Scale) ask(ctx context.Context) (domain.Reading, error) {
	deadline := time.Now().Add(replyTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.p.SetDeadline(deadline); err != nil {
		return domain.Reading{}, portError{fmt.Errorf("terazi (%s): %w", s.path, err)}
	}
	_ = s.p.Flush()
	if _, err := s.p.Write(s.proto.command); err != nil {
		return domain.Reading{}, portError{fmt.Errorf("teraziye (%s) yazılamadı: %w", s.path, err)}
	}
	reply := make([]byte, 0, 32)
	buf := make([]byte, 64)
	for {
		n, err := s.p.Read(buf)
		for _, b := range buf[:n] {
			// The protocols are ASCII: if a scale with 7 data bits is read with 8 bits
			// the top bit is the parity.
			b &= 0x7f
			reply = append(reply, b)
			if b == s.proto.end {
				return s.proto.parse(reply)
			}
		}
		if len(reply) > maxReply {
			return domain.Reading{}, domain.ErrScaleReply
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return domain.Reading{}, fmt.Errorf("terazi (%s) yanıt vermiyor: kabloyu, hız ve protokol ayarını denetleyin", s.path)
		}
		if err != nil {
			return domain.Reading{}, portError{fmt.Errorf("teraziden (%s) okunamadı: %w", s.path, err)}
		}
	}
}
