package serve

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "embed"

	"github.com/devenjarvis/lathe/internal/shortcuts"
)

// The shortcut registry on the page, and the desktop menu bar's way into it.
//
// Every page inlines the registry (internal/shortcuts) as JSON plus
// shortcuts.js, which binds the in-page keys. In a plain browser that is all
// there is. In the desktop app the menu bar owns the combos, and a menu click
// reaches the page the same way every other shell feature does: over
// same-origin HTTP, never a JS bridge.
//
//	GET  /-/shortcuts                  → the registry JSON (any build)
//	GET  /-/native/commands?path=…     → SSE: "hello" {client}, then "command" {id}
//	POST /-/native/context {client, enabled}  → 204; this page is the focused one
//
// A page opens the command stream when it loads and posts its context (the
// page-run commands it can carry out right now) on "hello" and whenever its
// window gains focus. The server keeps the focused page, hands the shell its
// MenuState (the shell enables and disables items from it) and delivers
// RunCommand to that page's stream. The /-/native/ endpoints 404 without a
// shell.

// NativeMenu is what a shell with a menu bar adds to Native. It is optional:
// the server checks for it with a type assertion, so a Native without menus
// keeps working.
type NativeMenu interface {
	// SetMenuState is called whenever the focused page or what it can run
	// changes. It is called from HTTP handler goroutines, one at a time.
	SetMenuState(MenuState)
}

// MenuState is the focused page, for the menu bar.
type MenuState struct {
	// Path is the page's same-origin path (with query), "" when no page is
	// connected.
	Path  string
	Scope shortcuts.Scope
	// Enabled holds the page-run commands (shortcuts.HandlerPage) the page
	// reported it can run, already filtered to known, built, in-scope ones.
	Enabled map[string]bool
}

//go:embed shortcuts.js
var shortcutsScript string

// shortcutsJSON and shortcutsJS are the template helpers behind the
// {{template "shortcuts"}} partial (shortcuts.html). The JSON is safe to
// inline: json.Marshal escapes <, > and &.
func shortcutsJSON() template.JS { return template.JS(shortcuts.JSON()) }
func shortcutsJS() template.JS   { return template.JS(shortcutsScript) }

// handleShortcuts serves the registry, for the palette and Settings.
func (s *Server) handleShortcuts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(shortcuts.JSON())
}

// commandClient is one page with an open command stream.
type commandClient struct {
	id      string
	path    string
	scope   shortcuts.Scope
	enabled map[string]bool
	active  uint64 // last time it was the focused page (hub tick)
	ch      chan string
}

// commandHub tracks the pages that can take menu commands and which one is
// focused. The zero value is ready to use.
type commandHub struct {
	mu      sync.Mutex
	clients map[string]*commandClient
	tick    uint64
	current string
	// pushMu orders the shell updates: the state is computed and pushed
	// under it, so two handlers can't hand the shell an older state last.
	pushMu sync.Mutex
}

func (h *commandHub) add(path string) *commandClient {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients == nil {
		h.clients = map[string]*commandClient{}
	}
	h.tick++
	c := &commandClient{
		id:      "c" + strconv.FormatUint(h.tick, 10),
		path:    path,
		scope:   shortcuts.ScopeForPath(path),
		enabled: map[string]bool{},
		active:  h.tick,
		ch:      make(chan string, 8),
	}
	h.clients[c.id] = c
	// The page that just loaded is the one in front.
	h.current = c.id
	return c
}

// remove drops a page whose stream closed. When it was the focused page, the
// most recently focused remaining page takes over.
func (h *commandHub) remove(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, id)
	if h.current != id {
		return
	}
	h.current = ""
	var best uint64
	for cid, c := range h.clients {
		if c.active > best {
			best, h.current = c.active, cid
		}
	}
}

// focus records what a page can run and makes it the focused page.
func (h *commandHub) focus(id string, enabled []string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.clients[id]
	if !ok {
		return false
	}
	c.enabled = filterEnabled(c.scope, enabled)
	h.tick++
	c.active = h.tick
	h.current = id
	return true
}

