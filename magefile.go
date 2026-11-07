//go:build mage

// Mage build/check targets for Lathe.
//
// This file is excluded from normal builds by the mage build tag, so it never
// collides with the real package main (main.go). It imports only the standard
// library on purpose, so it adds nothing to go.mod/go.sum -- mage compiles it
// itself.
//
// Run "mage" (defaults to Check) or a single target, e.g. "mage test". CI runs
// the same "mage check", so local and CI cannot drift.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// skillsSrcDir is the human-edited source of truth for skills; skillsDataDir is
// the tracked, embeddable copy (go:embed cannot reach paths starting with ".").
const (
	skillsSrcDir  = ".claude/skills"
	skillsDataDir = "internal/skills/data"
)

// Default target when `mage` is run with no arguments.
var Default = Check

// run executes a command with stdout/stderr wired through to the caller.
func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Fmt formats the tree with gofmt -w.
func Fmt() error {
	return run("gofmt", "-w", ".")
}

// FmtCheck fails if any files are not gofmt-clean (the CI-safe check).
func FmtCheck() error {
	out, err := exec.Command("gofmt", "-l", ".").Output()
	if err != nil {
		return err
	}
	if files := strings.TrimSpace(string(out)); files != "" {
		return fmt.Errorf("these files need gofmt:\n%s\nrun `mage fmt`", files)
	}
	return nil
}

// Vet runs go vet over all packages.
func Vet() error {
	return run("go", "vet", "./...")
}

// Lint runs golangci-lint (config in .golangci.yml).
func Lint() error {
	return run("golangci-lint", "run")
}

// Test runs the unit tests with the race detector.
func Test() error {
	return run("go", "test", "-race", "./...")
}

// Build compiles the self-contained binary (embedded assets included), stamping
// in a git-derived version so a locally built `./lathe --version` reports a real
// version instead of "dev". The fully-qualified ldflags path must match
// internal/buildinfo and .goreleaser.yaml.
func Build() error {
	const pkg = "github.com/devenjarvis/lathe/internal/buildinfo"
	version := gitDescribe()
	ldflags := fmt.Sprintf("-X %s.Version=%s -X %s.Commit=%s", pkg, version, pkg, gitCommit())
	return run("go", "build", "-ldflags", ldflags, "-o", "lathe")
}

