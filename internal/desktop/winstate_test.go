package desktop

import (
	"fmt"
	"testing"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/serve"
)

// testPages knows two tutorials, "zig" and "go".
var testPages = pageChecker{
	tutorial: func(p string) (string, bool) {
		switch p {
		case "/zig/", "/zig/part-01.md", "/zig/part-02.md":
			return "zig", true
		case "/go/part-01.md":
			return "go", true
		}
		return "", false
	},
	home: func(p string) bool { return p == "/" || p == "/?shelf=systems" },
}

func TestPlanRestore(t *testing.T) {
	got := planRestore([]config.WindowState{
		{Path: "/zig/part-02.md", Scroll: 900, X: 100, Y: 80, Width: 900, Height: 700},
		{Main: true, Path: "/?shelf=systems", Scroll: 40, X: 0, Y: 25, Width: 1280, Height: 860},
		{Main: true, Path: "/go/part-01.md"},               // a second main: ignored
		{Path: "/zig/part-01.md"},                          // the same tutorial twice: ignored
		{Path: "/gone/part-01.md"},                         // deleted since
		{Path: "/-/settings"},                              // not a tutorial
		{Path: "https://evil.example/"},                    // never
		{Path: "/go/part-01.md", Scroll: -3, Width: 99999}, // bad numbers dropped
	}, testPages)
	want := []restoreWindow{
		{name: "main", main: true, path: "/?shelf=systems", scroll: 40, bounds: rect{0, 25, 1280, 860}},
		{name: "tutorial:zig", path: "/zig/part-02.md", scroll: 900, bounds: rect{100, 80, 900, 700}},
		{name: "tutorial:go", path: "/go/part-01.md"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("planRestore =\n %+v\nwant\n %+v", got, want)
	}
}

func TestPlanRestoreDefaultsAndCaps(t *testing.T) {
	if got := planRestore(nil, testPages); len(got) != 1 || got[0].path != "/" || !got[0].main {
		t.Errorf("no saved windows = %+v, want the library", got)
	}
	// A main window on a page that is gone opens Home, without its scroll.
	if got := planRestore([]config.WindowState{{Main: true, Path: "/gone/x.md", Scroll: 500}}, testPages); got[0].path != "/" || got[0].scroll != 0 {
		t.Errorf("gone main = %+v", got)
	}
	many := []config.WindowState{}
	pages := pageChecker{tutorial: func(p string) (string, bool) { return p, true }, home: testPages.home}
	for i := range maxRestoreWindows + 5 {
		many = append(many, config.WindowState{Path: fmt.Sprintf("/t%d/", i)})
	}
	if got := planRestore(many, pages); len(got) != maxRestoreWindows+1 {
		t.Errorf("%d windows restored, want %d", len(got), maxRestoreWindows+1)
	}
}

func TestOnScreen(t *testing.T) {
	screens := []rect{{0, 0, 1440, 900}, {1440, 0, 1920, 1080}}
	for _, tc := range []struct {
		b    rect
		want bool
	}{
		{rect{100, 100, 800, 600}, true},
		{rect{1500, 50, 800, 600}, true},           // the second screen
		{rect{-300, 100, 800, 600}, true},          // partly off the left, title bar's middle on
		{rect{-2000, 100, 800, 600}, false},        // a monitor that is gone
		{rect{100, 2000, 800, 600}, false},         // below every screen
		{rect{}, false},                            // no saved bounds
		{rect{3300, 100, 800, 600}, false},         // right of everything
		{rect{100, -10, 800, 600}, true},           // title bar just on
		{rect{100, -40, 800, 600}, false},          // title bar above the screen
		{rect{1400, 0, 40, 40}, true},              // tiny but on
		{rect{0, 0, 1440, 900}, true},              // exactly the screen
		{rect{maxWindowCoord, 0, 800, 600}, false}, // far away
	} {
		if got := onScreen(tc.b, screens); got != tc.want {
			t.Errorf("onScreen(%+v) = %v, want %v", tc.b, got, tc.want)
		}
	}
}

func TestSnapshotWindows(t *testing.T) {
	open := []openWindow{
		{name: "tutorial:zig", path: "/zig/", bounds: rect{10, 20, 900, 700}},
		{name: "main", main: true, path: "/", bounds: rect{0, 0, 1280, 860}},
		{name: "tutorial:go", path: "/go/part-01.md"},
	}
	views := map[string]serve.WindowView{
		"main":         {Path: "/?shelf=systems", Scroll: 12},
		"tutorial:zig": {Path: "/zig/part-02.md", Scroll: 640},
		"window:/x":    {Path: "/x/part-01.md?agent=popout"},
	}
	got := snapshotWindows(open, views)
	want := []config.WindowState{
		{Main: true, Path: "/?shelf=systems", Scroll: 12, Width: 1280, Height: 860},
		{Path: "/zig/part-02.md", Scroll: 640, X: 10, Y: 20, Width: 900, Height: 700},
		{Path: "/go/part-01.md"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("snapshot =\n %+v\nwant\n %+v", got, want)
	}
	// And it round-trips through planRestore.
	if back := planRestore(got, testPages); len(back) != 3 || back[1].scroll != 640 || back[0].path != "/?shelf=systems" {
		t.Errorf("round trip = %+v", back)
	}
}

func TestTutorialPathSlug(t *testing.T) {
	for p, want := range map[string]string{"/zig/": "zig", "/zig/part-01.md": "zig", "/zig/part-01.md#agent": "zig"} {
		if got, ok := tutorialPathSlug(p); !ok || got != want {
			t.Errorf("tutorialPathSlug(%q) = %q %v", p, got, ok)
		}
	}
	for _, p := range []string{"", "/", "/zig", "/zig/part-01.md?agent=popout", "/-/settings", "/_static/x", "zig/", "/../x/"} {
		if got, ok := tutorialPathSlug(p); ok {
			t.Errorf("tutorialPathSlug(%q) = %q, want refused", p, got)
		}
	}
}
