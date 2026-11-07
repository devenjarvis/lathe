package update

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// BrewCommand is what a Homebrew cask install runs to update.
const BrewCommand = "brew upgrade --cask lathe"

// brewPrefixes are where Homebrew lives: Apple silicon, then Intel.
var brewPrefixes = []string{"/opt/homebrew", "/usr/local"}

// Install is how the running app was installed.
type Install struct {
	// Bundle is the macOS app bundle the binary runs from
	// ("/Applications/Lathe.app"), "" when it isn't in one.
	Bundle string
	// Homebrew is a Homebrew cask install: the bundle, with a lathe cask in
	// the Caskroom. It updates with `brew upgrade --cask lathe`.
	Homebrew bool
}

// DetectInstall works out how the binary at exe (symlinks already resolved)
// was installed. exists reports whether a path exists (os.Stat in the app);
// extra are Homebrew prefixes to check before the standard ones
// ($HOMEBREW_PREFIX, when set).
//
// The cask moves Lathe.app into /Applications and keeps only its metadata
// under <prefix>/Caskroom/lathe, so the bundle's path alone can't tell a cask
// install from a downloaded zip; the Caskroom entry can.
func DetectInstall(exe string, exists func(string) bool, extra ...string) Install {
	var in Install
	in.Bundle = bundleOf(exe)
	if in.Bundle == "" {
		return in
	}
	if strings.Contains(filepath.ToSlash(in.Bundle), "/Caskroom/lathe/") {
		in.Homebrew = true
		return in
	}
	for _, p := range append(extra, brewPrefixes...) {
		if p != "" && exists(filepath.Join(p, "Caskroom", "lathe")) {
			in.Homebrew = true
			break
		}
	}
	return in
}

// Detect is DetectInstall for this process.
func Detect() Install {
	exe, err := os.Executable()
	if err != nil {
		return Install{}
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return DetectInstall(exe, func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}, os.Getenv("HOMEBREW_PREFIX"))
}

// bundleOf returns the X.app directory when exe is X.app/Contents/MacOS/<bin>.
func bundleOf(exe string) string {
	exe = filepath.ToSlash(filepath.Clean(exe))
	macos := pathDir(exe)
	contents := pathDir(macos)
	app := pathDir(contents)
	if pathBase(macos) != "MacOS" || pathBase(contents) != "Contents" || !strings.HasSuffix(app, ".app") {
		return ""
	}
	return filepath.FromSlash(app)
}

func pathDir(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return ""
}

func pathBase(p string) string { return p[strings.LastIndex(p, "/")+1:] }

var (
	// CFBundleGetInfoString is "Lathe <full version>" (build/darwin/Info.plist).
	infoStringRE = regexp.MustCompile(`<key>CFBundleGetInfoString</key>\s*<string>\s*Lathe\s+([^<\s]+)\s*</string>`)
	// CFBundleShortVersionString is numeric X.Y.Z, prerelease stripped.
	shortVersionRE = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>\s*([^<\s]+)\s*</string>`)
)

// BundleVersion is the version of the app bundle on disk, read from its
// Info.plist, or "" when it can't be read. After `brew upgrade --cask lathe`
// replaces the bundle under the running app, this is newer than the running
// version, which is when the notice offers a restart.
func BundleVersion(bundle string, read func(string) ([]byte, error)) string {
	if bundle == "" {
		return ""
	}
	if read == nil {
		read = os.ReadFile
	}
	b, err := read(filepath.Join(bundle, "Contents", "Info.plist"))
	if err != nil {
		return ""
	}
	if m := infoStringRE.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	if m := shortVersionRE.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}
