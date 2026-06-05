package serve_test

import (
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

func makeTestTutorial(t *testing.T, dir, slug string, series bool) string {
	t.Helper()
	tutDir := filepath.Join(dir, slug)
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:    slug,
		Title:   "Test Tutorial",
		Status:  store.StatusVerified,
		Created: time.Now(),
	}
	if series {
		tut.Parts = []string{"part-01.md", "part-02.md"}
		for _, p := range tut.Parts {
			if err := os.WriteFile(filepath.Join(tutDir, p), []byte("# "+p), 0644); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		if err := os.WriteFile(filepath.Join(tutDir, "index.md"), []byte("# Index"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}
	return tutDir
}

// articleHeader returns the markup of the <header class="article-header"> block
// at the top of <main>, so callers can assert on title/badge/provenance without
// matching the same text elsewhere on the page (top-bar crumb, <title>, etc.).
func articleHeader(t *testing.T, body string) string {
	t.Helper()
	idx := strings.Index(body, `class="article-header"`)
	if idx < 0 {
		t.Fatalf("missing article-header block; body excerpt:\n%s", body)
	}
	end := strings.Index(body[idx:], "</header>")
	if end < 0 {
		t.Fatalf("article-header block not closed; body excerpt:\n%s", body)
	}
	return body[idx : idx+end]
}

func TestListPage(t *testing.T) {
	dir := t.TempDir()
	makeTestTutorial(t, dir, "test-tutorial", false)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET / = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "Test Tutorial") {
		t.Error("GET / response does not contain tutorial title")
	}
}

func TestListPageRendersTagsAndControls(t *testing.T) {
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "tagged-tutorial")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:    "tagged-tutorial",
		Title:   "Tagged Tutorial",
		Status:  store.StatusVerified,
		Created: time.Now(),
		Tags:    []string{"rust", "audio"},
	}
	if err := os.WriteFile(filepath.Join(tutDir, "index.md"), []byte("# Index"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `id="searchInput"`) {
		t.Error("list page missing the search input control")
	}
	if !strings.Contains(body, `id="sortSelect"`) {
		t.Error("list page missing the sort control")
	}
	if !strings.Contains(body, `id="filterToggle"`) {
		t.Error("list page missing the collapsible Filters toggle")
	}
	if !strings.Contains(body, `data-tags="rust,audio,"`) {
		t.Error("list page card missing data-tags attribute for search/filter")
	}
	if !strings.Contains(body, `<span class="tag">rust</span>`) {
		t.Error("list page missing rendered tag pill")
	}
	// Whole-card stretched link: the title anchor carries .tutorial-link and
	// points at the tutorial; the overlay (::after) makes the card clickable.
	if !strings.Contains(body, `class="tutorial-link" href="/tagged-tutorial/"`) {
		t.Error("list page card missing the stretched .tutorial-link anchor")
	}
	// Badge is now a quiet dot + serif label — no emoji pill.
	if !strings.Contains(body, `<span class="badge-dot"></span>`) {
		t.Error("list page badge missing the dot marker (dot + label restyle)")
	}
	if strings.Contains(body, "✅") || strings.Contains(body, "❌") {
		t.Error("list page badge still renders emoji; expected dot + label")
	}
}

func TestListPageGroupsByRepoAndRendersVersions(t *testing.T) {
	dir := t.TempDir()

	// Two tutorials in the same repo, one with no repo — exercises grouping and
	// the "No repo" bucket, plus version chips and the Versions filter row.
	mk := func(slug string, repo string, tools []store.Tool) {
		tutDir := filepath.Join(dir, slug)
		if err := os.MkdirAll(tutDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tutDir, "index.md"), []byte("# x"), 0644); err != nil {
			t.Fatal(err)
		}
		tut := &store.Tutorial{
			Slug:    slug,
			Title:   slug,
			Status:  store.StatusUnverified,
			Created: time.Now(),
			Repo:    repo,
			Tools:   tools,
		}
		if err := store.WriteMetadata(tutDir, tut); err != nil {
			t.Fatal(err)
		}
	}
	mk("synth-zig", "github.com/devenjarvis/lathe", []store.Tool{{Name: "zig", Version: "0.13.0"}})
	mk("compiler-go", "github.com/devenjarvis/lathe", []store.Tool{{Name: "go", Version: "1.22"}})
	mk("standalone", "", nil)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	body := w.Body.String()

	if !strings.Contains(body, `<span class="repo-group-name">devenjarvis/lathe</span>`) {
		t.Error("list page missing repo group header for devenjarvis/lathe")
	}
	if !strings.Contains(body, `<span class="repo-group-name">No repo</span>`) {
		t.Error("list page missing the 'No repo' group for repo-less tutorials")
	}
	if !strings.Contains(body, `data-repo="devenjarvis/lathe"`) {
		t.Error("card missing data-repo attribute for search/filter")
	}
	if !strings.Contains(body, `data-tools="zig 0.13.0,"`) {
		t.Error("card missing data-tools attribute for version filter")
	}
	if !strings.Contains(body, `<span class="version">zig 0.13.0</span>`) {
		t.Error("card missing rendered version chip")
	}
	if !strings.Contains(body, `id="versionFilters"`) {
		t.Error("list page missing the Versions filter row")
	}
	// The repo group must come before the no-repo bucket. Match the group-header
	// markup specifically (the bare strings also appear in the inline CSS).
	repoHdr := strings.Index(body, `repo-group-name">devenjarvis/lathe<`)
	noRepoHdr := strings.Index(body, `repo-group-name">No repo<`)
	if repoHdr == -1 || noRepoHdr == -1 || repoHdr > noRepoHdr {
		t.Errorf("repo group (%d) should render before the 'No repo' bucket (%d)", repoHdr, noRepoHdr)
	}
}

