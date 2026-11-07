package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
)

const (
	// AppName is the app's display name (window titles, about box, menus).
	AppName = "Lathe"
	// BundleID is the macOS bundle identifier. It doubles as the
	// single-instance id and the Linux GTK application id.
	BundleID = "com.devenjarvis.lathe"
)

// ErrNotBuilt is what Run returns from a binary built without the desktop tag.
var ErrNotBuilt = errors.New("this lathe was not built with the desktop app; " +
	"run `lathe serve` to read in your browser, or download the desktop build " +
	"from https://github.com/devenjarvis/lathe/releases")

// Options configures Run.
type Options struct {
	// Agent is the ACP command to host, as `lathe serve --agent` takes it.
	// Empty means the agent picked in the reader's agent panel
	// (config.Agent(), looked up in internal/agents).
	Agent string

	// PrepareEnv, when set, runs once before the agent command is resolved
	// and started. It is the seam for loading the login shell's environment
	// (PATH in particular) on a dock or Start-menu launch, which does not
	// inherit it. An error is logged and the app starts anyway.
	PrepareEnv func(context.Context) error

	// Log receives the shell's diagnostics (notifications it could not post,
	// a failed serve.json write, ...). Nil means os.Stderr.
	Log io.Writer
}

// HasDisplay reports whether this process can plausibly open a window, and
// when it cannot, why. macOS and Windows always have one for a GUI launch; on
// Linux and the BSDs a window needs an X11 or Wayland display.
func HasDisplay(getenv func(string) string) (bool, string) {
	return hasDisplay(runtime.GOOS, getenv)
}

func hasDisplay(goos string, getenv func(string) string) (bool, string) {
	switch goos {
	case "darwin", "windows":
		return true, ""
	}
	if getenv("WAYLAND_DISPLAY") != "" || getenv("DISPLAY") != "" {
		return true, ""
	}
	return false, "no display found (DISPLAY and WAYLAND_DISPLAY are unset)"
}

// SettingsPath is the Settings window's page (lathe serve's /-/settings).
// Lathe › Settings… (⌘,) opens it in its own window; a "#tab" deep-links to
// one of its tabs.
const SettingsPath = "/-/settings"

// windowKey names the app window that shows path, so opening the same view
// twice focuses the window already showing it. Every Settings tab shares one
// window: /-/settings#agent and /-/settings#reading are the same window at
// different tabs.
func windowKey(path string) (key string, settings bool) {
	base, _, _ := strings.Cut(path, "#")
	if base == SettingsPath {
		return "window:" + SettingsPath, true
	}
	return "window:" + path, false
}

// windowPath checks a path for OpenWindow: it must be a same-origin absolute
// path. "//host/x" is a network-path reference that would leave the app's
// origin, and a backslash is treated as a slash by some engines, so both are
// refused, as are control characters.
func windowPath(path string) (string, error) {
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("window path %q must start with /", path)
	}
	if strings.HasPrefix(path, "//") || strings.Contains(path, `\`) {
		return "", fmt.Errorf("window path %q must stay on this origin", path)
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("window path %q contains a control character", path)
		}
	}
	return path, nil
}
