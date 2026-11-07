package serve

// Go to anything (#144): GET /-/index is what the palette (palette.js)
// searches — every tutorial with its parts and their h2/h3 headings, its
// tags, shelf, status and tools. It is built from what the server already
// reads (metadata.json, shelves.json, the part files); the palette filters
// it in the browser.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "embed"

	"github.com/devenjarvis/lathe/internal/store"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

//go:embed palette.js
var paletteScript string

func paletteJS() template.JS { return template.JS(paletteScript) }

// indexJSON is the body of GET /-/index.
type indexJSON struct {
	Tutorials []indexTutorial `json:"tutorials"`
	// Tags is every tag with the number of tutorials carrying it, most used
	// first, with the All tutorials view that lists them.
	Tags []indexTag `json:"tags"`
}

type indexTutorial struct {
	Slug   string       `json:"slug"`
	Title  string       `json:"title"`
	Topic  string       `json:"topic,omitempty"`
	Href   string       `json:"href"`
	Status store.Status `json:"status"`
	Tags   []string     `json:"tags"`
	Tools  []store.Tool `json:"tools"`
	Shelf  indexShelf   `json:"shelf"`
	// Cover is the cover color (1–4, the --cover-N tokens), fixed per slug
	// so a tutorial keeps its swatch wherever the palette lists it.
	Cover int         `json:"cover"`
	Parts []indexPart `json:"parts"`
}

type indexShelf struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type indexPart struct {
	// Part is the part's file name ("part-02.md"; "index.md" for a legacy
	// single-file tutorial). Number is 1-based, 0 for index.md.
	Part     string         `json:"part"`
	Number   int            `json:"number"`
	Title    string         `json:"title"`
	Href     string         `json:"href"`
	Headings []indexHeading `json:"headings"`
}

// indexHeading is an h2 or h3. ID is the id the reading page gives the
// heading (the same goldmark pipeline assigns it), so Href jumps to it.
type indexHeading struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Level int    `json:"level"`
	Href  string `json:"href"`
}

type indexTag struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
	Href  string `json:"href"`
}

// partOutline is one part file's title and headings, cached by path and
// keyed on the file's mtime and size so an edited or extended part is
// re-read and an unchanged one is not.
type partOutline struct {
	mod      time.Time
	size     int64
	title    string
	headings []outlineHeading
}

type outlineHeading struct {
	id, text string
	level    int
}

// indexCache holds the part outlines. The zero value is ready to use.
type indexCache struct {
	mu    sync.Mutex
	parts map[string]partOutline
}

// outline returns the title and headings of the part file at path, from the
// cache when the file is unchanged. seen collects the paths used, so entries
// for deleted parts can be dropped.
func (c *indexCache) outline(path string, seen map[string]bool) (partOutline, bool) {
	seen[path] = true
	fi, err := os.Stat(path)
	if err != nil {
		return partOutline{}, false
	}
	c.mu.Lock()
	cached, ok := c.parts[path]
	c.mu.Unlock()
	if ok && cached.mod.Equal(fi.ModTime()) && cached.size == fi.Size() {
		return cached, true
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return partOutline{}, false
	}
	o := outlineOf(src)
	o.mod, o.size = fi.ModTime(), fi.Size()
	c.mu.Lock()
	if c.parts == nil {
		c.parts = map[string]partOutline{}
	}
	c.parts[path] = o
	c.mu.Unlock()
	return o, true
}

// prune drops cached outlines for files not in seen.
func (c *indexCache) prune(seen map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for p := range c.parts {
		if !seen[p] {
			delete(c.parts, p)
		}
	}
}

// outlineOf parses a part the way the reading page does (renderMarkdown) and
// collects its h1 text and its h2/h3 headings with their ids.
func outlineOf(src []byte) partOutline {
	src = preprocessMarkdown(src)
	doc := newMarkdown(0).Parser().Parse(text.NewReader(src))
	var o partOutline
	// The walk callback never returns an error, so the Walk result is safe to drop.
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		txt := strings.TrimSpace(extractInlineText(h, src))
		switch {
		case h.Level == 1 && o.title == "":
			o.title = txt
		case h.Level == 2 || h.Level == 3:
			id, ok := h.AttributeString("id")
			idBytes, _ := id.([]byte)
			if ok && len(idBytes) > 0 && txt != "" {
				o.headings = append(o.headings, outlineHeading{id: string(idBytes), text: txt, level: h.Level})
			}
		}
		return ast.WalkSkipChildren, nil
	})
	return o
}