func TestTutorialPage(t *testing.T) {
	dir := t.TempDir()
	makeTestTutorial(t, dir, "test-tutorial", false)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/test-tutorial/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /test-tutorial/ = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "Index") {
		t.Error("GET /test-tutorial/ response does not contain page content")
	}
}

func TestSeriesPartPage(t *testing.T) {
	dir := t.TempDir()
	makeTestTutorial(t, dir, "test-series", true)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/test-series/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /test-series/part-01.md = %d, want %d", w.Code, http.StatusOK)
	}
}

func makeSeriesTutorialWithParts(t *testing.T, dir, slug string, numParts int) {
	t.Helper()
	tutDir := filepath.Join(dir, slug)
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	parts := make([]string, numParts)
	for i := 0; i < numParts; i++ {
		parts[i] = fmt.Sprintf("part-%02d.md", i+1)
	}
	tut := &store.Tutorial{
		Slug:    slug,
		Title:   "Test Series",
		Status:  store.StatusVerified,
		Parts:   parts,
		Created: time.Now(),
	}
	for _, p := range parts {
		if err := os.WriteFile(filepath.Join(tutDir, p), []byte("# "+p), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}
}

func TestSeriesPartPrevNext(t *testing.T) {
	dir := t.TempDir()
	makeSeriesTutorialWithParts(t, dir, "test-series", 3)
	srv := serve.NewServer(dir)

	cases := []struct {
		part         string
		wantPrevHref string // empty => no prev expected
		wantNextHref string // empty => no next expected
		wantCrumb    string // breadcrumb segment after the › separator
	}{
		{"part-01.md", "", "/test-series/part-02.md", "Part 1"},
		{"part-02.md", "/test-series/part-01.md", "/test-series/part-03.md", "Part 2"},
		{"part-03.md", "/test-series/part-02.md", "", "Part 3"},
	}

	for _, tc := range cases {
		t.Run(tc.part, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test-series/"+tc.part, nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("GET /test-series/%s = %d, want %d", tc.part, w.Code, http.StatusOK)
			}
			body := w.Body.String()

			wantCrumb := `<span class="sep">›</span>` + tc.wantCrumb
			if !strings.Contains(body, wantCrumb) {
				t.Errorf("missing breadcrumb segment %q", wantCrumb)
			}

			hasPrev := strings.Contains(body, `class="prev"`)
			if tc.wantPrevHref == "" {
				if hasPrev {
					t.Errorf("expected no prev link on %s, found one", tc.part)
				}
			} else {
				if !hasPrev {
					t.Errorf("expected prev link on %s, found none", tc.part)
				}
				if !strings.Contains(body, `href="`+tc.wantPrevHref+`"`) {
					t.Errorf("expected prev href %q in body", tc.wantPrevHref)
				}
			}

			hasNext := strings.Contains(body, `class="next"`)
			if tc.wantNextHref == "" {
				if hasNext {
					t.Errorf("expected no next link on %s, found one", tc.part)
				}
			} else {
				if !hasNext {
					t.Errorf("expected next link on %s, found none", tc.part)
				}
				if !strings.Contains(body, `href="`+tc.wantNextHref+`"`) {
					t.Errorf("expected next href %q in body", tc.wantNextHref)
				}
			}
		})
	}
}

