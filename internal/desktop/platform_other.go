//go:build desktop && !windows && !linux

package desktop

// singleInstanceUsable: macOS locks with a file and a notification name, so
// the lock is always available there.
func singleInstanceUsable() (bool, string) { return true, "" }

func detachConsole() {}