// coverFor is a tutorial's cover color, 1–4, from its slug.
func coverFor(slug string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(slug))
	return int(h.Sum32()%4) + 1
}

// tagHref is the All tutorials view searched for tag: the list page's ?q=
// matches tags (as well as titles and topics).
func tagHref(tag string) string { return viewHref("view", viewAll, "q", tag) }

// buildIndex is the index of tutorials (newest first, as readTutorials
// returns them).
func (s *Server) buildIndex(tutorials []*store.Tutorial, shelves *store.Shelves) indexJSON {
	out := indexJSON{Tutorials: make([]indexTutorial, 0, len(tutorials)), Tags: []indexTag{}}
	seen := map[string]bool{}
	tagCount := map[string]int{}
	for _, t := range tutorials {
		sh := shelves.ShelfOf(t.Slug)
		it := indexTutorial{
			Slug:   t.Slug,
			Title:  t.Title,
			Topic:  t.Topic,
			Href:   "/" + t.Slug + "/",
			Status: t.Status,
			Tags:   append([]string{}, t.Tags...),
			Tools:  append([]store.Tool{}, t.Tools...),
			Shelf:  indexShelf{ID: sh.ID, Name: sh.Name},
			Cover:  coverFor(t.Slug),
			Parts:  []indexPart{},
		}
		if it.Status == "" {
			it.Status = store.StatusUnverified
		}
		for _, tag := range t.Tags {
			tagCount[tag]++
		}
		parts := t.Parts
		legacy := len(parts) == 0
		if legacy {
			parts = []string{"index.md"}
		}
		tutDir, ok := s.safeTutorialPath(t.Slug)
		if !ok {
			continue
		}
		for i, p := range parts {
			ip := indexPart{Part: p, Number: i + 1, Href: "/" + t.Slug + "/" + p, Headings: []indexHeading{}}
			if legacy {
				ip.Number, ip.Href = 0, it.Href
			}
			if o, ok := s.index.outline(filepath.Join(tutDir, p), seen); ok {
				ip.Title = o.title
				for _, h := range o.headings {
					ip.Headings = append(ip.Headings, indexHeading{ID: h.id, Text: h.text, Level: h.level, Href: ip.Href + "#" + h.id})
				}
			}
			if ip.Title == "" {
				if legacy {
					ip.Title = t.Title
				} else {
					ip.Title = store.SlugToTitle(strings.TrimSuffix(p, ".md"))
				}
			}
			it.Parts = append(it.Parts, ip)
		}
		out.Tutorials = append(out.Tutorials, it)
	}
	s.index.prune(seen)
	for tag, n := range tagCount {
		out.Tags = append(out.Tags, indexTag{Tag: tag, Count: n, Href: tagHref(tag)})
	}
	sort.Slice(out.Tags, func(a, b int) bool {
		if out.Tags[a].Count != out.Tags[b].Count {
			return out.Tags[a].Count > out.Tags[b].Count
		}
		return out.Tags[a].Tag < out.Tags[b].Tag
	})
	return out
}

// handleIndex is GET /-/index. The body carries an ETag, so a palette that
// opens again on an unchanged library gets a 304.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	tutorials, err := s.readTutorials()
	if err != nil {
		if !os.IsNotExist(err) {
			http.Error(w, "could not read tutorials", http.StatusInternalServerError)
			return
		}
		tutorials = nil
	}
	body, err := json.Marshal(s.buildIndex(tutorials, s.readShelvesForView()))
	if err != nil {
		http.Error(w, "could not build the index", http.StatusInternalServerError)
		return
	}
	sum := sha256.Sum256(body)
	etag := fmt.Sprintf(`"%s"`, hex.EncodeToString(sum[:8]))
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body)
}
