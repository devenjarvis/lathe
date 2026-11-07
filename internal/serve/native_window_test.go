package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTutorialPagePaths(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	for _, p := range []string{"/series/", "/series/part-01.md", "/series/part-02.md#step-2", "/series/part-01.md#agent", "/series/part-01.md#überblick"} {
		if _, ok := srv.tutorialPage(p); !ok {
			t.Errorf("tutorialPage(%q) refused", p)
		}
	}
	for _, p := range []string{
		"", "/", "series/", "//evil.example/series/", "https://evil.example/series/", "javascript:alert(1)",
		"/series", "/series/part-09.md", "/series/metadata.json", "/nope/", "/../series/", "/series/../series/",
		"/series%2Fpart-01.md", "/series/part-01.md?agent=popout", "/series/part-01.md?", "/series/part-01.md#a b",
		"/series/part-01.md#<script>", "/series/part-01.md#", "/-/settings", "/_static/x", "/series/part-01.md/x",
		"/\\evil.example/", "/series/part-01.md\n", "/series/" + strings.Repeat("a", 2000),
	} {
		if _, ok := srv.tutorialPage(p); ok {
			t.Errorf("tutorialPage(%q) accepted", p)
		}
	}
	if slug, title, ok := srv.TutorialPage("/series/part-02.md"); !ok || slug != "series" || title != "Test Tutorial" {
		t.Errorf("TutorialPage = %q %q %v", slug, title, ok)
	}
}

func TestHomePage(t *testing.T) {
	for _, p := range []string{"/", "/?shelf=systems", "/?view=all&q=zig"} {
		if !HomePage(p) {
			t.Errorf("HomePage(%q) = false", p)
		}
	}
	for _, p := range []string{"", "//x", "/x", "/#top", "/?q=%zz", "https://x/", "/series/"} {
		if HomePage(p) {
			t.Errorf("HomePage(%q) = true", p)
		}
	}
}

func TestNativeWindowOpensATutorial(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	n := newFakeNative()
	srv.SetNative(n)
	defer srv.Close()
	for _, p := range []string{"/series/", "/series/part-02.md#intro"} {
		body, _ := json.Marshal(map[string]string{"path": p})
		if w := nativePost(t, srv, "/-/native/window", string(body), "wails://localhost"); w.Code != http.StatusNoContent {
			t.Fatalf("window %q = %d %s", p, w.Code, w.Body)
		}
	}
	if len(n.windows) != 2 || n.windows[0] != [2]string{"/series/", "Test Tutorial"} || n.windows[1][0] != "/series/part-02.md#intro" {
		t.Errorf("OpenWindow calls = %v", n.windows)
	}
}

func TestWindowViewsAndRestore(t *testing.T) {
	dir := t.TempDir()
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	srv.SetNative(newFakeNative())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })

	srv.RestoreViews(map[string]WindowView{
		"tutorial:series": {Path: "/series/part-02.md", Scroll: 1200},
		"main":            {Path: "/", Scroll: 300},
	})

	// The restored window's first load of that page gets its position back.
	cs := openCommandsIn(t, ts, "/series/part-02.md", "tutorial:series")
	if !strings.Contains(cs.hello, `"scroll":1200`) {
		t.Errorf("hello = %s, want the restored scroll", cs.hello)
	}
	// The main window opened somewhere else: no position.
	main := openCommandsIn(t, ts, "/?shelf=x", "main")
	if strings.Contains(main.hello, "scroll") {
		t.Errorf("main hello = %s, want no scroll for another page", main.hello)
	}
	// A page with no window (a browser) records nothing.
	openCommands(t, ts, "/series/part-01.md")

	post := func(client string, y int) int {
		body, _ := json.Marshal(map[string]any{"client": client, "scroll": y})
		return nativePost(t, srv, "/-/native/view", string(body), "wails://localhost").Code
	}
	if code := post(cs.client, 1500); code != http.StatusNoContent {
		t.Fatalf("view = %d", code)
	}
	if code := post("nope", 10); code != http.StatusNotFound {
		t.Errorf("unknown client = %d", code)
	}
	if code := post(cs.client, -5); code != http.StatusBadRequest {
		t.Errorf("negative scroll = %d", code)
	}
	if w := nativePost(t, srv, "/-/native/view", `{"client":"`+cs.client+`","scroll":1}`, "https://evil.example"); w.Code != http.StatusForbidden {
		t.Errorf("cross-site view = %d", w.Code)
	}

	views := srv.WindowViews()
	if len(views) != 2 || views["tutorial:series"] != (WindowView{Path: "/series/part-02.md", Scroll: 1500}) || views["main"] != (WindowView{Path: "/?shelf=x"}) {
		t.Errorf("WindowViews = %+v", views)
	}

	// A second load of the restored window starts at the top.
	again := openCommandsIn(t, ts, "/series/part-02.md", "tutorial:series")
	if strings.Contains(again.hello, "scroll") {
		t.Errorf("second hello = %s, want no scroll", again.hello)
	}
}

func TestNativeViewNeedsAShell(t *testing.T) {
	srv := NewServer(t.TempDir())
	for _, p := range []string{"/-/native/view", "/-/native/drop/workspace", "/-/native/drop/add"} {
		if w := nativePost(t, srv, p, `{}`, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s without a shell = %d, want 404", p, w.Code)
		}
	}
}
