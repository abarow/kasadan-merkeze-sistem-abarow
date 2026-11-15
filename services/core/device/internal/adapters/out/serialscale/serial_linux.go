// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package serialscale

import (
	"os"

	"golang.org/x/sys/unix"
)

var bauds = map[int]uint32{
	1200: unix.B1200, 2400: unix.B2400, 4800: unix.B4800,
	9600: unix.B9600, 19200: unix.B19200, 38400: unix.B38400,
}

// tty is an open serial port. Since it is opened non-blocking (O_NONBLOCK), read
// and write times are limited by Go's poller.
type tty struct{ *os.File }

// openSerial opens the port in raw mode (no line editing, echo or flow control)
// with the given speed and framing.
func openSerial(path string, s Settings) (port, error) {
	f, err := os.OpenFile(path, os.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	if err := control(f, func(fd int) error { return configure(fd, s) }); err != nil {
		f.Close()
		return nil, err
	}
	return tty{f}, nil
}

func configure(fd int, s Settings) error {
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return err
	}
	speed := bauds[s.Baud]
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL |
		unix.IXON | unix.IXOFF | unix.IXANY | unix.INPCK
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB | unix.PARODD | unix.CSTOPB | unix.CRTSCTS | unix.CBAUD
	t.Cflag |= unix.CLOCAL | unix.CREAD | speed
	if s.DataBits == 7 {
		t.Cflag |= unix.CS7
	} else {
		t.Cflag |= unix.CS8
	}
	switch s.Parity {
	case 'E':
		t.Cflag |= unix.PARENB
	case 'O':
		t.Cflag |= unix.PARENB | unix.PARODD
	}
	if s.StopBits == 2 {
		t.Cflag |= unix.CSTOPB
	}
	t.Ispeed, t.Ospeed = speed, speed
	t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 1, 0
	return unix.IoctlSetTermios(fd, unix.TCSETS, t)
}

func control(f *os.File, fn func(fd int) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := rc.Control(func(fd uintptr) { ferr = fn(int(fd)) }); err != nil {
		return err
	}
	return ferr
}

func (t tty) Flush() error {
	return control(t.File, func(fd int) error { return unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH) })
}
