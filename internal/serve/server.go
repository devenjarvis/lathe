package serve

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/devenjarvis/lathe/internal/store"
	"github.com/devenjarvis/lathe/internal/voice"
)

//go:embed layout.html list.html components.html
var templateFS embed.FS

// styles.css is the entire design system (tokens + components). It's loaded
// once at startup and injected inline via the {{define "head"}} partial,
// mirroring how HighlightCSS is injected — no extra request, no FOUC.
//
//go:embed styles.css
var stylesCSS string

//go:embed static/mermaid.min.js
//go:embed static/katex.min.js static/katex-auto-render.min.js static/katex.min.css
//go:embed static/favicon.svg
//go:embed static/fonts/fraunces.woff2 static/fonts/newsreader.woff2 static/fonts/newsreader-italic.woff2 static/fonts/jetbrains-mono.woff2
//go:embed static/fonts/KaTeX_*.woff2
var staticFS embed.FS

type Server struct {
	tutorialsDir string
	layoutTmpl   *template.Template
	listTmpl     *template.Template
	highlightCSS template.CSS
	designCSS    template.CSS
}

func NewServer(tutorialsDir string) *Server {
	funcMap := template.FuncMap{
		"add":          func(a, b int) int { return a + b },
		"cardProgress": cardProgress,
	}
	// components.html is parsed into both template sets so its shared partials
	// ({{define "head"}}, "badge", "themeToggle") are available to each page.
	layoutTmpl := template.Must(template.New("layout.html").Funcs(funcMap).ParseFS(templateFS, "components.html", "layout.html"))
	listTmpl := template.Must(template.New("list.html").Funcs(funcMap).ParseFS(templateFS, "components.html", "list.html"))
	css, err := HighlightCSS()
	if err != nil {
		panic(fmt.Sprintf("lathe: failed to build syntax-highlight CSS: %v", err))
	}
	return &Server{
		tutorialsDir: tutorialsDir,
		layoutTmpl:   layoutTmpl,
		listTmpl:     listTmpl,
		highlightCSS: css,
		designCSS:    template.CSS(stylesCSS),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_static/{name}", s.handleStatic)
	mux.HandleFunc("GET /{$}", s.handleList)
	mux.HandleFunc("GET /{slug}/", s.handleTutorial)
	mux.HandleFunc("GET /{slug}/{part}", s.handlePart)
	mux.HandleFunc("POST /-/delete/{slug}", s.handleDelete)
	mux.HandleFunc("POST /-/ask/{slug}/{part}", s.handleAsk)
	mux.HandleFunc("POST /-/progress/{slug}/{part}", s.handleProgress)
	mux.HandleFunc("POST /-/extend/{slug}", s.handleExtend)
	mux.HandleFunc("POST /-/verify/{slug}", s.handleVerify)
	mux.HandleFunc("GET /-/status/{slug}/{part}", s.handleStatus)
	return mux
}

// staticAssets whitelists the embedded files we expose under /_static/. Keeping
// it explicit means no embed.FS path can be coaxed out of the binary by an
// unexpected route — even though the {name} wildcard already can't contain a
// slash, this is the cheap belt-and-suspenders check.
var staticAssets = map[string]string{
	"mermaid.min.js":           "application/javascript; charset=utf-8",
	"katex.min.js":             "application/javascript; charset=utf-8",
	"katex-auto-render.min.js": "application/javascript; charset=utf-8",
	"katex.min.css":            "text/css; charset=utf-8",
	"favicon.svg":              "image/svg+xml",
	"fraunces.woff2":           "font/woff2",
	"newsreader.woff2":         "font/woff2",
	"newsreader-italic.woff2":  "font/woff2",
	"jetbrains-mono.woff2":     "font/woff2",
}

// The KaTeX math fonts join the whitelist from the embed FS itself rather than
// by hand-listing all 20: the //go:embed glob (static/fonts/KaTeX_*.woff2) is
// the explicit boundary, and reading it back keeps the whitelist in lockstep
// across KaTeX upgrades. The vendored katex.min.css references them flat
// (url(KaTeX_…)) so they resolve under the same /_static/<name>.woff2 route as
// the text fonts.
func init() {
	entries, err := staticFS.ReadDir("static/fonts")
	if err != nil {
		panic(fmt.Sprintf("lathe: embedded font dir unreadable: %v", err))
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "KaTeX_") && strings.HasSuffix(name, ".woff2") {
			staticAssets[name] = "font/woff2"
		}
	}
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	contentType, ok := staticAssets[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Fonts are served at flat /_static/<name>.woff2 (single-segment route,
	// whitelisted above) but live under static/fonts/ on disk.
	embedPath := "static/" + name
	if strings.HasSuffix(name, ".woff2") {
		embedPath = "static/fonts/" + name
	}
	data, err := staticFS.ReadFile(embedPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(data)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.tutorialsDir)
	if err != nil {
		http.Error(w, "could not read tutorials", http.StatusInternalServerError)
		return
	}
	var tutorials []*store.Tutorial
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		tut, err := store.ReadMetadata(filepath.Join(s.tutorialsDir, e.Name()))
		if err != nil {
			continue
		}
		tutorials = append(tutorials, tut)
	}
	// Flat, newest-first ordering — matches the previous within-group default so
	// the JS-off initial order is unchanged. The client sort control re-orders
	// from here.
	sort.SliceStable(tutorials, func(a, b int) bool {
		return tutorials[a].Created.After(tutorials[b].Created)
	})
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		type ProgressJSON struct {
			Slug     string        `json:"slug"`
			Progress *CardProgress `json:"progress"`
		}
		var list []ProgressJSON
		for _, tut := range tutorials {
			list = append(list, ProgressJSON{
				Slug:     tut.Slug,
				Progress: cardProgress(tut),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
		return
	}
	var buf bytes.Buffer
	if err := s.listTmpl.Execute(&buf, map[string]any{
		"Tutorials":    tutorials,
		"CSS":          s.designCSS,
		"HighlightCSS": s.highlightCSS,
	}); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func (s *Server) safeTutorialPath(parts ...string) (string, bool) {
	p := filepath.Join(append([]string{s.tutorialsDir}, parts...)...)
	if !strings.HasPrefix(p, s.tutorialsDir+string(filepath.Separator)) {
		return "", false
	}
	return p, true
}

func (s *Server) handleTutorial(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	tutDir, ok := s.safeTutorialPath(slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Any tutorial with parts (single or series) lives in part-NN.md files, not
	// index.md. Prefer a valid saved-progress part when one is saved, otherwise keep
	// the historical first-part redirect. The index.md fallback is only for
	// legacy tutorials that were never split into parts.
	if len(tut.Parts) > 0 {
		part := tut.Parts[0]
		if tut.Progress != nil && isKnownPart(tut, tut.Progress.Part) {
			part = tut.Progress.Part
		}
		http.Redirect(w, r, fmt.Sprintf("/%s/%s", slug, part), http.StatusFound)
		return
	}
	s.renderPart(w, tut, tutDir, "index.md")
}

func (s *Server) handlePart(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	part := r.PathValue("part")
	tutDir, ok := s.safeTutorialPath(slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Only render files the metadata declares as parts (plus the legacy
	// index.md fallback). Without this, the {part} route would happily read and
	// render any file in the tutorial dir — metadata.json, verify-result.json.
	if !isKnownPart(tut, part) {
		http.NotFound(w, r)
		return
	}
	s.renderPart(w, tut, tutDir, part)
}

// isKnownPart reports whether part is one of the tutorial's declared parts or
// the legacy single-file index.md.
func isKnownPart(tut *store.Tutorial, part string) bool {
	// index.md is the legacy single-file fallback — valid only when the
	// tutorial was never split into parts (matching handleTutorial).
	if part == "index.md" {
		return len(tut.Parts) == 0
	}
	for _, p := range tut.Parts {
		if p == part {
			return true
		}
	}
	return false
}

// partIndex returns the 0-based position of part in tut.Parts, or -1 when the
// part is not found. Used by the cross-part monotonic guard to compare part
// ordering (lower index = earlier in the series).
func partIndex(tut *store.Tutorial, part string) int {
	for i, p := range tut.Parts {
		if p == part {
			return i
		}
	}
	return -1
}

// isLastPart reports whether part is the final declared part of the series (or
// the sole part). A legacy index.md tutorial (no parts) counts as last.
func isLastPart(tut *store.Tutorial, part string) bool {
	if len(tut.Parts) == 0 {
		return true
	}
	return partIndex(tut, part) == len(tut.Parts)-1
}

// partAfter returns the part immediately following part in the series as a
// SeriesEntry (Slug/Title/Number), or ok=false when part is unknown or already
// last. handleStatus uses it to point the "Part N is ready" link at the part an
// extend just appended.
func partAfter(tut *store.Tutorial, part string) (SeriesEntry, bool) {
	idx := partIndex(tut, part)
	if idx < 0 || idx+1 >= len(tut.Parts) {
		return SeriesEntry{}, false
	}
	next := tut.Parts[idx+1]
	return SeriesEntry{
		Slug:   next,
		Title:  store.SlugToTitle(strings.TrimSuffix(next, ".md")),
		Number: idx + 2,
	}, true
}

// verifyMeta surfaces the verifier's recorded result and the friendly "Verified
// <date>" string for a tutorial's current status. On failure the result carries
// the part/step/error detail; on verified/skipped it carries the CheckedAt
// timestamp formatted as a date. Best-effort: a missing or malformed
// verify-result.json (or an unparseable timestamp) yields a nil result and/or an
// empty date, and the caller simply renders no panel and no date.
func verifyMeta(tut *store.Tutorial, tutDir string) (*store.VerifyResult, string) {
	var verifyResult *store.VerifyResult
	switch tut.Status {
	case store.StatusFailed, store.StatusVerified, store.StatusSkipped:
		if vr, err := store.ReadVerifyResult(tutDir); err == nil {
			verifyResult = vr
		}
	}
	var verifiedDate string
	if verifyResult != nil && (tut.Status == store.StatusVerified || tut.Status == store.StatusSkipped) {
		if ts, err := time.Parse(time.RFC3339, verifyResult.CheckedAt); err == nil {
			verifiedDate = ts.Format("Jan 2, 2006")
		}
	}
	return verifyResult, verifiedDate
}

// sameOrigin reports whether a state-changing request originated from a page
// served by this server. It rejects a *present* Origin or Referer that points
// elsewhere — the defense against CSRF, where another site (or a LAN device)
// POSTs to our predictable localhost port. A request with neither header (e.g.
// curl, or a same-origin form POST that omits Origin) is allowed.
func sameOrigin(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		return isLocalOrigin(origin)
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		return isLocalOrigin(ref)
	}
	return true
}

// isLocalOrigin reports whether a URL's host is loopback. We match on host
// rather than an exact port because the listen port is configurable (--port).
func isLocalOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	slug := r.PathValue("slug")
	tutDir, ok := s.safeTutorialPath(slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(tutDir)
	if err != nil || !info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if err := os.RemoveAll(tutDir); err != nil {
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// SeriesEntry is a row in the "In this series" list rendered at the bottom of
// each series part. Title is precomputed from the part filename so the template
// doesn't need to call into the store package.
type SeriesEntry struct {
	Slug    string
	Title   string
	Number  int
	Current bool
}

func (s *Server) renderPart(w http.ResponseWriter, tut *store.Tutorial, tutDir, part string) {
	src, err := os.ReadFile(filepath.Join(tutDir, part))
	if err != nil {
		http.Error(w, "part not found", http.StatusNotFound)
		return
	}
	content, toc, err := RenderMarkdownWithTOC(src)
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}

	var prevPart, nextPart, prevTitle, nextTitle string
	var prevNumber, nextNumber, currentNumber int
	var seriesTOC []SeriesEntry
	isLast := true
	if tut.IsSeries() {
		seriesTOC = make([]SeriesEntry, 0, len(tut.Parts))
		for i, p := range tut.Parts {
			seriesTOC = append(seriesTOC, SeriesEntry{
				Slug:    p,
				Title:   store.SlugToTitle(strings.TrimSuffix(p, ".md")),
				Number:  i + 1,
				Current: p == part,
			})
			if p == part {
				currentNumber = i + 1
				isLast = i == len(tut.Parts)-1
				if i > 0 {
					prevPart = tut.Parts[i-1]
					prevTitle = store.SlugToTitle(strings.TrimSuffix(prevPart, ".md"))
					prevNumber = i
				}
				if i < len(tut.Parts)-1 {
					nextPart = tut.Parts[i+1]
					nextTitle = store.SlugToTitle(strings.TrimSuffix(nextPart, ".md"))
					nextNumber = i + 2
				}
			}
		}
	}

	// Surface the verifier's recorded result (failure detail) and the friendly
	// "Verified <date>" provenance string. Best-effort: a missing or malformed
	// verify-result.json simply renders no panel and no date.
	verifyResult, verifiedDate := verifyMeta(tut, tutDir)

	// Count inline [!UNVERIFIED] callouts so the page can flag, near the badge,
	// how many claims the author couldn't ground in a source. Derived at render
	// time from the rendered HTML, so it stays live as parts change with no
	// metadata bookkeeping.
	unverifiedCount := bytes.Count(content, []byte("callout-unverified"))

	// Render the voice spec body (markdown) for the byline's inline reveal.
	// Best-effort and only for voiced tutorials: a deleted/unresolvable custom
	// voice — or a spec that fails to render — yields empty HTML, so the byline
	// still shows the name but renders no <details>. Pre-feature tutorials (empty
	// Voice) get no reveal — the byline shows the model only, matching the old
	// footer behavior.
	var voiceSpec template.HTML
	if tut.Voice != "" {
		if v, err := voice.Resolve(tut.Voice); err == nil {
			if rendered, rerr := RenderMarkdown([]byte(v.Body())); rerr == nil {
				voiceSpec = template.HTML(rendered)
			}
		}
	}

	currentProgress := currentPartProgress(tut, part)

	// Per-part exercise checkbox state lives in its own sidecar (exercises.json),
	// deliberately outside the monotonic progress record. Best-effort like
	// progress: a read error just yields no restored checks. Only the current
	// part's indices are surfaced — they match the data-exercise-index values the
	// renderer assigned for this part.
	var checkedExercises []int
	if state, err := store.ReadExercises(tutDir); err == nil {
		checkedExercises = state[part]
	}

	// Compute the 1-based part number of the globally saved progress so the
	// client can perform a cheap cross-part monotonic check and avoid
	// unnecessary API calls when re-visiting an earlier part.
	savedPartNumber := 0
	if tut.Progress != nil {
		idx := partIndex(tut, tut.Progress.Part)
		if idx >= 0 {
			savedPartNumber = idx + 1
		}
	}

	var buf bytes.Buffer
	if err := s.layoutTmpl.Execute(&buf, map[string]any{
		"Title":             tut.Title,
		"Tutorial":          tut,
		"VerifyResult":      verifyResult,
		"VerifiedDate":      verifiedDate,
		"UnverifiedCount":   unverifiedCount,
		"VoiceSpec":         voiceSpec,
		"CurrentPart":       part,
		"CurrentProgress":   currentProgress,
		"CheckedExercises":  checkedExercises,
		"CurrentPartNumber": currentNumber,
		"SavedPartNumber":   savedPartNumber,
		"TotalParts":        len(tut.Parts),
		"Content":           template.HTML(content),
		"CSS":               s.designCSS,
		"HighlightCSS":      s.highlightCSS,
		"PrevPart":          prevPart,
		"NextPart":          nextPart,
		"PrevTitle":         prevTitle,
		"NextTitle":         nextTitle,
		"PrevNumber":        prevNumber,
		"NextNumber":        nextNumber,
		"TOC":               toc,
		"SeriesTOC":         seriesTOC,
		"IsLastPart":        isLast,
		"NextPartNumber":    len(tut.Parts) + 1,
		"PendingPartNumber": pendingPartNumber(tut.PendingPart, len(tut.Parts)+1),
		// JustAddedPart is only ever set by handleStatus, which renders the shared
		// extendSection partial after an extend completes. On the full page it is
		// nil, so the partial's "ready link" branch stays inert.
		"JustAddedPart": nil,
	}); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func pendingPartNumber(pendingPart string, fallback int) int {
	if pendingPart == "" {
		return fallback
	}
	s := strings.TrimSuffix(strings.TrimPrefix(pendingPart, "part-"), ".md")
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

func currentPartProgress(tut *store.Tutorial, part string) *store.Progress {
	if tut.Progress == nil || tut.Progress.Part != part {
		return nil
	}
	return tut.Progress
}

// handleStatus is the polling endpoint behind the in-place status updates. The
// reading page polls it every 5s while a tutorial is verifying or extending —
// an out-of-process skill owns those transitions, writing metadata.json, and
// ReadMetadata picks the change up on the next request. It re-renders just the
// status-dependent regions (the header badge, the verify section, the extend
// section) from the same partials the full page uses, so the client can swap
// them into the DOM without a full reload.
//
// `done` reports whether the tutorial has left its transient state — the signal
// for the client to apply the swap and stop polling. The optional ?from= query
// names the state the client is polling out of: when an extend finishes
// (from=extending) and a new part now follows the requested part, the extend
// section renders a "Part N is ready" link to it instead of the form.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	part := r.PathValue("part")
	tutDir, ok := s.safeTutorialPath(slug)
	if !ok {
		http.NotFound(w, r)
		return
	}
	tut, err := store.ReadMetadata(tutDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !isKnownPart(tut, part) {
		http.NotFound(w, r)
		return
	}

	verifyResult, verifiedDate := verifyMeta(tut, tutDir)
	last := isLastPart(tut, part)
	done := tut.Status != store.StatusVerifying && tut.Status != store.StatusExtending

	// When an extend completes, the part it appended now follows the page's
	// (formerly last) part — surface it as a "ready" link. Gated on from=extending
	// so a verify poll on a middle part can never show a spurious link.
	var justAdded any
	if done && r.URL.Query().Get("from") == "extending" {
		if next, ok := partAfter(tut, part); ok {
			justAdded = next
		}
	}

	data := map[string]any{
		"Tutorial":          tut,
		"VerifyResult":      verifyResult,
		"VerifiedDate":      verifiedDate,
		"IsLastPart":        last,
		"NextPartNumber":    len(tut.Parts) + 1,
		"PendingPartNumber": pendingPartNumber(tut.PendingPart, len(tut.Parts)+1),
		"JustAddedPart":     justAdded,
	}

	// The extend region only has content on the last part, or when an extend just
	// added the next part (the ready link) — mirror the full page's wrapper guard
	// so a verify completion on a middle part returns an empty extend region.
	var extend string
	if last || justAdded != nil {
		extend = s.renderPartial("extendSection", data)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": tut.Status,
		"done":   done,
		"badge":  s.renderPartial("statusHeader", data),
		"verify": s.renderPartial("verifySection", data),
		"extend": extend,
	})
}

// renderPartial executes a named layout partial against data and returns the
// rendered HTML. A template error yields "" — the polling client treats an empty
// region as "nothing to swap", so a transient render error degrades gracefully
// rather than corrupting the page.
func (s *Server) renderPartial(name string, data any) string {
	var buf bytes.Buffer
	if err := s.layoutTmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return ""
	}
	return buf.String()
}
