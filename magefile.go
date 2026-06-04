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
	"fmt"
	"os"
	"os/exec"
	"strings"
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

// Build compiles the self-contained binary (embedded assets included).
func Build() error {
	return run("go", "build", "-o", "lathe")
}

// Check runs the full gate: fmt check, vet, lint, test, build. This is what CI
// runs and what you should run before opening a PR. It stops at the first
// failure.
func Check() error {
	for _, step := range []struct {
		name string
		fn   func() error
	}{
		{"fmt-check", FmtCheck},
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
