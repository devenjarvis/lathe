//go:build !unix

package acp

import (
	"os"
	"os/exec"
)

func setProcessGroup(*exec.Cmd) {}

func killProcess(cmd *exec.Cmd) { _ = cmd.Process.Kill() }

func exitInfo(ps *os.ProcessState) (*int, *string) {
	if ps == nil {
		return nil, nil
	}
	code := ps.ExitCode()
	return &code, nil
}