func TestSeriesSidebarAndBottomList(t *testing.T) {
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "test-series")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:    "test-series",
		Title:   "Test Series",
		Status:  store.StatusVerified,
		Parts:   []string{"part-01.md", "part-02.md"},
		Created: time.Now(),
	}
	// Part 1 has two h2 sections so we can assert TOC links exist.
	body1 := "# Part One\n\n## Setup\n\nFoo.\n\n## Wire it up\n\nBar.\n"
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte(body1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tutDir, "part-02.md"), []byte("# Part Two\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/test-series/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /test-series/part-01.md = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()

	// The persistent top bar carries the back-link to the list page.
	if !strings.Contains(body, `class="back-link"`) || !strings.Contains(body, "All tutorials") {
		t.Error("page missing back-link to /")
	}

	// The on-page TOC now lives in the right-edge section rail (#tocRail), with one
	// anchor per h2. (The same anchors also appear in the narrow drawer, so we
	// scope the assertion to the markup after the rail marker.)
	railIdx := strings.Index(body, `id="tocRail"`)
	if railIdx < 0 {
		t.Fatalf("missing #tocRail section rail; body excerpt:\n%s", body)
	}
	rail := body[railIdx:]
	if !strings.Contains(rail, `href="#setup"`) {
		t.Errorf("section rail missing anchor to first h2; body excerpt:\n%s", body)
	}
	if !strings.Contains(rail, `href="#wire-it-up"`) {
		t.Error("section rail missing anchor to second h2")
	}

	// The article-header block atop <main> carries the title and status badge.
	header := articleHeader(t, body)
	if !strings.Contains(header, "Test Series") {
		t.Error("article-header missing tutorial title")
	}
	if !strings.Contains(header, `class="badge verified"`) {
		t.Errorf("article-header missing status badge; header:\n%s", header)
	}

	// The old in-sidebar parts list pattern (an <a class="active"> inside the
	// sidebar pointing to the current part's URL) should no longer appear.
	oldPattern := `<a href="/test-series/part-01.md" class="active"`
	if strings.Contains(body, oldPattern) {
		t.Errorf("sidebar still renders old parts-list pattern: %s", oldPattern)
	}

	// Bottom of main should contain the new "In this series" section listing
	// all parts, with the current part marked.
	if !strings.Contains(body, `class="series-toc"`) {
		t.Error("main missing .series-toc section")
	}
	if !strings.Contains(body, "In this series") {
		t.Error("main missing 'In this series' label")
	}
	if !strings.Contains(body, `class="current-row"`) {
		t.Error("series-toc missing current-row marker for current part")
	}
	// Non-current parts must be real links.
	if !strings.Contains(body, `href="/test-series/part-02.md"`) {
		t.Error("series-toc missing link to non-current part")
	}
}

