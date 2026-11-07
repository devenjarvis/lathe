package serve_test

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/serve"
	"github.com/devenjarvis/lathe/internal/store"
)

func post(t *testing.T, srv *serve.Server, path, ctype, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

const formCT = "application/x-www-form-urlencoded"

// slugOrder lists the data-slug attributes in body, in order.
func slugOrder(body string) []string {
	var out []string
	for _, part := range strings.Split(body, `data-slug="`)[1:] {
		out = append(out, part[:strings.Index(part, `"`)])
	}
	return out
}

// groupSlugs returns the group headings of a grouped grid, each with the
// slugs under it.
func groupSlugs(t *testing.T, body string) map[string][]string {
	t.Helper()
	i := strings.Index(body, `id="tutorialList"`)
	if i < 0 {
		t.Fatal("no grid")
	}
	grid := body[i:strings.Index(body, `id="noResults"`)]
	out := map[string][]string{}
	for _, sec := range strings.Split(grid, `<h3 class="card-group-head"`)[1:] {
		head := sec[strings.Index(sec, ">")+1:]
		label := strings.TrimSpace(head[:strings.Index(head, "<span")])
		var slugs []string
		for _, s := range slugOrder(sec) {
			if len(slugs) == 0 || slugs[len(slugs)-1] != s {
				slugs = append(slugs, s)
			}
		}
		out[label] = slugs
	}
	return out
}

func TestShelfPageSortsAndDescribes(t *testing.T) {
	dir := sampleLibrary(t)
	if _, err := store.UpdateShelves(store.ShelvesPath(dir), func(s *store.Shelves) error {
		return s.Describe("systems", "Kernels, storage and networks")
	}); err != nil {
		t.Fatal(err)
	}
	srv := serve.NewServer(dir)
	_, body := getList(t, srv, "/?shelf=systems")
	for _, want := range []string{
		`<h2>Systems <span class="shelf-count">3</span></h2>`,
		`<p class="shelf-desc">Kernels, storage and networks</p>`,
		// Recently read is a shelf page's default sort.
		`<a href="/?shelf=systems" aria-current="true">Recently read</a>`,
		`<a href="/?shelf=systems&amp;sort=newest">Newest</a>`,
		`<a href="/?group=language&amp;shelf=systems">Language</a>`,
		`<details class="shelf-menu" id="shelfMenu">`,
		`action="/-/shelves/systems/rename"`, `action="/-/shelves/systems/describe"`, `action="/-/shelves/systems/delete"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("shelf page lacks %s", want)
		}
	}
	grid := gridOf(body)
	// done (read now) and kv (3h ago) by when they were read; kernel never.
	if got := strings.Join(dedupe(slugOrder(grid)), ","); got != "done,kv,kernel" {
		t.Errorf("recently read order = %s", got)
	}
	_, body = getList(t, srv, "/?shelf=systems&sort=newest")
	if got := strings.Join(dedupe(slugOrder(gridOf(body))), ","); got != "kernel,kv,done" {
		t.Errorf("newest order = %s", got)
	}
	// The search form carries the sort and grouping through a scripts-off
	// search.
	if !strings.Contains(body, `<input type="hidden" name="sort" value="newest">`) {
		t.Error("search form drops the sort")
	}

	// Unsorted is virtual: no edit menu, a fixed description.
	_, body = getList(t, srv, "/?shelf=unsorted")
	if strings.Contains(body, `id="shelfMenu"`) || !strings.Contains(body, html.EscapeString(store.UnsortedShelfDescription)) {
		t.Error("Unsorted page should have its fixed description and no edit menu")
	}
	// The All grid keeps newest first.
	_, body = getList(t, srv, "/?view=all")
	if !strings.Contains(body, `<a href="/?view=all" aria-current="true">Newest</a>`) {
		t.Error("All tutorials should default to Newest")
	}
}

// gridOf is the markup of the page's grid.
func gridOf(body string) string {
	i := strings.Index(body, `id="tutorialList"`)
	j := strings.Index(body, `id="noResults"`)
	if i < 0 || j < i {
		return ""
	}
	return body[i:j]
}

func dedupe(s []string) []string {
	var out []string
	for _, v := range s {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return out
}

func TestGroupByLanguageFoldsSingletons(t *testing.T) {
	dir := sampleLibrary(t)
	srv := serve.NewServer(dir)
	_, body := getList(t, srv, "/?view=all&group=language")
	groups := groupSlugs(t, body)
	// c1 and c3 share Go (any case); zig has one tutorial, so it folds into
	// Other languages with the tutorials that name none.
	if got := strings.Join(groups["Go"], ","); got != "c1,c3" {
		t.Errorf("Go group = %s (groups %v)", got, groups)
	}
	if _, ok := groups["zig"]; ok {
		t.Error("a one-tutorial language should fold into Other languages")
	}
	other := groups["Other languages"]
	if len(other) != 8 || other[0] != "c2" {
		t.Errorf("Other languages = %v", other)
	}
	if strings.Index(body, ">Go <span") > strings.Index(body, ">Other languages <span") {
		t.Error("Other languages should come last")
	}
}

func TestGroupByStatus(t *testing.T) {
	dir := sampleLibrary(t)
	writeLibrary(t, dir, nil, []libTut{{slug: "ok", status: store.StatusVerified, age: 11 * time.Hour}})
	srv := serve.NewServer(dir)
	_, body := getList(t, srv, "/?view=all&group=status")
	groups := groupSlugs(t, body)
	want := map[string]string{
		"In progress":  "kv,loose",
		"Needs a look": "c2",
		"Verified":     "ok",
		"Finished":     "done",
	}
	for label, slugs := range want {
		if got := strings.Join(groups[label], ","); got != slugs {
			t.Errorf("%s = %s, want %s", label, got, slugs)
		}
	}
	if len(groups["Not started"]) != 6 {
		t.Errorf("Not started = %v", groups["Not started"])
	}
	order := []string{"In progress", "Needs a look", "Verified", "Finished", "Not started"}
	for i := 1; i < len(order); i++ {
		if strings.Index(body, ">"+order[i-1]+" <span") > strings.Index(body, ">"+order[i]+" <span") {
			t.Errorf("%s should come before %s", order[i-1], order[i])
		}
	}
	// A ?q= search that empties a group hides the group too (scripts off).
	_, body = getList(t, srv, "/?view=all&group=status&q=c2")
	if !strings.Contains(body, `<section class="card-group hidden" aria-labelledby="group-in-progress">`) ||
		!strings.Contains(body, `<section class="card-group" aria-labelledby="group-attention">`) {
		t.Error("groups should follow the search")
	}
}

func TestCoverMenu(t *testing.T) {
	dir := sampleLibrary(t)
	srv := serve.NewServer(dir)
	_, body := getList(t, srv, "/?shelf=systems")
	card := body[strings.Index(body, `data-slug="kv"`):]
	card = card[:strings.Index(card, `class="trash-form"`)+300]
	for _, want := range []string{
		`<summary class="cover-menu-btn" aria-label="More for Title kv"`,
		`<a class="menu-item" href="/kv/">Open</a>`,
		`<a class="menu-item" href="/kv/part-02.md#extendRegion">Add a part</a>`,
		`<a class="menu-item" href="/kv/#verify">Verify</a>`,
		`<form class="menu-sub-panel" method="POST" action="/-/shelves/assign"`,
		`name="shelf" value="systems" aria-checked="true" disabled>Systems</button>`,
		`name="shelf" value="unsorted" aria-checked="false">Unsorted</button>`,
		`<form method="POST" action="/-/delete/kv" class="trash-form">`,
		`Move to Trash`,
	} {
		if !strings.Contains(card, want) {
			t.Errorf("cover menu lacks %s", want)
		}
	}
	// No confirm() and no × any more; no Finder item without the shell.
	if strings.Contains(body, "confirm(") || strings.Contains(body, `class="delete-btn"`) || strings.Contains(card, `data-action="reveal"`) {
		t.Error("old delete affordances or a desktop-only item in a browser")
	}
	_, body = getList(t, srv, "/?view=attention")
	if !strings.Contains(body, `href="/c2/#verify">Verify again</a>`) {
		t.Error("a verified-before tutorial should offer Verify again")
	}
	// Sidebar shelves are drop targets.
	if !strings.Contains(body, `<a class="nav-link" data-shelf-id="systems" href="/?shelf=systems">`) {
		t.Error("sidebar shelf links should carry data-shelf-id")
	}
}

func TestTrashUndoFlow(t *testing.T) {
	dir := sampleLibrary(t)
	srv := serve.NewServer(dir)

	// Form post (scripts off): back to the page it came from, with a toast.
	w := post(t, srv, "/-/delete/kv", "", "", map[string]string{"Referer": "http://localhost:4242/?shelf=systems&moved=x"})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", w.Code)
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	if loc.Query().Get("shelf") != "systems" || loc.Query().Get("moved") != "" || !strings.HasPrefix(loc.Query().Get("trashed"), "kv-") {
		t.Fatalf("redirect = %s", loc)
	}
	_, body := getList(t, srv, loc.String())
	id := loc.Query().Get("trashed")
	for _, want := range []string{
		`<span class="toast-msg">Moved “Title kv” to Trash</span>`,
		`<form class="toast-undo-form" method="POST" action="/-/trash/` + id + `/restore">`,
		`<a class="toast-close" href="/?shelf=systems"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("toast lacks %s", want)
		}
	}
	if strings.Contains(body[strings.Index(body, `id="libraryMain"`):], `data-slug="kv"`) {
		t.Error("trashed tutorial still listed")
	}
	// The sidebar shows the Trash now.
	if !strings.Contains(body, `href="/?view=trash"><span class="nav-label">Trash</span><span class="nav-count">1</span>`) {
		t.Error("sidebar lacks the Trash")
	}

	// The Trash view lists it, with Restore and Delete now.
	_, body = getList(t, srv, "/?view=trash")
	for _, want := range []string{"Title kv", `action="/-/trash/` + id + `/restore"`, `action="/-/trash/` + id + `/delete"`, "deleted for good 30 days after", `action="/-/trash/empty"`} {
		if !strings.Contains(body, want) {
			t.Errorf("Trash view lacks %s", want)
		}
	}

	// Undo: back on its shelf.
	w = post(t, srv, "/-/trash/"+id+"/restore", "", "", map[string]string{"Accept": "application/json"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"slug":"kv"`) {
		t.Fatalf("restore = %d %s", w.Code, w.Body.String())
	}
	_, body = getList(t, srv, "/?shelf=systems&restored=kv")
	if !strings.Contains(body, `data-slug="kv"`) || !strings.Contains(body, "Restored “Title kv”") {
		t.Error("restored tutorial should be back on its shelf, with a toast")
	}
	if w := post(t, srv, "/-/trash/"+id+"/restore", "", "", nil); w.Code != http.StatusNotFound {
		t.Errorf("second restore = %d, want 404", w.Code)
	}

	// JSON delete (the page script), then Delete now.
	w = post(t, srv, "/-/delete/kv", "", "", map[string]string{"Accept": "application/json"})
	var res map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res["title"] != "Title kv" || !strings.HasSuffix(res["restore"], "/restore") {
		t.Fatalf("JSON delete = %d %s", w.Code, w.Body.String())
	}
	w = post(t, srv, "/-/trash/"+res["id"]+"/delete", "", "", nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/?view=trash" {
		t.Errorf("delete now = %d %s", w.Code, w.Header().Get("Location"))
	}
	if s, _ := store.ReadShelves(store.ShelvesPath(dir)); s.Placements["kv"] != "" {
		t.Error("permanent delete kept the shelf placement")
	}

	// Empty Trash.
	post(t, srv, "/-/delete/c1", "", "", nil)
	post(t, srv, "/-/delete/c2", "", "", nil)
	if w := post(t, srv, "/-/trash/empty", "", "", nil); w.Code != http.StatusSeeOther {
		t.Errorf("empty = %d", w.Code)
	}
	if list, _ := store.ListTrash(dir); len(list) != 0 {
		t.Errorf("Trash not emptied: %v", list)
	}
}

func TestRestoreConflict(t *testing.T) {
	dir := sampleLibrary(t)
	srv := serve.NewServer(dir)
	w := post(t, srv, "/-/delete/loose", "", "", map[string]string{"Accept": "application/json"})
	var res map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	writeLibrary(t, dir, nil, []libTut{{slug: "loose"}})
	if w := post(t, srv, res["restore"], "", "", nil); w.Code != http.StatusConflict {
		t.Errorf("restore over a stored tutorial = %d, want 409", w.Code)
	}
	_, body := getList(t, srv, "/?view=trash")
	if !strings.Contains(body, "can't be restored over it") || !strings.Contains(body, `class="btn btn-ghost btn-sm" disabled>Restore`) {
		t.Error("Trash view should say why Restore is off")
	}
}

func TestShelfAssign(t *testing.T) {
	dir := sampleLibrary(t)
	srv := serve.NewServer(dir)
	w := post(t, srv, "/-/shelves/assign", formCT, "slug=loose&shelf=systems", map[string]string{"Referer": "http://localhost:4242/?shelf=unsorted"})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/?moved=loose&shelf=unsorted&to=systems" {
		t.Fatalf("assign = %d %q", w.Code, w.Header().Get("Location"))
	}
	_, body := getList(t, srv, w.Header().Get("Location"))
	if !strings.Contains(body, "Moved “Title loose” to Systems") {
		t.Error("move toast missing")
	}
	w = post(t, srv, "/-/shelves/assign", "application/json", `{"slug":"loose","shelf":"unsorted"}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"shelf":"unsorted"`) {
		t.Errorf("JSON assign = %d %s", w.Code, w.Body.String())
	}
	for _, c := range []struct {
		body string
		code int
	}{
		{`{"slug":"loose","shelf":"nope"}`, http.StatusNotFound},
		{`{"slug":"missing","shelf":"systems"}`, http.StatusNotFound},
		{`{"slug":"../x","shelf":"systems"}`, http.StatusNotFound},
	} {
		if w := post(t, srv, "/-/shelves/assign", "application/json", c.body, nil); w.Code != c.code {
			t.Errorf("assign %s = %d, want %d", c.body, w.Code, c.code)
		}
	}
}

func TestShelfRenameDescribeDelete(t *testing.T) {
	dir := sampleLibrary(t)
	srv := serve.NewServer(dir)
	w := post(t, srv, "/-/shelves/systems/rename", formCT, "name=Systems+programming", nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/?shelf=systems" {
		t.Fatalf("rename = %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := post(t, srv, "/-/shelves/systems/rename", "application/json", `{"name":"graphics"}`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("rename onto a taken name = %d, want 400", w.Code)
	}
	w = post(t, srv, "/-/shelves/systems/describe", "application/json", `{"description":"Down to the metal"}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"description":"Down to the metal"`) {
		t.Errorf("describe = %d %s", w.Code, w.Body.String())
	}
	if w := post(t, srv, "/-/shelves/unsorted/rename", formCT, "name=Inbox", nil); w.Code != http.StatusBadRequest {
		t.Errorf("rename Unsorted = %d, want 400", w.Code)
	}
	if w := post(t, srv, "/-/shelves/nope/delete", "", "", nil); w.Code != http.StatusNotFound {
		t.Errorf("delete a missing shelf = %d, want 404", w.Code)
	}

	w = post(t, srv, "/-/shelves/systems/delete", "", "", nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/?shelf=unsorted&shelfdeleted=3" {
		t.Fatalf("delete = %d %q", w.Code, w.Header().Get("Location"))
	}
	_, body := getList(t, srv, w.Header().Get("Location"))
	if !strings.Contains(body, "Shelf deleted. Its 3 tutorials are on Unsorted now") {
		t.Error("delete toast missing")
	}
	grid := gridOf(body)
	for _, slug := range []string{"kernel", "kv", "done", "loose"} {
		if !strings.Contains(grid, `data-slug="`+slug+`"`) {
			t.Errorf("%s should be on Unsorted after its shelf went", slug)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "kv")); err != nil {
		t.Error("deleting a shelf touched a tutorial")
	}
}

func TestLibraryActionsRejectForeignOrigin(t *testing.T) {
	dir := sampleLibrary(t)
	srv := serve.NewServer(dir)
	evil := map[string]string{"Origin": "http://evil.example"}
	for _, path := range []string{
		"/-/shelves/assign", "/-/shelves/systems/rename", "/-/shelves/systems/describe", "/-/shelves/systems/delete",
		"/-/trash/kv-20261107T093000.123Z/restore", "/-/trash/kv-20261107T093000.123Z/delete", "/-/trash/empty",
	} {
		if w := post(t, srv, path, formCT, "slug=kv&shelf=graphics&name=x&description=x", evil); w.Code != http.StatusForbidden {
			t.Errorf("%s from another origin = %d, want 403", path, w.Code)
		}
	}
	s, _ := store.ReadShelves(store.ShelvesPath(dir))
	if sh, _ := s.Get("systems"); sh.Name != "Systems" || s.ShelfOf("kv").ID != "systems" {
		t.Error("a forbidden request changed the shelves")
	}
}

// Forged toast parameters can't put free text on the page: messages are
// built from what's on disk.
func TestToastIgnoresUnknownValues(t *testing.T) {
	dir := sampleLibrary(t)
	srv := serve.NewServer(dir)
	for _, q := range []string{"?trashed=evil", "?restored=nope", "?moved=kv&to=nope", "?shelfdeleted=lots"} {
		_, body := getList(t, srv, "/"+q)
		if !strings.Contains(body, `id="libraryToast" role="status" aria-live="polite" data-undo-key=`) || !strings.Contains(body, `<span class="toast-msg"></span>`) {
			t.Errorf("%s rendered a toast", q)
		}
	}
}
