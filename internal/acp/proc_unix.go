//go:build unix

package acp

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup puts the command in its own process group, so kill stops
// everything it started (a shell pipeline, a build's children).
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcess(cmd *exec.Cmd) {
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}

func exitInfo(ps *os.ProcessState) (*int, *string) {
	if ps == nil {
		return nil, nil
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		name := signalName(ws.Signal())
		return nil, &name
	}
	code := ps.ExitCode()
	return &code, nil
}

func signalName(s syscall.Signal) string {
	switch s {
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGSEGV:
		return "SIGSEGV"
	case syscall.SIGABRT:
		return "SIGABRT"
	default:
		return s.String()
	}
}
