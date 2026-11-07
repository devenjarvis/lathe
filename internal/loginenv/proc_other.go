//go:build !unix

package loginenv

import "os/exec"

// detach is a no-op off unix; Prepare doesn't probe on windows anyway, and
// exec.CommandContext's default Cancel kills the shell on timeout.
func detach(*exec.Cmd) {}
