package serve_test

import (
	"bytes"
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

func listPageBody(t *testing.T, dir string) string {
	t.Helper()
	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	return w.Body.String()
}

func tutorialCard(t *testing.T, body, slug string) string {
	t.Helper()
	slugAttr := `data-slug="` + slug + `"`
	slugIdx := strings.Index(body, slugAttr)
	if slugIdx < 0 {
		t.Fatalf("missing tutorial card for %q; body excerpt:\n%s", slug, body)
	}
	start := strings.LastIndex(body[:slugIdx], `<div class="tutorial"`)
	if start < 0 {
		t.Fatalf("missing tutorial card wrapper for %q; body excerpt:\n%s", slug, body)
	}
	endMarker := "</form>\n    </div>\n  </div>"
	end := strings.Index(body[slugIdx:], endMarker)
	if end < 0 {
		t.Fatalf("tutorial card for %q not closed; body excerpt:\n%s", slug, body[slugIdx:])
	}
	return body[start : slugIdx+end+len(endMarker)]
}

func TestDeleteRejectsForeignOrigin(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "victim", false)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/delete/victim", nil)
	req.Header.Set("Origin", "http://evil.example.com")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("cross-origin delete = %d, want %d", w.Code, http.StatusForbidden)
	}
	if _, err := os.Stat(tutDir); err != nil {
		t.Errorf("tutorial was deleted by a cross-origin request: %v", err)
	}
}

func TestDeleteRejectsForeignReferer(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "victim", false)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/delete/victim", nil)
	req.Header.Set("Referer", "http://evil.example.com/page")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("cross-referer delete = %d, want %d", w.Code, http.StatusForbidden)
	}
	if _, err := os.Stat(tutDir); err != nil {
		t.Errorf("tutorial was deleted by a cross-referer request: %v", err)
	}
}

func TestDeleteAllowsSameOrigin(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "victim", false)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/delete/victim", nil)
	req.Header.Set("Origin", "http://localhost:4242")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("same-origin delete = %d, want %d", w.Code, http.StatusSeeOther)
	}
	if _, err := os.Stat(tutDir); !os.IsNotExist(err) {
		t.Errorf("same-origin delete did not remove the tutorial dir: err=%v", err)
	}
}

func TestDeleteAllowsNoOriginHeaders(t *testing.T) {
	// A plain form POST from the page itself may carry no Origin and an
	// allowed Referer; a request with neither header (e.g. curl) is also
	// allowed — the guard only rejects a *present* foreign origin/referer.
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "victim", false)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/delete/victim", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("headerless delete = %d, want %d", w.Code, http.StatusSeeOther)
	}
	if _, err := os.Stat(tutDir); !os.IsNotExist(err) {
		t.Errorf("headerless delete did not remove the tutorial dir: err=%v", err)
	}
}

