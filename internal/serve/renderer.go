package serve

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/gohugoio/hugo-goldmark-extensions/passthrough"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

// TOCEntry is a single h2 heading collected for the in-page table of contents
// rendered in the sidebar. ID matches the auto-heading-id slug, so anchor links
// (#first-section) jump directly to the heading.
type TOCEntry struct {
	ID   string
	Text string
}

// Chroma syntax styles, chosen to harmonize with the warm "paper"/"ember"
// palette: tango's muted browns/olives in light, gruvbox's warm ambers/oranges
// in dark. Only the syntax-token hues come from these — the code-block
// container background is owned by our --code-bg token (see pre.chroma in
// styles.css), so chroma's own background never shows through.
const (
	lightStyle = "tango"
	darkStyle  = "gruvbox"
)

// mermaidBlock matches a fenced code block whose info string is "mermaid".
// Group 1 is the body. Up to three leading spaces of indentation are allowed
// per CommonMark; trailing whitespace on the fence line is tolerated.
var mermaidBlock = regexp.MustCompile("(?ms)^[ \t]{0,3}```[ \t]*mermaid[ \t]*\r?\n(.*?)\r?\n[ \t]{0,3}```[ \t]*$")

// calloutBlock matches a GFM-alert-style blockquote whose first line is
// `> [!TYPE]`. Group 1 is the type, group 2 is the body (still blockquote-
// prefixed; preprocessCallouts strips the `> ` from each body line).
var calloutBlock = regexp.MustCompile(`(?m)^[ \t]{0,3}>[ \t]*\[!(NOTE|TIP|WARNING|HEADS-UP|ASIDE|DESIGN-NOTE|PREDICT|RECALL|UNVERIFIED)\][ \t]*\r?\n((?:[ \t]{0,3}>.*(?:\r?\n|$))*)`)

// calloutLineStrip removes the `>` (and one optional following space) from the
// start of each body line of a callout, leaving the inner markdown.
var calloutLineStrip = regexp.MustCompile(`(?m)^[ \t]{0,3}> ?`)

func RenderMarkdown(src []byte) ([]byte, error) {
	out, _, err := RenderMarkdownWithTOC(src)
	return out, err
}

// RenderMarkdownWithTOC renders markdown to HTML and returns a list of h2
// headings for the in-page TOC. parser.WithAutoHeadingID assigns each heading
// a stable id slug; the same slug is captured here for anchor links.
func RenderMarkdownWithTOC(src []byte) ([]byte, []TOCEntry, error) {
	src = preprocessCallouts(src)
	src = preprocessMermaid(src)
	md := goldmark.New(
		goldmark.WithExtensions(
			highlighting.NewHighlighting(
				highlighting.WithStyle(lightStyle),
				highlighting.WithFormatOptions(
					chromahtml.WithClasses(true),
				),
			),
			// GFM tables — without this, pipe-delimited tables fall through as
			// literal text. styles.css already styles <table>/<th>/<td>.
			extension.Table,
			// LaTeX math passes through goldmark untouched — without this,
			// CommonMark backslash-escapes corrupt TeX before it reaches the
			// browser (`\|` -> `|`, `\;` -> `;`). KaTeX auto-render in
			// layout.html picks up the same delimiters client-side. Parsing at
			// the AST level (not a preprocess regex) means $ inside code spans
			// and fenced blocks is naturally left alone.
			passthrough.New(passthrough.Config{
				InlineDelimiters: []passthrough.Delimiters{
					{Open: "$", Close: "$"},
					{Open: `\(`, Close: `\)`},
				},
				BlockDelimiters: []passthrough.Delimiters{
					{Open: "$$", Close: "$$"},
					{Open: `\[`, Close: `\]`},
				},
			}),
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(
			goldmarkhtml.WithUnsafe(),
		),
	)
	doc := md.Parser().Parse(text.NewReader(src))
	toc := collectH2TOC(doc, src)
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), toc, nil
}

// collectH2TOC walks the parsed AST and returns one TOCEntry per <h2>. h1, h3,
// and deeper levels are skipped — h1 is the page title, h3+ would clutter the
// sidebar. Heading text is the concatenation of inline ast.Text segments, so
// formatting like `code` or *emphasis* contributes plain text only.
func collectH2TOC(doc ast.Node, src []byte) []TOCEntry {
	var toc []TOCEntry
	// The walk callback never returns an error, so the Walk result is safe to drop.
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok || h.Level != 2 {
			return ast.WalkContinue, nil
		}
		// AutoHeadingID assigns an id to every non-empty heading; a missing id
		// means the heading text was empty (`##` with no content). Skip — there
		// is nothing to label the entry with anyway.
		idAttr, ok := h.AttributeString("id")
		if !ok {
			return ast.WalkContinue, nil
		}
		idBytes, _ := idAttr.([]byte)
		toc = append(toc, TOCEntry{
			ID:   string(idBytes),
			Text: extractInlineText(h, src),
		})
		return ast.WalkSkipChildren, nil
	})
	return toc
}

