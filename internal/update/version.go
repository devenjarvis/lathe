// Package update tells the desktop app when a newer Lathe is out (#149).
//
// It is notify-only: it reads the latest GitHub release of devenjarvis/lathe
// (the public API, no auth, nothing sent but the request itself), compares it
// with the running build (internal/buildinfo), and words a quiet notice. It
// never downloads or replaces anything. Homebrew cask installs are told to run
// `brew upgrade --cask lathe`; once the bundle on disk is newer than the
// running app (the reader ran that command), the notice offers a restart.
//
// The package is plain Go with no Wails or HTTP-server dependencies, and the
// network fetch is a Fetcher the caller supplies, so tests never touch the
// network.
package update

import (
	"regexp"
	"strconv"
	"strings"
)

// Version is a release version: MAJOR.MINOR.PATCH with an optional
// prerelease ("rc.1").
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

// releaseRE matches the versions Lathe is released under: a tag "v1.2.3" or
// GoReleaser's "1.2.3", optionally with a prerelease "-rc.1". Anything else
// (git describe output, Go pseudo-versions, snapshots, "dev") is a
// development build, which is never offered an update.
var releaseRE = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:alpha|beta|rc)(?:\.?\d+)?))?$`)

// Parse reads a release version. ok is false for a development build:
// anything that isn't a plain release or a alpha/beta/rc prerelease, and
// 0.0.0 (the packaging dry run's placeholder).
func Parse(s string) (v Version, ok bool) {
	m := releaseRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, false
	}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	v.Patch, _ = strconv.Atoi(m[3])
	v.Pre = m[4]
	if v.Major == 0 && v.Minor == 0 && v.Patch == 0 {
		return Version{}, false
	}
	return v, true
}

// String is the version without a leading "v" ("0.9.0", "1.0.0-rc.1").
func (v Version) String() string {
	s := strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Short is the version as the notice says it: "0.9" for 0.9.0, else String.
func (v Version) Short() string {
	if v.Patch == 0 && v.Pre == "" {
		return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor)
	}
	return v.String()
}

// Compare orders versions by semver precedence: -1 when a < b, 0 when equal,
// +1 when a > b. A prerelease sorts before its release.
func Compare(a, b Version) int {
	for _, d := range [...]int{a.Major - b.Major, a.Minor - b.Minor, a.Patch - b.Patch} {
		if d != 0 {
			return sign(d)
		}
	}
	switch {
	case a.Pre == b.Pre:
		return 0
	case a.Pre == "":
		return 1
	case b.Pre == "":
		return -1
	}
	return comparePre(a.Pre, b.Pre)
}

// comparePre compares prerelease strings identifier by identifier, numbers
// numerically ("rc.2" < "rc.10"), the rest as text.
func comparePre(a, b string) int {
	as, bs := splitPre(a), splitPre(b)
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				return sign(an - bn)
			}
		case aerr == nil:
			return -1 // numeric identifiers sort first
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return sign(len(as) - len(bs))
}

// splitPre splits "rc.1" and "rc1" alike into ["rc", "1"].
func splitPre(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ".") {
		i := strings.IndexFunc(part, func(r rune) bool { return r >= '0' && r <= '9' })
		if i > 0 {
			out = append(out, part[:i], part[i:])
			continue
		}
		out = append(out, part)
	}
	return out
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// Newer reports whether candidate is a release newer than current. It is
// false when either is not a release version (a development build is never
// offered an update, and a malformed tag is never offered as one).
func Newer(candidate, current string) bool {
	c, ok := Parse(candidate)
	if !ok {
		return false
	}
	cur, ok := Parse(current)
	if !ok {
		return false
	}
	return Compare(c, cur) > 0
}