func TestSectionRailOmittedWithoutMultipleHeadings(t *testing.T) {
	// The rail is a minimap for jumping *between* sections, so it is omitted for a
	// part with 0 or 1 h2 (a single tick navigates nowhere useful). Both boundary
	// cases must drop #tocRail.
	cases := []struct {
		name string
		body string
	}{
		{"zero-h2", "# Title only\n\nProse with no sections.\n"},
		{"one-h2", "# Title\n\n## The only section\n\nProse.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tutDir := filepath.Join(dir, "solo")
			if err := os.MkdirAll(tutDir, 0755); err != nil {
				t.Fatal(err)
			}
			tut := &store.Tutorial{Slug: "solo", Title: "Solo", Status: store.StatusUnverified, Created: time.Now(), Parts: []string{"part-01.md"}}
			if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			if err := store.WriteMetadata(tutDir, tut); err != nil {
				t.Fatal(err)
			}

			srv := serve.NewServer(dir)
			req := httptest.NewRequest(http.MethodGet, "/solo/part-01.md", nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("GET /solo/part-01.md = %d, want %d", w.Code, http.StatusOK)
			}
			if strings.Contains(w.Body.String(), `id="tocRail"`) {
				t.Errorf("part with <=1 h2 (%s) should not render the #tocRail section rail", tc.name)
			}
		})
	}
}

func TestNonSeriesNoSeriesTOC(t *testing.T) {
	dir := t.TempDir()
	makeTestTutorial(t, dir, "single", false)
	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/single/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /single/ = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if strings.Contains(body, `class="series-toc"`) {
		t.Error("non-series tutorial should not render .series-toc block")
	}
	if strings.Contains(body, "In this series") {
		t.Error("non-series tutorial should not render 'In this series' label")
	}
}

func TestNonSeriesNoPartNav(t *testing.T) {
	dir := t.TempDir()
	makeTestTutorial(t, dir, "single", false)
	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/single/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /single/ = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if strings.Contains(body, `class="part-nav"`) {
		t.Error("non-series tutorial should not render part-nav block")
	}
	if strings.Contains(body, `<span class="sep">`) {
		t.Error("non-series tutorial should not render breadcrumb separator")
	}
}

func TestProvenanceSourcesRendered(t *testing.T) {
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "researched")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:    "researched",
		Title:   "Researched Tutorial",
		Status:  store.StatusUnverified,
		Created: time.Now(),
		Parts:   []string{"part-01.md"},
		Sources: []string{"https://ziglang.org/documentation/master/#comptime"},
	}
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte("# Part 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/researched/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	// Provenance now renders in the article-header block atop <main>, no longer
	// in the (relocated) sidebar.
	header := articleHeader(t, w.Body.String())
	if !strings.Contains(header, "Researched against 1 source") {
		t.Error("article-header missing the 'Researched against N sources' provenance line")
	}
	if !strings.Contains(header, "https://ziglang.org/documentation/master/#comptime") {
		t.Error("article-header missing the consulted source link")
	}
}

func TestVerifiedDateRendered(t *testing.T) {
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "checked")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:    "checked",
		Title:   "Checked Tutorial",
		Status:  store.StatusVerified,
		Created: time.Now(),
		Parts:   []string{"part-01.md"},
	}
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte("# Part 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteVerifyResult(tutDir, &store.VerifyResult{Status: store.StatusVerified, CheckedAt: "2026-06-03T12:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/checked/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	// The verified-date line renders in the article-header block atop <main>.
	header := articleHeader(t, w.Body.String())
	if !strings.Contains(header, "Verified Jun 3, 2026") {
		t.Error("article-header missing the 'Verified <date>' provenance line")
	}
}

func TestUnverifiedCalloutCountRendered(t *testing.T) {
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "shaky")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:    "shaky",
		Title:   "Shaky Tutorial",
		Status:  store.StatusUnverified,
		Created: time.Now(),
		Parts:   []string{"part-01.md"},
	}
	body := "# Part 1\n\n> [!UNVERIFIED]\n> The default buffer size is 4096 — I couldn't confirm this.\n"
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/shaky/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	got := w.Body.String()
	if !strings.Contains(got, `callout-unverified`) {
		t.Error("part page missing rendered [!UNVERIFIED] callout")
	}
	if !strings.Contains(got, "1 claim flagged unverified") {
		t.Error("part page missing the unverified-claim count note near the badge")
	}
}

func TestNotFound(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/nonexistent/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("GET /nonexistent/ = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestSeriesRedirect(t *testing.T) {
	dir := t.TempDir()
	makeTestTutorial(t, dir, "test-series", true)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/test-series/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("GET /test-series/ = %d, want %d (redirect)", w.Code, http.StatusFound)
	}
	loc := w.Header().Get("Location")
	if loc != "/test-series/part-01.md" {
		t.Errorf("redirect Location = %q, want %q", loc, "/test-series/part-01.md")
	}
}