func TestPartRejectsNonPartFile(t *testing.T) {
	dir := t.TempDir()
	makeTestTutorial(t, dir, "test-series", true)

	srv := serve.NewServer(dir)
	// metadata.json lives in the tutorial dir but is not a part; it must not
	// be readable/renderable through the {part} route.
	req := httptest.NewRequest(http.MethodGet, "/test-series/metadata.json", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("GET /test-series/metadata.json = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestPartServesKnownPart(t *testing.T) {
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

func TestListPageRendersCardsAndVersions(t *testing.T) {
	dir := t.TempDir()

	// Three tutorials with distinct created times — exercises the flat
	// newest-first list, repo as searchable metadata (data-repo), and version
	// chips + the Versions filter row.
	mk := func(slug string, repo string, tools []store.Tool, created time.Time, progress *store.Progress) {
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
			Created: created,
			Repo:    repo,
			Tools:   tools,
		}
		if err := store.WriteMetadata(tutDir, tut); err != nil {
			t.Fatal(err)
		}
		if progress != nil {
			if err := store.WriteProgress(tutDir, progress); err != nil {
				t.Fatal(err)
			}
		}
	}
	now := time.Now()
	mk("synth-zig", "github.com/devenjarvis/lathe", []store.Tool{{Name: "zig", Version: "0.13.0"}}, now, &store.Progress{Part: "index.md", Ratio: 0.42, UpdatedAt: now})
	mk("compiler-go", "github.com/devenjarvis/lathe", []store.Tool{{Name: "go", Version: "1.22"}}, now.Add(-time.Hour), nil)
	mk("standalone", "", nil, now.Add(-2*time.Hour), nil)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	body := w.Body.String()

	// Repo stays searchable metadata even though grouping is gone.
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
	if !strings.Contains(body, `aria-label="Progress at 42%"`) {
		t.Error("list page missing progress label")
	}
	if !strings.Contains(body, `<span class="fill" style="width:42%"></span>`) {
		t.Error("list page missing progress bar width")
	}

	// All three cards render in one flat list, newest-first.
	posSynth := strings.Index(body, `data-slug="synth-zig"`)
	posCompiler := strings.Index(body, `data-slug="compiler-go"`)
	posStandalone := strings.Index(body, `data-slug="standalone"`)
	if posSynth == -1 || posCompiler == -1 || posStandalone == -1 {
		t.Fatalf("expected all three cards to render (synth=%d compiler=%d standalone=%d)", posSynth, posCompiler, posStandalone)
	}
	if posSynth >= posCompiler || posCompiler >= posStandalone {
		t.Errorf("cards should render newest-first: synth(%d) < compiler(%d) < standalone(%d)", posSynth, posCompiler, posStandalone)
	}
}

func TestListPageRendersSeriesCardProgress(t *testing.T) {
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "test-series")
	makeSeriesTutorialWithParts(t, dir, "test-series", 4)
	if err := store.WriteProgress(tutDir, &store.Progress{Part: "part-02.md", Ratio: 0.42, UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("WriteProgress: %v", err)
	}

	body := listPageBody(t, dir)
	card := tutorialCard(t, body, "test-series")

	if !strings.Contains(card, `aria-label="Saved at part 2 of 4"`) {
		t.Error("series card missing part-position aria label")
	}
	if !strings.Contains(card, `<span class="tutorial-progress-label">Part 2 of 4 (42%)</span>`) {
		t.Error("series card missing Part 2 of 4 current-part progress label")
	}
	if got := strings.Count(card, `class="segment`); got != 4 {
		t.Errorf("series card segment count = %d, want 4", got)
	}
	if got := strings.Count(card, `class="fill"`); got != 4 {
		t.Errorf("series card fill count = %d, want 4", got)
	}
	if !strings.Contains(card, `class="segment current"`) {
		t.Error("series card missing current segment")
	}
	if !strings.Contains(card, `<span class="segment" aria-hidden="true"><span class="fill" style="width:100%"></span></span>`) {
		t.Error("series card should fill completed parts")
	}
	if !strings.Contains(card, `<span class="segment current" aria-hidden="true"><span class="fill" style="width:42%"></span></span>`) {
		t.Error("series card should partially fill the saved part")
	}
	if strings.Contains(card, `Progress 42%`) || strings.Contains(card, `aria-label="Progress at 42%"`) {
		t.Error("series card should not render saved part scroll ratio as whole-series percentage")
	}
}

func TestListPageOmitsStaleSeriesCardProgress(t *testing.T) {
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "test-series")
	makeSeriesTutorialWithParts(t, dir, "test-series", 4)
	if err := store.WriteProgress(tutDir, &store.Progress{Part: "part-99.md", Ratio: 0.42, UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("WriteProgress: %v", err)
	}

	body := listPageBody(t, dir)
	card := tutorialCard(t, body, "test-series")

	if strings.Contains(card, `tutorial-progress`) {
		t.Error("list page should omit card progress for stale series progress part")
	}
}

func TestListPageOmitsStaleNonSeriesCardProgress(t *testing.T) {
	// A 1-part/legacy tutorial whose saved part no longer exists (e.g. index.md
	// promoted to part-01.md) must render no card progress bar — mirroring the
	// series branch's validity check.
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "legacy-tut", false)
	if err := store.WriteProgress(tutDir, &store.Progress{Part: "part-99.md", Ratio: 0.42, UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("WriteProgress: %v", err)
	}

	body := listPageBody(t, dir)
	card := tutorialCard(t, body, "legacy-tut")

	if strings.Contains(card, `tutorial-progress`) {
		t.Error("list page should omit card progress for stale non-series progress part")
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
	body := w.Body.String()
	if !strings.Contains(body, `id="saveProgressButton"`) {
		t.Error("tutorial page missing floating desktop progress control")
	}
	dockButtonMarkup := `<button type="button" class="btn btn-ghost btn-sm progress-button" data-progress-save>Save progress</button>`
	if strings.Count(body, dockButtonMarkup) != 1 {
		t.Errorf("tutorial page should render one dock progress control; body excerpt:\n%s", body)
	}
	if !strings.Contains(body, `id="progressStatus"`) {
		t.Error("tutorial page missing progress status live region")
	}
	if !strings.Contains(body, `data-slug="test-tutorial"`) || !strings.Contains(body, `data-part="index.md"`) {
		t.Error("tutorial page missing progress routing data on progress bar")
	}
}

func TestTutorialPageRendersCurrentProgressData(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)
	if err := store.WriteProgress(tutDir, &store.Progress{Part: "part-02.md", Ratio: 0.42, HeadingID: "next-step", UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("WriteProgress: %v", err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/test-series/part-02.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /test-series/part-02.md = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-slug="test-series"`) || !strings.Contains(body, `data-part="part-02.md"`) {
		t.Error("progress bar missing current tutorial/part data")
	}
	if !strings.Contains(body, `data-saved-progress="0.42"`) {
		t.Error("progress bar missing current saved progress")
	}
	if !strings.Contains(body, `data-saved-heading-id="next-step"`) {
		t.Error("progress bar missing current saved heading")
	}
	if !strings.Contains(body, `id="savedProgressMarker"`) {
		t.Error("tutorial page missing progress marker element")
	}
	ratioRestore := strings.Index(body, "var ratio = savedProgressRatio();")
	headingRestore := strings.Index(body, "var headingID = savedProgressHeadingID();")
	if ratioRestore < 0 || headingRestore < 0 {
		t.Fatal("tutorial page missing saved progress restore script")
	}
	if ratioRestore > headingRestore {
		t.Error("restore should prefer exact saved ratio before falling back to saved heading")
	}
}

func TestBylineShowsModelAndVoiceReveal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir()) // hermetic voice resolution (no stray custom voices)
	tutDir := filepath.Join(dir, "synth")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte("# Synth"), 0644); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{Slug: "synth", Title: "Synth", Status: store.StatusUnverified, Parts: []string{"part-01.md"}, Voice: "companion", Model: "Claude Opus 4.8"}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/synth/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	header := articleHeader(t, w.Body.String())
	// Authorship moved to the byline under the title; the old footer is gone.
	if strings.Contains(w.Body.String(), `class="article-footer"`) {
		t.Error("article-footer should be removed — authorship now lives in the byline")
	}
	if !strings.Contains(header, `class="article-byline"`) {
		t.Errorf("article-header missing the author byline; header:\n%s", header)
	}
	if !strings.Contains(header, "Generated by") || !strings.Contains(header, "Claude Opus 4.8") {
		t.Error("byline should disclose the LLM author and the recorded model")
	}
	// A resolvable voice renders as an inline <details> reveal whose body is the
	// voice spec rendered as markdown (not a raw <pre> dump).
	if !strings.Contains(header, `class="voice-reveal"`) {
		t.Errorf("byline missing the voice-reveal <details>; header:\n%s", header)
	}
	if !strings.Contains(header, `class="voice-reveal-body"`) {
		t.Errorf("byline missing the rendered voice-reveal body container; header:\n%s", header)
	}
	if !strings.Contains(header, "companion") {
		t.Error("byline should name the recorded voice")
	}
	// The body must be rendered markdown: companion's spec opens with "# Companion",
	// so the reveal should carry a rendered <h1> with that text — never the literal
	// "# Companion" markdown.
	if !strings.Contains(header, ">Companion</h1>") {
		t.Errorf("voice-reveal body should render the spec markdown to HTML; header:\n%s", header)
	}
	if strings.Contains(header, "# Companion") {
		t.Error("voice-reveal body should not contain raw markdown heading syntax")
	}
}

func TestBylineFallsBackToGenericLLMAndOmitsVoice(t *testing.T) {
	dir := t.TempDir()
	makeTestTutorial(t, dir, "test-tutorial", false) // no Voice, no Model recorded

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/test-tutorial/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	header := articleHeader(t, w.Body.String())
	if !strings.Contains(header, "Generated by") || !strings.Contains(header, "an LLM") {
		t.Error("byline should fall back to a generic 'an LLM' when no model is recorded")
	}
	if strings.Contains(header, "· voice") {
		t.Error("byline should omit the voice clause when no voice is recorded")
	}
	if strings.Contains(header, "voice-reveal") {
		t.Error("byline should render no voice-reveal when no voice is recorded")
	}
}

func TestBylineDegradesWhenVoiceUnresolved(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir()) // empty voices dir → custom voice won't resolve
	tutDir := filepath.Join(dir, "ghost")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte("# Ghost"), 0644); err != nil {
		t.Fatal(err)
	}
	// A custom voice that no longer exists on disk: name shows, but no reveal.
	tut := &store.Tutorial{Slug: "ghost", Title: "Ghost", Status: store.StatusUnverified, Parts: []string{"part-01.md"}, Voice: "deleted-voice", Model: "Claude Opus 4.8"}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/ghost/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /ghost/part-01.md = %d, want %d (must not 500 on a missing voice)", w.Code, http.StatusOK)
	}
	header := articleHeader(t, w.Body.String())
	if !strings.Contains(header, "deleted-voice") {
		t.Error("byline should still name the recorded voice even when it can't be resolved")
	}
	if strings.Contains(header, "voice-reveal") {
		t.Error("byline should render no voice-reveal when the voice can't be resolved")
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

			// The breadcrumb part segment now lives inside the part-picker's
			// <summary> (a <details> disclosure listing every part); the ›
			// separator still precedes it.
			if !strings.Contains(body, `<span class="sep">›</span>`) {
				t.Errorf("missing breadcrumb separator for %s", tc.part)
			}
			wantCrumb := `<summary>` + tc.wantCrumb
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

func TestSeriesRedirectUsesProgressPart(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)
	if err := store.WriteProgress(tutDir, &store.Progress{Part: "part-02.md", Ratio: 0.5, UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("WriteProgress: %v", err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/test-series/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("GET /test-series/ = %d, want %d (redirect)", w.Code, http.StatusFound)
	}
	if loc := w.Header().Get("Location"); loc != "/test-series/part-02.md" {
		t.Errorf("redirect Location = %q, want %q", loc, "/test-series/part-02.md")
	}
}

func TestSeriesRedirectIgnoresStaleProgressPart(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)
	if err := store.WriteProgress(tutDir, &store.Progress{Part: "part-99.md", Ratio: 0.5, UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("WriteProgress: %v", err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/test-series/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("GET /test-series/ = %d, want %d (redirect)", w.Code, http.StatusFound)
	}
	if loc := w.Header().Get("Location"); loc != "/test-series/part-01.md" {
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

func TestProgressEndpointSavesProgress(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)
	before, err := os.ReadFile(filepath.Join(tutDir, "metadata.json"))
	if err != nil {
		t.Fatalf("ReadFile before: %v", err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/progress/test-series/part-02.md", bytes.NewBufferString(`{"ratio":0.42,"heading_id":"next-step"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:4242")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("POST /-/progress/test-series/part-02.md = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var response struct {
		Progress *store.Progress `json:"progress"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode progress response: %v", err)
	}
	if response.Progress == nil {
		t.Fatal("response progress = nil, want saved progress")
	}
	if response.Progress.Part != "part-02.md" || response.Progress.Ratio != 0.42 || response.Progress.HeadingID != "next-step" {
		t.Errorf("response progress = %+v, want part/ratio/heading to match request", response.Progress)
	}
	after, err := os.ReadFile(filepath.Join(tutDir, "metadata.json"))
	if err != nil {
		t.Fatalf("ReadFile after: %v", err)
	}
	if string(after) != string(before) {
		t.Error("progress save rewrote metadata.json")
	}

	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if tut.Progress == nil {
		t.Fatal("metadata progress = nil, want saved progress")
	}
	if tut.Progress.Part != "part-02.md" {
		t.Errorf("metadata progress part = %q, want part-02.md", tut.Progress.Part)
	}
	if tut.Progress.Ratio != 0.42 {
		t.Errorf("metadata progress ratio = %v, want 0.42", tut.Progress.Ratio)
	}
	if tut.Progress.HeadingID != "next-step" {
		t.Errorf("metadata progress heading = %q, want next-step", tut.Progress.HeadingID)
	}
	if tut.Progress.UpdatedAt.IsZero() {
		t.Error("metadata progress updated_at is zero")
	}
}

func TestProgressEndpointRejectsForeignOrigin(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/progress/test-series/part-01.md", bytes.NewBufferString(`{"ratio":0.5}`))
	req.Header.Set("Origin", "http://evil.example.com")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("foreign-origin progress = %d, want %d", w.Code, http.StatusForbidden)
	}
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if tut.Progress != nil {
		t.Errorf("foreign-origin progress wrote metadata: %+v", tut.Progress)
	}
}

func TestProgressEndpointRejectsUnknownTutorial(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/progress/missing/part-01.md", bytes.NewBufferString(`{"ratio":0.5}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("unknown tutorial progress = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestProgressEndpointRejectsUnknownPart(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/progress/test-series/part-99.md", bytes.NewBufferString(`{"ratio":0.5}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("unknown part progress = %d, want %d", w.Code, http.StatusNotFound)
	}
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if tut.Progress != nil {
		t.Errorf("unknown part progress wrote metadata: %+v", tut.Progress)
	}
}

func TestProgressEndpointRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/progress/test-series/part-01.md", bytes.NewBufferString(`{nope`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON progress = %d, want %d", w.Code, http.StatusBadRequest)
	}
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if tut.Progress != nil {
		t.Errorf("invalid JSON progress wrote metadata: %+v", tut.Progress)
	}
}

func TestProgressEndpointRejectsMissingRatio(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/progress/test-series/part-01.md", bytes.NewBufferString(`{"heading_id":"intro"}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("missing-ratio progress = %d, want %d", w.Code, http.StatusBadRequest)
	}
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if tut.Progress != nil {
		t.Errorf("missing-ratio progress wrote metadata: %+v", tut.Progress)
	}
}

func TestProgressEndpointClampsProgress(t *testing.T) {
	cases := []struct {
		name string
		body string
		want float64
	}{
		{"below zero", `{"ratio":-0.5}`, 0},
		{"above one", `{"ratio":1.5}`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tutDir := makeTestTutorial(t, dir, "test-series", true)
			srv := serve.NewServer(dir)

			req := httptest.NewRequest(http.MethodPost, "/-/progress/test-series/part-01.md", bytes.NewBufferString(tc.body))
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("progress save = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
			}
			tut, err := store.ReadMetadata(tutDir)
			if err != nil {
				t.Fatalf("ReadMetadata: %v", err)
			}
			if tut.Progress == nil {
				t.Fatal("Progress = nil, want saved progress")
			}
			if tut.Progress.Ratio != tc.want {
				t.Errorf("Progress.Ratio = %v, want %v", tut.Progress.Ratio, tc.want)
			}
		})
	}
}

func TestProgressEndpointRejectsOversizeBody(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)

	// A body well past maxProgressBytes (1 KiB) should be a 413, not a 400.
	big := `{"ratio":0.5,"heading_id":"` + strings.Repeat("a", 4096) + `"}`
	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodPost, "/-/progress/test-series/part-01.md", bytes.NewBufferString(big))
	req.Header.Set("Origin", "http://localhost:4242")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize progress body = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if tut.Progress != nil {
		t.Errorf("oversize progress wrote metadata: %+v", tut.Progress)
	}
}

func TestProgressEndpointAutoSaveRejectsCrossPartRegression(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)

	// Pre-set progress at part-02 with ratio 0.6
	err := store.WriteProgress(tutDir, &store.Progress{
		Part:      "part-02.md",
		Ratio:     0.6,
		HeadingID: "later-section",
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("WriteProgress: %v", err)
	}

	srv := serve.NewServer(dir)
	// Auto-save to part-01 (an earlier part) — should be rejected
	req := httptest.NewRequest(http.MethodPost, "/-/progress/test-series/part-01.md", bytes.NewBufferString(`{"ratio":0.9,"heading_id":"intro","auto":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:4242")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("cross-part regression = %d, want %d", w.Code, http.StatusOK)
	}
	var response struct {
		Progress *store.Progress `json:"progress"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// The response should return the existing (later) progress, not the
	// incoming request
	if response.Progress.Part != "part-02.md" {
		t.Errorf("response part = %q, want part-02.md (existing)", response.Progress.Part)
	}
	if response.Progress.Ratio != 0.6 {
		t.Errorf("response ratio = %v, want 0.6 (existing)", response.Progress.Ratio)
	}

	// Verify the file on disk was not changed
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if tut.Progress.Part != "part-02.md" {
		t.Errorf("on-disk part = %q, want part-02.md", tut.Progress.Part)
	}
}

func TestProgressEndpointAutoSaveRejectsSamePartRatioRegression(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)

	// Pre-set progress at part-01 with ratio 0.7
	err := store.WriteProgress(tutDir, &store.Progress{
		Part:      "part-01.md",
		Ratio:     0.7,
		HeadingID: "deep-section",
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("WriteProgress: %v", err)
	}

	srv := serve.NewServer(dir)
	// Auto-save same part with lower ratio — should be rejected
	req := httptest.NewRequest(http.MethodPost, "/-/progress/test-series/part-01.md", bytes.NewBufferString(`{"ratio":0.3,"heading_id":"intro","auto":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:4242")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("same-part ratio regression = %d, want %d", w.Code, http.StatusOK)
	}
	var response struct {
		Progress *store.Progress `json:"progress"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Progress.Ratio != 0.7 {
		t.Errorf("response ratio = %v, want 0.7 (existing)", response.Progress.Ratio)
	}
}

func TestProgressEndpointAutoSaveAllowsForwardProgress(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)

	// Pre-set progress at part-01 with ratio 0.5
	err := store.WriteProgress(tutDir, &store.Progress{
		Part:      "part-01.md",
		Ratio:     0.5,
		HeadingID: "intro",
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("WriteProgress: %v", err)
	}

	srv := serve.NewServer(dir)
	// Auto-save to part-02 (a later part) — should succeed
	req := httptest.NewRequest(http.MethodPost, "/-/progress/test-series/part-02.md", bytes.NewBufferString(`{"ratio":0.3,"heading_id":"new-section","auto":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:4242")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("forward progress = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var response struct {
		Progress *store.Progress `json:"progress"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Progress.Part != "part-02.md" {
		t.Errorf("response part = %q, want part-02.md", response.Progress.Part)
	}
	if response.Progress.Ratio != 0.3 {
		t.Errorf("response ratio = %v, want 0.3", response.Progress.Ratio)
	}

	// Verify the file on disk was updated
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if tut.Progress.Part != "part-02.md" {
		t.Errorf("on-disk part = %q, want part-02.md", tut.Progress.Part)
	}
}

func TestProgressEndpointManualSaveAlwaysWritesThrough(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)

	// Pre-set progress at part-02 with ratio 0.8
	err := store.WriteProgress(tutDir, &store.Progress{
		Part:      "part-02.md",
		Ratio:     0.8,
		HeadingID: "late-section",
		UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("WriteProgress: %v", err)
	}

	srv := serve.NewServer(dir)
	// Manual save (auto omitted, defaults false) to an earlier part with
	// lower ratio — the user explicitly chose to resume here, so it must
	// write through.
	req := httptest.NewRequest(http.MethodPost, "/-/progress/test-series/part-01.md", bytes.NewBufferString(`{"ratio":0.2,"heading_id":"intro"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:4242")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("manual save = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var response struct {
		Progress *store.Progress `json:"progress"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Manual save must write to the requested part, not the existing one
	if response.Progress.Part != "part-01.md" {
		t.Errorf("response part = %q, want part-01.md", response.Progress.Part)
	}
	if response.Progress.Ratio != 0.2 {
		t.Errorf("response ratio = %v, want 0.2", response.Progress.Ratio)
	}

	// Verify the file on disk was overwritten
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if tut.Progress.Part != "part-01.md" {
		t.Errorf("on-disk part = %q, want part-01.md", tut.Progress.Part)
	}
	if tut.Progress.Ratio != 0.2 {
		t.Errorf("on-disk ratio = %v, want 0.2", tut.Progress.Ratio)
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
	// With JS the status poller updates the extend region in place; the full-page
	// meta-refresh now only fires as a <noscript> fallback.
	if !strings.Contains(body, `<noscript><meta http-equiv="refresh" content="5"></noscript>`) {
		t.Error("extending page should carry the noscript meta-refresh fallback")
	}
	if !strings.Contains(body, `id="extendRegion"`) {
		t.Error("extending page should render the #extendRegion swap container")
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

func TestStaticKatexAssets(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)

	cases := []struct {
		name     string
		wantType string
		wantBody string
	}{
		{"katex.min.js", "application/javascript", "katex"},
		{"katex-auto-render.min.js", "application/javascript", "renderMathInElement"},
		{"katex.min.css", "text/css", ".katex"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/_static/"+c.name, nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("GET /_static/%s = %d, want %d", c.name, w.Code, http.StatusOK)
			}
			if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, c.wantType) {
				t.Errorf("%s Content-Type = %q, want prefix %q", c.name, ct, c.wantType)
			}
			if !strings.Contains(w.Body.String(), c.wantBody) {
				t.Errorf("%s body missing %q", c.name, c.wantBody)
			}
		})
	}
}

func TestStaticKatexFonts(t *testing.T) {
	dir := t.TempDir()
	srv := serve.NewServer(dir)

	// A representative subset of the KaTeX woff2 faces. The vendored
	// katex.min.css has its url(fonts/...) references flattened to url(...)
	// so they resolve to the same flat /_static/<name>.woff2 scheme as the
	// text fonts.
	fonts := []string{
		"KaTeX_Main-Regular.woff2",
		"KaTeX_Math-Italic.woff2",
		"KaTeX_AMS-Regular.woff2",
		"KaTeX_Size2-Regular.woff2",
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
			body := w.Body.Bytes()
			if len(body) < 4 || string(body[:4]) != "wOF2" {
				t.Errorf("%s missing wOF2 signature", name)
			}
		})
	}
}

func TestPartPageLoadsKatex(t *testing.T) {
	dir := t.TempDir()
	tutDir := filepath.Join(dir, "mathy")
	if err := os.MkdirAll(tutDir, 0755); err != nil {
		t.Fatal(err)
	}
	part := "# Mathy\n\nThe loss is $D_{KL}(p \\| q)$ in disguise.\n"
	if err := os.WriteFile(filepath.Join(tutDir, "part-01.md"), []byte(part), 0644); err != nil {
		t.Fatal(err)
	}
	tut := &store.Tutorial{Slug: "mathy", Title: "Mathy", Status: store.StatusUnverified, Parts: []string{"part-01.md"}}
	if err := store.WriteMetadata(tutDir, tut); err != nil {
		t.Fatal(err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/mathy/part-01.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /mathy/part-01.md = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	// TeX must arrive uncorrupted for the client-side renderer.
	if !strings.Contains(body, `$D_{KL}(p \| q)$`) {
		t.Error("part page corrupted the TeX before it reached the browser")
	}
	// The KaTeX loader mirrors the mermaid one: inline script, local bundle.
	if !strings.Contains(body, "/_static/katex.min.js") {
		t.Error("part page missing the KaTeX bundle loader")
	}
	if !strings.Contains(body, "renderMathInElement") {
		t.Error("part page missing the auto-render invocation")
	}
}

func sameIntSlice(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func postProgress(t *testing.T, srv *serve.Server, slug, part, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/-/progress/"+slug+"/"+part, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:4242")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestProgressEndpointSavesExercises(t *testing.T) {
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)
	srv := serve.NewServer(dir)

	w := postProgress(t, srv, "test-series", "part-02.md", `{"ratio":0.42,"exercises":[0,2]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST progress = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	state, err := store.ReadExercises(tutDir)
	if err != nil {
		t.Fatalf("ReadExercises: %v", err)
	}
	if !sameIntSlice(state["part-02.md"], []int{0, 2}) {
		t.Errorf("saved exercises = %v, want [0 2]", state["part-02.md"])
	}
}

func TestProgressEndpointOmittedExercisesCreatesNoSidecar(t *testing.T) {
	// A part with no exercises sends no "exercises" field (nil slice); the handler
	// must skip the write entirely rather than create an empty exercises.json.
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)
	srv := serve.NewServer(dir)

	w := postProgress(t, srv, "test-series", "part-02.md", `{"ratio":0.42,"heading_id":"next-step"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST progress = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(tutDir, "exercises.json")); !os.IsNotExist(err) {
		t.Errorf("exercises.json should not exist after an exercise-less save, stat err = %v", err)
	}
}

func TestProgressEndpointEmptyExercisesClearsEntry(t *testing.T) {
	// A present-but-empty array means the reader unchecked everything: it writes
	// through and clears the part's saved entry.
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)
	srv := serve.NewServer(dir)

	if w := postProgress(t, srv, "test-series", "part-02.md", `{"ratio":0.42,"exercises":[0,1]}`); w.Code != http.StatusOK {
		t.Fatalf("seed POST progress = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if w := postProgress(t, srv, "test-series", "part-02.md", `{"ratio":0.5,"exercises":[]}`); w.Code != http.StatusOK {
		t.Fatalf("clear POST progress = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	state, err := store.ReadExercises(tutDir)
	if err != nil {
		t.Fatalf("ReadExercises: %v", err)
	}
	if _, ok := state["part-02.md"]; ok {
		t.Errorf("part-02 entry should be cleared by an empty array, got %v", state["part-02.md"])
	}
}

func TestProgressEndpointSavesExercisesDespiteMonotonicGuard(t *testing.T) {
	// Headline design point: exercise state persists independently of the monotonic
	// reading-progress guard. An *auto* save for an earlier part is rejected for
	// progress (no regression) yet still records that part's boxes.
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)
	// High-water reading progress sits at the later part-02.
	if err := store.WriteProgress(tutDir, &store.Progress{Part: "part-02.md", Ratio: 0.8, UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("WriteProgress: %v", err)
	}
	srv := serve.NewServer(dir)

	// Auto-save while reviewing the earlier part-01, checking a box there.
	w := postProgress(t, srv, "test-series", "part-01.md", `{"ratio":0.1,"exercises":[0],"auto":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST progress = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	// Progress must not regress: the response still reports part-02 as the position.
	var resp struct {
		Progress *store.Progress `json:"progress"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Progress == nil || resp.Progress.Part != "part-02.md" {
		t.Errorf("progress regressed: got %+v, want it held at part-02.md", resp.Progress)
	}

	// ...but the part-01 exercise box must still be recorded.
	state, err := store.ReadExercises(tutDir)
	if err != nil {
		t.Fatalf("ReadExercises: %v", err)
	}
	if !sameIntSlice(state["part-01.md"], []int{0}) {
		t.Errorf("part-01 exercises = %v, want [0] recorded despite the progress guard", state["part-01.md"])
	}
}

func TestPartPageRendersSavedExercises(t *testing.T) {
	// The renderPart read path surfaces the current part's saved boxes to the
	// template as data-saved-exercises, so the client can restore them on load.
	dir := t.TempDir()
	tutDir := makeTestTutorial(t, dir, "test-series", true)
	if err := store.WriteExercisePart(tutDir, "part-02.md", []int{0, 2}); err != nil {
		t.Fatalf("WriteExercisePart: %v", err)
	}

	srv := serve.NewServer(dir)
	req := httptest.NewRequest(http.MethodGet, "/test-series/part-02.md", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /test-series/part-02.md = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `data-saved-exercises="0,2"`) {
		t.Errorf("part page missing restored data-saved-exercises; body excerpt:\n%s", w.Body.String())
	}
}
