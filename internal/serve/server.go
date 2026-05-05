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

//go:embed static/mermaid.min.js
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
	return mux
}

// staticAssets whitelists the embedded files we expose under /_static/. Keeping
// it explicit means no embed.FS path can be coaxed out of the binary by an
// unexpected route — even though the {name} wildcard already can't contain a
// slash, this is the cheap belt-and-suspenders check.
var staticAssets = map[string]string{
	"mermaid.min.js": "application/javascript; charset=utf-8",
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
	if tut.Series && len(tut.Parts) > 0 {
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

func (s *Server) renderPart(w http.ResponseWriter, tut *store.Tutorial, tutDir, part string) {
	src, err := os.ReadFile(filepath.Join(tutDir, part))
	if err != nil {
		http.Error(w, "part not found", http.StatusNotFound)
		return
	}
	content, err := RenderMarkdown(src)
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := s.layoutTmpl.Execute(&buf, map[string]any{
		"Title":        tut.Title,
		"Tutorial":     tut,
		"CurrentPart":  part,
		"Content":      template.HTML(content),
		"HighlightCSS": s.highlightCSS,
	}); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}