func TestSinglePartRedirect(t *testing.T) {
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "single-part")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:    "single-part",
		Title:   "Single Part",
		Status:  store.StatusUnverified,
		Created: time.Now(),
		Parts:   []string{"part-01.md"},
	}
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte("# Only Part"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/single-part/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("GET /single-part/ = %d, want %d (redirect)", w.Code, http.StatusFound)
	}
	loc := w.Header().Get("Location")
	if loc != "/single-part/part-01.md" {
		t.Errorf("redirect Location = %q, want %q", loc, "/single-part/part-01.md")
	}
}

func TestStaticMermaidAsset(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/_static/mermaid.min.js", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /_static/mermaid.min.js = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("Content-Type = %q, want application/javascript", ct)
	}
	if w.Body.Len() < 100_000 {
		t.Errorf("mermaid bundle suspiciously small (%d bytes)", w.Body.Len())
	}
	// Sanity-check that this is the real UMD bundle by looking for the global
	// it installs on window.
	if !strings.Contains(w.Body.String(), "mermaid") {
		t.Error("mermaid bundle body does not mention 'mermaid'")
	}
}

func TestStaticFaviconAsset(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/_static/favicon.svg", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /_static/favicon.svg = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("Content-Type = %q, want image/svg+xml", ct)
	}
	// Sanity-check it's the real SVG mark, including the dark-mode swap that lets
	// the favicon follow the OS theme.
	body := w.Body.String()
	if !strings.Contains(body, "<svg") {
		t.Error("favicon body is not an SVG document")
	}
	if !strings.Contains(body, "prefers-color-scheme: dark") {
		t.Error("favicon missing the prefers-color-scheme dark swap")
	}
}

func TestStaticFontAssets(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)

	// The embedded woff2 fonts live under static/fonts/ on disk but are served
	// at flat /_static/<name>.woff2 (single-segment route + whitelist). Verify
	// the .woff2 → static/fonts/ path resolution works for every bundled font.
	fonts := []string{
		"fraunces.woff2",
		"newsreader.woff2",
		"newsreader-italic.woff2",
		"jetbrains-mono.woff2",
	}
	for _, name := range fonts {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/_static/"+name, nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("GET /_static/%s = %d, want %d", name, w.Code, http.StatusOK)
			}
			if ct := w.Header().Get("Content-Type"); ct != "font/woff2" {
				t.Errorf("%s Content-Type = %q, want font/woff2", name, ct)
			}
			// woff2 files start with the "wOF2" signature; also sanity-check
			// they're not suspiciously small (subset latin faces are >10KB).
			body := w.Body.Bytes()
			if len(body) < 10_000 {
				t.Errorf("%s suspiciously small (%d bytes)", name, len(body))
			}
			if len(body) < 4 || string(body[:4]) != "wOF2" {
				t.Errorf("%s missing wOF2 signature", name)
			}
		})
	}
}

func TestStaticAssetWhitelist(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/_static/anything-else.js", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("GET /_static/anything-else.js = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDeleteEndpointRemovesTutorial(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "doomed", false)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/delete/doomed", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /-/delete/doomed = %d, want %d", w.Code, http.StatusSeeOther)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("redirect Location = %q, want %q", loc, "/")
	}
	if _, err := os.Stat(tutDir); !os.IsNotExist(err) {
		t.Errorf("tutorial dir still exists after delete: stat err = %v", err)
	}
}

func TestDeleteEndpointRejectsGet(t *testing.T) {
	dir := t.TempDir()
	makeTestTutorial(t, dir, "stay", false)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/-/delete/stay", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusSeeOther || w.Code == http.StatusOK {
		t.Errorf("GET /-/delete/stay = %d, want method not allowed", w.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "stay")); err != nil {
		t.Errorf("tutorial removed via GET: %v", err)
	}
}

