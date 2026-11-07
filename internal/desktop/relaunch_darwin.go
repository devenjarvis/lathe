//go:build desktop && darwin

package desktop

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// relaunch starts a detached helper that waits for this process to exit (so
// the single-instance lock is free) and then opens bundle, the upgraded
// Lathe.app. The caller quits right after.
func relaunch(bundle string) error {
	if bundle == "" {
		return errors.New("not running from an app bundle")
	}
	const script = `while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; exec /usr/bin/open "$2"`
	cmd := exec.Command("/bin/sh", "-c", script, "lathe-relaunch", strconv.Itoa(os.Getpid()), bundle)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
