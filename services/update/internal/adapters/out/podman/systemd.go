// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package podman

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	sdDest    = "org.freedesktop.systemd1"
	sdPath    = dbus.ObjectPath("/org/freedesktop/systemd1")
	sdManager = "org.freedesktop.systemd1.Manager"
)

// DBus is the Systemd implementation that manages systemd --user over the user session's
// D-Bus socket. It has not been run on real systemd yet.
type DBus struct {
	conn *dbus.Conn
	// JobTimeout is how long to wait for the restart job to finish (default 3 min).
	JobTimeout time.Duration
}

// ConnectDBus connects to the given socket path (the unix:path=... form is not accepted, only the path).
func ConnectDBus(socket string) (*DBus, error) {
	conn, err := dbus.Dial("unix:path=" + socket)
	if err != nil {
		return nil, err
	}
	// EXTERNAL authentication is done with the user who owns the socket.
	if err := conn.Auth(nil); err != nil {
		conn.Close()
		return nil, fmt.Errorf("D-Bus kimlik doğrulaması: %w", err)
	}
	if err := conn.Hello(); err != nil {
		conn.Close()
		return nil, err
	}
	return &DBus{conn: conn, JobTimeout: 3 * time.Minute}, nil
}

// Close closes the connection.
func (d *DBus) Close() error { return d.conn.Close() }

func (d *DBus) manager() dbus.BusObject { return d.conn.Object(sdDest, sdPath) }

// Reload does systemd daemon-reload (the Quadlet units are regenerated).
func (d *DBus) Reload(ctx context.Context) error {
	return d.manager().CallWithContext(ctx, sdManager+".Reload", 0).Err
}

// Restart restarts the unit and waits until the job is done. If the job result is not
// "done" (failed, timeout, dependency) it returns an error.
func (d *DBus) Restart(ctx context.Context, unit string) error {
	// Subscribe to the signal first so as not to miss the job result.
	if err := d.conn.AddMatchSignalContext(ctx, dbus.WithMatchObjectPath(sdPath), dbus.WithMatchInterface(sdManager), dbus.WithMatchMember("JobRemoved")); err != nil {
		return err
	}
	ch := make(chan *dbus.Signal, 32)
	d.conn.Signal(ch)
	defer d.conn.RemoveSignal(ch)

	var job dbus.ObjectPath
	if err := d.manager().CallWithContext(ctx, sdManager+".RestartUnit", 0, unit, "replace").Store(&job); err != nil {
		return err
	}
	timeout := d.JobTimeout
	if timeout == 0 {
		timeout = 3 * time.Minute
	}
	deadline := time.After(timeout)
	for {
		select {
		case sig := <-ch:
			if sig.Name != sdManager+".JobRemoved" || len(sig.Body) < 4 {
				continue
			}
			if p, _ := sig.Body[1].(dbus.ObjectPath); p != job {
				continue
			}
			if result, _ := sig.Body[3].(string); result != "done" {
				return fmt.Errorf("%s yeniden başlatma işi %q ile bitti", unit, result)
			}
			return nil
		case <-deadline:
			return fmt.Errorf("%s yeniden başlatma işi %s içinde bitmedi", unit, timeout)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Active says whether the unit is active (active/running).
func (d *DBus) Active(ctx context.Context, unit string) (bool, error) {
	var path dbus.ObjectPath
	if err := d.manager().CallWithContext(ctx, sdManager+".GetUnit", 0, unit).Store(&path); err != nil {
		return false, err
	}
	v, err := d.conn.Object(sdDest, path).GetProperty("org.freedesktop.systemd1.Unit.ActiveState")
	if err != nil {
		return false, err
	}
	state, ok := v.Value().(string)
	if !ok {
		return false, errors.New("ActiveState okunamadı")
	}
	return state == "active", nil
}
