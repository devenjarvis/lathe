package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/devenjarvis/lathe/internal/acp/acptest"
	"github.com/devenjarvis/lathe/internal/mcp"
	"github.com/devenjarvis/lathe/internal/shortcuts"
	"github.com/devenjarvis/lathe/internal/store"
)

func TestSuggestShelfByMatch(t *testing.T) {
	shelves := &store.Shelves{
		Shelves: []store.Shelf{
			{ID: "audio", Name: "Audio & DSP"},
			{ID: "dist", Name: "Distributed systems", Description: "Consensus, replication and clocks"},
			{ID: "langs", Name: "Languages"},
		},
		Placements: map[string]string{"synth": "audio", "raft": "dist"},
	}
	tuts := []*store.Tutorial{
		{Slug: "synth", Title: "A digital synth in Zig", Tags: []string{"oscillators", "synthesis"}, Tools: []store.Tool{{Name: "zig"}}},
		{Slug: "raft", Title: "A Raft key-value store in Rust", Tags: []string{"consensus"}, Tools: []store.Tool{{Name: "rust"}}},
		{Slug: "loose", Title: "Compilers in Rust"}, // Unsorted: counts for nothing
	}
	for _, c := range []struct{ topic, want string }{
		{"An audio plugin in C++", "audio"},                      // the shelf's own name
		{"Build a wavetable synth in Rust", "audio"},             // a tutorial on it ("synth")
		{"Clocks and replication", "dist"},                       // the description
		{"Multi-Paxos consensus", "dist"},                        // a tag
		{"A step sequencer in Rust", ""},                         // only a language in common
		{"", ""},                                                 // nothing to go on
		{"How to build a tutorial", ""},                          // only stopwords
		{"Languages: an interpreter for synth patches", "langs"}, // name (3) beats one tutorial word (1)
	} {
		if got := suggestShelfByMatch(c.topic, shelves, tuts); got != c.want {
			t.Errorf("suggestShelf(%q) = %q, want %q", c.topic, got, c.want)
		}
	}
	if got := suggestShelfByMatch("audio", &store.Shelves{}, nil); got != "" {
		t.Errorf("no shelves: %q", got)
	}
	// Ties go to the shelf listed first.
	tie := &store.Shelves{Shelves: []store.Shelf{{ID: "b", Name: "Graphics"}, {ID: "a", Name: "Graphics programming"}}}
	if got := suggestShelfByMatch("graphics", tie, nil); got != "b" {
		t.Errorf("tie = %q, want the first shelf", got)
	}
}