func TestDeleteEndpointMissingSlug(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/delete/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("POST /-/delete/nonexistent = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestPathTraversalBlocked(t *testing.T) {
	dir := t.TempDir()
	makeTestTutorial(t, dir, "test-tutorial", false)

	srv := serve.NewServer(dir)
	// URL-decode happens before ServeMux matching so %2f won't work,
	// but a literal .. in the path still needs to be blocked
	req := httptest.NewRequest(http.MethodGet, "/test-tutorial/../../../etc/passwd", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Error("path traversal should not succeed")
	}
}

func TestExtendFormRendersOnLastPart(t *testing.T) {
	dir := t.TempDir()
	makeSeriesTutorialWithParts(t, dir, "test-series", 3)
	srv := serve.NewServer(dir)

	req := httptest.NewRequest(http.MethodGet, "/test-series/part-03.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /test-series/part-03.md = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="extendForm"`) {
		t.Error("last part should render extend form with id=extendForm")
	}
	if !strings.Contains(body, `action="/-/extend/test-series"`) {
		t.Error("extend form should post to /-/extend/test-series")
	}
	if !strings.Contains(body, `placeholder="What should the next part cover?`) {
		t.Error("extend form should have guidance textarea with placeholder")
	}
}

func TestExtendFormHiddenOnNonLastPart(t *testing.T) {
	dir := t.TempDir()
	makeSeriesTutorialWithParts(t, dir, "test-series", 3)
	srv := serve.NewServer(dir)

	for _, part := range []string{"part-01.md", "part-02.md"} {
		t.Run(part, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test-series/"+part, nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("GET /test-series/%s = %d, want %d", part, w.Code, http.StatusOK)
			}
			if strings.Contains(w.Body.String(), `id="extendForm"`) {
				t.Errorf("non-last part %s should not render extend form", part)
			}
		})
	}
}

func TestExtendFormOnSinglePart(t *testing.T) {
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "single-tut")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:    "single-tut",
		Title:   "Single Tutorial",
		Status:  store.StatusVerified,
		Parts:   []string{"part-01.md"},
		Created: time.Now(),
	}
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte("# Part 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/single-tut/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /single-tut/part-01.md = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `id="extendForm"`) {
		t.Error("single-part tutorial should render extend form on its only part")
	}
}

func TestExtendingPanelRendersAndAutoRefreshes(t *testing.T) {
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "test-extending")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:        "test-extending",
		Title:       "Test Extending",
		Status:      store.StatusExtending,
		Parts:       []string{"part-01.md", "part-02.md", "part-03.md"},
		PendingPart: "part-04.md",
		Created:     time.Now(),
	}
	for _, p := range tut.Parts {
		if err := os.WriteFile(filepath.Join(tutDir, p), []byte("# "+p), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/test-extending/part-03.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /test-extending/part-03.md = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()

	if !strings.Contains(body, "Generating part 4") {
		t.Error("extending panel should show 'Generating part 4'")
	}
	if !strings.Contains(body, `http-equiv="refresh"`) {
		t.Error("extending page should have meta refresh tag")
	}
	if strings.Contains(body, `id="extendForm"`) {
		t.Error("extend form should NOT appear while status is extending")
	}
}

func TestExtendingBadgeRendersOnList(t *testing.T) {
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "test-extending")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:        "test-extending",
		Title:       "Test Extending",
		Status:      store.StatusExtending,
		Parts:       []string{"part-01.md", "part-02.md"},
		PendingPart: "part-03.md",
		Created:     time.Now(),
	}
	for _, p := range tut.Parts {
		if err := os.WriteFile(filepath.Join(tutDir, p), []byte("# "+p), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `badge extending`) {
		t.Error("list page missing extending badge for tutorial with status=extending")
	}
}

func TestExtendingBadgeRendersOnPart(t *testing.T) {
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "test-extending")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{
		Slug:        "test-extending",
		Title:       "Test Extending",
		Status:      store.StatusExtending,
		Parts:       []string{"part-01.md", "part-02.md"},
		PendingPart: "part-03.md",
		Created:     time.Now(),
	}
	for _, p := range tut.Parts {
		if err := os.WriteFile(filepath.Join(tutDir, p), []byte("# "+p), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/test-extending/part-02.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /test-extending/part-02.md = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `badge extending`) {
		t.Error("part page missing extending badge for tutorial with status=extending")
	}
}