// extractInlineText concatenates the inline text of all ast.Text descendants of
// n, in document order. Code spans and emphasis nodes contain ast.Text children
// so this captures their visible text without HTML markup.
func extractInlineText(n ast.Node, src []byte) string {
	var b strings.Builder
	// The walk callback never returns an error, so the Walk result is safe to drop.
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if t, ok := c.(*ast.Text); ok {
			b.Write(t.Segment.Value(src))
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// preprocessCallouts rewrites GFM-alert-style blockquotes (lines starting with
// `> [!TYPE]`) into raw <aside> HTML blocks. The body is left as markdown,
// separated by blank lines so goldmark's CommonMark HTML-block-type-6 rules
// re-enable markdown rendering inside.
func preprocessCallouts(src []byte) []byte {
	return calloutBlock.ReplaceAllFunc(src, func(match []byte) []byte {
		sub := calloutBlock.FindSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		kind := strings.ToLower(strings.ReplaceAll(string(sub[1]), "-", ""))
		label := calloutLabel(string(sub[1]))
		body := calloutLineStrip.ReplaceAll(sub[2], nil)
		var b bytes.Buffer
		b.WriteString("\n<aside class=\"callout callout-")
		b.WriteString(kind)
		b.WriteString("\">\n<p class=\"callout-label\">")
		b.WriteString(label)
		b.WriteString("</p>\n\n")
		b.Write(body)
		if !bytes.HasSuffix(body, []byte("\n")) {
			b.WriteByte('\n')
		}
		b.WriteString("\n</aside>\n\n")
		return b.Bytes()
	})
}

func calloutLabel(kind string) string {
	switch kind {
	case "DESIGN-NOTE":
		return "Design note"
	case "HEADS-UP":
		return "Heads up"
	case "NOTE":
		return "Note"
	case "TIP":
		return "Tip"
	case "WARNING":
		return "Warning"
	case "ASIDE":
		return "Aside"
	case "PREDICT":
		return "Predict"
	case "RECALL":
		return "Recall"
	case "UNVERIFIED":
		return "Unverified"
	}
	return kind
}

// preprocessMermaid rewrites ```mermaid fenced blocks into raw HTML divs that
// the browser-side mermaid library renders into SVG. The body is HTML-escaped
// so labels containing < > & survive intact; the browser un-escapes them when
// mermaid reads textContent.
func preprocessMermaid(src []byte) []byte {
	return mermaidBlock.ReplaceAllFunc(src, func(match []byte) []byte {
		sub := mermaidBlock.FindSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		var b bytes.Buffer
		b.WriteString("\n<div class=\"mermaid\">\n")
		b.WriteString(html.EscapeString(string(sub[1])))
		b.WriteString("\n</div>\n")
		return b.Bytes()
	})
}

func HighlightCSS() (template.CSS, error) {
	formatter := chromahtml.New(chromahtml.WithClasses(true))

	light := styles.Get(lightStyle)
	if light == nil {
		return "", fmt.Errorf("chroma style %q not found", lightStyle)
	}
	var lightBuf bytes.Buffer
	if err := formatter.WriteCSS(&lightBuf, light); err != nil {
		return "", err
	}

	dark := styles.Get(darkStyle)
	if dark == nil {
		return "", fmt.Errorf("chroma style %q not found", darkStyle)
	}
	var darkBuf bytes.Buffer
	if err := formatter.WriteCSS(&darkBuf, dark); err != nil {
		return "", err
	}

	// Scope EACH palette to its own theme. The light rules must be scoped too:
	// if they stay global, any token the (less exhaustive) dark style leaves
	// undefined falls through to the light color — often near-black — and is
	// unreadable on the dark code background. Scoping makes undefined tokens
	// fall back to the style's own readable default foreground instead.
	//
	// stripWrapperBackground drops chroma's PreWrapper background so the warm
	// --code-bg token (styles.css) owns the container in both themes; only the
	// syntax-token hues come through.
	var out strings.Builder
	out.WriteString(stripWrapperBackground(scopeCSS(lightBuf.String(), `:root:not([data-theme="dark"])`)))
	out.WriteString(stripWrapperBackground(scopeCSS(darkBuf.String(), `[data-theme="dark"]`)))

	return template.CSS(out.String()), nil
}

// wrapperBackground matches a single `background-color: …;` declaration, used to
// strip chroma's container background from the PreWrapper (.chroma) and .bg
// rules so our --code-bg token controls the code-block background instead.
var wrapperBackground = regexp.MustCompile(`background-color:[^;}]*;?`)

func stripWrapperBackground(css string) string {
	var b strings.Builder
	for _, line := range strings.Split(css, "\n") {
		if strings.Contains(line, ".chroma {") || strings.Contains(line, ".bg {") {
			line = wrapperBackground.ReplaceAllString(line, "")
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// scopeCSS prefixes every CSS rule in src with the given selector. It assumes
// the chroma WriteCSS layout: one rule per line, each starting with a
// "/* ... */" comment followed by selector and declaration block.
func scopeCSS(src, prefix string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if line == "" {
			b.WriteByte('\n')
			continue
		}
		end := strings.LastIndex(line, "*/")
		if end == -1 {
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}
		b.WriteString(line[:end+2])
		b.WriteByte(' ')
		b.WriteString(prefix)
		b.WriteString(line[end+2:])
		b.WriteByte('\n')
	}
	return b.String()
}
