package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/config"
	"github.com/devenjarvis/lathe/internal/store"
)

// dropServer is a desktop server on the user's real (temp HOME) library,
// as the app runs it.
func dropServer(t *testing.T) (*Server, *httptest.Server, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := config.TutorialsDir()
	if err != nil {
		t.Fatal(err)
	}
	makeTutFixture(t, dir, "series", true)
	srv := NewServer(dir)
	srv.SetNative(newFakeNative())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); srv.Close() })
	return srv, ts, home
}

// tutorialFolder writes a folder that `lathe store` would take.
func tutorialFolder(t *testing.T, parent, name string, meta *store.Tutorial) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"part-01.md", "part-02.md"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte("# "+p+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if meta != nil {
		if err := store.WriteMetadata(dir, meta); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func dropEvent(t *testing.T, cs *commandStream) dropPlan {
	t.Helper()
	ev := cs.next(t)
	if ev[0] != "drop" {
		t.Fatalf("event = %v, want drop", ev)
	}
	var p dropPlan
	if err := json.Unmarshal([]byte(ev[1]), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDropAFolderOnATutorialSetsItsWorkspace(t *testing.T) {
	srv, ts, home := dropServer(t)
	cs := openCommandsIn(t, ts, "/series/part-01.md", "tutorial:series")
	other := openCommandsIn(t, ts, "/", "main")
	ws := filepath.Join(home, "code", "zig-synth")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}

	// The drop names no client: the window decides which page gets it.
	srv.ReportDrop("tutorial:series", "", []string{ws + "/"})
	p := dropEvent(t, cs)
	other.none(t)
	if p.Kind != dropWorkspace || p.Token == "" || p.Path != ws || p.Display != "~/code/zig-synth" || p.Tutorial != "Test Tutorial" {
		t.Fatalf("plan = %+v", p)
	}

	// Only the drop's own kind and token are accepted.
	if w := nativePost(t, srv, "/-/native/drop/add", `{"token":"`+p.Token+`","title":"x"}`, "wails://localhost"); w.Code != http.StatusGone {
		t.Errorf("workspace token on add = %d, want 410", w.Code)
	}
	if w := nativePost(t, srv, "/-/native/drop/workspace", `{"token":"`+p.Token+`"}`, "https://evil.example"); w.Code != http.StatusForbidden {
		t.Errorf("cross-site = %d", w.Code)
	}
	w := nativePost(t, srv, "/-/native/drop/workspace", `{"token":"`+p.Token+`"}`, "wails://localhost")
	if w.Code != http.StatusOK {
		t.Fatalf("use = %d %s", w.Code, w.Body)
	}
	got, err := store.ReadWorkspace(filepath.Join(srv.tutorialsDir, "series"))
	if err != nil || got.Path != ws {
		t.Errorf("workspace = %+v, %v", got, err)
	}
	if w := nativePost(t, srv, "/-/native/drop/workspace", `{"token":"`+p.Token+`"}`, "wails://localhost"); w.Code != http.StatusGone {
		t.Errorf("token reused = %d, want 410", w.Code)
	}

	// The same folder again: nothing to change.
	srv.ReportDrop("tutorial:series", cs.client, []string{ws})
	if p := dropEvent(t, cs); p.Kind != dropError || !strings.Contains(p.Message, "already") {
		t.Errorf("same folder plan = %+v", p)
	}
}

func TestDropATutorialFolderOnHomeAddsIt(t *testing.T) {
	srv, ts, home := dropServer(t)
	sh, err := store.UpdateShelves(srv.shelvesPath(), func(s *store.Shelves) error {
		_, err := s.Create("Systems")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	systems := sh.Shelves[0].ID
	cs := openCommandsIn(t, ts, "/?shelf="+systems, "main")
	src := tutorialFolder(t, filepath.Join(home, "Downloads"), "lathe-zig-synth", &store.Tutorial{Title: "Build a synth in Zig", Tags: []string{"zig", "audio"}})

	srv.ReportDrop("main", cs.client, []string{src})
	p := dropEvent(t, cs)
	if p.Kind != dropAdd || p.Slug != "zig-synth" || p.Title != "Build a synth in Zig" || p.Parts != 2 ||
		strings.Join(p.Tags, ",") != "zig,audio" || p.Shelf != store.UnsortedShelfID || len(p.Shelves) != 2 || p.Shelves[0].Name != "Systems" {
		t.Fatalf("plan = %+v", p)
	}

	if w := nativePost(t, srv, "/-/native/drop/add", `{"token":"`+p.Token+`","title":"x","shelf":"nope"}`, "wails://localhost"); w.Code != http.StatusBadRequest {
		t.Errorf("unknown shelf = %d", w.Code)
	}
	body, _ := json.Marshal(map[string]string{"token": p.Token, "title": "  My   synth ", "shelf": systems, "tags": "Zig, DSP"})
	w := nativePost(t, srv, "/-/native/drop/add", string(body), "wails://localhost")
	if w.Code != http.StatusOK {
		t.Fatalf("add = %d %s", w.Code, w.Body)
	}
	var res struct{ Slug, URL string }
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Slug != "zig-synth" || res.URL != "/zig-synth/" {
		t.Errorf("add result = %s", w.Body)
	}
	tut, err := store.ReadMetadata(filepath.Join(srv.tutorialsDir, "zig-synth"))
	if err != nil || tut.Title != "My synth" || strings.Join(tut.Tags, ",") != "zig,dsp" || len(tut.Parts) != 2 {
		t.Errorf("stored = %+v, %v", tut, err)
	}
	if got, _ := store.ReadShelves(srv.shelvesPath()); got.ShelfOf("zig-synth").ID != systems {
		t.Errorf("not on the shelf: %+v", got.Placements)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("the original folder should stay: %v", err)
	}

	// Dropped again, the slug is taken.
	srv.ReportDrop("main", cs.client, []string{src})
	if p := dropEvent(t, cs); p.Kind != dropError || !strings.Contains(p.Message, "already has") {
		t.Errorf("second drop = %+v", p)
	}
}

func TestDropRefusals(t *testing.T) {
	srv, ts, home := dropServer(t)
	cs := openCommandsIn(t, ts, "/", "main")
	settings := openCommandsIn(t, ts, "/-/settings", "window:/-/settings")

	file := filepath.Join(home, "notes.txt")
	_ = os.WriteFile(file, []byte("x"), 0o644)
	noParts := filepath.Join(home, "project")
	_ = os.MkdirAll(noParts, 0o755)
	linked := filepath.Join(home, "linked")
	_ = os.MkdirAll(linked, 0o755)
	_ = os.Symlink(file, filepath.Join(linked, "part-01.md"))
	big := tutorialFolder(t, home, "big", nil)
	for i := range maxDropFiles + 1 {
		_ = os.WriteFile(filepath.Join(big, "f"+strconv.Itoa(i)+".txt"), nil, 0o644)
	}
	taken := tutorialFolder(t, home, "series", nil)
	inStore := filepath.Join(srv.tutorialsDir, "series")
	tutorial := tutorialFolder(t, home, "ok", nil)

	for name, tc := range map[string]struct {
		paths []string
		want  string
	}{
		"a file":         {[]string{file}, "is a file"},
		"no part-01":     {[]string{noParts}, "no part-01.md"},
		"linked part":    {[]string{linked}, "no part-01.md"},
		"too big":        {[]string{big}, "too big"},
		"slug taken":     {[]string{taken}, "already has"},
		"inside lathe":   {[]string{inStore}, "inside Lathe"},
		"lathe dir":      {[]string{filepath.Dir(srv.tutorialsDir)}, "inside Lathe"},
		"relative":       {[]string{"ok"}, "can't use"},
		"control char":   {[]string{tutorial + "\n"}, "can't use"},
		"missing":        {[]string{filepath.Join(home, "gone")}, "isn't there"},
		"root":           {[]string{"/"}, "whole disk"},
		"two folders":    {[]string{tutorial, noParts}, "one folder"},
		"too long":       {[]string{"/" + strings.Repeat("a", maxDropPath)}, "can't use"},
		"traversal home": {[]string{tutorial + "/../ok/../../" + filepath.Base(home) + "/.lathe"}, "inside Lathe"},
	} {
		p := srv.ReportDrop("main", cs.client, tc.paths)
		if p.Kind != dropError || !strings.Contains(p.Message, tc.want) || p.Token != "" {
			t.Errorf("%s: plan = %+v, want an error with %q", name, p, tc.want)
			continue
		}
		dropEvent(t, cs)
	}

	// A page that takes no drops says where to drop.
	if p := srv.ReportDrop("window:/-/settings", settings.client, []string{tutorial}); p.Kind != dropError || !strings.Contains(p.Message, "on Home") {
		t.Errorf("settings drop = %+v", p)
	}
	// No pages in that window: nothing happens.
	if p := srv.ReportDrop("tutorial:nope", "", []string{tutorial}); p.Kind != "" {
		t.Errorf("drop on an unknown window = %+v", p)
	}
	// A client from another window doesn't take it.
	if p := srv.ReportDrop("tutorial:nope", cs.client, []string{tutorial}); p.Kind != "" {
		t.Errorf("drop naming another window's client = %+v", p)
	}
}

func TestDropBookExpires(t *testing.T) {
	var b dropBook
	now := time.Now()
	tok := b.put(pendingDrop{kind: dropAdd, created: now})
	if _, ok := b.take(tok, dropAdd, now.Add(dropTTL+1)); ok {
		t.Error("an expired drop was taken")
	}
	for range maxPendingDrops + 4 {
		b.put(pendingDrop{kind: dropAdd, created: now})
	}
	if len(b.m) > maxPendingDrops {
		t.Errorf("%d pending drops, want at most %d", len(b.m), maxPendingDrops)
	}
}