func TestParseVersions(t *testing.T) {
	got := parseVersions(" Zig 0.13.0, llvm:18\nnode v20 ;; go ")
	want := []store.Tool{{Name: "zig", Version: "0.13.0"}, {Name: "llvm", Version: "18"}, {Name: "node", Version: "v20"}, {Name: "go"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseVersions = %+v, want %+v", got, want)
	}
	if got := parseVersions(""); len(got) != 0 {
		t.Errorf("empty = %+v", got)
	}
	if s := formatVersions(want); s != "zig 0.13.0, llvm 18, node v20, go" {
		t.Errorf("formatVersions = %q", s)
	}
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetectWorkspace(t *testing.T) {
	if got := detectWorkspace(filepath.Join(t.TempDir(), "missing")); got.Exists || got.Repo != "" || len(got.Tools) != 0 {
		t.Errorf("missing folder = %+v", got)
	}
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		".git/config":    "[core]\n\tbare = false\n[remote \"upstream\"]\n\turl = https://example.com/up.git\n[remote \"origin\"]\n\turl = git@github.com:me/synth.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n",
		".git/HEAD":      "ref: refs/heads/dev\n",
		"go.mod":         "module example.com/x\n\ngo 1.22.3\n",
		"build.zig.zon":  ".{\n    .name = \"x\",\n    .minimum_zig_version = \"0.13.0\",\n}\n",
		"package.json":   `{"engines": {"node": ">=20.11"}}`,
		".tool-versions": "# pinned\ngolang 1.23.0\npython 3.12.1\n",
	})
	got := detectWorkspace(dir)
	want := workspaceInfo{Exists: true, Repo: "git@github.com:me/synth.git", Branch: "dev", Tools: []store.Tool{
		{Name: "go", Version: "1.22.3"}, {Name: "zig", Version: "0.13.0"}, {Name: "node", Version: "20.11"}, {Name: "python", Version: "3.12.1"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("detectWorkspace = %+v\nwant %+v", got, want)
	}

	// A detached HEAD has no branch; rust-toolchain.toml and .nvmrc count.
	dir2 := t.TempDir()
	writeFiles(t, dir2, map[string]string{
		".git/HEAD":           "3f2a9c0d\n",
		"rust-toolchain.toml": "[toolchain]\nchannel = \"1.82.0\"\n",
		".nvmrc":              "v22.1.0\n",
	})
	got = detectWorkspace(dir2)
	if got.Branch != "" || got.Repo != "" || got.Versions() != "rust 1.82.0, node 22.1.0" {
		t.Errorf("detectWorkspace(detached) = %+v (%q)", got, got.Versions())
	}
}

// sheetServer serves a scratch library with two shelves and a tutorial on
// one of them, without an agent.
func sheetServer(t *testing.T) (*Server, *httptest.Server, string) {
	t.Helper()
	srv, ts, tutorials := newTutServer(t, &fakeHost{}, nil)
	dir := filepath.Join(tutorials, "digital-synth-zig")
	writeFiles(t, dir, map[string]string{"part-01.md": "# Part 1\n"})
	if err := store.WriteMetadata(dir, &store.Tutorial{Slug: "digital-synth-zig", Title: "Build a digital synth in Zig", Tags: []string{"synth", "audio"}, Tools: []store.Tool{{Name: "zig", Version: "0.13.0"}}, Parts: []string{"part-01.md"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateShelves(srv.shelvesPath(), func(s *store.Shelves) error {
		if _, err := s.Create("Audio & DSP"); err != nil {
			return err
		}
		if _, err := s.Create("Compilers"); err != nil {
			return err
		}
		return s.Assign("digital-synth-zig", "audio-dsp")
	}); err != nil {
		t.Fatal(err)
	}
	return srv, ts, tutorials
}

func TestNewSheetPage(t *testing.T) {
	_, ts, _ := sheetServer(t)

	page := get(t, ts, "/-/new?topic="+url.QueryEscape("A granular synth in Zig"))
	for _, want := range []string{
		"<title>New tutorial — Lathe</title>",
		`<form class="new-sheet-form" id="newSheetForm" method="POST" action="/-/new" data-mode="handoff" data-auto-workspace`,
		`>A granular synth in Zig</textarea>`,
		// The suggested shelf, picked from what's on it.
		`<option value="audio-dsp" selected>Audio &amp; DSP</option>`,
		"Suggested: it matches what's on Audio &amp; DSP.",
		// The starting level, unset so the agent asks.
		`<input type="radio" name="level" value="some familiarity">`,
		// Voices: the default first, checked and marked, with a sample.
		`<input type="radio" name="voice" value="plainspoken" checked>`,
		`<span class="new-voice-default">Default</span>`,
		`<a class="new-manage" href="/-/settings#voices" data-settings-link>Manage voices…</a>`,
		// <code root>/<slug>, followed by the server while unedited.
		`value="~/code/granular-synth-zig"`,
		`<input type="hidden" name="workspaceAuto" value="~/code/granular-synth-zig">`,
		`<details class="new-more" id="newMore">`,
		// No agent: the footer says what Start writing does instead.
		"No agent is set up, so you get a <code>/lathe</code> command",
		`>Get the command<kbd`,
		// Scripts off: Cancel is a link, the chips are links.
		`<a class="btn btn-ghost" href="/">Cancel</a>`,
		`<a class="new-idea" href="/-/new?topic=Write%20a%20tiny%20Lisp%20interpreter%20in%20Go"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("/-/new lacks %q", want)
		}
	}
	if strings.Contains(page, `id="newPickFolder"`) {
		t.Error("plain lathe serve offers Choose folder…")
	}
	if strings.Index(page, `value="plainspoken"`) > strings.Index(page, `value="companion"`) {
		t.Error("the default voice is not first")
	}
	// A chip whose tutorial exists is not suggested.
	if strings.Contains(page, `data-idea="Build a digital synth in Zig"`) {
		t.Error("Or try suggests a tutorial already in the library")
	}

	// The fragment the sheet loads is the bare form.
	frag := get(t, ts, "/-/new?partial=1")
	if strings.Contains(frag, "<html") || !strings.HasPrefix(strings.TrimSpace(frag), `<form class="new-sheet-form"`) {
		t.Errorf("partial is not the bare form:\n%.300s", frag)
	}
	if !strings.Contains(frag, `data-new-close`) || !strings.Contains(frag, `value="~/code/tutorial"`) {
		t.Error("partial lacks the close button or the default workspace")
	}
}

func TestNewSheetFooterNamesTheAgentAndOffersThePicker(t *testing.T) {
	srv, ts, _ := newTutServer(t, &fakeHost{}, func() *acptest.Agent { return &acptest.Agent{} })
	srv.SetNative(newFakeNative())
	page := get(t, ts, "/-/new?partial=1")
	for _, want := range []string{`data-mode="agent"`, "<strong>fake-acp</strong> writes Part 1", "It may ask a question or two", `>Start writing<kbd`, `id="newPickFolder" data-pick-url="/-/native/pick-folder"`} {
		if !strings.Contains(page, want) {
			t.Errorf("sheet with an agent and a shell lacks %q", want)
		}
	}
}

func TestEveryPageOpensTheSheet(t *testing.T) {
	_, ts, _ := sheetServer(t)
	for _, path := range []string{"/", "/digital-synth-zig/part-01.md", "/-/settings", "/-/new"} {
		page := get(t, ts, path)
		if !strings.Contains(page, `<dialog class="new-sheet" id="newSheet"`) || !strings.Contains(page, "window.latheNewSheet") {
			t.Errorf("%s lacks the New tutorial sheet", path)
		}
	}
	home := get(t, ts, "/")
	if !strings.Contains(home, `<a class="new-open" href="/-/new" data-new-open`) {
		t.Error("Home's title bar lacks the New tutorial button")
	}
	if !strings.Contains(get(t, ts, "/digital-synth-zig/part-01.md"), `<a class="new-open" href="/-/new" data-new-open`) {
		t.Error("the reading page's title bar lacks the New tutorial button")
	}
	// Settings › Shortcuts lists it now that it is built.
	if !strings.Contains(get(t, ts, "/-/settings"), "<dt>New tutorial…</dt>") {
		t.Error("Settings › Shortcuts does not list New tutorial")
	}
	sc, ok := shortcuts.ByID("library.new")
	if !ok || sc.Stub || sc.Handler != shortcuts.HandlerPage || sc.Href != "/-/new" || sc.Scope != shortcuts.ScopeGlobal {
		t.Errorf("library.new = %+v, want a built global page command going to /-/new", sc)
	}
}

func TestNewSuggest(t *testing.T) {
	_, ts, _ := sheetServer(t)
	home, _ := os.UserHomeDir()
	ws := filepath.Join(home, "code", "step-sequencer-zig")
	writeFiles(t, ws, map[string]string{".git/config": "[remote \"origin\"]\n\turl = git@github.com:me/seq.git\n", ".git/HEAD": "ref: refs/heads/main\n", "build.zig.zon": `.minimum_zig_version = "0.14.0"`})

	var out struct {
		Slug, Workspace, Shelf, ShelfName, Versions string
		Detected                                    workspaceInfo
	}
	read := func(q string) {
		t.Helper()
		out.Detected = workspaceInfo{}
		if err := json.Unmarshal([]byte(get(t, ts, "/-/new/suggest?"+q)), &out); err != nil {
			t.Fatal(err)
		}
	}
	read("topic=" + url.QueryEscape("A step sequencer synth in Zig"))
	if out.Slug != "step-sequencer-synth-zig" || out.Workspace != "~/code/step-sequencer-synth-zig" || out.Shelf != "audio-dsp" || out.ShelfName != "Audio & DSP" || out.Detected.Exists {
		t.Errorf("suggest = %+v", out)
	}
	read("topic=" + url.QueryEscape("A step sequencer in Zig"))
	if out.Shelf != "" || !out.Detected.Exists || out.Detected.Repo != "git@github.com:me/seq.git" || out.Detected.Branch != "main" || out.Versions != "zig 0.14.0" {
		t.Errorf("suggest for an existing folder = %+v", out)
	}
	// An explicit folder is looked in instead, but never Lathe's own store.
	read("topic=x&workspace=" + url.QueryEscape("~/.lathe/tutorials/digital-synth-zig"))
	if out.Detected.Exists {
		t.Error("suggest looked inside the tutorial store")
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/-/new/suggest?topic=x", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin suggest = %d, want 403", resp.StatusCode)
	}
}

func postForm(t *testing.T, ts *httptest.Server, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.PostForm(ts.URL+path, form)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestNewFormPostWithoutScripts(t *testing.T) {
	_, ts, _ := sheetServer(t)
	home, _ := os.UserHomeDir()

	// An unedited workspace follows the topic; versions are pinned.
	resp, page := postForm(t, ts, "/-/new", url.Values{
		"topic": {"A tiny Forth in C"}, "level": {"beginner"}, "voice": {"companion"},
		"workspace": {"~/code/tutorial"}, "workspaceAuto": {"~/code/tutorial"},
		"versions": {"gcc 14.1, make 4.4"}, "shelf": {"compilers"},
	})
	want := "/lathe A tiny Forth in C --level beginner --voice companion --workspace " + filepath.Join(home, "code", "tiny-forth-c") + " --tool gcc:14.1 --tool make:4.4"
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, want) {
		t.Errorf("handoff page = %d, want the command %q in:\n%.2000s", resp.StatusCode, want, page)
	}
	// The answers survive the round trip.
	for _, keep := range []string{`>A tiny Forth in C</textarea>`, `value="beginner" checked`, `value="companion" checked`, `<option value="compilers" selected>`, `value="gcc 14.1, make 4.4"`} {
		if !strings.Contains(page, keep) {
			t.Errorf("handoff page lost %q", keep)
		}
	}

	// A typed workspace is kept as typed.
	_, page = postForm(t, ts, "/-/new", url.Values{"topic": {"A tiny Forth in C"}, "workspace": {"~/src/forth"}, "workspaceAuto": {"~/code/tutorial"}})
	if !strings.Contains(page, "--workspace "+filepath.Join(home, "src", "forth")) {
		t.Error("a typed workspace was replaced")
	}

	// A bad request comes back as the page with the error.
	resp, page = postForm(t, ts, "/-/new", url.Values{"topic": {"  "}, "level": {"beginner"}})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(page, `role="alert">topic is required</p>`) || !strings.Contains(page, `value="beginner" checked`) {
		t.Errorf("bad form = %d:\n%.1500s", resp.StatusCode, page)
	}
	resp, _ = postForm(t, ts, "/-/new", url.Values{"topic": {"x"}, "shelf": {"nope"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown shelf = %d", resp.StatusCode)
	}
}

func TestNewVersionsFieldPinsTools(t *testing.T) {
	srv, _, _ := sheetServer(t)
	in, err := srv.validateNew(newRequest{Topic: "t", Versions: "zig 0.13.0, llvm 18"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(in.command(), "--tool zig:0.13.0 --tool llvm:18") {
		t.Errorf("command = %q", in.command())
	}
	// Explicit tools win over the text.
	in, _ = srv.validateNew(newRequest{Topic: "t", Versions: "zig 0.13.0", Tools: []store.Tool{{Name: "go", Version: "1.23"}}})
	if len(in.Tools) != 1 || in.Tools[0].Name != "go" {
		t.Errorf("tools = %+v", in.Tools)
	}
	if _, err := srv.validateNew(newRequest{Topic: "t", Versions: "bad\"name 1"}); err == nil {
		t.Error("a quoted version was accepted")
	}
}

// A scripts-off submit with a hosted agent goes to the tutorial's page, and
// the run gets the sheet's answers; the shelf is applied once it's stored.
func TestNewFormPostStartsTheHostedRun(t *testing.T) {
	var (
		mu     sync.Mutex
		prompt string
	)
	srv, ts, tutorials := newTutServer(t, &fakeHost{}, func() *acptest.Agent {
		return &acptest.Agent{Reply: func(p string) string {
			slug, src := writePart1(t, p)
			if text, isErr := mcpCall(t, &mcp.Server{Slug: slug, New: true}, "lathe_store", map[string]any{"path": src, "title": "Forth"}); isErr {
				t.Errorf("store: %s", text)
			}
			mu.Lock()
			prompt = p
			mu.Unlock()
			return "Stored."
		}}
	})
	sh, err := store.UpdateShelves(srv.shelvesPath(), func(s *store.Shelves) error { _, err := s.Create("Languages"); return err })
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	resp, _ := postForm(t, ts, "/-/new", url.Values{
		"topic": {"A tiny Forth in C"}, "level": {"some familiarity"}, "voice": {"plainspoken"},
		"workspace": {"~/code/tutorial"}, "workspaceAuto": {"~/code/tutorial"},
		"versions": {"gcc 14.1"}, "shelf": {sh.Shelves[0].ID},
	})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/tiny-forth-c/" {
		t.Fatalf("form start = %d → %q, want 303 to /tiny-forth-c/", resp.StatusCode, resp.Header.Get("Location"))
	}
	waitFor(t, "the tutorial on its shelf", func() bool {
		s, err := store.ReadShelves(srv.shelvesPath())
		return err == nil && s.ShelfOf("tiny-forth-c").ID == sh.Shelves[0].ID
	})
	mu.Lock()
	p := prompt
	mu.Unlock()
	for _, want := range []string{"- Experience level: some familiarity", "- Versions: gcc 14.1 — pinned", "- Workspace: " + filepath.Join(home, "code", "tiny-forth-c")} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if _, err := os.Stat(filepath.Join(tutorials, "tiny-forth-c", "part-01.md")); err != nil {
		t.Errorf("not stored: %v", err)
	}
}

func TestNativePickFolderForANewWorkspace(t *testing.T) {
	srv, ts, _ := sheetServer(t)
	n := newFakeNative()
	n.pick = "/somewhere/else"
	srv.SetNative(n)
	home, _ := os.UserHomeDir()
	code, body := postJSONBody(t, ts.URL+"/-/native/pick-folder", map[string]string{"for": "new-workspace", "path": "~/code/not-yet/deeper"})
	if code != http.StatusOK || !strings.Contains(body, `"path":"/somewhere/else"`) {
		t.Fatalf("pick = %d %s", code, body)
	}
	// It starts at the nearest folder that exists.
	if err := os.MkdirAll(filepath.Join(home, "code"), 0o755); err != nil {
		t.Fatal(err)
	}
	postJSONBody(t, ts.URL+"/-/native/pick-folder", map[string]string{"for": "new-workspace", "path": "~/code/not-yet"})
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.picks) != 2 || n.picks[0][1] != home || n.picks[1][1] != filepath.Join(home, "code") {
		t.Errorf("picker starts = %+v", n.picks)
	}
}
