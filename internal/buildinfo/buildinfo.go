// Package buildinfo carries the version metadata stamped into the lathe binary.
//
// The exported vars are overridden at link time via -ldflags "-X ...=value"
// (the local mage build and the GoReleaser release pipeline both do this). When
// they are left at their zero/dev defaults -- e.g. a plain `go install` -- we
// fall back to the module version reported by runtime/debug.ReadBuildInfo so
// installed binaries still show something useful (like v0.1.0) instead of "dev".
//
// This package deliberately has no cobra import so internal/ stays cobra-free
// (see AGENTS.md conventions).
package buildinfo

import "runtime/debug"

// These are overridden via -ldflags at build time. Keep the fully-qualified
// path in sync with magefile.go and .goreleaser.yaml:
//
//	-X github.com/devenjarvis/lathe/internal/buildinfo.Version=...
var (
	// Version is the semantic version (e.g. "v0.1.0"), or "dev" for an
	// un-stamped build.
	Version = "dev"
	// Commit is the short git SHA the binary was built from.
	Commit = ""
	// Date is the build timestamp (RFC3339 from GoReleaser).
	Date = ""
)

// Resolve returns the best available version string.
//
// Precedence: an ldflags-injected Version (anything other than the "dev"
// default) wins; otherwise we consult the module build info so `go install`ed
// binaries report their module version; finally we fall back to "dev".
func Resolve() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

// String returns a human-friendly one-line description including the commit and
// build date when those are known.
func String() string {
	s := Resolve()
	if Commit != "" {
		s += " (commit " + Commit
		if Date != "" {
			s += ", built " + Date
		}
		s += ")"
	} else if Date != "" {
		s += " (built " + Date + ")"
	}
	return s
}
