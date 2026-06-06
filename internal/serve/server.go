package serve

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
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
//go:embed static/favicon.svg
//go:embed static/fonts/fraunces.woff2 static/fonts/newsreader.woff2 static/fonts/newsreader-italic.woff2 static/fonts/jetbrains-mono.woff2
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
		"add": func(a, b int) int { return a + b },
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
	mux.HandleFunc("POST /-/extend/{slug}", s.handleExtend)
	mux.HandleFunc("POST /-/verify/{slug}", s.handleVerify)
	return mux
}

// staticAssets whitelists the embedded files we expose under /_static/. Keeping
// it explicit means no embed.FS path can be coaxed out of the binary by an
// unexpected route — even though the {name} wildcard already can't contain a
// slash, this is the cheap belt-and-suspenders check.
var staticAssets = map[string]string{
	"mermaid.min.js":          "application/javascript; charset=utf-8",
	"favicon.svg":             "image/svg+xml",
	"fraunces.woff2":          "font/woff2",
	"newsreader.woff2":        "font/woff2",
	"newsreader-italic.woff2": "font/woff2",
	"jetbrains-mono.woff2":    "font/woff2",
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

// RepoGroup is a set of tutorials sharing a git repo, used to render the list
// page grouped by repo. Repo is the canonical key ("" for the no-repo bucket)
// and Name is the human-facing label shown in the group header.
type RepoGroup struct {
	Repo      string
	Name      string
	Tutorials []*store.Tutorial
}

// groupByRepo buckets tutorials by their canonical Repo, ordering groups so the
// most-recently-touched repos come first and the catch-all "No repo" bucket
// comes last. Tutorials within each group default to newest-first (the client
// sort control can re-order them).
func groupByRepo(tutorials []*store.Tutorial) []RepoGroup {
	idx := make(map[string]int)
	var groups []RepoGroup
	for _, t := range tutorials {
		i, ok := idx[t.Repo]
		if !ok {
			name := t.RepoDisplay()
			if t.Repo == "" {
				name = "No repo"
			}
			idx[t.Repo] = len(groups)
			i = len(groups)
			groups = append(groups, RepoGroup{Repo: t.Repo, Name: name})
		}
		groups[i].Tutorials = append(groups[i].Tutorials, t)
	}
	newest := func(tuts []*store.Tutorial) time.Time {
		var max time.Time
		for _, t := range tuts {
			if t.Created.After(max) {
				max = t.Created
			}
		}
		return max
	}
	for gi := range groups {
		g := groups[gi].Tutorials
		sort.SliceStable(g, func(a, b int) bool { return g[a].Created.After(g[b].Created) })
	}
	sort.SliceStable(groups, func(a, b int) bool {
		// The no-repo bucket always sinks to the bottom.
		if (groups[a].Repo == "") != (groups[b].Repo == "") {
			return groups[b].Repo == ""
		}
		na, nb := newest(groups[a].Tutorials), newest(groups[b].Tutorials)
		if !na.Equal(nb) {
			return na.After(nb)
		}
		return groups[a].Name < groups[b].Name
	})
	return groups
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
	var buf bytes.Buffer
	if err := s.listTmpl.Execute(&buf, map[string]any{
		"Groups":       groupByRepo(tutorials),
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
	// index.md, so redirect to the first part. The index.md fallback is only for
	// legacy tutorials that were never split into parts.
	if len(tut.Parts) > 0 {
		http.Redirect(w, r, fmt.Sprintf("/%s/%s", slug, tut.Parts[0]), http.StatusFound)
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
	s.renderPart(w, tut, tutDir, part)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
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

	// Surface the verifier's recorded result. On failure it explains what broke
	// (part/step/error); on verified/skipped it carries the CheckedAt timestamp
	// we show as "Verified <date>". Best-effort: a missing or malformed
	// verify-result.json simply renders no panel and no date.
	var verifyResult *store.VerifyResult
	switch tut.Status {
	case store.StatusFailed, store.StatusVerified, store.StatusSkipped:
		if vr, err := store.ReadVerifyResult(tutDir); err == nil {
			verifyResult = vr
		}
	}

	// On verified/skipped, format the verifier's timestamp as a friendly date for
	// the "Verified <date>" provenance line. Best-effort: an unparseable or empty
	// CheckedAt simply yields no date.
	var verifiedDate string
	if verifyResult != nil && (tut.Status == store.StatusVerified || tut.Status == store.StatusSkipped) {
		if ts, err := time.Parse(time.RFC3339, verifyResult.CheckedAt); err == nil {
			verifiedDate = ts.Format("Jan 2, 2006")
		}
	}

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

	var buf bytes.Buffer
	if err := s.layoutTmpl.Execute(&buf, map[string]any{
		"Title":             tut.Title,
		"Tutorial":          tut,
		"VerifyResult":      verifyResult,
		"VerifiedDate":      verifiedDate,
		"UnverifiedCount":   unverifiedCount,
		"VoiceSpec":         voiceSpec,
		"CurrentPart":       part,
		"CurrentPartNumber": currentNumber,
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
