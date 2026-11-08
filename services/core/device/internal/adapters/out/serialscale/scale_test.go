// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package serialscale

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"abarow.com/services/core/device/internal/domain"
)

func TestParseToledo(t *testing.T) {
	for _, c := range []struct {
		reply string
		want  domain.Reading
		err   error
	}{
		{"\x0201.250\r", domain.Reading{Grams: 1250, Stable: true}, nil},
		{"\x02 0.5N\r", domain.Reading{Grams: 500, Stable: true}, nil},
		{"çöp\x0212.005\r", domain.Reading{Grams: 12005, Stable: true}, nil},
		{"\x02?A\r", domain.Reading{Stable: false}, nil},    // 0x41: hareket
		{"\x02?P\r", domain.Reading{Stable: true}, nil},     // 0x50: exact zero
		{"\x02?B\r", domain.Reading{}, domain.ErrScaleOver}, // 0x42: kapasite
		{"\x02?D\r", domain.Reading{}, domain.ErrScaleNegative},
		{"\x0201250\r", domain.Reading{}, domain.ErrScaleReply}, // no point: the unit is not clear
		{"01.250\r", domain.Reading{}, domain.ErrScaleReply},
	} {
		got, err := Toledo8217.parse([]byte(c.reply))
		if got != c.want || !errors.Is(err, c.err) && !(err == nil && c.err == nil) {
			t.Errorf("%q → %+v %v; expected %+v %v", c.reply, got, err, c.want, c.err)
		}
	}
}

func TestParseNCI(t *testing.T) {
	for _, c := range []struct {
		reply string
		want  domain.Reading
		err   error
	}{
		{"\n01.250KG\r\nS00\r\x03", domain.Reading{Grams: 1250, Stable: true}, nil},
		{"\n0.75KG\r\nS00\r\x03", domain.Reading{Grams: 750, Stable: true}, nil},
		{"\n01250GM\r\nS00\r\x03", domain.Reading{Grams: 1250, Stable: true}, nil},
		{"\nS10\r\x03", domain.Reading{Stable: false}, nil},        // 1. bayt bit 0: hareket
		{"\nS20\r\x03", domain.Reading{Stable: true}, nil},         // at zero, no weight
		{"\nS01\r\x03", domain.Reading{}, domain.ErrScaleNegative}, // under capacity
		{"\nS02\r\x03", domain.Reading{}, domain.ErrScaleOver},
		{"\nS40\r\x03", domain.Reading{}, domain.ErrScaleFault}, // RAM error
		{"\n02.000LB\r\nS00\r\x03", domain.Reading{}, domain.ErrScaleUnits},
		{"\n?\r\x03", domain.Reading{}, domain.ErrScaleReply},
	} {
		got, err := NCI.parse([]byte(c.reply))
		if got != c.want || !errors.Is(err, c.err) && !(err == nil && c.err == nil) {
			t.Errorf("%q → %+v %v; expected %+v %v", c.reply, got, err, c.want, c.err)
		}
	}
}

func TestParseSettings(t *testing.T) {
	if s, err := ParseSettings("9600,7e1"); err != nil || s != (Settings{9600, 7, 'E', 1}) {
		t.Errorf("%+v %v", s, err)
	}
	for _, bad := range []string{"9600", "9601,7E1", "9600,9E1", "9600,7X1", "9600,7E3"} {
		if _, err := ParseSettings(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// pipePort stands in for the serial port in tests.
type pipePort struct{ net.Conn }

func (pipePort) Flush() error { return nil }

// fakeScale imitates a scale that reads the question and writes the reply.
func fakeScale(t *testing.T, proto Protocol, answers ...string) *Scale {
	t.Helper()
	opened := 0
	s := New("/dev/test", Settings{9600, 7, 'E', 1}, proto)
	s.open = func(string, Settings) (port, error) {
		opened++
		host, scale := net.Pipe()
		go func() {
			defer scale.Close()
			buf := make([]byte, 16)
			for _, a := range answers {
				n, err := scale.Read(buf)
				if err != nil || string(buf[:n]) != string(proto.command) {
					return
				}
				if a == "kopuk" {
					return
				}
				if a != "" {
					scale.Write([]byte(a))
				}
			}
		}()
		return pipePort{host}, nil
	}
	return s
}

func TestScaleRead(t *testing.T) {
	ctx := context.Background()
	s := fakeScale(t, Toledo8217, "\x0201.250\r", "", "\x0200.300\r")
	if r, err := s.Read(ctx); err != nil || r.Grams != 1250 {
		t.Fatalf("%+v %v", r, err)
	}
	// A scale that gives no reply: the time runs out, the port stays open.
	start := time.Now()
	if _, err := s.Read(ctx); err == nil || !strings.Contains(err.Error(), "yanıt vermiyor") || time.Since(start) > 2*time.Second {
		t.Fatalf("no answer: %v", err)
	}
	if r, err := s.Read(ctx); err != nil || r.Grams != 300 {
		t.Fatalf("after no answer: %+v %v", r, err)
	}
	// A port that breaks is closed, reopened at the next read.
	s = fakeScale(t, NCI, "kopuk")
	if _, err := s.Read(ctx); err == nil {
		t.Fatal("kopuk port")
	}
	if s.p != nil {
		t.Error("the disconnected port was not closed")
	}
	// The converter is not plugged in: the cashier sees the reason.
	s.open = func(string, Settings) (port, error) {
		return nil, &os.PathError{Op: "open", Path: "/dev/test", Err: syscall.ENXIO}
	}
	if _, err := s.Read(ctx); err == nil || !strings.Contains(err.Error(), "takılı değil") {
		t.Errorf("not plugged in: %v", err)
	}
}
