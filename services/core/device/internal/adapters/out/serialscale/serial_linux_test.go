// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package serialscale

import (
	"context"
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// On a real terminal (a pseudo-terminal pair): the port is opened in raw mode with the set
// framing, the question goes, the reply is read.
func TestSerialPort(t *testing.T) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo terminal: %v", err)
	}
	defer master.Close()
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	s := New(fmt.Sprintf("/dev/pts/%d", n), Settings{9600, 7, 'E', 1}, Toledo8217)
	go func() {
		buf := make([]byte, 8)
		if n, err := master.Read(buf); err == nil && string(buf[:n]) == "W" {
			master.Write([]byte("\x0201.250\r"))
		}
	}()
	r, err := s.Read(context.Background())
	if err != nil || r.Grams != 1250 || !r.Stable {
		t.Fatalf("%+v %v", r, err)
	}
	pt := s.p.(tty)
	tio, err := unix.IoctlGetTermios(int(pt.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	// The pseudo-terminal determines the data bits and parity itself (always CS8,
	// no parity); the speed and raw mode can be checked.
	if tio.Cflag&unix.CBAUD != unix.B9600 || tio.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG) != 0 || tio.Oflag&unix.OPOST != 0 {
		t.Errorf("port setting: cflag %#o lflag %#o oflag %#o", tio.Cflag, tio.Lflag, tio.Oflag)
	}
}
