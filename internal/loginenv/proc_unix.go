//go:build unix

package loginenv

import (
	"os/exec"
	"syscall"
)

// detach starts the shell in a new session: it has no controlling terminal
// (so an interactive rc file can't open /dev/tty and wait on the user), and
// it leads its own process group, which a timeout kills whole — the shell
// and anything its rc files started.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
