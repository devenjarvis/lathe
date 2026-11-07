package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devenjarvis/lathe/internal/store"
)

type indexBody struct {
	Tutorials []struct {
		Slug   string       `json:"slug"`
		Title  string       `json:"title"`
		Href   string       `json:"href"`
		Status string       `json:"status"`
		Tags   []string     `json:"tags"`
		Tools  []store.Tool `json:"tools"`
		Shelf  struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"shelf"`
		Cover int `json:"cover"`
		Parts []struct {
			Part     string `json:"part"`
			Number   int    `json:"number"`
			Title    string `json:"title"`
			Href     string `json:"href"`
			Headings []struct {
				ID    string `json:"id"`
				Text  string `json:"text"`
				Level int    `json:"level"`
				Href  string `json:"href"`
			} `json:"headings"`
		} `json:"parts"`
	} `json:"tutorials"`
	Tags []struct {
		Tag   string `json:"tag"`
		Count int    `json:"count"`
		Href  string `json:"href"`
	} `json:"tags"`
}

// writeIndexFixture stores a two-part "parser" series on a Compilers shelf
// and a legacy single-file tutorial.
func writeIndexFixture(t *testing.T, dir string) {
	t.Helper()
	parser := filepath.Join(dir, "parser")
	if err := os.MkdirAll(parser, 0o755); err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{
		"part-01.md": "# Tokens first\n\nIntro.\n\n## Set up the `lexer`\n\nText.\n\n### Read a *number*\n\n## Setup\n\n## Setup\n\n#### Too deep\n",
		"part-02.md": "# A Pratt parser\n\n> [!NOTE]\n> A callout.\n\n## Binding power\n\n### Prefix operators\n",
	}
	for name, src := range parts {
		if err := os.WriteFile(filepath.Join(parser, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.WriteMetadata(parser, &store.Tutorial{
		Slug: "parser", Title: "Write a parser", Topic: "parsing in Go", Status: store.StatusVerified,
		Created: time.Now(), Tags: []string{"parsing", "go"}, Parts: []string{"part-01.md", "part-02.md"},
		Tools: []store.Tool{{Name: "Go", Version: "1.24"}},
	}); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "legacy")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "index.md"), []byte("# Old one\n\n## Only section\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMetadata(legacy, &store.Tutorial{
		Slug: "legacy", Title: "Legacy", Status: store.StatusUnverified, Created: time.Now().Add(-time.Hour), Tags: []string{"parsing"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateShelves(store.ShelvesPath(dir), func(s *store.Shelves) error {
		sh, err := s.Create("Compilers")
		if err != nil {
			return err
		}
		return s.Assign("parser", sh.ID)
	}); err != nil {
		t.Fatal(err)
	}
}

func getIndex(t *testing.T, ts *httptest.Server) (indexBody, *http.Response) {
	t.Helper()
	res, err := http.Get(ts.URL + "/-/index")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("GET /-/index = %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	var body indexBody
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body, res
}

func TestIndexEndpoint(t *testing.T) {
	dir := t.TempDir()
	writeIndexFixture(t, dir)
	ts := httptest.NewServer(NewServer(dir).Handler())
	t.Cleanup(ts.Close)

	body, _ := getIndex(t, ts)
	if len(body.Tutorials) != 2 || body.Tutorials[0].Slug != "parser" || body.Tutorials[1].Slug != "legacy" {
		t.Fatalf("tutorials = %+v, want parser then legacy (newest first)", body.Tutorials)
	}
	p := body.Tutorials[0]
	if p.Title != "Write a parser" || p.Href != "/parser/" || p.Status != "verified" || p.Shelf.Name != "Compilers" || p.Shelf.ID == "" {
		t.Errorf("parser entry = %+v", p)
	}
	if strings.Join(p.Tags, ",") != "parsing,go" || len(p.Tools) != 1 || p.Tools[0].Name != "Go" || p.Cover < 1 || p.Cover > 4 {
		t.Errorf("parser tags/tools/cover = %v %v %d", p.Tags, p.Tools, p.Cover)
	}
	if len(p.Parts) != 2 {
		t.Fatalf("parts = %+v", p.Parts)
	}
	p1, p2 := p.Parts[0], p.Parts[1]
	if p1.Number != 1 || p1.Title != "Tokens first" || p1.Href != "/parser/part-01.md" || p2.Number != 2 || p2.Title != "A Pratt parser" {
		t.Errorf("part entries = %+v / %+v", p1, p2)
	}
	// h2 and h3 only, inline formatting reduced to text, duplicate headings
	// numbered the way the page numbers them.
	var got []string
	for _, h := range p1.Headings {
		got = append(got, h.Text+"|"+h.ID+"|"+string(rune('0'+h.Level)))
	}
	want := "Set up the lexer|set-up-the-lexer|2,Read a number|read-a-number|3,Setup|setup|2,Setup|setup-1|2"
	if strings.Join(got, ",") != want {
		t.Errorf("part 1 headings = %v, want %s", got, want)
	}
	if len(p2.Headings) != 2 || p2.Headings[0].Href != "/parser/part-02.md#binding-power" || p2.Headings[1].Level != 3 {
		t.Errorf("part 2 headings = %+v", p2.Headings)
	}

	// Every heading id is one the reading page carries.
	for _, part := range p.Parts {
		page := get(t, ts, part.Href)
		for _, h := range part.Headings {
			if !strings.Contains(page, `id="`+h.ID+`"`) {
				t.Errorf("%s has no heading id %q", part.Href, h.ID)
			}
		}
	}

	l := body.Tutorials[1]
	if len(l.Parts) != 1 || l.Parts[0].Part != "index.md" || l.Parts[0].Number != 0 || l.Parts[0].Href != "/legacy/" ||
		len(l.Parts[0].Headings) != 1 || l.Parts[0].Headings[0].Href != "/legacy/#only-section" || l.Shelf.Name != "Unsorted" {
		t.Errorf("legacy entry = %+v", l)
	}

	if len(body.Tags) != 2 || body.Tags[0].Tag != "parsing" || body.Tags[0].Count != 2 || body.Tags[0].Href != "/?q=parsing&view=all" ||
		body.Tags[1].Tag != "go" || body.Tags[1].Count != 1 {
		t.Errorf("tags = %+v", body.Tags)
	}
	// The tag link is the All view searched for the tag.
	if page := get(t, ts, body.Tags[0].Href); !strings.Contains(page, `value="parsing"`) {
		t.Error("the tag href does not open the All view searched for the tag")
	}
}

func TestIndexFollowsEditsAndRevalidates(t *testing.T) {
	dir := t.TempDir()
	writeIndexFixture(t, dir)
	ts := httptest.NewServer(NewServer(dir).Handler())
	t.Cleanup(ts.Close)

	_, res := getIndex(t, ts)
	etag := res.Header.Get("ETag")
	if etag == "" || res.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("ETag %q, Cache-Control %q", etag, res.Header.Get("Cache-Control"))
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/-/index", nil)
	req.Header.Set("If-None-Match", etag)
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res2.Body)
	_ = res2.Body.Close()
	if res2.StatusCode != http.StatusNotModified {
		t.Errorf("unchanged index = %d, want 304", res2.StatusCode)
	}

	// An edited part (new mtime and size) is re-read.
	path := filepath.Join(dir, "parser", "part-02.md")
	if err := os.WriteFile(path, []byte("# A Pratt parser\n\n## Precedence climbing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	body, res3 := getIndex(t, ts)
	if res3.Header.Get("ETag") == etag {
		t.Error("ETag unchanged after an edit")
	}
	hs := body.Tutorials[0].Parts[1].Headings
	if len(hs) != 1 || hs[0].ID != "precedence-climbing" {
		t.Errorf("edited part headings = %+v", hs)
	}
}

func TestIndexEmptyLibrary(t *testing.T) {
	ts := httptest.NewServer(NewServer(filepath.Join(t.TempDir(), "missing")).Handler())
	t.Cleanup(ts.Close)
	body, _ := getIndex(t, ts)
	if body.Tutorials == nil || len(body.Tutorials) != 0 || body.Tags == nil {
		t.Errorf("empty library index = %+v, want empty lists", body)
	}
}

// Every page carries the palette once, after the shortcut registry, and the
// masthead search button, a link to All tutorials, where it has a masthead.
func TestPaletteOnEveryPageOnce(t *testing.T) {
	dir := t.TempDir()
	writeIndexFixture(t, dir)
	ts := httptest.NewServer(NewServer(dir).Handler())
	t.Cleanup(ts.Close)
	for path, button := range map[string]bool{"/": true, "/?view=all": true, "/parser/part-01.md": true, "/legacy/": true, "/-/settings": false} {
		page := get(t, ts, path)
		if n := strings.Count(page, `<dialog class="palette" id="palette"`); n != 1 {
			t.Errorf("%s: %d palettes, want 1", path, n)
		}
		if n := strings.Count(page, "window.lathePalette = "); n != 1 {
			t.Errorf("%s: palette script %d times, want 1", path, n)
		}
		if strings.Index(page, `id="latheShortcuts"`) > strings.Index(page, `id="palette"`) {
			t.Errorf("%s: the palette comes before the shortcut registry", path)
		}
		if got := strings.Contains(page, `<a class="palette-open" href="/?view=all" data-palette-open`); got != button {
			t.Errorf("%s: masthead search button = %v, want %v", path, got, button)
		}
	}
	if page := get(t, ts, "/parser/part-01.md"); strings.Count(page, `<a class="palette-open"`) != 1 {
		t.Error("reading page should have one masthead search button")
	}
}
