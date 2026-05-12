package serve

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/devenjarvis/lathe/internal/store"
)

//go:embed layout.html list.html
var templateFS embed.FS

//go:embed static/mermaid.min.js static/marked.min.js static/dompurify.min.js
var staticFS embed.FS

type Server struct {
	tutorialsDir string
	layoutTmpl   *template.Template
	listTmpl     *template.Template
	highlightCSS template.CSS
}

func NewServer(tutorialsDir string) *Server {
	funcMap := template.FuncMap{
		"add": func(a, b int) int { return a + b },
	}
	layoutTmpl := template.Must(template.New("layout.html").Funcs(funcMap).ParseFS(templateFS, "layout.html"))
	listTmpl := template.Must(template.New("list.html").ParseFS(templateFS, "list.html"))
	css, err := HighlightCSS()
	if err != nil {
		panic(fmt.Sprintf("lathe: failed to build syntax-highlight CSS: %v", err))
	}
	return &Server{tutorialsDir: tutorialsDir, layoutTmpl: layoutTmpl, listTmpl: listTmpl, highlightCSS: css}
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
	return mux
}

// staticAssets whitelists the embedded files we expose under /_static/. Keeping
// it explicit means no embed.FS path can be coaxed out of the binary by an
// unexpected route — even though the {name} wildcard already can't contain a
// slash, this is the cheap belt-and-suspenders check.
var staticAssets = map[string]string{
	"mermaid.min.js":   "application/javascript; charset=utf-8",
	"marked.min.js":    "application/javascript; charset=utf-8",
	"dompurify.min.js": "application/javascript; charset=utf-8",
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	contentType, ok := staticAssets[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, err := staticFS.ReadFile("static/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(data)
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
		"HighlightCSS": s.highlightCSS,
	}); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
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
	if tut.IsSeries() && len(tut.Parts) > 0 {
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

	var buf bytes.Buffer
	if err := s.layoutTmpl.Execute(&buf, map[string]any{
		"Title":             tut.Title,
		"Tutorial":          tut,
		"CurrentPart":       part,
		"CurrentPartNumber": currentNumber,
		"Content":           template.HTML(content),
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
	}); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}
