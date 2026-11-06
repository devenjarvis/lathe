package serve

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"strings"

	"github.com/devenjarvis/lathe/internal/jig"
	"github.com/gohugoio/hugo-goldmark-extensions/passthrough"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// mathPassthrough hands LaTeX delimiters to the browser verbatim (KaTeX typesets
// them client-side; see renderMarkdown). Shared by the page renderer and the jig
// text renderer so math inside a jig behaves exactly like math in prose.
func mathPassthrough() goldmark.Extender {
	return passthrough.New(passthrough.Config{
		InlineDelimiters: []passthrough.Delimiters{
			{Open: "$", Close: "$"},
			{Open: `\(`, Close: `\)`},
		},
		BlockDelimiters: []passthrough.Delimiters{
			{Open: "$$", Close: "$$"},
			{Open: `\[`, Close: `\]`},
		},
	})
}

// jigMarkdown renders the short markdown strings inside a jig (a question, an
// option, an explanation). Unlike the page renderer it does not pass raw HTML
// through: jig text is data the jig lays out, not markup to inject. Math is
// escaped on the way out too (escapedMath), which KaTeX doesn't mind: it reads
// the delimiters back from textContent.
var jigMarkdown = goldmark.New(
	goldmark.WithExtensions(mathPassthrough()),
	goldmark.WithRendererOptions(renderer.WithNodeRenderers(
		// Below the passthrough extension's own renderers (99 and 101), so
		// these win.
		util.Prioritized(escapedMath{}, 50),
	)),
)

// escapedMath writes passthrough math HTML-escaped. The extension's renderer
// writes it raw, so "$<img onerror=…>$" in a jig option would otherwise be
// markup.
type escapedMath struct{}

func (escapedMath) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(passthrough.KindPassthroughInline, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if p, ok := n.(*passthrough.PassthroughInline); ok && entering {
			_, _ = w.WriteString(html.EscapeString(string(p.Segment.Value(src))))
		}
		return ast.WalkContinue, nil
	})
	reg.Register(passthrough.KindPassthroughBlock, func(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			for i := 0; i < n.Lines().Len(); i++ {
				seg := n.Lines().At(i)
				_, _ = w.WriteString(html.EscapeString(string(seg.Value(src))))
			}
			_, _ = w.WriteString("\n")
		}
		return ast.WalkSkipChildren, nil
	})
}

// jigInline renders one line of markdown without its paragraph wrapper. Text
// that parses as anything but a single paragraph (a "# " that would become a
// heading, a "1. " that would become a list) is shown literally instead.
func jigInline(s string) template.HTML {
	src := []byte(s)
	doc := jigMarkdown.Parser().Parse(text.NewReader(src))
	if !singleParagraph(doc) {
		return template.HTML(html.EscapeString(s))
	}
	var buf bytes.Buffer
	if err := jigMarkdown.Renderer().Render(&buf, src, doc); err != nil {
		return template.HTML(html.EscapeString(s))
	}
	out := strings.TrimSpace(buf.String())
	out = strings.TrimSuffix(strings.TrimPrefix(out, "<p>"), "</p>")
	return template.HTML(out)
}

// singleParagraph reports whether doc is one paragraph. The passthrough
// extension's transformer can leave an empty paragraph beside the real one (it
// splits paragraphs around block math), so empty paragraphs don't count.
func singleParagraph(doc ast.Node) bool {
	n := 0
	for c := doc.FirstChild(); c != nil; c = c.NextSibling() {
		if c.Kind() == ast.KindParagraph && c.ChildCount() == 0 {
			continue
		}
		if c.Kind() != ast.KindParagraph {
			return false
		}
		n++
	}
	return n == 1
}

// jigBlock renders markdown with its paragraphs.
func jigBlock(s string) template.HTML {
	var buf bytes.Buffer
	if err := jigMarkdown.Convert([]byte(s), &buf); err != nil {
		return template.HTML("<p>" + html.EscapeString(s) + "</p>")
	}
	return template.HTML(buf.String())
}

// renderJig renders a ```jig <kind> fence through the jig library. A jig that
// doesn't parse still renders: jig.Render falls back to an error note and the
// original source, so a typo never takes the page down.
func (r *tutorialBlockRenderer) renderJig(w util.BufWriter, src []byte, n *ast.FencedCodeBlock) (ast.WalkStatus, error) {
	r.jigs++
	var info string
	if n.Info != nil {
		_, info, _ = strings.Cut(strings.TrimSpace(string(n.Info.Segment.Value(src))), " ")
	}
	ctx := &jig.Context{ID: fmt.Sprintf("jig-%d", r.jigs), Inline: jigInline, Block: jigBlock}
	if err := jig.Render(w, info, string(n.Lines().Value(src)), ctx); err != nil {
		return ast.WalkStop, err
	}
	return ast.WalkSkipChildren, nil
}
