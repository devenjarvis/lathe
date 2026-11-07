//go:build desktop && linux

package desktop

import "github.com/godbus/dbus/v5"

// singleInstanceUsable reports whether Wails' single-instance lock can work.
// On Linux the lock is a name on the D-Bus session bus, and Wails treats a
// missing bus as fatal, so without one (a bare X server, some containers) the
// app runs without the lock rather than not at all.
func singleInstanceUsable() (bool, string) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return false, "no D-Bus session bus (" + err.Error() + ")"
	}
	_ = conn.Close()
	return true, ""
}

func detachConsole() {}
