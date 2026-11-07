package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/shortcuts"
)

// menuNative is a fake shell with a menu bar.
type menuNative struct {
	*fakeNative
	states chan MenuState
}

func newMenuNative() *menuNative {
	return &menuNative{fakeNative: newFakeNative(), states: make(chan MenuState, 32)}
}

func (m *menuNative) SetMenuState(st MenuState) { m.states <- st }

// waitState returns the next pushed state that satisfies ok.
func (m *menuNative) waitState(t *testing.T, ok func(MenuState) bool) MenuState {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case st := <-m.states:
			if ok(st) {
				return st
			}
		case <-deadline:
			t.Fatal("no matching menu state")
			return MenuState{}
		}
	}
}

// commandStream is an open GET /-/native/commands.
type commandStream struct {
	client string
	hello  string         // the hello event's data
	events chan [2]string // event, data
	cancel context.CancelFunc
}

func openCommands(t *testing.T, ts *httptest.Server, path string) *commandStream {
	t.Helper()
	return openCommandsIn(t, ts, path, "")
}

// openCommandsIn opens the stream as a page in app window window (the
// x-wails-window-name header the shell's webview sends; "" for none).
func openCommandsIn(t *testing.T, ts *httptest.Server, path, window string) *commandStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/-/native/commands?path="+url.QueryEscape(path), nil)
	if window != "" {
		req.Header.Set("x-wails-window-name", window)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("commands = %d", res.StatusCode)
	}
	cs := &commandStream{events: make(chan [2]string, 8), cancel: cancel}
	go func() {
		defer func() { _ = res.Body.Close() }()
		sc := bufio.NewScanner(res.Body)
		var ev string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cs.events <- [2]string{ev, strings.TrimPrefix(line, "data: ")}
			}
		}
		close(cs.events)
	}()
	hello := cs.next(t)
	var h struct{ Client string }
	if hello[0] != "hello" || json.Unmarshal([]byte(hello[1]), &h) != nil || h.Client == "" {
		t.Fatalf("first event = %v, want hello with a client", hello)
	}
	cs.client = h.Client
	cs.hello = hello[1]
	t.Cleanup(cancel)
	return cs
}