// Desktop builds the binary with the desktop app (internal/desktop, Wails v3)
// as ./lathe-desktop: the same CLI, plus `lathe`/`lathe app` opening the app.
// "production" drops Wails' dev-mode logging and web inspector. On Linux it
// needs cgo plus the GTK4 and WebKitGTK 6.0 dev packages (Debian/Ubuntu:
// libgtk-4-dev libwebkitgtk-6.0-dev); macOS and Windows need nothing extra.
//
// Desktop and DesktopCheck are deliberately not part of Check: CI's ubuntu job
// has no GTK/WebKit libraries, and the default build stays pure Go.
func Desktop() error {
	const pkg = "github.com/devenjarvis/lathe/internal/buildinfo"
	ldflags := fmt.Sprintf("-X %s.Version=%s -X %s.Commit=%s", pkg, gitDescribe(), pkg, gitCommit())
	cmd := exec.Command("go", "build", "-tags", "desktop,production", "-ldflags", ldflags, "-o", "lathe-desktop", ".")
	cmd.Env = os.Environ()
	if runtime.GOOS == "darwin" {
		cmd.Env = append(cmd.Env, darwinMinOSEnv()...)
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// darwinMinOS must match MIN_MACOS in build/darwin/package.sh and
// LSMinimumSystemVersion in build/darwin/Info.plist.
const darwinMinOS = "12.0"

// darwinMinOSEnv pins the cgo objects and the final link to darwinMinOS. The
// Go linker ignores MACOSX_DEPLOYMENT_TARGET and links for 11.0, while clang
// compiles cgo objects for the host macOS, so without both settings ld warns
// once per object file that it was "built for newer 'macOS' version".
func darwinMinOSEnv() []string {
	return []string{
		"MACOSX_DEPLOYMENT_TARGET=" + darwinMinOS,
		"CGO_LDFLAGS=" + strings.TrimSpace(os.Getenv("CGO_LDFLAGS")+" -mmacosx-version-min="+darwinMinOS),
	}
}

// DesktopCheck vets, lints and tests the desktop-tagged code, and checks the
// Windows desktop build still cross-compiles without cgo. Needs the same
// libraries as Desktop; not part of Check (see Desktop).
func DesktopCheck() error {
	steps := [][]string{
		{"go", "vet", "-tags", "desktop", "./..."},
		{"golangci-lint", "run", "--build-tags", "desktop", "./internal/desktop/...", "./cmd/..."},
		{"go", "test", "-race", "-tags", "desktop", "./internal/desktop/...", "./cmd/..."},
	}
	for _, s := range steps {
		fmt.Printf("==> %s\n", strings.Join(s, " "))
		if err := run(s[0], s[1:]...); err != nil {
			return err
		}
	}
	fmt.Println("==> windows desktop cross-build")
	cmd := exec.Command("go", "build", "-tags", "desktop", "-o", os.DevNull, ".")
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// gitDescribe returns `git describe --tags --always --dirty`, or "dev" if git
// is unavailable (so Build never fails just because there are no tags yet).
func gitDescribe() string {
	out, err := exec.Command("git", "describe", "--tags", "--always", "--dirty").Output()
	if v := strings.TrimSpace(string(out)); err == nil && v != "" {
		return v
	}
	return "dev"
}

// gitCommit returns the short HEAD SHA, or "" if git is unavailable.
func gitCommit() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Skills regenerates the embeddable copy of the skills under internal/skills/data
// from the human-edited source at .claude/skills. The generated copy is tracked
// in git so `go build`/`go install` work without mage.
func Skills() error {
	names, err := skillNames()
	if err != nil {
		return err
	}
	// Wipe and rebuild the data dir so deletions/renames in the source are
	// reflected (and never leave stale skills behind).
	if err := os.RemoveAll(skillsDataDir); err != nil {
		return fmt.Errorf("clear %s: %w", skillsDataDir, err)
	}
	for _, name := range names {
		src := filepath.Join(skillsSrcDir, name, "SKILL.md")
		dst := filepath.Join(skillsDataDir, name, "SKILL.md")
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read %s: %w", src, err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("mkdir for %s: %w", dst, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
	}
	fmt.Printf("synced %d skills into %s\n", len(names), skillsDataDir)
	return nil
}

// SkillsCheck fails if the embedded copy under internal/skills/data has drifted
// from the source at .claude/skills (mirrors FmtCheck: read-only, CI-safe).
func SkillsCheck() error {
	names, err := skillNames()
	if err != nil {
		return err
	}
	var drift []string
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
		src := filepath.Join(skillsSrcDir, name, "SKILL.md")
		dst := filepath.Join(skillsDataDir, name, "SKILL.md")
		want, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read %s: %w", src, err)
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			drift = append(drift, fmt.Sprintf("%s (missing in data)", name))
			continue
		}
		if !bytes.Equal(want, got) {
			drift = append(drift, fmt.Sprintf("%s (content differs)", name))
		}
	}
	// Catch stale skills that exist in data/ but no longer in the source.
	if entries, err := os.ReadDir(skillsDataDir); err == nil {
		for _, e := range entries {
			if e.IsDir() && !seen[e.Name()] {
				drift = append(drift, fmt.Sprintf("%s (stale in data)", e.Name()))
			}
		}
	}
	if len(drift) > 0 {
		return fmt.Errorf("embedded skills are out of date:\n  %s\nrun `mage skills`", strings.Join(drift, "\n  "))
	}
	return nil
}

// skillNames lists the skill directory names under the source dir.
func skillNames() ([]string, error) {
	entries, err := os.ReadDir(skillsSrcDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", skillsSrcDir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// Check runs the full gate: fmt check, skills parity check, vet, lint, test,
// build. This is what CI runs and what you should run before opening a PR. It
// stops at the first failure.
func Check() error {
	for _, step := range []struct {
		name string
		fn   func() error
	}{
		{"fmt-check", FmtCheck},
		{"skills-check", SkillsCheck},
		{"vet", Vet},
		{"lint", Lint},
		{"test", Test},
		{"build", Build},
	} {
		fmt.Printf("==> %s\n", step.name)
		if err := step.fn(); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}
	return nil
}
