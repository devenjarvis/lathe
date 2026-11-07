package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
)

func runStart(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	startCmd.SetOut(&out)
	t.Cleanup(func() { startCmd.SetOut(nil); startNoOpen = false })
	err := startCmd.RunE(startCmd, args)
	return out.String(), err
}

func TestStartSetsWorkspaceFromPath(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	tutDir := writeTutorial(t, home, "synth", store.StatusUnverified, []string{"part-01.md"})

	out, err := runStart(t, "synth", "~/code/synth")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	want := filepath.Join(home, "code", "synth")
	if !strings.Contains(out, "Workspace for synth: "+want+" (created it, ran git init)") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "lathe serve") {
		t.Errorf("with no server running, want a hint to run lathe serve: %q", out)
	}
	ws, err := store.ReadWorkspace(tutDir)
	if err != nil || ws == nil || ws.Path != want {
		t.Fatalf("workspace = %+v, %v; want %s", ws, err, want)
	}
}

func TestStartDefaultsToCurrentDirAndPrintsURL(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	tutDir := writeTutorial(t, home, "synth", store.StatusUnverified, []string{"part-01.md"})
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "main.zig"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)
	if err := config.WriteServeRuntime(&config.ServeRuntime{URL: "http://localhost:4242", PID: 1, Started: time.Now()}); err != nil {
		t.Fatal(err)
	}

	startNoOpen = true
	out, err := runStart(t, "synth")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(out, "Read it at http://localhost:4242/synth/") {
		t.Errorf("output = %q, want the tutorial URL", out)
	}
	if strings.Contains(out, "git init") {
		t.Errorf("a non-empty folder must not be git-inited: %q", out)
	}
	ws, _ := store.ReadWorkspace(tutDir)
	real, _ := filepath.EvalSymlinks(work)
	if ws == nil || (ws.Path != work && ws.Path != real) {
		t.Errorf("workspace = %+v, want the current dir %s", ws, work)
	}
}

func TestStartUnknownTutorial(t *testing.T) {
	setHome(t, t.TempDir())
	if _, err := runStart(t, "nope", "/tmp/x"); err == nil {
		t.Error("start on an unknown tutorial succeeded")
	}
}
