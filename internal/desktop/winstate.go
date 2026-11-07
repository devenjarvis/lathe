package desktop

import (
	"strings"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/serve"
)

// Windows and restore on launch (#146). The library window is "main"; a
// tutorial opened in its own window (⌘↩ on a cover, ⌘-click) is
// "tutorial:<slug>", one per tutorial. When the app quits it records, for the
// main window and each tutorial window still open, the page it showed (from
// the server, which knows each window's page by the x-wails-window-name
// header), how far it was scrolled, and its bounds, in config.json's
// "windows". On launch planRestore checks every saved entry before anything
// opens: only Lathe's own reading pages and library views, sizes within
// reason, and positions on a screen that is still there.

const (
	// mainWindowName is the library window's Wails name.
	mainWindowName = "main"
	// tutorialWindowPrefix starts a tutorial window's name.
	tutorialWindowPrefix = "tutorial:"
	// maxRestoreWindows caps the tutorial windows reopened on launch.
	maxRestoreWindows = 8
	// maxWindowSide bounds a restored window's width and height.
	maxWindowSide = 10000
	// maxWindowCoord bounds a restored window's position.
	maxWindowCoord = 100000
)

// tutorialWindowName is the window a tutorial opens in.
func tutorialWindowName(slug string) string { return tutorialWindowPrefix + slug }

// tutorialPathSlug returns the slug of a reading-page path ("/slug/…") the
// shell may open as a tutorial window: not a pop-out (it has a query), not
// Settings or another app page. The server has already checked the path
// (serve.Server.TutorialPage); this only names the window.
func tutorialPathSlug(path string) (string, bool) {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?") {
		return "", false
	}
	base, _, _ := strings.Cut(path, "#")
	slug, _, ok := strings.Cut(strings.TrimPrefix(base, "/"), "/")
	if !ok || slug == "" || strings.HasPrefix(slug, "-") || strings.HasPrefix(slug, "_") || slug == "." || slug == ".." {
		return "", false
	}
	return slug, true
}

// rect is a window's or a screen's bounds (DIP).
type rect struct{ X, Y, W, H int }

// restoreWindow is one window to open on launch.
type restoreWindow struct {
	name   string
	main   bool
	path   string
	scroll int
	bounds rect // zero: the default size, centered
}

// pageChecker is what planRestore asks the server: whether a path is a
// stored tutorial's reading page (and its slug), or a library view.
type pageChecker struct {
	tutorial func(path string) (slug string, ok bool)
	home     func(path string) bool
}

// planRestore turns the saved windows into the windows to open: the main
// window first (at "/" when its page is gone or unknown), then up to
// maxRestoreWindows tutorial windows, one per tutorial, for tutorials that
// still exist. Anything else in the file is dropped.
func planRestore(saved []config.WindowState, pc pageChecker) []restoreWindow {
	mainW := restoreWindow{name: mainWindowName, main: true, path: "/"}
	var out []restoreWindow
	seen := map[string]bool{}
	gotMain := false
	for _, ws := range saved {
		b := savedBounds(ws)
		if ws.Main {
			if gotMain {
				continue
			}
			gotMain = true
			mainW.bounds = b
			if _, ok := pc.tutorial(ws.Path); ok || pc.home(ws.Path) {
				mainW.path, mainW.scroll = ws.Path, clampScroll(ws.Scroll)
			}
			continue
		}
		slug, ok := pc.tutorial(ws.Path)
		if !ok || seen[slug] || len(out) >= maxRestoreWindows {
			continue
		}
		seen[slug] = true
		out = append(out, restoreWindow{name: tutorialWindowName(slug), path: ws.Path, scroll: clampScroll(ws.Scroll), bounds: b})
	}
	return append([]restoreWindow{mainW}, out...)
}

func clampScroll(y int) int {
	if y < 0 || y > 10_000_000 {
		return 0
	}
	return y
}

// savedBounds is an entry's bounds, or zero when they aren't usable.
func savedBounds(ws config.WindowState) rect {
	if ws.Width < mainMinWidthDIP || ws.Height < mainMinHeightDIP || ws.Width > maxWindowSide || ws.Height > maxWindowSide {
		return rect{}
	}
	if abs(ws.X) > maxWindowCoord || abs(ws.Y) > maxWindowCoord {
		return rect{}
	}
	return rect{ws.X, ws.Y, ws.Width, ws.Height}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Restored windows are at least this big (the main window's minimum).
const mainMinWidthDIP, mainMinHeightDIP = 360, 300

// onScreen reports whether enough of b (its title bar's middle) lies in one
// of the screens' work areas that the reader can grab it. A window saved on
// a monitor that is gone opens at the default place instead.
func onScreen(b rect, screens []rect) bool {
	if b.W == 0 || b.H == 0 {
		return false
	}
	// A point a little below the top edge, in the middle: the title bar.
	px, py := b.X+b.W/2, b.Y+16
	for _, s := range screens {
		if px >= s.X && px < s.X+s.W && py >= s.Y && py < s.Y+s.H {
			return true
		}
	}
	return false
}

// openWindow is a window the shell tracks for the next restore.
type openWindow struct {
	name   string
	main   bool
	path   string // the page it opened at (the server knows what it shows now)
	bounds rect
}

// snapshotWindows is what config.json records at quit: each tracked window
// with the page it shows now and how far it is scrolled (views, from
// serve.Server.WindowViews), main window first.
func snapshotWindows(open []openWindow, views map[string]serve.WindowView) []config.WindowState {
	var mainState []config.WindowState
	var rest []config.WindowState
	for _, w := range open {
		st := config.WindowState{Main: w.main, Path: w.path, X: w.bounds.X, Y: w.bounds.Y, Width: w.bounds.W, Height: w.bounds.H}
		if v, ok := views[w.name]; ok && v.Path != "" {
			st.Path, st.Scroll = v.Path, v.Scroll
		}
		if w.main {
			mainState = append(mainState[:0], st)
		} else {
			rest = append(rest, st)
		}
	}
	return append(mainState, rest...)
}
