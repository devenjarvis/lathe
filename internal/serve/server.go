package serve

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/devenjarvis/lathe/internal/store"
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

	// On failure, surface the verifier's recorded part/step/error so the page
	// explains what broke instead of just showing a red badge. Best-effort:
	// a missing or malformed verify-result.json simply renders no panel.
	var verifyResult *store.VerifyResult
	if tut.Status == store.StatusFailed {
		if vr, err := store.ReadVerifyResult(tutDir); err == nil {
			verifyResult = vr
		}
	}

	var buf bytes.Buffer
	if err := s.layoutTmpl.Execute(&buf, map[string]any{
		"Title":             tut.Title,
		"Tutorial":          tut,
		"VerifyResult":      verifyResult,
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