func (cs *commandStream) next(t *testing.T) [2]string {
	t.Helper()
	select {
	case ev, ok := <-cs.events:
		if !ok {
			t.Fatal("command stream closed")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
		return [2]string{}
	}
}

func (cs *commandStream) none(t *testing.T) {
	t.Helper()
	select {
	case ev := <-cs.events:
		t.Errorf("unexpected event %v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func postContext(t *testing.T, ts *httptest.Server, client string, enabled ...string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"client": client, "enabled": enabled})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/-/native/context", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", ts.URL)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

func TestShortcutsEndpoint(t *testing.T) {
	srv := NewServer(t.TempDir())
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/-/shortcuts", nil))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("GET /-/shortcuts = %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	var got struct {
		Shortcuts []shortcuts.Shortcut  `json:"shortcuts"`
		Groups    []shortcuts.GroupList `json:"groups"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Shortcuts) != len(shortcuts.All()) || len(got.Groups) != 4 {
		t.Errorf("got %d shortcuts, %d groups", len(got.Shortcuts), len(got.Groups))
	}
	check := got.Shortcuts[0]
	for _, s := range got.Shortcuts {
		if s.ID == "agent.check" {
			check = s
		}
	}
	if check.ID != "agent.check" || check.Mac != "Cmd+Shift+K" || check.Other != "Ctrl+Shift+K" || check.Scope != shortcuts.ScopeTutorial {
		t.Errorf("agent.check = %+v", check)
	}
}

var shortcutsJSONRe = regexp.MustCompile(`(?s)<script type="application/json" id="latheShortcuts">(.*?)</script>`)

// Every page, in a browser and in the app, inlines the registry and the key
// handler script.
func TestPagesInlineTheShortcutRegistry(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })

	for _, native := range []bool{false, true} {
		if native {
			srv.SetNative(newFakeNative())
		}
		for _, path := range []string{"/", "/series/part-01.md"} {
			page := get(t, ts, path)
			m := shortcutsJSONRe.FindStringSubmatch(page)
			if m == nil {
				t.Fatalf("%s (native=%v): no #latheShortcuts", path, native)
			}
			var got struct {
				Shortcuts []shortcuts.Shortcut `json:"shortcuts"`
			}
			if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &got); err != nil {
				t.Fatalf("%s: inlined registry: %v\n%s", path, err, m[1])
			}
			if len(got.Shortcuts) != len(shortcuts.All()) {
				t.Errorf("%s: %d shortcuts inlined", path, len(got.Shortcuts))
			}
			if strings.Count(page, `id="latheShortcuts"`) != 1 || !strings.Contains(page, "window.latheShortcuts = {") {
				t.Errorf("%s (native=%v): want the registry and shortcuts.js once", path, native)
			}
		}
	}
}

// The embedded script can't end its <script> element early, and it maps
// every built page-run command.
func TestShortcutsScript(t *testing.T) {
	if strings.Contains(strings.ToLower(shortcutsScript), "</script") {
		t.Error("shortcuts.js contains </script")
	}
	for _, s := range shortcuts.All() {
		mapped := strings.Contains(shortcutsScript, "'"+s.ID+"': ")
		want := s.Handler == shortcuts.HandlerPage && !s.Stub
		if mapped != want {
			t.Errorf("%s: mapped in shortcuts.js = %v, want %v (page-run and built)", s.ID, mapped, want)
		}
	}
}

func TestNativeCommandEndpointsNeedAShell(t *testing.T) {
	srv := NewServer(t.TempDir())
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/-/native/commands?path=/", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("commands without a shell = %d", w.Code)
	}
	if w := nativePost(t, srv, "/-/native/context", `{"client":"c1"}`, ""); w.Code != http.StatusNotFound {
		t.Errorf("context without a shell = %d", w.Code)
	}
	if srv.RunCommand("library.home") {
		t.Error("RunCommand delivered with no page")
	}
}

func TestNativeCommandsRefuseForeignPagesAndBadPaths(t *testing.T) {
	srv := NewServer(t.TempDir())
	srv.SetNative(newFakeNative())
	for _, c := range []struct {
		path, origin string
		want         int
	}{
		{"/", "https://evil.example", http.StatusForbidden},
		{"", "", http.StatusBadRequest},
		{"//evil.example/", "", http.StatusBadRequest},
		{"https://evil.example/", "", http.StatusBadRequest},
	} {
		req := httptest.NewRequest(http.MethodGet, "/-/native/commands?path="+url.QueryEscape(c.path), nil)
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != c.want {
			t.Errorf("commands path=%q origin=%q = %d, want %d", c.path, c.origin, w.Code, c.want)
		}
	}
	if w := nativePost(t, srv, "/-/native/context", `{"client":"c1"}`, "https://evil.example"); w.Code != http.StatusForbidden {
		t.Errorf("foreign context = %d", w.Code)
	}
	if w := nativePost(t, srv, "/-/native/context", `{"client":"nope"}`, ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown client = %d", w.Code)
	}
}

func TestMenuCommandsReachTheFocusedPage(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	n := newMenuNative()
	srv.SetNative(n)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })

	part := openCommands(t, ts, "/series/part-01.md")
	// Until it reports, the page can run nothing.
	n.waitState(t, func(st MenuState) bool { return st.Path == "/series/part-01.md" && len(st.Enabled) == 0 })

	// Unknown, stub, shell and native ids are dropped.
	if code := postContext(t, ts, part.client, "reading.next", "agent.check", "nope", "library.new", "app.settings", "window.close"); code != http.StatusNoContent {
		t.Fatalf("context = %d", code)
	}
	st := n.waitState(t, func(st MenuState) bool { return len(st.Enabled) > 0 })
	if st.Scope != shortcuts.ScopeTutorial || len(st.Enabled) != 2 || !st.Enabled["reading.next"] || !st.Enabled["agent.check"] {
		t.Errorf("state = %+v", st)
	}

	if !srv.RunCommand("reading.next") {
		t.Fatal("RunCommand(reading.next) not delivered")
	}
	if ev := part.next(t); ev[0] != "command" || ev[1] != `{"id":"reading.next"}` {
		t.Errorf("event = %v", ev)
	}
	for _, id := range []string{"reading.prev", "library.new", "app.settings", "nope"} {
		if srv.RunCommand(id) {
			t.Errorf("RunCommand(%s) delivered", id)
		}
	}
	part.none(t)

	// Home can't switch on tutorial commands. A newer page takes the focus.
	home := openCommands(t, ts, "/")
	postContext(t, ts, home.client, "reading.next", "library.home")
	st = n.waitState(t, func(st MenuState) bool { return st.Path == "/" && len(st.Enabled) > 0 })
	if st.Scope != shortcuts.ScopeHome || len(st.Enabled) != 1 || !st.Enabled["library.home"] {
		t.Errorf("home state = %+v", st)
	}
	if !srv.RunCommand("library.home") {
		t.Fatal("home not delivered")
	}
	if ev := home.next(t); ev[1] != `{"id":"library.home"}` {
		t.Errorf("home event = %v", ev)
	}
	part.none(t)

	// Focusing the part page again moves the commands back.
	postContext(t, ts, part.client, "reading.next")
	n.waitState(t, func(st MenuState) bool { return st.Path == "/series/part-01.md" })
	if !srv.RunCommand("reading.next") {
		t.Fatal("not delivered after refocus")
	}
	part.next(t)
	home.none(t)

	// When the focused page goes away, the last focused one left takes over.
	part.cancel()
	n.waitState(t, func(st MenuState) bool { return st.Path == "/" && st.Enabled["library.home"] })
	if got := srv.MenuState(); got.Path != "/" {
		t.Errorf("MenuState after close = %+v", got)
	}
	home.cancel()
	n.waitState(t, func(st MenuState) bool { return st.Path == "" && len(st.Enabled) == 0 })
	if srv.RunCommand("library.home") {
		t.Error("delivered with no page left")
	}
}

// A shell without a menu (the plain Native) still gets the streams; nothing
// is pushed to it.
func TestCommandsWithoutAMenuShell(t *testing.T) {
	srv := NewServer(t.TempDir())
	srv.SetNative(newFakeNative())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })
	cs := openCommands(t, ts, "/")
	if code := postContext(t, ts, cs.client, "library.home"); code != http.StatusNoContent {
		t.Fatalf("context = %d", code)
	}
	if !srv.RunCommand("library.home") {
		t.Fatal("not delivered")
	}
	cs.next(t)
}
