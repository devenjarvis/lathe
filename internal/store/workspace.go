package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WorkspaceFile is the per-tutorial sidecar that records where the reader
// types the tutorial's code. It is local reader state, like progress.json and
// exercises.json, and deliberately not part of metadata.json: a tutorial can
// be shared or re-stored, but its learner's folder is theirs. Unlike
// progress.json it survives a re-store, because the folder does not depend on
// the tutorial's content.
const WorkspaceFile = "workspace.json"

// Workspace is the reader's code folder for one tutorial.
type Workspace struct {
	Path      string    `json:"path"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ReadWorkspace returns the tutorial's workspace, or nil (and no error) when
// none has been set.
func ReadWorkspace(tutorialDir string) (*Workspace, error) {
	var ws Workspace
	if err := readJSONFile(filepath.Join(tutorialDir, WorkspaceFile), &ws); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(ws.Path) == "" {
		return nil, nil
	}
	return &ws, nil
}

// WorkspaceSetup reports what SetWorkspace did to the folder.
type WorkspaceSetup struct {
	Path    string `json:"path"`
	Created bool   `json:"created"` // the folder did not exist
	GitInit bool   `json:"gitInit"` // it was empty, so Lathe ran git init
}

// ExpandPath turns a "~" or "~/…" prefix into the home dir and cleans the
// result. It does not make a relative path absolute.
func ExpandPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, p[1:])
	}
	if p == "" {
		return "", errors.New("workspace path is empty")
	}
	return filepath.Clean(p), nil
}

// SetWorkspace records path as the tutorial's workspace, creating the folder
// if needed and running `git init` in it when it is empty. path must be
// absolute (after ~ expansion); a CLI caller resolves a relative path against
// its own working directory first. The tutorials dir itself is refused, so
// the reader's code never lands inside Lathe's store.
func SetWorkspace(tutorialDir, path string) (*WorkspaceSetup, error) {
	p, err := ExpandPath(path)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(p) {
		return nil, fmt.Errorf("workspace path must be absolute (or start with ~): %q", path)
	}
	if store := filepath.Dir(tutorialDir); within(p, store) {
		return nil, fmt.Errorf("pick a folder outside Lathe's tutorial store (%s)", store)
	}

	setup := &WorkspaceSetup{Path: p}
	info, err := os.Stat(p)
	switch {
	case err == nil && !info.IsDir():
		return nil, fmt.Errorf("%s is a file, not a folder", p)
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(p, 0o755); err != nil {
			return nil, fmt.Errorf("create workspace: %w", err)
		}
		setup.Created = true
	case err != nil:
		return nil, err
	}

	empty, err := dirEmpty(p)
	if err != nil {
		return nil, err
	}
	if empty {
		cmd := exec.Command("git", "init", "-q")
		cmd.Dir = p
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git init in %s: %v: %s", p, err, strings.TrimSpace(string(out)))
		}
		setup.GitInit = true
	}

	ws := &Workspace{Path: p, UpdatedAt: time.Now().UTC()}
	if err := writeJSONFile(filepath.Join(tutorialDir, WorkspaceFile), ws); err != nil {
		return nil, err
	}
	return setup, nil
}

// within reports whether p is root or below it.
func within(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func dirEmpty(dir string) (bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}
