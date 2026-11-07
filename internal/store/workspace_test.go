package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadWorkspaceMissingIsNil(t *testing.T) {
	ws, err := ReadWorkspace(t.TempDir())
	if err != nil || ws != nil {
		t.Fatalf("ReadWorkspace on a fresh dir = %+v, %v; want nil, nil", ws, err)
	}
}

func TestSetWorkspaceCreatesAndInitsEmptyFolder(t *testing.T) {
	tut := cancelFixture(t, StatusUnverified, "part-01.md")
	target := filepath.Join(t.TempDir(), "code", "synth")

	setup, err := SetWorkspace(tut, target)
	if err != nil {
		t.Fatalf("SetWorkspace: %v", err)
	}
	if !setup.Created || !setup.GitInit || setup.Path != target {
		t.Errorf("setup = %+v, want created + git init at %s", setup, target)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		t.Errorf("no .git in the new workspace: %v", err)
	}
	ws, err := ReadWorkspace(tut)
	if err != nil || ws == nil || ws.Path != target {
		t.Fatalf("ReadWorkspace = %+v, %v; want %s", ws, err, target)
	}
	// The workspace never touches metadata.json.
	meta, err := os.ReadFile(filepath.Join(tut, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(meta), "synth") {
		t.Errorf("metadata.json mentions the workspace: %s", meta)
	}
}

func TestSetWorkspaceLeavesNonEmptyFolderAlone(t *testing.T) {
	tut := cancelFixture(t, StatusUnverified, "part-01.md")
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "main.zig"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	setup, err := SetWorkspace(tut, target)
	if err != nil {
		t.Fatalf("SetWorkspace: %v", err)
	}
	if setup.Created || setup.GitInit {
		t.Errorf("setup = %+v, want an existing folder left as is", setup)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); !os.IsNotExist(err) {
		t.Errorf("git init ran in a non-empty folder (stat err %v)", err)
	}
}

func TestSetWorkspaceExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tut := cancelFixture(t, StatusUnverified, "part-01.md")
	setup, err := SetWorkspace(tut, "~/code/x")
	if err != nil {
		t.Fatalf("SetWorkspace: %v", err)
	}
	if want := filepath.Join(home, "code", "x"); setup.Path != want {
		t.Errorf("path = %q, want %q", setup.Path, want)
	}
}

func TestSetWorkspaceRefusals(t *testing.T) {
	tut := cancelFixture(t, StatusUnverified, "part-01.md")
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{
		"relative": "code/x",
		"empty":    "  ",
		"file":     file,
		"store":    filepath.Join(filepath.Dir(tut), "elsewhere"),
		"tutorial": tut,
	} {
		if _, err := SetWorkspace(tut, p); err == nil {
			t.Errorf("%s: SetWorkspace(%q) succeeded, want an error", name, p)
		}
	}
	if ws, _ := ReadWorkspace(tut); ws != nil {
		t.Errorf("a refused path was recorded: %+v", ws)
	}
}
