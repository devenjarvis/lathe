package serve_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/devenjarvis/lathe/internal/store"
)

type libTut struct {
	slug     string
	status   store.Status
	parts    int
	tools    []store.Tool
	age      time.Duration
	progress *store.Progress
	shelf    string
}

// writeLibrary stores tutorials in dir (a tutorials dir) and files them on the
// named shelves, created in the order given.
func writeLibrary(t *testing.T, dir string, shelves []string, tuts []libTut) {
	t.Helper()
	now := time.Now()
	for _, lt := range tuts {
		tutDir := filepath.Join(dir, lt.slug)
		if err := os.MkdirAll(tutDir, 0o755); err != nil {
			t.Fatal(err)
		}
		n := lt.parts
		if n == 0 {
			n = 1
		}
		tut := &store.Tutorial{Slug: lt.slug, Title: "Title " + lt.slug, Status: lt.status, Created: now.Add(-lt.age), Tools: lt.tools}
		if tut.Status == "" {
			tut.Status = store.StatusUnverified
		}
		for i := 1; i <= n; i++ {
			p := fmt.Sprintf("part-%02d.md", i)
			tut.Parts = append(tut.Parts, p)
			if err := os.WriteFile(filepath.Join(tutDir, p), []byte("# Part\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.WriteMetadata(tutDir, tut); err != nil {
			t.Fatal(err)
		}
		if lt.progress != nil {
			if err := store.WriteProgress(tutDir, lt.progress); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := store.UpdateShelves(store.ShelvesPath(dir), func(s *store.Shelves) error {
		ids := map[string]string{}
		for _, name := range shelves {
			sh, err := s.Create(name)
			if err != nil {
				return err
			}
			ids[name] = sh.ID
		}
		for _, lt := range tuts {
			if lt.shelf != "" {
				if err := s.Assign(lt.slug, ids[lt.shelf]); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func getList(t *testing.T, srv *serve.Server, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

// section returns the markup from the first occurrence of start up to the next
// "</section>".
func section(t *testing.T, body, start string) string {
	t.Helper()
	i := strings.Index(body, start)
	if i < 0 {
		t.Fatalf("missing %q in body", start)
	}
	j := strings.Index(body[i:], "</section>")
	if j < 0 {
		t.Fatalf("%q not closed", start)
	}
	return body[i : i+j]
}

func sampleLibrary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	now := time.Now()
	writeLibrary(t, dir, []string{"Compilers & languages", "Systems", "Graphics"}, []libTut{
		{slug: "c1", age: 1 * time.Hour, shelf: "Compilers & languages", tools: []store.Tool{{Name: "Go", Version: "1.23"}}},
		{slug: "c2", age: 2 * time.Hour, shelf: "Compilers & languages", status: store.StatusFailed},
		{slug: "c3", age: 3 * time.Hour, shelf: "Compilers & languages", tools: []store.Tool{{Name: "go"}}},
		{slug: "c4", age: 4 * time.Hour, shelf: "Compilers & languages", tools: []store.Tool{{Name: "zig", Version: "0.13.0"}}},
		{slug: "c5", age: 5 * time.Hour, shelf: "Compilers & languages"},
		{slug: "c6", age: 6 * time.Hour, shelf: "Compilers & languages"},
		{slug: "kernel", age: 7 * time.Hour, shelf: "Systems", status: store.StatusExtending, parts: 2},
		{slug: "kv", age: 8 * time.Hour, shelf: "Systems", parts: 2,
			progress: &store.Progress{Part: "part-01.md", Ratio: 0.5, UpdatedAt: now.Add(-3 * time.Hour)}},
		{slug: "done", age: 9 * time.Hour, shelf: "Systems", parts: 2,
			progress: &store.Progress{Part: "part-02.md", Ratio: 1, UpdatedAt: now}},
		{slug: "loose", age: 10 * time.Hour, parts: 3,
			progress: &store.Progress{Part: "part-02.md", Ratio: 0.1, UpdatedAt: now.Add(-time.Hour)}},
	})
	return dir
}

func TestHomeShowsPickUpThenShelfRows(t *testing.T) {
	dir := sampleLibrary(t)
	code, body := getList(t, serve.NewServer(dir), "/")
	if code != http.StatusOK {
		t.Fatalf("GET / = %d", code)
	}

	pick := section(t, body, `aria-labelledby="pickUpHeading"`)
	if !strings.Contains(pick, "Pick up where you left off") {
		t.Error("Home missing the Pick up heading")
	}
	// New tutorial cover first, then being written, then in progress by last
	// read (loose read 1h ago before kv 3h ago); finished and unstarted
	// tutorials stay off the row.
	order := []string{`class="new-cover"`, `data-slug="kernel"`, `data-slug="loose"`, `data-slug="kv"`}
	last := -1
	for _, m := range order {
		i := strings.Index(pick, m)
		if i < 0 || i < last {
			t.Fatalf("Pick up row order wrong at %s (pos %d after %d):\n%s", m, i, last, pick)
		}
		last = i
	}
	for _, slug := range []string{"done", "c1"} {
		if strings.Contains(pick, `data-slug="`+slug+`"`) {
			t.Errorf("Pick up row should not show %s", slug)
		}
	}
	if !strings.Contains(pick, `<a class="new-cover" href="/-/new" data-new-open>`) {
		t.Error("New tutorial cover is not a link to the New tutorial sheet")
	}

	// Shelf rows in stored order, Unsorted last.
	iComp := strings.Index(body, `id="shelf-compilers-languages"`)
	iSys := strings.Index(body, `id="shelf-systems"`)
	iGfx := strings.Index(body, `id="shelf-graphics"`)
	iUns := strings.Index(body, `id="shelf-unsorted"`)
	if iComp < 0 || iSys < iComp || iGfx < iSys || iUns < iGfx {
		t.Errorf("shelf rows out of order: compilers=%d systems=%d graphics=%d unsorted=%d", iComp, iSys, iGfx, iUns)
	}

	comp := section(t, body, `aria-labelledby="shelf-compilers-languages"`)
	if got := strings.Count(comp, `<div class="tutorial"`); got != 4 {
		t.Errorf("Compilers row shows %d covers, want 4", got)
	}
	if strings.Contains(comp, `data-slug="c5"`) || !strings.Contains(comp, `data-slug="c1"`) {
		t.Error("Compilers row should show the four newest")
	}
	if !strings.Contains(comp, `<span class="more-count">+2</span> more`) {
		t.Error("Compilers row missing the +2 more tile")
	}
	if !strings.Contains(comp, `href="/?shelf=compilers-languages"`) || !strings.Contains(comp, "See all") {
		t.Error("Compilers row missing See all link")
	}
	sys := section(t, body, `aria-labelledby="shelf-systems"`)
	if strings.Contains(sys, "more-tile") {
		t.Error("a shelf with 3 tutorials should have no more tile")
	}
	gfx := section(t, body, `aria-labelledby="shelf-graphics"`)
	if !strings.Contains(gfx, "Nothing on this shelf yet.") {
		t.Error("empty shelf row missing its empty note")
	}
	uns := section(t, body, `aria-labelledby="shelf-unsorted"`)
	if !strings.Contains(uns, `data-slug="loose"`) || !strings.Contains(uns, `data-shelf="unsorted"`) {
		t.Error("unshelved tutorial should be on Unsorted")
	}
	// Home has no grid, so no client search controls.
	if strings.Contains(body, `id="tutorialList"`) {
		t.Error("Home should not render the All grid")
	}
	if !strings.Contains(body, `name="q"`) || !strings.Contains(body, `value="all"`) {
		t.Error("Home missing its search form into All tutorials")
	}
}

func TestLibrarySidebar(t *testing.T) {
	dir := sampleLibrary(t)
	_, body := getList(t, serve.NewServer(dir), "/")
	nav := body[strings.Index(body, `<nav class="library-nav"`):strings.Index(body, `</nav>`)]

	for _, want := range []string{
		`<a class="nav-link" href="/" aria-current="page"><span class="nav-label">Home</span></a>`,
		`href="/?view=all"><span class="nav-label">All tutorials</span><span class="nav-count">10</span>`,
		`href="/?view=writing"><span class="nav-label">Being written</span><span class="nav-count">1</span>`,
		`href="/?shelf=compilers-languages"><span class="nav-label">Compilers &amp; languages</span><span class="nav-count">6</span>`,
		`href="/?shelf=systems"><span class="nav-label">Systems</span><span class="nav-count">3</span>`,
		`href="/?shelf=graphics"><span class="nav-label">Graphics</span></a>`,
		`href="/?shelf=unsorted"><span class="nav-label">Unsorted</span><span class="nav-count">1</span>`,
		// Languages merge case ("Go" + "go") and sort by count.
		`href="/?tool=go"><span class="nav-label">Go</span><span class="nav-count">2</span>`,
		`href="/?tool=zig"><span class="nav-label">zig</span><span class="nav-count">1</span>`,
		`href="/?view=attention"><span class="nav-label">Needs a look</span><span class="nav-count">1</span>`,
		`<form class="new-shelf-form" method="POST" action="/-/shelves">`,
		// Open by default so it works with scripts off.
		`<details class="library-nav-panel" id="libraryNav" open>`,
	} {
		if !strings.Contains(nav, want) {
			t.Errorf("sidebar missing %s", want)
		}
	}
	if strings.Index(nav, `?tool=go"`) > strings.Index(nav, `?tool=zig"`) {
		t.Error("languages should sort by count")
	}
}

func TestLibraryGridViews(t *testing.T) {
	dir := sampleLibrary(t)
	srv := serve.NewServer(dir)
	cases := []struct {
		path, heading string
		want, not     []string
	}{
		{"/?view=all", "All tutorials", []string{"c1", "kernel", "loose", "done"}, nil},
		{"/?shelf=systems", "Systems", []string{"kernel", "kv", "done"}, []string{"c1", "loose"}},
		{"/?shelf=unsorted", "Unsorted", []string{"loose"}, []string{"c1"}},
		{"/?tool=GO", "Go", []string{"c1", "c3"}, []string{"c4", "kernel"}},
		{"/?view=writing", "Being written", []string{"kernel"}, []string{"c1"}},
		{"/?view=attention", "Needs a look", []string{"c2"}, []string{"c1"}},
	}
	for _, c := range cases {
		code, body := getList(t, srv, c.path)
		if code != http.StatusOK {
			t.Errorf("GET %s = %d", c.path, code)
			continue
		}
		if !strings.Contains(body, "<h2>"+c.heading+" ") {
			t.Errorf("GET %s heading missing %q", c.path, c.heading)
		}
		if !strings.Contains(body, `id="searchInput"`) || !strings.Contains(body, `id="tutorialList"`) {
			t.Errorf("GET %s missing the searchable grid", c.path)
		}
		grid := body[strings.Index(body, `id="tutorialList"`):]
		for _, s := range c.want {
			if !strings.Contains(grid, `data-slug="`+s+`"`) {
				t.Errorf("GET %s missing %s", c.path, s)
			}
		}
		for _, s := range c.not {
			if strings.Contains(grid, `data-slug="`+s+`"`) {
				t.Errorf("GET %s should not list %s", c.path, s)
			}
		}
	}

	if code, _ := getList(t, srv, "/?shelf=nope"); code != http.StatusNotFound {
		t.Errorf("unknown shelf = %d, want 404", code)
	}
	_, body := getList(t, srv, "/?shelf=graphics")
	if !strings.Contains(body, "Nothing on this shelf yet.") || strings.Contains(body, `id="tutorialList"`) {
		t.Error("empty shelf view should show its note, not a grid")
	}
	_, body = getList(t, srv, "/?view=writing")
	if !strings.Contains(body, `aria-current="page"><span class="nav-label">Being written`) {
		t.Error("Being written view should mark its sidebar link current")
	}
}

// A ?q= search marks non-matching covers hidden on the server, so a search
// submitted with scripts off still narrows the grid.
func TestLibrarySearchWorksWithoutScripts(t *testing.T) {
	dir := sampleLibrary(t)
	srv := serve.NewServer(dir)
	_, body := getList(t, srv, "/?view=all&q=zig")
	if !strings.Contains(body, `value="zig"`) {
		t.Error("search box should keep the query")
	}
	if !strings.Contains(body, `<div class="tutorial" data-title="Title c4"`) {
		t.Error("matching cover should stay visible")
	}
	if !strings.Contains(body, `<div class="tutorial hidden" data-title="Title c1"`) {
		t.Error("non-matching cover should be hidden")
	}
	if !strings.Contains(body, `id="noResults" aria-live="polite" hidden`) {
		t.Error("no-results note should stay hidden when something matches")
	}
	// The shelf name is searchable too.
	_, body = getList(t, srv, "/?q=systems")
	if !strings.Contains(body, `<div class="tutorial" data-title="Title kv"`) {
		t.Error("search should match the shelf name (and ?q= alone opens All)")
	}
	_, body = getList(t, srv, "/?view=all&q=zzzz")
	if strings.Contains(body, `id="noResults" aria-live="polite" hidden`) {
		t.Error("no-results note should show when nothing matches")
	}
}

func TestLibraryEmpty(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)
	_, body := getList(t, srv, "/")
	if !strings.Contains(body, "Start your library") || !strings.Contains(body, `class="new-cover"`) {
		t.Error("empty Home should offer the New tutorial cover")
	}
	if strings.Contains(body, `id="shelf-unsorted"`) {
		t.Error("empty library should show no Unsorted row")
	}
	_, body = getList(t, srv, "/?view=all")
	if !strings.Contains(body, "No tutorials yet") {
		t.Error("empty All view missing the empty state")
	}
}

func TestListJSONCarriesShelf(t *testing.T) {
	dir := sampleLibrary(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	serve.NewServer(dir).Handler().ServeHTTP(w, req)
	var list []struct {
		Slug       string          `json:"slug"`
		Status     string          `json:"status"`
		Shelf      string          `json:"shelf"`
		ShelfName  string          `json:"shelfName"`
		InProgress bool            `json:"inProgress"`
		Progress   json.RawMessage `json:"progress"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	got := map[string]string{}
	for _, it := range list {
		got[it.Slug] = fmt.Sprintf("%s/%s/%v", it.Shelf, it.ShelfName, it.InProgress)
	}
	for slug, want := range map[string]string{
		"kv":    "systems/Systems/true",
		"done":  "systems/Systems/false",
		"loose": "unsorted/Unsorted/true",
		"c1":    "compilers-languages/Compilers & languages/false",
	} {
		if got[slug] != want {
			t.Errorf("%s = %s, want %s", slug, got[slug], want)
		}
	}
}

func TestListToleratesCorruptShelves(t *testing.T) {
	dir := t.TempDir()
	makeTestTutorial(t, dir, "t1", true)
	if err := os.WriteFile(store.ShelvesPath(dir), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, body := getList(t, serve.NewServer(dir), "/")
	if code != http.StatusOK || !strings.Contains(body, `id="shelf-unsorted"`) {
		t.Errorf("corrupt shelves.json should fall back to Unsorted, got %d", code)
	}
}

func postShelf(t *testing.T, srv *serve.Server, contentType, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/-/shelves", strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestShelfCreateFormRedirects(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)
	w := postShelf(t, srv, "application/x-www-form-urlencoded", "name=Audio+%26+DSP", nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/?shelf=audio-dsp" {
		t.Fatalf("form create = %d %q", w.Code, w.Header().Get("Location"))
	}
	s, err := store.ReadShelves(store.ShelvesPath(dir))
	if err != nil || len(s.Shelves) != 1 || s.Shelves[0].Name != "Audio & DSP" {
		t.Fatalf("shelves after create = %+v, %v", s, err)
	}
	_, body := getList(t, srv, "/")
	if !strings.Contains(body, `href="/?shelf=audio-dsp"`) {
		t.Error("new shelf missing from the sidebar")
	}
}

func TestShelfCreateJSON(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)
	w := postShelf(t, srv, "application/json", `{"name":"Systems"}`, map[string]string{"Accept": "application/json"})
	if w.Code != http.StatusCreated {
		t.Fatalf("JSON create = %d %s", w.Code, w.Body.String())
	}
	var got map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["id"] != "systems" || got["url"] != "/?shelf=systems" {
		t.Errorf("JSON create body = %v", got)
	}
	// Duplicate (any case) and reserved names are the reader's error.
	for _, body := range []string{`{"name":"SYSTEMS"}`, `{"name":"Unsorted"}`, `{"name":"  "}`} {
		w := postShelf(t, srv, "application/json", body, nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("create %s = %d, want 400", body, w.Code)
		}
	}
	if w := postShelf(t, srv, "application/json", `{"name":`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("bad JSON = %d", w.Code)
	}
}

func TestShelfCreateRejectsForeignOrigin(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)
	w := postShelf(t, srv, "application/x-www-form-urlencoded", "name=Evil", map[string]string{"Origin": "http://evil.example"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign origin = %d, want 403", w.Code)
	}
	if _, err := os.Stat(store.ShelvesPath(dir)); !os.IsNotExist(err) {
		t.Error("a forbidden create wrote shelves.json")
	}
}

func TestShelfCreateRefusesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := store.ShelvesPath(dir)
	if err := os.WriteFile(path, []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := postShelf(t, serve.NewServer(dir), "application/x-www-form-urlencoded", "name=Systems", nil)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("create over corrupt file = %d, want 500", w.Code)
	}
	if data, _ := os.ReadFile(path); string(data) != "{oops" {
		t.Error("corrupt shelves.json was overwritten")
	}
}
