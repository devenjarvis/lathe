package serve

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/devenjarvis/lathe/internal/store"
)

// Windows in the desktop app (#146): a tutorial opens in its own window, and
// the app reopens its windows, their pages and scroll positions on launch.
//
//	POST /-/native/window {"path": "/{slug}/" | "/{slug}/{part}[#id]"} → 204 (a tutorial window)
//	POST /-/native/view   {"client", "scroll"} → 204 (this page's scroll position, for restore)
//
// The server knows which app window a page is in from the header Wails puts
// on every request of a window (x-wails-window-name), so the shell can ask
// which page, scrolled how far, each window last showed (WindowViews), and
// hand back the positions to restore on launch (RestoreViews).

// windowHeader names the app window a request came from. Wails sets it on
// every asset request of a window; a plain browser never does.
const windowHeader = "X-Wails-Window-Name"

// fragmentID is the #fragment a window or notification path may carry: a
// heading id or a panel anchor ("#agent"), nothing that could hold a script.
var fragmentID = regexp.MustCompile(`^[\p{L}\p{N}_-]{1,128}$`)

// maxWindowPath caps the paths windows and notifications may open.
const maxWindowPath = 1024

// cleanSamePath splits raw into a same-origin path and its fragment, or
// reports false for anything that could leave the origin or carry markup:
// another scheme or host, "//", a backslash, control characters, a fragment
// that isn't a plain id, or an encoded path.
func cleanSamePath(raw string) (u *url.URL, frag string, ok bool) {
	if raw == "" || len(raw) > maxWindowPath || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(raw, `\`) {
		return nil, "", false
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return nil, "", false
		}
	}
	base, frag, hasFrag := strings.Cut(raw, "#")
	if hasFrag && !fragmentID.MatchString(frag) {
		return nil, "", false
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" || u.RawPath != "" {
		return nil, "", false
	}
	return u, frag, true
}

// tutorialPage reports whether raw is a reading page of a stored tutorial,
// "/{slug}/" or "/{slug}/{part}" for a declared part, with no query and at
// most a plain #fragment, and returns the tutorial.
func (s *Server) tutorialPage(raw string) (*store.Tutorial, bool) {
	u, _, ok := cleanSamePath(raw)
	if !ok || u.RawQuery != "" || u.ForceQuery {
		return nil, false
	}
	segs := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(segs) != 2 || segs[0] == "" || segs[0] == "." || segs[0] == ".." || strings.HasPrefix(segs[0], "-") || strings.HasPrefix(segs[0], "_") {
		return nil, false
	}
	tutDir, ok := s.safeTutorialPath(segs[0])
	if !ok {
		return nil, false
	}
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		return nil, false
	}
	if segs[1] != "" && !isKnownPart(tut, segs[1]) {
		return nil, false
	}
	return tut, true
}

// TutorialPage reports whether path is a reading page of a stored tutorial
// (see tutorialPage) and returns the tutorial's slug and title. The desktop
// shell uses it to name tutorial windows and to check restored ones.
func (s *Server) TutorialPage(path string) (slug, title string, ok bool) {
	tut, ok := s.tutorialPage(path)
	if !ok {
		return "", "", false
	}
	return tut.Slug, tutorialName(tut), true
}

// HomePage reports whether path is the library ("/", optionally with a view
// query such as "?shelf=systems"), which the main window may reopen at.
func HomePage(path string) bool {
	u, frag, ok := cleanSamePath(path)
	if !ok || frag != "" || u.Path != "/" {
		return false
	}
	_, err := url.ParseQuery(u.RawQuery)
	return err == nil
}

// windowView is what one app window last showed.
type windowView struct {
	Path   string
	Scroll int
}

// WindowView is a window's page and scroll position, for the shell.
type WindowView struct {
	Path   string
	Scroll int
}

// viewBook remembers the page each app window shows and the scroll
// positions waiting to be restored. The zero value is ready to use.
type viewBook struct {
	mu      sync.Mutex
	last    map[string]windowView // window name → what it last showed
	restore map[string]windowView // window name → where to put it back
}

// opened records that a page loaded in window and returns the scroll
// position to restore it to (once: a later load of the same window starts at
// the top, as usual).
func (b *viewBook) opened(window, path string) int {
	if window == "" {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.last == nil {
		b.last = map[string]windowView{}
	}
	scroll := 0
	if r, ok := b.restore[window]; ok {
		delete(b.restore, window)
		if r.Path == path {
			scroll = r.Scroll
		}
	}
	b.last[window] = windowView{Path: path, Scroll: scroll}
	return scroll
}

// scrolled records a window's scroll position, if path is still the page it
// shows (a stale report from the page it left is dropped).
func (b *viewBook) scrolled(window, path string, y int) {
	if window == "" {
		return
	}
	if y < 0 {
		y = 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if v, ok := b.last[window]; ok && v.Path == path {
		v.Scroll = y
		b.last[window] = v
	}
}

// WindowViews returns what each app window last showed, by window name.
func (s *Server) WindowViews() map[string]WindowView {
	s.views.mu.Lock()
	defer s.views.mu.Unlock()
	out := make(map[string]WindowView, len(s.views.last))
	for k, v := range s.views.last {
		out[k] = WindowView(v)
	}
	return out
}

// RestoreViews tells the server where to scroll each window's first page on
// launch: when window loads exactly that path, its command stream's hello
// carries the position and the page scrolls there.
func (s *Server) RestoreViews(views map[string]WindowView) {
	s.views.mu.Lock()
	defer s.views.mu.Unlock()
	s.views.restore = make(map[string]windowView, len(views))
	for k, v := range views {
		s.views.restore[k] = windowView(v)
	}
}

// maxScroll bounds a reported scroll position (pixels).
const maxScroll = 10_000_000

// handleNativeView records how far the posting page is scrolled, so the app
// can reopen it there.
func (s *Server) handleNativeView(w http.ResponseWriter, r *http.Request) {
	if s.nativeShell() == nil {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var payload struct {
		Client string `json:"client"`
		Scroll int    `json:"scroll"`
	}
	if !readJSONBody(w, r, maxNativeBody, &payload) {
		return
	}
	if payload.Scroll < 0 || payload.Scroll > maxScroll {
		http.Error(w, "scroll out of range", http.StatusBadRequest)
		return
	}
	window, path, ok := s.cmds.clientWindow(payload.Client)
	if !ok {
		http.Error(w, "unknown client", http.StatusNotFound)
		return
	}
	s.views.scrolled(window, path, payload.Scroll)
	w.WriteHeader(http.StatusNoContent)
}