// filterEnabled keeps the ids that name a built page-run command allowed in
// scope: a page can't switch on a menu item that isn't its to run.
func filterEnabled(scope shortcuts.Scope, ids []string) map[string]bool {
	out := map[string]bool{}
	for _, id := range ids {
		sc, ok := shortcuts.ByID(id)
		if !ok || sc.Stub || sc.Handler != shortcuts.HandlerPage || !sc.Allowed(scope) {
			continue
		}
		out[id] = true
	}
	return out
}

func (h *commandHub) state() MenuState {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.clients[h.current]
	if c == nil {
		return MenuState{Enabled: map[string]bool{}}
	}
	en := make(map[string]bool, len(c.enabled))
	for k, v := range c.enabled {
		en[k] = v
	}
	return MenuState{Path: c.path, Scope: c.scope, Enabled: en}
}

// send delivers a command to the focused page if it can run it.
func (h *commandHub) send(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	c := h.clients[h.current]
	if c == nil || !c.enabled[id] {
		return false
	}
	select {
	case c.ch <- id:
		return true
	default:
		// The page is not reading (it is busy or going away); a dropped
		// click is better than a stale one later.
		return false
	}
}

// pushMenuState hands the shell the current state, when it has a menu.
func (s *Server) pushMenuState() {
	nm, ok := s.nativeShell().(NativeMenu)
	if !ok {
		return
	}
	s.cmds.pushMu.Lock()
	defer s.cmds.pushMu.Unlock()
	nm.SetMenuState(s.cmds.state())
}

// MenuState is the focused page's state, for a shell building its menu.
func (s *Server) MenuState() MenuState { return s.cmds.state() }

// RunCommand delivers a menu click on a page-run command to the focused page.
// It reports whether a page took it: false when no page is connected, or the
// focused page can't run it now (the shell may then fall back, e.g. navigate
// its window for a command with an Href).
func (s *Server) RunCommand(id string) bool {
	sc, ok := shortcuts.ByID(id)
	if !ok || sc.Stub || sc.Handler != shortcuts.HandlerPage {
		return false
	}
	return s.cmds.send(id)
}

// handleNativeCommands is a page's command stream (SSE). The first event,
// "hello", carries the page's client id for POST /-/native/context; each
// "command" event is one menu click to run.
func (s *Server) handleNativeCommands(w http.ResponseWriter, r *http.Request) {
	if s.nativeShell() == nil {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	path := r.URL.Query().Get("path")
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || len(path) > 2048 {
		http.Error(w, "path must be this page's same-origin path", http.StatusBadRequest)
		return
	}
	c := s.cmds.add(path)
	defer func() {
		s.cmds.remove(c.id)
		s.pushMenuState()
	}()

	flusher, _ := w.(http.Flusher)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	hello, _ := json.Marshal(map[string]string{"client": c.id})
	if _, err := fmt.Fprintf(w, "event: hello\ndata: %s\n\n", hello); err != nil {
		return
	}
	if flusher != nil {
		flusher.Flush()
	}
	// Until the page posts its context it can run nothing; the menu shows
	// that rather than the previous page's items.
	s.pushMenuState()

	keepAlive := time.NewTicker(20 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case id := <-c.ch:
			raw, _ := json.Marshal(map[string]string{"id": id})
			if _, err := fmt.Fprintf(w, "event: command\ndata: %s\n\n", raw); err != nil {
				return
			}
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// handleNativeContext records what the posting page can run and makes it the
// focused page.
func (s *Server) handleNativeContext(w http.ResponseWriter, r *http.Request) {
	if s.nativeShell() == nil {
		http.NotFound(w, r)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var payload struct {
		Client  string   `json:"client"`
		Enabled []string `json:"enabled"`
	}
	if !readJSONBody(w, r, maxNativeBody, &payload) {
		return
	}
	if !s.cmds.focus(payload.Client, payload.Enabled) {
		http.Error(w, "unknown client: reopen the command stream", http.StatusNotFound)
		return
	}
	s.pushMenuState()
	w.WriteHeader(http.StatusNoContent)
}
