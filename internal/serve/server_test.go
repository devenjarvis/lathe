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

	// Sidebar should contain the back-link and the on-page TOC (no parts list).
	if !strings.Contains(body, `class="back-link"`) || !strings.Contains(body, "All tutorials") {
		t.Error("sidebar missing back-link to /")
	}
	if !strings.Contains(body, "On this page") {
		t.Error("sidebar missing 'On this page' label")
	}
	if !strings.Contains(body, `href="#setup"`) {
		t.Errorf("sidebar TOC missing anchor to first h2; body excerpt:\n%s", body)
	}
	if !strings.Contains(body, `href="#wire-it-up"`) {
		t.Error("sidebar TOC missing anchor to second h2")
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

func TestStaticMarkedAndDompurifyAssets(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)

	cases := []struct {
		name     string
		path     string
		minBytes int
		mustHave string
	}{
		{"marked", "/_static/marked.min.js", 10_000, "marked"},
		{"dompurify", "/_static/dompurify.min.js", 10_000, "DOMPurify"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want %d", tc.path, w.Code, http.StatusOK)
			}
			if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
				t.Errorf("%s Content-Type = %q, want application/javascript", tc.name, ct)
			}
			if w.Body.Len() < tc.minBytes {
				t.Errorf("%s bundle suspiciously small (%d bytes)", tc.name, w.Body.Len())
			}
			if !strings.Contains(w.Body.String(), tc.mustHave) {
				t.Errorf("%s bundle missing identifier %q", tc.name, tc.mustHave)
			}
		})
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
