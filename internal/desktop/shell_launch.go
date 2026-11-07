//go:build desktop

package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/serve"
)

// The shell's serve.WorkspaceLauncher: Open in editor and Run in Terminal.
// launch.go plans the command lines; this runs them.

var _ serve.WorkspaceLauncher = (*shell)(nil)

// launchTimeout bounds the launchers that return at once (osascript, open,
// cmd /c start): one that hangs (a permission prompt left open) must not
// hold a request forever.
const launchTimeout = 30 * time.Second

func (s *shell) launchEnv(stage string) launchEnv {
	return launchEnv{goos: runtime.GOOS, shell: os.Getenv("SHELL"), stage: stage, lookPath: exec.LookPath}
}

// runStep runs one planned program.
func (s *shell) runStep(st launchStep) error {
	if !st.wait {
		cmd := exec.Command(st.argv[0], st.argv[1:]...)
		cmd.Dir = st.dir
		if err := cmd.Start(); err != nil {
			return err
		}
		go func() {
			if err := cmd.Wait(); err != nil {
				s.logf("%s exited: %v", filepath.Base(st.argv[0]), err)
			}
		}()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), launchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, st.argv[0], st.argv[1:]...)
	cmd.Dir = st.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "-1743") || strings.Contains(msg, "Not authorized") {
			return errors.New("macOS didn't let Lathe control the terminal. Allow it in System Settings › Privacy & Security › Automation")
		}
		if msg != "" {
			return fmt.Errorf("%v: %s", err, msg)
		}
		return err
	}
	return nil
}

// OpenInEditor opens a workspace file or folder in the reader's editor, or
// in the system's default app for it.
func (s *shell) OpenInEditor(editor, path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	step, ok, err := planEditor(s.launchEnv(""), editor, path)
	if err != nil {
		return err
	}
	if !ok {
		return s.app.Browser.OpenFile(path)
	}
	if err := s.runStep(step); err != nil {
		if e, found := config.EditorByID(editor); found && runtime.GOOS == "darwin" {
			return fmt.Errorf("couldn't open %s. Is it installed? (%v)", e.Label, err)
		}
		return err
	}
	return nil
}

// OpenTerminal opens the reader's terminal at dir with command typed in, not
// run (see launch.go for how, per platform and shell).
func (s *shell) OpenTerminal(terminal, dir, command string) (serve.TerminalOutcome, error) {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return serve.TerminalOutcome{}, fmt.Errorf("no folder at %s", dir)
	}
	stage := ""
	if command != "" {
		d, err := os.MkdirTemp("", "lathe-run-")
		if err != nil {
			return serve.TerminalOutcome{}, err
		}
		stage = d
	}
	plan, err := planTerminal(s.launchEnv(stage), terminal, dir, command)
	cleanup := func() {
		if stage != "" {
			_ = os.RemoveAll(stage)
		}
	}
	if err != nil {
		cleanup()
		return serve.TerminalOutcome{}, err
	}
	if len(plan.files) == 0 {
		cleanup()
	}
	for name, content := range plan.files {
		if err := os.WriteFile(filepath.Join(stage, name), []byte(content), 0o600); err != nil {
			cleanup()
			return serve.TerminalOutcome{}, err
		}
	}
	// The clipboard first, so a fallback holds even if the terminal is slow.
	if plan.clipboard != "" && !s.app.Clipboard.SetText(plan.clipboard) {
		plan.outcome.Clipboard = false
	}
	for _, st := range plan.steps {
		if err := s.runStep(st); err != nil {
			cleanup()
			return serve.TerminalOutcome{}, err
		}
	}
	return plan.outcome, nil
}
